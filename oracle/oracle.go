// Copyright © 2024 Luther Systems, Ltd. All right reserved.

// Package oracle is a framework that provides a REST/JSON API defined using a
// GRPC spec, that communicates with the phylum.
package oracle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	healthcheck "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/healthcheck/v1"
	"github.com/luthersystems/shiroclient-sdk-go/shiroclient"
	"github.com/luthersystems/shiroclient-sdk-go/shiroclient/phylum"
	"github.com/luthersystems/svc/grpclogging"
	"github.com/luthersystems/svc/opttrace"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

const (
	// timestampFormat uses RFC3339 for all timestamps.
	timestampFormat = time.RFC3339

	// healthCheckPath is used to override health check functionality.
	// IMPORTANT: this must be kept in sync with api/srvpb/*proto
	healthCheckPath = "/v1/health_check"

	// swaggerPath is used to serve the current swagger json.
	// IMPORTANT: this must be kept in sync with api/swagger/*json
	swaggerPath = "/swagger.json"

	// metricsPath is used to serve prometheus metrics.
	// IMPORTANT: this should not be accessible externally
	metricsPath = "/metrics"

	// metricsAddr is the http addr the prometheus server listens on.
	metricsAddr = ":9600"
)

// DefaultConfig returns a default config.
func DefaultConfig() *Config {
	return &Config{
		Verbose:   true,
		EmulateCC: false,
		// IMPORTANT: Phylum bootstrap expects ListenAddress on :8080 for
		// FakeAuth IDP. Only change this if you know what you're doing!
		ListenAddress:     ":8080",
		PhylumPath:        "./phylum",
		PhylumServiceName: "phylum",
		ServiceName:       "oracle",
		RequestIDHeader:   "X-Request-ID",
		Version:           "v0.0.1",
	}
}

// Config configures an oracle.
type Config struct {
	// swaggerHandler configures an endpoint to serve the
	// swagger API.
	swaggerHandler http.Handler
	// ListenAddress is an address the oracle HTTP listens on.
	ListenAddress string `yaml:"listen-address"`
	// PhylumPath is the the path for the business logic.
	PhylumPath string `yaml:"phylum-path"`
	// GatewayEndpoint is an address to the shiroclient gateway.
	GatewayEndpoint string `yaml:"gateway-endpoint"`
	// PhylumServiceName is the app-specific name of the conneted phylum.
	PhylumServiceName string `yaml:"phylum-service-name"`
	// ServiceName is the app-specific name of the Oracle.
	ServiceName string `yaml:"service-name"`
	// RequestIDHeader is the HTTP header encoding the request ID.
	RequestIDHeader string `yaml:"request-id-header"`
	// Version is the oracle version.
	Version string `yaml:"version"`
	// TraceOpts are tracing options.
	TraceOpts []opttrace.Option
	// Verbose increases logging.
	Verbose bool `yaml:"verbose"`
	// EmulateCC emulates chaincode in memory (for testing).
	EmulateCC bool `yaml:"emulate-cc"`
}

// SetSwaggerHandler configures an endpoint to serve the swagger API.
func (c *Config) SetSwaggerHandler(h http.Handler) {
	if c == nil {
		return
	}
	c.swaggerHandler = h
}

// SetOTLPEndpoint is a helper to set the OTLP trace endpoint.
func (c *Config) SetOTLPEndpoint(endpoint string) {
	if c == nil || endpoint == "" {
		return
	}
	c.TraceOpts = append(c.TraceOpts, opttrace.WithOTLPExporter(endpoint))
}

// Valid validates an oracle configuration.
func (c *Config) Valid() error {
	if c == nil {
		return fmt.Errorf("missing phylum config")
	}
	if c.ListenAddress == "" {
		return fmt.Errorf("missing listen address")
	}
	if c.PhylumPath == "" {
		return fmt.Errorf("missing phylum path")
	}
	if c.PhylumServiceName == "" {
		return fmt.Errorf("missing phylum service name")
	}
	if c.ServiceName == "" {
		return fmt.Errorf("missing service name")
	}
	if c.RequestIDHeader == "" {
		return fmt.Errorf("missing request ID header")
	}
	if c.Version == "" {
		return fmt.Errorf("missing version")
	}
	return nil
}

