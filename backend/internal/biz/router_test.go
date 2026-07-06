package biz

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

func newTestRouter(t *testing.T) (*RouterManager, *miniredis.Miniredis, *gorm.DB) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AIProvider{}, &model.AIGatewayRouterEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rm := NewRouterManager(rdb, db, nil, log.NewStdLogger(testWriter{t}))
	return rm, mr, db
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }

// resetStateCache clears the local micro-cache so tests observe Redis truth.
func (rm *RouterManager) resetStateCache() {
	rm.stateCache.Range(func(k, _ any) bool { rm.stateCache.Delete(k); return true })
}

func TestBreakerOpensAfterThresholdFailures(t *testing.T) {
	rm, _, _ := newTestRouter(t)
	ctx := context.Background()

	for i := 0; i < breakerFailThreshold-1; i++ {
		rm.ReportResult(ctx, 1, AttemptRetryableError)
		rm.resetStateCache()
		if !rm.TryPass(ctx, 1) {
			t.Fatalf("breaker opened too early at failure %d", i+1)
		}
	}
	rm.ReportResult(ctx, 1, AttemptRetryableError)
	rm.resetStateCache()
	if rm.TryPass(ctx, 1) {
		t.Fatal("breaker must be open after threshold failures")
	}
	if got := rm.StateOf(ctx, 1); got != model.BreakerStateOpen {
		t.Fatalf("state = %q, want open", got)
	}
}

func TestBreakerSuccessResetsFailureCount(t *testing.T) {
	rm, _, _ := newTestRouter(t)
	ctx := context.Background()

	for i := 0; i < breakerFailThreshold-1; i++ {
		rm.ReportResult(ctx, 2, AttemptRetryableError)
	}
	rm.ReportResult(ctx, 2, AttemptSuccess) // resets window
	for i := 0; i < breakerFailThreshold-1; i++ {
		rm.ReportResult(ctx, 2, AttemptRetryableError)
	}
	rm.resetStateCache()
	if !rm.TryPass(ctx, 2) {
		t.Fatal("success should have reset the failure counter")
	}
}

