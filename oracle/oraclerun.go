package oracle

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/luthersystems/svc/grpclogging"
	"github.com/luthersystems/svc/logmon"
	"github.com/luthersystems/svc/midware"
	"github.com/luthersystems/svc/svcerr"
	"github.com/luthersystems/svc/txctx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

var versionTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "version_total",
		Help: "How many versions seen, partitioned by oracle and phylum.",
	},
	[]string{"oracle_name", "oracle_version", "phylum_name", "phylum_version"},
)

const dependentTxMetadataKey = "commit-transaction-id"

func init() {
	// Provider per endpoint histograms (at expense of memory/performance).
	grpc_prometheus.EnableClientHandlingTimeHistogram(
		grpc_prometheus.WithHistogramBuckets(prometheus.ExponentialBuckets(0.05, 1.25, 25)),
	)

	// Expose log severity counts to prometheus.
	logrus.AddHook(logmon.NewPrometheusHook())

	prometheus.MustRegister(versionTotal)
}

// gatewayForwardedHeaders are HTTP headers which the grpc-gateway will encode
// as grpc request metadata and forward to the oracle grpc server.  Forwarded
// headers may be used for authentication flows, request tracing, etc.
func (orc *Oracle) gatewayForwardedHeaders() []string {
	return append([]string{
		"Cookie",
		"X-Forwarded-For",
		"User-Agent",
		"X-Forwarded-User-Agent",
		"Referer",
		orc.cfg.RequestIDHeader,
	}, orc.cfg.ForwardedHeaders...)
}

func (orc *Oracle) incomingHeaderMatcher(h string) (string, bool) {
	headers := orc.gatewayForwardedHeaders()

	for i := range headers {
		if strings.EqualFold(h, headers[i]) {
			return h, true
		}
	}
	return "", false
}

func (orc *Oracle) grpcGatewayMux() *runtime.ServeMux {
	opts := []runtime.ServeMuxOption{
		runtime.WithErrorHandler(svcerr.ErrIntercept(orc.Log)),
		runtime.WithIncomingHeaderMatcher(orc.incomingHeaderMatcher),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: false,
			},
		}),
	}
	if orc.cfg.DependentTxCookie != "" {
		// set outgoing deptx cookie
		opts = append(opts, runtime.WithForwardResponseOption(cookieHandler(
			dependentTxMetadataKey,
			orc.cfg.DependentTxCookie,
			int(dependentTxCookieMaxAge.Seconds()),
			!orc.cfg.InsecureCookies)))
	}
	// TODO: set auth cookie here as well
	opts = append(opts, orc.cfg.gatewayOpts...)

	return runtime.NewServeMux(opts...)
}

func txctxInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	newCtx := txctx.Context(ctx)
	resp, err := handler(newCtx, req)
	txID := txctx.GetTransactionDetails(newCtx).TransactionID
	if txID != "" {
		grpclogging.AddLogrusField(newCtx, "commit_transaction_id", txID)
		setGRPCHeader(newCtx, dependentTxMetadataKey, txID)
	}
	return resp, err
}

func (orc *Oracle) grpcGateway(swaggerHandler http.Handler) (*runtime.ServeMux, http.Handler) {
	jsonapi := orc.grpcGatewayMux()
	pathOverides := midware.PathOverrides{
		healthCheckPath: orc.healthCheckHandler(),
	}
	if swaggerHandler != nil {
		pathOverides[swaggerPath] = swaggerHandler
	}
	middleware := midware.Chain{
		// The trace header middleware appears early in the chain
		// because of how important it is that they happen for essentially all
		// requests.
		midware.TraceHeaders(orc.cfg.RequestIDHeader, true),
		orc.addServerHeader(),
		// PathOverrides and other middleware that may serve requests or have
		// potential failure states should appear below here so they may rely
		// on the presence of the generic utility middleware above.
		pathOverides,
	}

	return jsonapi, middleware.Wrap(jsonapi)
}

// GrpcGatewayConfig configures the grpc gateway used by the oracle.
type GrpcGatewayConfig interface {
	// RegisterServiceServer is required to be overidden by the implementation.
	RegisterServiceServer(grpcServer *grpc.Server)
	// RegisterServiceClient is required to be overidden by the implementation.
	RegisterServiceClient(ctx context.Context, grpcCon *grpc.ClientConn, mux *runtime.ServeMux) error
}

