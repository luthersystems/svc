// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package oracle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	healthcheck "buf.build/gen/go/luthersystems/protos/protocolbuffers/go/healthcheck/v1"
	"github.com/luthersystems/svc/opttrace"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// fakeGateway stands in for the shiroclient gateway's health_check endpoint
// and records the trace context each request carries.
type fakeGateway struct {
	mu       sync.Mutex
	incoming []trace.SpanContext
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sc := trace.SpanContextFromContext(
		propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header)))
	g.mu.Lock()
	g.incoming = append(g.incoming, sc)
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

func newTracedGatewayOracle(t *testing.T) (*Oracle, *tracetest.InMemoryExporter, *fakeGateway) {
	t.Helper()
	gw := &fakeGateway{}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)

	exp := tracetest.NewInMemoryExporter()
	cfg := DefaultConfig()
	cfg.GatewayEndpoint = srv.URL
	cfg.TraceOpts = []opttrace.Option{
		opttrace.WithExporter(exp),
		opttrace.WithSyncExport(),
	}
	orc, err := newOracle(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = orc.tracer.Shutdown(context.Background()) })
	return orc, exp, gw
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
