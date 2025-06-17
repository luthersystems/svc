package oracle

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testOracleService struct {
	orc *Oracle
}

func (s *testOracleService) RegisterServiceServer(grpcServer *grpc.Server) {
	hellov1.RegisterHelloServiceServer(grpcServer, &serverImpl{}) // Use your existing gRPC server implementation
}

func (s *testOracleService) RegisterServiceClient(ctx context.Context, grpcConn *grpc.ClientConn, mux *runtime.ServeMux) error {
	return hellov1.RegisterHelloServiceHandler(ctx, mux, grpcConn)
}

const (
	depTxCookie = "dep-tx"
)

func makeTestOracleServer(t *testing.T) (*Oracle, func()) {
	t.Helper()

	cfg := DefaultConfig()
	cfg.PhylumPath = "./testservice/phylum/"
	cfg.PhylumConfigPath = "./testservice/phylum/example_config.yaml"
	cfg.AddDepTxCookieForwarder(depTxCookie, int((5 * time.Minute).Seconds()), false, true)

	// NOTE: oracle.close called when StartGateway is canceled.
	orc, _ := NewTestOracle(t, cfg)

	// Define the gRPC configuration object implementing RegisterServiceServer & RegisterServiceClient
	grpcConfig := &testOracleService{orc: orc}

	ctx, cancel := context.WithCancel(context.Background())

	// Start the server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- orc.StartGateway(ctx, grpcConfig)
	}()

	// Wait for the server to start (replace with proper readiness check if needed)
	time.Sleep(50 * time.Millisecond)

	// Define cleanup function
	stop := func() {
		cancel() // Stop the server
		err := <-errCh
		if err != nil && err != context.Canceled {
			t.Fatalf("StartGateway returned an error: %v", err)
		}
	}

	return orc, stop
}

// serverImpl implements HelloServiceServer from hello.proto
type serverImpl struct {
	hellov1.UnimplementedHelloServiceServer

	nextID    int
	cookieFwd *CookieForwarder
	headerFwd *HeaderForwarder
}

// SayHello is the main RPC. We'll set a cookie & header here.
func (s *serverImpl) SayHello(ctx context.Context, req *hellov1.HelloRequest) (*hellov1.HelloResponse, error) {
	// Confirm we reached the method
	fmt.Println("Test: In SayHello, setting cookie & header forwarders")

	// Set cookie
	if s.cookieFwd != nil {
		s.cookieFwd.SetValue(ctx, "cookie-hello-value")
	}
	// Set header
	if s.headerFwd != nil {
		s.headerFwd.SetValue(ctx, "header-hello-value")
	}

	greeting := "Hello, " + req.GetName()
	return &hellov1.HelloResponse{Greeting: greeting}, nil
}

func (s *serverImpl) Ping(ctx context.Context, req *emptypb.Empty) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func TestCookieAndHeaderForwarders(t *testing.T) {
	// 1) Create an oracle.Config, adding forwarders
	cfg := &Config{}
	cf := cfg.AddCookieForwarder("myCookie", 3600, false, true)
	hf := cfg.AddHeaderForwarder("X-My-Header")

	// 2) Create our server that references these forwarders
	srv := &serverImpl{
		cookieFwd: cf,
		headerFwd: hf,
	}

	// 3) Spin up an in-process gRPC server on a random port
	grpcServer := grpc.NewServer()
	hellov1.RegisterHelloServiceServer(grpcServer, srv)

	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = grpcServer.Serve(grpcLis)
	}()

	// 4) Dial that gRPC server from the gateway
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcLis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	// 5) Construct a runtime.ServeMux with the forwarders from cfg
	gwMux := runtime.NewServeMux(cfg.gatewayOpts...)

	// 6) Register the auto-generated gateway for HelloService
	err = hellov1.RegisterHelloServiceHandler(ctx, gwMux, conn)
	require.NoError(t, err)

	// 7) Spin up an HTTP server to serve the gateway
	gwLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gwSrv := &http.Server{
		Handler:           gwMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = gwSrv.Serve(gwLis)
	}()

	// 8) Make an HTTP request that hits POST /v1/hello with a JSON body
	reqBody := bytes.NewBufferString(`{"name": "Bob"}`)
	resp, err := http.Post("http://"+gwLis.Addr().String()+"/v1/hello", "application/json", reqBody)
	require.NoError(t, err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			require.NoError(t, err)
		}
	}()

	// Should be 200 OK
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 9) Confirm the response has the header
	require.Equal(t, "header-hello-value", resp.Header.Get("X-My-Header"))

	// 10) Confirm the cookie
	var foundCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "myCookie" {
			foundCookie = c
			break
		}
	}
	require.NotNil(t, foundCookie, "Expected to find myCookie in the response")
	require.Equal(t, "cookie-hello-value", foundCookie.Value)

	// 11) Clean up
	grpcServer.Stop()
	_ = gwSrv.Close()
}
