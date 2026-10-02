// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	healthcheck "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/healthcheck/v1"
	"github.com/luthersystems/svc/opttrace"
	hellov1 "github.com/luthersystems/svc/oracle/testservice/gen/go/proto/hello/v1"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// fakeGateway stands in for the shiroclient gateway's health_check endpoint
// and records the trace context each request carries.
type fakeGateway struct {
	mu       sync.Mutex
	incoming []trace.SpanContext
	headers  []http.Header
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sc := trace.SpanContextFromContext(
		propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header)))
	g.mu.Lock()
	g.incoming = append(g.incoming, sc)
	g.headers = append(g.headers, r.Header.Clone())
	g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	type report struct {
		Timestamp      string `json:"timestamp"`
		Status         string `json:"status"`
		ServiceName    string `json:"service_name"`
		ServiceVersion string `json:"service_version"`
	}
	body := struct {
		Reports []report `json:"reports"`
	}{Reports: []report{{
		Timestamp:      time.Now().Format(timestampFormat),
		Status:         "UP",
		ServiceName:    "phylum",
		ServiceVersion: "v1",
	}}}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (g *fakeGateway) requests() []trace.SpanContext {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]trace.SpanContext(nil), g.incoming...)
}

func (g *fakeGateway) requestHeaders() []http.Header {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]http.Header(nil), g.headers...)
}

func newTracedGatewayOracle(t *testing.T) (*Oracle, *tracetest.InMemoryExporter, *fakeGateway) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	orc, gw := newGatewayOracle(t,
		opttrace.WithExporter(exp),
		opttrace.WithSyncExport(),
	)
	return orc, exp, gw
}

// newGatewayOracle builds an oracle whose phylum is a fake gateway, with
// tracing configured by traceOpts (none: tracing off).
func newGatewayOracle(t *testing.T, traceOpts ...opttrace.Option) (*Oracle, *fakeGateway) {
	t.Helper()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.GatewayEndpoint = srv.URL
	cfg.TraceOpts = traceOpts
	orc, err := newOracle(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = orc.tracer.Shutdown(context.Background()) })
	return orc, gw
}

func findSpan(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	for _, s := range spans {
		if s.Name == name {
			return s
		}
	}
	require.Failf(t, "span not found", "no span named %q in %d spans", name, len(spans))
	return tracetest.SpanStub{}
}

// TestHealthCheckPropagatesTrace pins luthersystems/svc#55: the phylum
// health check made under the HealthCheck span must carry that trace to the
// gateway and record a child span of HealthCheck.
func TestHealthCheckPropagatesTrace(t *testing.T) {
	orc, exp, gw := newTracedGatewayOracle(t)

	resp, err := orc.GetHealthCheck(context.Background(), &healthcheck.GetHealthCheckRequest{})
	require.NoError(t, err)
	for _, report := range resp.GetReports() {
		require.Equal(t, "UP", report.GetStatus(), report.GetServiceName())
	}

	spans := exp.GetSpans()
	hc := findSpan(t, spans, "HealthCheck")
	require.Equal(t, trace.SpanKindServer, hc.SpanKind)

	var children []tracetest.SpanStub
	for _, s := range spans {
		if s.Parent.SpanID() == hc.SpanContext.SpanID() {
			children = append(children, s)
		}
	}
	require.NotEmpty(t, children, "the phylum health check made no child span of HealthCheck")

	reqs := gw.requests()
	require.Len(t, reqs, 1)
	require.True(t, reqs[0].IsValid(), "gateway health_check request carried no trace context")
	require.Equal(t, hc.SpanContext.TraceID(), reqs[0].TraceID())
	parentIsChild := false
	for _, c := range children {
		if c.SpanContext.SpanID() == reqs[0].SpanID() {
			parentIsChild = true
		}
	}
	require.True(t, parentIsChild, "gateway request's parent span is not a child of HealthCheck")
}

// TestHealthCheckHandlerContinuesIncomingTrace checks that the HTTP health
// check endpoint, which bypasses the grpc server's otel handler, continues a
// trace whose context arrives in the request headers.
func TestHealthCheckHandlerContinuesIncomingTrace(t *testing.T) {
	orc, exp, _ := newTracedGatewayOracle(t)

	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0xa, 0xb, 0xc},
		SpanID:     trace.SpanID{0x1, 0x2, 0x3},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, healthCheckPath, nil)
	propagation.TraceContext{}.Inject(
		trace.ContextWithSpanContext(context.Background(), remote),
		propagation.HeaderCarrier(req.Header))
	rec := httptest.NewRecorder()
	orc.healthCheckHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	hc := findSpan(t, exp.GetSpans(), "HealthCheck")
	require.Equal(t, remote.TraceID(), hc.SpanContext.TraceID())
	require.Equal(t, remote.SpanID(), hc.Parent.SpanID())
}