func TestBreakerHalfOpenProbesAndRecovery(t *testing.T) {
	rm, mr, _ := newTestRouter(t)
	ctx := context.Background()

	for i := 0; i < breakerFailThreshold; i++ {
		rm.ReportResult(ctx, 3, AttemptRetryableError)
	}
	rm.resetStateCache()
	if rm.TryPass(ctx, 3) {
		t.Fatal("must be open")
	}

	// simulate cooldown elapsing by rewinding opened_at
	past := time.Now().Add(-time.Duration(breakerCooldownSec+5) * time.Second).Unix()
	mr.HSet(breakerKey(3), "opened_at", intToStr(past))

	rm.resetStateCache()
	if !rm.TryPass(ctx, 3) {
		t.Fatal("cooldown elapsed: must allow a half-open probe")
	}
	if got := rm.StateOf(ctx, 3); got != model.BreakerStateHalfOpen {
		t.Fatalf("state = %q, want half_open", got)
	}

	// enough probe successes close the breaker
	for i := 0; i < breakerProbeSuccesses; i++ {
		rm.ReportResult(ctx, 3, AttemptSuccess)
	}
	rm.resetStateCache()
	if got := rm.StateOf(ctx, 3); got != model.BreakerStateClosed {
		t.Fatalf("state after probe successes = %q, want closed", got)
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	rm, mr, _ := newTestRouter(t)
	ctx := context.Background()

	for i := 0; i < breakerFailThreshold; i++ {
		rm.ReportResult(ctx, 4, AttemptRetryableError)
	}
	past := time.Now().Add(-time.Duration(breakerCooldownSec+5) * time.Second).Unix()
	mr.HSet(breakerKey(4), "opened_at", intToStr(past))
	rm.resetStateCache()
	if !rm.TryPass(ctx, 4) {
		t.Fatal("expected half-open probe slot")
	}
	rm.ReportResult(ctx, 4, AttemptRetryableError)
	rm.resetStateCache()
	if rm.TryPass(ctx, 4) {
		t.Fatal("probe failure must reopen the breaker")
	}
}

func TestBreakerFailsOpenWithoutRedis(t *testing.T) {
	rm := NewRouterManager(nil, nil, nil, log.NewStdLogger(testWriter{t}))
	if !rm.TryPass(context.Background(), 9) {
		t.Fatal("no Redis ⇒ fail open (allow)")
	}
}

func intToStr(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// -----------------------------------------------------------------------------
// Candidate ordering
// -----------------------------------------------------------------------------

func seedProvider(t *testing.T, db *gorm.DB, id uint, name string, weight, priority int, models ...string) {
	t.Helper()
	type pm struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	}
	items := make([]pm, 0, len(models))
	for _, m := range models {
		items = append(items, pm{Name: m})
	}
	raw, _ := json.Marshal(items)
	p := &model.AIProvider{
		Name: name, BaseURL: "http://example", APIKey: "enc", IsEnabled: true,
		Weight: weight, Priority: priority, Models: datatypes.JSON(raw),
	}
	p.ID = id
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	// GORM omits zero-value fields on insert, letting the column default (100)
	// win — force the intended weight explicitly so weight-0 tests are honest.
	if err := db.Model(p).Update("weight", weight).Error; err != nil {
		t.Fatalf("seed weight: %v", err)
	}
}

func TestCandidatesPrimaryFirstAndModelFiltered(t *testing.T) {
	rm, _, db := newTestRouter(t)
	ctx := context.Background()
	seedProvider(t, db, 1, "a", 100, 0, "gpt-4o", "gpt-4o-mini")
	seedProvider(t, db, 2, "b", 100, 0, "gpt-4o")
	seedProvider(t, db, 3, "c", 100, 0, "other-model")

	cands := rm.Candidates(ctx, "gpt-4o", 1, StrategyWeighted)
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates (provider 3 lacks the model), got %d: %+v", len(cands), cands)
	}
	if cands[0].ProviderID != 1 {
		t.Fatalf("primary must come first, got %+v", cands)
	}
	if cands[1].ProviderID != 2 {
		t.Fatalf("fallback should be provider 2, got %+v", cands)
	}
}

func TestCandidatesZeroWeightIsDrained(t *testing.T) {
	rm, _, db := newTestRouter(t)
	seedProvider(t, db, 1, "a", 100, 0, "m")
	seedProvider(t, db, 2, "b", 0, 0, "m") // drained

	cands := rm.Candidates(context.Background(), "m", 1, StrategyWeighted)
	for _, c := range cands[1:] {
		if c.ProviderID == 2 {
			t.Fatal("weight-0 provider must not receive fallback traffic")
		}
	}
}

func TestCandidatesPriorityTiersOrdered(t *testing.T) {
	rm, _, db := newTestRouter(t)
	seedProvider(t, db, 1, "primary", 100, 0, "m")
	seedProvider(t, db, 2, "tier1", 100, 1, "m")
	seedProvider(t, db, 3, "tier0", 100, 0, "m")

	// run repeatedly: tier0 (priority 0) must always precede tier1 among fallbacks
	for i := 0; i < 20; i++ {
		cands := rm.Candidates(context.Background(), "m", 1, StrategyWeighted)
		if len(cands) != 3 {
			t.Fatalf("want 3 candidates, got %d", len(cands))
		}
		var pos2, pos3 int
		for idx, c := range cands {
			if c.ProviderID == 2 {
				pos2 = idx
			}
			if c.ProviderID == 3 {
				pos3 = idx
			}
		}
		if pos3 > pos2 {
			t.Fatalf("priority 0 provider must precede priority 1: %+v", cands)
		}
	}
}

func TestIsRetryableStatus(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 529} {
		if !IsRetryableStatus(code) {
			t.Fatalf("%d should be retryable", code)
		}
	}
	for _, code := range []int{200, 201, 400, 401, 403, 404, 422} {
		if IsRetryableStatus(code) {
			t.Fatalf("%d should NOT be retryable", code)
		}
	}
}
