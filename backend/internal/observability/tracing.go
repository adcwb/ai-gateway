// OpenTelemetry tracing setup (docs/design/05-observability.md, P2).
//
// Instrumentation call sites use the package-level Tracer directly rather
// than threading a trace.Tracer through the Wire graph. When tracing is
// disabled (SetupTracing never called, or OTLPEndpoint empty) the global
// OTel TracerProvider stays the SDK's no-op default, so every span created
// below costs a handful of no-op function calls — the "zero overhead when
// off" requirement from the design doc.
package observability

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/adcwb/ai-gateway/internal/conf"
)

// Tracer is the single tracer instance used across the gateway.
var Tracer = otel.Tracer("github.com/adcwb/ai-gateway")

const defaultSampleRatio = 0.01

type forceSampleCtxKey struct{}

// WithForceSample marks ctx so the sampler always records the span it seeds,
// regardless of the configured ratio. Set only after the force-trace header
// has been verified against the admin token (internal/middleware/tracing.go).
func WithForceSample(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceSampleCtxKey{}, true)
}

// forceSampler always-samples when WithForceSample marked the parent context,
// otherwise defers to the ratio sampler.
type forceSampler struct {
	ratio sdktrace.Sampler
}

func (s forceSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if force, _ := p.ParentContext.Value(forceSampleCtxKey{}).(bool); force {
		return sdktrace.AlwaysSample().ShouldSample(p)
	}
	return s.ratio.ShouldSample(p)
}

func (s forceSampler) Description() string { return "aigw.forceSampler" }

// SetupTracing wires the OTel SDK to an OTLP/gRPC exporter when configured.
// It never fails startup: exporter construction errors are logged and
// tracing stays disabled — tracing is an operational aid, not a security or
// economics control, so this follows the "fail open" side of design
// principle 3 rather than blocking boot.
func SetupTracing(ctx context.Context, cfg *conf.Observability, logger log.Logger) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if cfg == nil || cfg.OTLPEndpoint == "" {
		return noop, nil
	}
	helper := log.NewHelper(logger)

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exporter, expErr := otlptracegrpc.New(ctx, opts...)
	if expErr != nil {
		helper.Warnf("OTel exporter 初始化失败，追踪保持关闭 endpoint=%s err=%v", cfg.OTLPEndpoint, expErr)
		return noop, nil
	}

	res := sdkresource.NewSchemaless(
		attribute.String("service.name", "ai-gateway"),
	)

	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = defaultSampleRatio
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(forceSampler{ratio: sdktrace.TraceIDRatioBased(ratio)})),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	Tracer = otel.Tracer("github.com/adcwb/ai-gateway")

	helper.Infof("OTel 追踪已启用 endpoint=%s sampleRatio=%.4f", cfg.OTLPEndpoint, ratio)
	return tp.Shutdown, nil
}

// TraceIDFromContext extracts the current span's trace ID for audit-row
// correlation. Empty when there is no recording span (tracing disabled or
// the span was not sampled).
func TraceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanIDFromContext extracts the current span's span ID; see TraceIDFromContext.
func SpanIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}
