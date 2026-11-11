// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package opttrace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

type failingDetector struct{}

func (failingDetector) Detect(context.Context) (*resource.Resource, error) {
	return nil, errors.New("user: Current requires cgo or $USER set in environment")
}

// TestNewWithFailingDetector checks that a resource detector that fails does
// not stop New (svc#145): the tracer starts, its spans keep the service name,
// and the error goes to the OTel error handler.
func TestNewWithFailingDetector(t *testing.T) {
	orig := resourceOptions
	t.Cleanup(func() { resourceOptions = orig })
	resourceOptions = func(serviceName string) []resource.Option {
		return append(orig(serviceName), resource.WithDetectors(failingDetector{}))
	}

	var handled []error
	origHandler := otel.GetErrorHandler()
	t.Cleanup(func() { otel.SetErrorHandler(origHandler) })
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { handled = append(handled, err) }))

	exp := tracetest.NewInMemoryExporter()
	tr, err := New(context.Background(), "test-service", WithExporter(exp), WithSyncExport())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })

	_, span := tr.Span(context.Background(), "op")
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Resource.Attributes(), semconv.ServiceName("test-service"))
	require.Len(t, handled, 1)
	assert.ErrorContains(t, handled[0], "requires cgo")
}
