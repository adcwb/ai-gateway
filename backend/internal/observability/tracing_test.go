package observability

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/opscenter/ai-gateway/internal/conf"
)

// TestSetupTracing_Disabled asserts the "zero overhead when off" contract
// (docs/design/05-observability.md): an empty/nil config never constructs an
// exporter or errors, and returns a harmless no-op shutdown.
func TestSetupTracing_Disabled(t *testing.T) {
	for _, cfg := range []*conf.Observability{nil, {}, {OTLPEndpoint: ""}} {
		shutdown, err := SetupTracing(context.Background(), cfg, log.DefaultLogger)
		if err != nil {
			t.Fatalf("SetupTracing(%+v) returned error: %v", cfg, err)
		}
		if shutdown == nil {
			t.Fatalf("SetupTracing(%+v) returned nil shutdown", cfg)
		}
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("no-op shutdown returned error: %v", err)
		}
	}
}

// TestForceSampler asserts the force-trace override: a ratio of 0 never
// samples on its own, but always samples once WithForceSample marks the
// parent context (the debug-header path in middleware/tracing.go).
func TestForceSampler(t *testing.T) {
	sampler := forceSampler{ratio: sdktrace.TraceIDRatioBased(0)}

	params := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       [16]byte{1},
	}
	if got := sampler.ShouldSample(params).Decision; got != sdktrace.Drop {
		t.Fatalf("expected Drop without force-sample, got %v", got)
	}

	params.ParentContext = WithForceSample(context.Background())
	if got := sampler.ShouldSample(params).Decision; got != sdktrace.RecordAndSample {
		t.Fatalf("expected RecordAndSample with force-sample, got %v", got)
	}
}

// TestTraceSpanIDFromContext_Empty asserts the audit-correlation helpers
// degrade to empty strings (not panics) when there is no recording span.
func TestTraceSpanIDFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	if got := TraceIDFromContext(ctx); got != "" {
		t.Fatalf("expected empty trace id, got %q", got)
	}
	if got := SpanIDFromContext(ctx); got != "" {
		t.Fatalf("expected empty span id, got %q", got)
	}
}
