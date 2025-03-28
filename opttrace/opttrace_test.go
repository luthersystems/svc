package opttrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
