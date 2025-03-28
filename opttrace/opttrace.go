package opttrace

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	tracerName                     = "opttrace"
	disableElpsFilteringTraceState = "disable_elps_filtering"
)

var noopTracerProvider = noop.NewTracerProvider()

// Tracer provides a tracing interface that generates traces only if configured
// with a trace exporter
type Tracer struct {
	exportTP *sdktrace.TracerProvider
}

// Option provides Tracer configuration options
type Option func(*config) error

type config struct {
	otlpEndpointURI string
	sampler         sdktrace.Sampler
	syncExport      bool
	batchOpts       []sdktrace.BatchSpanProcessorOption
	exporter        sdktrace.SpanExporter
}

// WithOTLPExporter configured an OTLP trace exporter
func WithOTLPExporter(endpointURI string) Option {
	return func(c *config) error {
		c.otlpEndpointURI = endpointURI
		return nil
	}
}

// WithSampler sets the sampler to be used by the underlying tracing
// provider. If not set, it takes the default of sampling based on whether the
// parent span was sampled.
func WithSampler(sampler sdktrace.Sampler) Option {
	return func(c *config) error {
		c.sampler = sampler
		return nil
	}
}

// WithBatchOptions allows overriding the default span batch processing options.
func WithBatchOptions(opts []sdktrace.BatchSpanProcessorOption) Option {
	return func(c *config) error {
		c.batchOpts = opts
		return nil
	}
}

// WithSyncExport can be used in tests and disables batch span processing.
func WithSyncExport() Option {
	return func(c *config) error {
		c.syncExport = true
		return nil
	}
}

// WithExporter sets a span exporter other than standard one provided by
// WithOTLPExporter.
func WithExporter(exp sdktrace.SpanExporter) Option {
	return func(c *config) error {
		c.exporter = exp
		return nil
	}
}

// New creates a Tracer that will create spans if configured with an exporter.
// If not, the Span method will use a no-op tracing provider. When enabled, the
// spans will have the supplied service name.  The context is used to initialize
// a configured OTLP exporter, if any.
func New(ctx context.Context, serviceName string, opts ...Option) (*Tracer, error) {
	c := &config{}
	for _, opt := range opts {
		err := opt(c)
		if err != nil {
			return nil, err
		}
	}
	var err error
	exp := c.exporter
	if exp == nil {
		if c.otlpEndpointURI == "" {
			return &Tracer{}, nil
		}
		exp, err = otlpExporter(ctx, c.otlpEndpointURI)
		if err != nil {
			return nil, err
		}
	}
	resources, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("resource lookup: %v", err)
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resources),
	}
	if c.sampler != nil {
		tpOpts = append(tpOpts, sdktrace.WithSampler(c.sampler))
	}
	if c.syncExport {
		tpOpts = append(tpOpts, sdktrace.WithSyncer(exp))
	} else {
		tpOpts = append(tpOpts,
			sdktrace.WithBatcher(exp, c.batchOpts...))
	}
	return &Tracer{
		exportTP: sdktrace.NewTracerProvider(tpOpts...),
	}, nil
}

func otlpExporter(ctx context.Context, traceURI string) (*otlptrace.Exporter, error) {
	u, err := url.Parse(traceURI)
	if err != nil {
		return nil, fmt.Errorf("invalid profiler endpoint URI: %v", err)
	}
	otlpOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(u.Host),
	}
	if strings.ToLower(u.Scheme) != "https" {
		otlpOpts = append(otlpOpts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, otlpOpts...)
}

// IsTraceContextWithoutELPSFilter determines if the context has elps filtering disabled.
func IsTraceContextWithoutELPSFilter(ctx context.Context) bool {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return false
	}

	traceState := spanCtx.TraceState()
	if value := traceState.Get(disableElpsFilteringTraceState); value == "true" {
		return true
	}

	return false
}

// TraceContextWithoutELPSFilter takes adds trace state into the propagated
// context to signify that elps filtering should be disabled for the request.
// NOTE: this results in very large traces.
func TraceContextWithoutELPSFilter(ctx context.Context) (context.Context, error) {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return ctx, fmt.Errorf("not trace context")
	}

	traceState := spanCtx.TraceState()
	if value := traceState.Get(disableElpsFilteringTraceState); value == "true" {
		return ctx, nil
	}

	newTraceState, err := traceState.Insert(disableElpsFilteringTraceState, "true")
	if err != nil {
		return ctx, fmt.Errorf("state insert: %w", err)
	}

	newSpanCtx := spanCtx.WithTraceState(newTraceState)
	return trace.ContextWithSpanContext(ctx, newSpanCtx), nil
}

// Span creates a new trace span and returns the supplied context with span
// added.  The returned span must be ended to avoid leaking resources.
func (t *Tracer) Span(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t == nil || t.exportTP == nil {
		return noopTracerProvider.Tracer(tracerName).Start(ctx, spanName, opts...)
	}
	tracer := t.exportTP.Tracer(tracerName)
	return tracer.Start(ctx, spanName, opts...)
}

// Shutdown releases all resources allocated by the tracing provider.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t != nil && t.exportTP != nil {
		return t.exportTP.Shutdown(ctx)
	}
	return nil
}

// SetGlobalTracer sets the global tracer provider to this tracer instance
func (t *Tracer) SetGlobalTracer() {
	if t != nil && t.exportTP != nil {
		otel.SetTracerProvider(t.exportTP)
	}
}
