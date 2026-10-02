package opttrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestIsTraceContextWithoutELPSFilter(t *testing.T) {
	t.Run("returns false if no span context", func(t *testing.T) {
		ctx := context.Background()
		assert.False(t, IsTraceContextWithoutELPSFilter(ctx))
	})

	t.Run("returns false if trace state does not contain key", func(t *testing.T) {
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    [16]byte{1, 2, 3},
			SpanID:     [8]byte{4, 5, 6},
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), sc)
		assert.False(t, IsTraceContextWithoutELPSFilter(ctx))
	})

	t.Run("returns true if trace state contains disable_elps_filtering=true", func(t *testing.T) {
		ts, err := trace.ParseTraceState("disable_elps_filtering=true")
		require.NoError(t, err)

		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    [16]byte{1, 2, 3},
			SpanID:     [8]byte{4, 5, 6},
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
			TraceState: ts,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), sc)
		assert.True(t, IsTraceContextWithoutELPSFilter(ctx))
	})

	t.Run("returns false if trace state contains disable_elps_filtering=false", func(t *testing.T) {
		ts, err := trace.ParseTraceState("disable_elps_filtering=false")
		require.NoError(t, err)

		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    [16]byte{1, 2, 3},
			SpanID:     [8]byte{4, 5, 6},
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
			TraceState: ts,
		})
		ctx := trace.ContextWithSpanContext(context.Background(), sc)
		assert.False(t, IsTraceContextWithoutELPSFilter(ctx))
	})
}

// restoreOtelGlobals restores the global tracer provider and propagator
// when the test ends, so tests that install them leave no state behind.
func restoreOtelGlobals(t *testing.T) {
	t.Helper()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
}

// TestSetGlobalTracerPropagator checks that SetGlobalTracer installs the W3C
// trace context and baggage propagator only when tracing is configured.
func TestSetGlobalTracerPropagator(t *testing.T) {
	restoreOtelGlobals(t)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	ctx := context.Background()

	off, err := New(ctx, "test")
	require.NoError(t, err)
	assert.False(t, off.Enabled())
	off.SetGlobalTracer()
	assert.Empty(t, otel.GetTextMapPropagator().Fields(),
		"tracing off must leave the no-op default propagator")

	on, err := New(ctx, "test", WithExporter(tracetest.NewInMemoryExporter()), WithSyncExport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = on.Shutdown(ctx) })
	assert.True(t, on.Enabled())
	on.SetGlobalTracer()
	assert.ElementsMatch(t, Propagator().Fields(), otel.GetTextMapPropagator().Fields())
	assert.Contains(t, otel.GetTextMapPropagator().Fields(), "traceparent")
	assert.Contains(t, otel.GetTextMapPropagator().Fields(), "baggage")
}

// TestSetGlobalTracerKeepsAppPropagator checks that SetGlobalTracer does not
// replace a propagator the application installed.
func TestSetGlobalTracerKeepsAppPropagator(t *testing.T) {
	restoreOtelGlobals(t)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	ctx := context.Background()

	on, err := New(ctx, "test", WithExporter(tracetest.NewInMemoryExporter()), WithSyncExport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = on.Shutdown(ctx) })
	on.SetGlobalTracer()
	assert.ElementsMatch(t, propagation.TraceContext{}.Fields(), otel.GetTextMapPropagator().Fields(),
		"SetGlobalTracer replaced the application's propagator")
}
