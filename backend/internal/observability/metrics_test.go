package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewMetrics_ExposesRuntimeMetricsEvenIdle guards against the "why do I
// only see two metrics on a freshly booted instance" gap: this project uses
// its own isolated prometheus.Registry (for test isolation) rather than the
// global DefaultRegisterer, so unlike a typical promhttp.Handler() setup the
// standard Go runtime and process collectors are not registered for free —
// NewMetrics must wire them in explicitly. Every aigw_* metric besides
// AuditQueueDepth/ConcurrencySlots is a *Vec and, per normal client_golang
// behavior, only appears after its first .WithLabelValues(...) call — this
// test only asserts on the collectors that must be visible even with zero
// traffic.
func TestNewMetrics_ExposesRuntimeMetricsEvenIdle(t *testing.T) {
	m := NewMetrics(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"go_goroutines",
		"go_memstats_alloc_bytes",
		"process_start_time_seconds",
		"aigw_audit_queue_depth",
		"aigw_concurrency_slots",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics output on an idle instance, got:\n%s", want, body)
		}
	}
}

// A nil db must not panic NewMetrics or the resulting handler — this is the
// path every unit test that constructs a GatewayUseCase/RouterManager/
// BillingManager without a real database exercises indirectly.
func TestNewMetrics_NilDBIsSafe(t *testing.T) {
	m := NewMetrics(nil)
	if m == nil || m.Registry == nil {
		t.Fatal("expected a usable Metrics instance with a nil db")
	}
}
