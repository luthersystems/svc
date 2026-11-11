// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package opttrace

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

type failingDetector struct{}

// Detect fails as the process owner detector does in a static binary that
// runs as a uid with no /etc/passwd entry and no $USER (svc#145).
func (failingDetector) Detect(context.Context) (*resource.Resource, error) {
	return nil, errors.New("user: Current requires cgo or $USER set in environment")
}

func withOwnerDetector(o resource.Option) Option {
	return func(c *config) error {
		c.ownerDetector = o
		return nil
	}
}

// clearResourceEnv keeps the tests apart from the OTEL_* variables of the
// environment they run in.
func clearResourceEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
}

// A process owner that cannot be read does not stop New: the tracer starts,
// its spans keep the service name and have no process.owner, and a warning
// is logged.
func TestNewWithoutProcessOwner(t *testing.T) {
	clearResourceEnv(t)
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	exp := tracetest.NewInMemoryExporter()
	tr, err := New(context.Background(), "test-service",
		WithExporter(exp), WithSyncExport(),
		withOwnerDetector(resource.WithDetectors(failingDetector{})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })

	_, span := tr.Span(context.Background(), "op")
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	attrs := spans[0].Resource.Attributes()
	assert.Contains(t, attrs, semconv.ServiceName("test-service"))
	for _, a := range attrs {
		assert.NotEqual(t, semconv.ProcessOwnerKey, a.Key)
	}
	assert.Contains(t, attrs, semconv.ProcessPID(os.Getpid()))
	entry := hook.LastEntry()
	require.NotNil(t, entry)
	assert.Equal(t, logrus.WarnLevel, entry.Level)
	assert.ErrorContains(t, entry.Data[logrus.ErrorKey].(error), "requires cgo")
}

// The other detectors still fail fast: a malformed OTEL_RESOURCE_ATTRIBUTES
// stops New, as it did before.
func TestNewMalformedResourceAttributes(t *testing.T) {
	clearResourceEnv(t)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=prod,team")
	_, err := New(context.Background(), "test-service",
		WithExporter(tracetest.NewInMemoryExporter()), WithSyncExport())
	assert.ErrorContains(t, err, "resource lookup")
}

// With a process owner that can be read, the resource has process.owner,
// as resource.WithProcess() gave it.
func TestNewWithProcessOwner(t *testing.T) {
	clearResourceEnv(t)
	owner := resource.WithAttributes(semconv.ProcessOwner("tester"))
	res, err := newResource(context.Background(), "test-service", owner)
	require.NoError(t, err)
	assert.Contains(t, res.Attributes(), semconv.ProcessOwner("tester"))
	assert.Contains(t, res.Attributes(), semconv.ServiceName("test-service"))
}