type oracleState int

const (
	oracleStateInit oracleState = iota
	oracleStateStarted
	oracleStateStopped
	oracleStateTesting
)

// Oracle provides services.
type Oracle struct {
	swaggerHandler http.Handler

	// log provides logging.
	logBase *logrus.Entry

	// phylum interacts with phylum.
	phylum *phylum.Client

	// Optional application tracing provider
	tracer *opttrace.Tracer

	// txConfigs generates default transaction configs
	txConfigs func(context.Context, ...shiroclient.Config) []shiroclient.Config

	cachedPhylumVersion string

	cfg Config

	state oracleState

	//  stateMut guards state.
	stateMut sync.RWMutex

	// phylumVersionMut guards cachedPhylumVersion.
	phylumVersionMut sync.RWMutex
}

// option provides additional configuration to the oracle. Primarily for
// testing.
type option func(*Oracle) error

// withLogBase allows setting a custom base logger.
func withLogBase(logBase *logrus.Entry) option {
	return func(orc *Oracle) error {
		orc.logBase = logBase
		return nil
	}
}

// withPhylum connects to shiroclient gateway.
func withPhylum(gatewayEndpoint string) option {
	return func(orc *Oracle) error {
		ph, err := phylum.New(gatewayEndpoint, orc.logBase)
		if err != nil {
			return fmt.Errorf("unable to initialize phylum: %w", err)
		}

		ph.GetLogMetadata = grpclogging.GetLogrusFields
		orc.phylum = ph
		return nil
	}
}

// withMockPhylum runs the phylum in memory.
func withMockPhylum(path string) option {
	return withMockPhylumFrom(path, nil)
}

// withMockPhylumFrom runs the phylum in memory from a snapshot.
func withMockPhylumFrom(path string, r io.Reader) option {
	return func(orc *Oracle) error {
		orc.logBase.Infof("NewMock")
		ph, err := phylum.NewMockFrom(path, orc.logBase, r)
		if err != nil {
			return fmt.Errorf("unable to initialize mock phylum: %w", err)
		}
		ph.GetLogMetadata = grpclogging.GetLogrusFields
		orc.phylum = ph
		return nil
	}
}

// NewOracle allocates an oracle
func NewOracle(config *Config) (*Oracle, error) {
	return newOracle(config)
}

