// Package observability provides the Prometheus instrument set, the metrics
// listener, and the health/readiness checkers described in docs/design/05-observability.md.
//
// Cardinality rule: labels may include provider, model and coarse status_class —
// never virtual_key or request_id (per-key detail is the audit system's job).
package observability

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

// Metrics is the gateway instrument set, registered on its own registry so
// tests can construct isolated instances.
type Metrics struct {
	Registry *prometheus.Registry

	RequestsTotal      *prometheus.CounterVec // route, status_class
	RequestDuration    *prometheus.HistogramVec
	UpstreamAttempts   *prometheus.CounterVec // provider, outcome
	FailoverTotal      *prometheus.CounterVec // from_provider, to_provider
	BreakerState       *prometheus.GaugeVec   // provider → 0 closed / 1 half-open / 2 open
	TokensTotal        *prometheus.CounterVec // provider, model, token_class
	QuotaRejections    *prometheus.CounterVec // dimension
	BillingRejections  *prometheus.CounterVec // reason: suspended / insufficient_balance / budget_alert
	CacheRequests      *prometheus.CounterVec // cache_type, outcome: hit / miss / bypass
	GuardrailActions   *prometheus.CounterVec // detector, action
	KeyCacheHits       *prometheus.CounterVec // level: l1 / l2 / db
	AuditQueueDepth    prometheus.Gauge
	ConcurrencySlots   prometheus.Gauge
}

// NewMetrics constructs and registers the instrument set.
//
// Note on why a fresh scrape of an idle instance looks sparse: every
// aigw_* metric below except AuditQueueDepth/ConcurrencySlots is a *Vec
// (CounterVec/HistogramVec/GaugeVec) — the Prometheus client library does
// not emit a time series for a label combination until .WithLabelValues(...)
// is actually called with it, so e.g. aigw_requests_total only appears once
// at least one proxy request has completed. This is standard client_golang
// behavior, not a bug; it self-resolves once real traffic flows.
//
// db is optional (nil-safe) and used only to register client_golang's
// DBStatsCollector (open/in-use/idle connections, wait count/duration) —
// this project uses its own isolated prometheus.Registry (for test
// isolation) rather than the global DefaultRegisterer, so unlike a typical
// promhttp.Handler() setup, the standard Go runtime and process collectors
// (goroutines, GC pauses, heap size, CPU seconds, RSS, open FDs, ...) are
// NOT registered automatically and must be added explicitly below.
func NewMetrics(db *gorm.DB) *Metrics {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	if db != nil {
		if sqlDB, err := db.DB(); err == nil {
			reg.MustRegister(collectors.NewDBStatsCollector(sqlDB, "primary"))
		}
	}

	m := &Metrics{
		Registry: reg,
		RequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_requests_total",
			Help: "Proxy requests by route and status class.",
		}, []string{"route", "status_class"}),
		RequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aigw_request_duration_seconds",
			Help:    "End-to-end proxy latency.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"provider", "model"}),
		UpstreamAttempts: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_upstream_attempts_total",
			Help: "Upstream attempts by provider and outcome (success / retryable_error / fatal_error).",
		}, []string{"provider", "outcome"}),
		FailoverTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_failover_total",
			Help: "Failovers between providers.",
		}, []string{"from_provider", "to_provider"}),
		BreakerState: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "aigw_breaker_state",
			Help: "Circuit breaker state per provider: 0 closed, 1 half-open, 2 open.",
		}, []string{"provider"}),
		TokensTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_tokens_total",
			Help: "Tokens by provider, model and class (input / output / cache_read).",
		}, []string{"provider", "model", "token_class"}),
		QuotaRejections: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_quota_rejections_total",
			Help: "Requests rejected by quota dimension.",
		}, []string{"dimension"}),
		BillingRejections: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_billing_rejections_total",
			Help: "Billing gate outcomes (suspended / insufficient_balance / budget_alert).",
		}, []string{"reason"}),
		CacheRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_cache_requests_total",
			Help: "Response-cache lookups by type and outcome.",
		}, []string{"cache_type", "outcome"}),
		GuardrailActions: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_guardrail_actions_total",
			Help: "Guardrail/PII detector findings by action.",
		}, []string{"detector", "action"}),
		KeyCacheHits: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "aigw_key_cache_hits_total",
			Help: "Virtual-key resolution by cache level (l1 / l2 / db).",
		}, []string{"level"}),
		AuditQueueDepth: factory.NewGauge(prometheus.GaugeOpts{
			Name: "aigw_audit_queue_depth",
			Help: "Pending entries in the async audit queue.",
		}),
		ConcurrencySlots: factory.NewGauge(prometheus.GaugeOpts{
			Name: "aigw_concurrency_slots",
			Help: "Concurrency slots currently reserved.",
		}),
	}
	return m
}

// Handler returns the /metrics HTTP handler for the metrics listener.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// StatusClass maps an HTTP status code to a coarse label ("2xx", "4xx", ...).
func StatusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}

// -----------------------------------------------------------------------------
// Health / readiness
// -----------------------------------------------------------------------------

// Pinger is anything that can be health-checked (DB, Redis).
type Pinger func(ctx context.Context) error

// ReadyChecker runs dependency pings with a short timeout and caches the
// result briefly so probes cannot stampede the dependencies.
type ReadyChecker struct {
	mu       sync.Mutex
	pingers  map[string]Pinger
	cachedAt time.Time
	cached   map[string]error
	// ShuttingDown is flipped during graceful shutdown so LBs drain first.
	ShuttingDown bool
}

const (
	readyPingTimeout = 1 * time.Second
	readyCacheTTL    = 2 * time.Second
)

func NewReadyChecker(pingers map[string]Pinger) *ReadyChecker {
	return &ReadyChecker{pingers: pingers}
}

// Check returns per-dependency errors (nil error = healthy), cached for readyCacheTTL.
func (rc *ReadyChecker) Check(ctx context.Context) map[string]error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if time.Since(rc.cachedAt) < readyCacheTTL && rc.cached != nil {
		return rc.cached
	}
	results := make(map[string]error, len(rc.pingers))
	for name, ping := range rc.pingers {
		pctx, cancel := context.WithTimeout(ctx, readyPingTimeout)
		results[name] = ping(pctx)
		cancel()
	}
	rc.cachedAt = time.Now()
	rc.cached = results
	return results
}