func (orc *Oracle) StartGateway(ctx context.Context, grpcConfig GrpcGatewayConfig) error {
	orc.stateMut.Lock()
	if orc.state != oracleStateInit {
		return fmt.Errorf("run: invalid oracle state: %d", orc.state)
	}
	orc.state = oracleStateStarted

	trySendError := func(c chan<- error, err error) {
		if err == nil {
			return
		}
		select {
		case c <- err:
		default:
		}
	}
	errServe := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		if err := orc.close(); err != nil {
			orc.Log(ctx).WithError(err).Warn("failed to close oracle")
		}
	}()

	orc.Log(ctx).WithFields(logrus.Fields{
		"gateway_endpoint": orc.cfg.GatewayEndpoint,
		"phylum_path":      orc.cfg.PhylumPath,
		"emulate_cc":       orc.cfg.EmulateCC,
		"version":          orc.cfg.Version,
		"service":          orc.cfg.ServiceName,
		"phylum_name":      orc.cfg.PhylumServiceName,
		"listen_address":   orc.cfg.ListenAddress,
	}).Infof("starting oracle")

	nBig, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt32))
	if err != nil {
		panic(err)
	}

	// Start a grpc server listening on the unix socket at grpcAddr
	grpcAddr := fmt.Sprintf("/tmp/oracle.grpc.%d.sock", nBig.Int64())

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
			grpclogging.LogrusMethodInterceptor(
				orc.logBase,
				grpclogging.UpperBoundTimer(time.Millisecond),
				grpclogging.RealTime()),
			txctxInterceptor,
			svcerr.AppErrorUnaryInterceptor(orc.Log))))

	grpcConfig.RegisterServiceServer(grpcServer)

	orc.stateMut.Unlock()

	listener, err := net.Listen("unix", grpcAddr)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			orc.Log(ctx).WithError(err).Warn("failed to close listener")
		}
	}()

	go func() {
		trySendError(errServe, grpcServer.Serve(listener))
	}()

	// Create a grpc client which connects to grpcAddr
	grpcConn, err := grpc.NewClient("unix://"+grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcmiddleware.ChainUnaryClient(
			grpc_prometheus.UnaryClientInterceptor)))
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}

	mux, httpHandler := orc.grpcGateway(orc.swaggerHandler)
	if err := grpcConfig.RegisterServiceClient(ctx, grpcConn, mux); err != nil {
		return fmt.Errorf("register service client: %w", err)
	}

	go func() {
		orc.Log(ctx).Infof("init healthcheck")
		hctx, hcancel := context.WithDeadline(ctx, time.Now().Add(10*time.Second))
		defer hcancel()
		orc.phylumHealthCheck(hctx)
	}()

	go func() {
		orc.Log(ctx).Infof("oracle listen")
		server := &http.Server{
			Addr:              orc.cfg.ListenAddress,
			Handler:           httpHandler,
			ReadHeaderTimeout: 3 * time.Second,
		}
		trySendError(errServe, server.ListenAndServe())
	}()

	go func() {
		// metrics server
		h := http.NewServeMux()
		h.Handle(metricsPath, promhttp.Handler())
		s := &http.Server{
			Addr:              metricsAddr,
			WriteTimeout:      10 * time.Second,
			ReadHeaderTimeout: 2 * time.Second,
			Handler:           h,
		}
		orc.Log(ctx).Infof("prometheus listen")
		trySendError(errServe, s.ListenAndServe())
	}()

	// Both methods grpcServer.Start and http.ListenAndServe will block
	// forever.  An error in either the grpc server or the http server will
	// appear in the errServe channel and halt the process.
	return <-errServe
}

func setGRPCHeader(ctx context.Context, header, value string) {
	m := make(map[string]string, 1)
	m[header] = value
	err := grpc.SetHeader(ctx, metadata.New(m))
	if err != nil {
		logrus.WithError(err).Error("failed to set gRPC metadata header for cookie forwarding")
	}
}

// getGRPCHeader looksup a header on the grpc context.
func getGRPCHeader(ctx context.Context, grpcHeaderKey string) string {
	md, ok := runtime.ServerMetadataFromContext(ctx)
	if !ok {
		return ""
	}
	values := md.HeaderMD.Get(grpcHeaderKey)
	if len(values) < 1 {
		return ""
	}
	return values[0]
}