// traceCaptureHello records the span context its handler runs under.
type traceCaptureHello struct {
	hellov1.UnimplementedHelloServiceServer

	got chan trace.SpanContext
}

func (s *traceCaptureHello) SayHello(ctx context.Context, req *hellov1.HelloRequest) (*hellov1.HelloResponse, error) {
	s.got <- trace.SpanContextFromContext(ctx)
	return &hellov1.HelloResponse{Greeting: "Hello, " + req.GetName()}, nil
}

// TestGRPCEndpointContinuesIncomingTrace checks that, with tracing
// configured, the oracle's grpc server continues a trace whose context
// arrives in the request metadata. This needs the global propagator that
// opttrace installs: the otel default propagates nothing.
func TestGRPCEndpointContinuesIncomingTrace(t *testing.T) {
	orc, exp, _ := newTracedGatewayOracle(t)

	srv := &traceCaptureHello{got: make(chan trace.SpanContext, 1)}
	grpcServer := orc.newGRPCServer()
	hellov1.RegisterHelloServiceServer(grpcServer, srv)
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0xd, 0xe, 0xf},
		SpanID:     trace.SpanID{0x4, 0x5, 0x6},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	md := metadata.MD{}
	propagation.TraceContext{}.Inject(
		trace.ContextWithSpanContext(context.Background(), remote),
		metadataCarrier(md))
	ctx := metadata.NewOutgoingContext(t.Context(), md)

	_, err = hellov1.NewHelloServiceClient(conn).SayHello(ctx, &hellov1.HelloRequest{Name: "trace"})
	require.NoError(t, err)

	handlerSpan := <-srv.got
	require.Equal(t, remote.TraceID(), handlerSpan.TraceID())

	var server tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.SpanContext.SpanID() == handlerSpan.SpanID() {
			server = s
		}
	}
	require.True(t, server.SpanContext.IsValid(), "no recorded span for the grpc handler")
	require.Equal(t, remote.SpanID(), server.Parent.SpanID())
}

// metadataCarrier adapts grpc metadata to a propagation.TextMapCarrier.
type metadataCarrier metadata.MD

func (c metadataCarrier) Get(key string) string {
	if v := metadata.MD(c).Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (c metadataCarrier) Set(key, value string) { metadata.MD(c).Set(key, value) }

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// TestHealthCheckTracingOffForwardsNoTrace checks that with tracing off the
// health check sends the gateway no trace headers, as before #55, even when
// the caller sent some and a global propagator is installed (by another
// oracle in the process, or another library).
func TestHealthCheckTracingOffForwardsNoTrace(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(opttrace.Propagator())
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	orc, gw := newGatewayOracle(t)

	ts, err := trace.ParseTraceState("vendor=value")
	require.NoError(t, err)
	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x1, 0x2, 0x3},
		SpanID:     trace.SpanID{0x7, 0x8, 0x9},
		TraceFlags: trace.FlagsSampled,
		TraceState: ts,
		Remote:     true,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, healthCheckPath, nil)
	propagation.TraceContext{}.Inject(
		trace.ContextWithSpanContext(context.Background(), remote),
		propagation.HeaderCarrier(req.Header))
	require.NotEmpty(t, req.Header.Get("tracestate"))
	rec := httptest.NewRecorder()
	orc.healthCheckHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	hdrs := gw.requestHeaders()
	require.Len(t, hdrs, 1)
	require.Empty(t, hdrs[0].Get("traceparent"), "tracing off forwarded traceparent")
	require.Empty(t, hdrs[0].Get("tracestate"), "tracing off forwarded tracestate")
}

// TestHealthCheckForwardsUnsampledTrace checks that, with tracing on, an
// unsampled caller trace still reaches the gateway, unsampled, so the
// gateway keeps the caller's sampling decision instead of starting a new
// root trace.
func TestHealthCheckForwardsUnsampledTrace(t *testing.T) {
	orc, _, gw := newTracedGatewayOracle(t)

	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{0x5, 0x6, 0x7},
		SpanID:  trace.SpanID{0x8, 0x9, 0xa},
		Remote:  true,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, healthCheckPath, nil)
	propagation.TraceContext{}.Inject(
		trace.ContextWithSpanContext(context.Background(), remote),
		propagation.HeaderCarrier(req.Header))
	require.NotEmpty(t, req.Header.Get("traceparent"))
	rec := httptest.NewRecorder()
	orc.healthCheckHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	reqs := gw.requests()
	require.Len(t, reqs, 1)
	require.True(t, reqs[0].IsValid(), "unsampled caller trace was not forwarded")
	require.Equal(t, remote.TraceID(), reqs[0].TraceID())
	require.False(t, reqs[0].IsSampled(), "forwarded trace became sampled")
}