// newOracle constructs a new oracle.
func newOracle(config *Config, opts ...option) (*Oracle, error) {
	if config.Verbose {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if err := config.Valid(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if config.EmulateCC {
		opts = append(opts, withMockPhylum(config.PhylumPath))
	}
	err := config.Valid()
	if err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	oracle := &Oracle{
		cfg:            *config,
		swaggerHandler: config.swaggerHandler,
	}
	oracle.logBase = logrus.StandardLogger().WithFields(nil)
	for _, opt := range opts {
		err := opt(oracle)
		if err != nil {
			return nil, err
		}
	}
	if oracle.phylum == nil {
		if oracle.cfg.GatewayEndpoint == "" {
			oracle.cfg.GatewayEndpoint = fmt.Sprintf("http://shiroclient_gw_%s:8082", oracle.cfg.PhylumServiceName)
		}
		err := withPhylum(oracle.cfg.GatewayEndpoint)(oracle)
		if err != nil {
			return nil, err
		}
	}
	oracle.txConfigs = txConfigs()
	t, err := opttrace.New(context.Background(), "oracle", oracle.cfg.TraceOpts...)
	if err != nil {
		return nil, err
	}
	t.SetGlobalTracer()
	oracle.tracer = t

	return oracle, nil
}

func (orc *Oracle) log(ctx context.Context) *logrus.Entry {
	return grpclogging.GetLogrusEntry(ctx, orc.logBase)
}

func txConfigs() func(context.Context, ...shiroclient.Config) []shiroclient.Config {
	return func(ctx context.Context, extend ...shiroclient.Config) []shiroclient.Config {
		fields := grpclogging.GetLogrusFields(ctx)
		configs := []shiroclient.Config{
			shiroclient.WithLogrusFields(fields),
		}
		if fields["req_id"] != nil {
			logrus.WithField("req_id", fields["req_id"]).Debugf("setting request id")
			configs = append(configs, shiroclient.WithID(fmt.Sprint(fields["req_id"])))
		}
		configs = append(configs, extend...)
		return configs
	}
}

// setPhylumVersion sets the last seen phylum version and is concurrency safe.
func (orc *Oracle) setPhylumVersion(version string) {
	orc.phylumVersionMut.Lock()
	defer orc.phylumVersionMut.Unlock()
	orc.cachedPhylumVersion = version
	if orc.cachedPhylumVersion != "" {
		versionTotal.WithLabelValues(orc.cfg.ServiceName, orc.cfg.Version, orc.cfg.PhylumServiceName, orc.cachedPhylumVersion).Inc()
	}
}

// getLastPhylumVersion retrieves the last set phylum version and is concurrency safe.
func (orc *Oracle) getLastPhylumVersion() string {
	orc.phylumVersionMut.RLock()
	defer orc.phylumVersionMut.RUnlock()
	return orc.cachedPhylumVersion
}

func (orc *Oracle) phylumHealthCheck(ctx context.Context) []*healthcheck.HealthCheckReport {
	sopts := orc.txConfigs(ctx)
	ccHealth, err := orc.phylum.GetHealthCheck(ctx, []string{"phylum"}, sopts...)
	if err != nil && !errors.Is(err, context.Canceled) {
		return []*healthcheck.HealthCheckReport{{
			ServiceName:    orc.cfg.PhylumServiceName,
			ServiceVersion: "",
			Timestamp:      time.Now().Format(timestampFormat),
			Status:         "DOWN",
		}}
	}
	reports := ccHealth.GetReports()
	for _, report := range reports {
		if strings.EqualFold(report.GetServiceName(), orc.cfg.PhylumServiceName) {
			orc.setPhylumVersion(report.GetServiceVersion())
			break
		}
	}
	if orc.getLastPhylumVersion() == "" {
		orc.log(ctx).Warnf("missing phylum version")
	}
	return reports
}

// GetHealthCheck checks this service and all dependent services to construct a
// health report. Returns a grpc error code if a service is down.
func (orc *Oracle) GetHealthCheck(ctx context.Context, req *healthcheck.GetHealthCheckRequest) (*healthcheck.GetHealthCheckResponse, error) {
	// No ACL: Open to everyone
	healthy := true
	var reports []*healthcheck.HealthCheckReport
	if !req.GetHttpOnly() {
		reports = orc.phylumHealthCheck(ctx)
		for _, report := range reports {
			if !strings.EqualFold(report.GetStatus(), "UP") {
				healthy = false
				break
			}
		}
	}
	reports = append(reports, &healthcheck.HealthCheckReport{
		ServiceName:    orc.cfg.ServiceName,
		ServiceVersion: orc.cfg.Version,
		Timestamp:      time.Now().Format(timestampFormat),
		Status:         "UP",
	})
	resp := &healthcheck.GetHealthCheckResponse{
		Reports: reports,
	}
	if !healthy {
		reportsJSON, err := json.Marshal(resp)
		if err != nil {
			orc.log(ctx).WithError(err).Errorf("Oracle unhealthy")
		} else {
			orc.log(ctx).WithField("reports_json", string(reportsJSON)).Errorf("Oracle unhealthy")
		}
	}

	return resp, nil
}

// close blocks the caller until all spawned go routines complete, then closes the phylum
func (orc *Oracle) close() error {
	orc.stateMut.Lock()
	defer orc.stateMut.Unlock()
	if orc.state != oracleStateStarted && orc.state != oracleStateTesting {
		return fmt.Errorf("close: invalid oracle state: %d", orc.state)
	}
	orc.state = oracleStateStopped

	return orc.phylum.Close()
}

// Call calls the phylum.
func Call[K proto.Message, R proto.Message](s *Oracle, ctx context.Context, methodName string, req K, resp R, config ...shiroclient.Config) (R, error) {
	configs := s.txConfigs(ctx)
	configs = append(configs, config...)
	return phylum.Call(s.phylum, ctx, methodName, req, resp, configs...)
}
