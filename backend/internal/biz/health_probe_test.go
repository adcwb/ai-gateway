package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/adcwb/ai-gateway/internal/conf"
	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/pkg"
)

const testEncryptionKey = "01234567890123456789012345678901" // 32 bytes... trimmed below

func newTestGatewayForProbes(t *testing.T) (*GatewayUseCase, *miniredis.Miniredis, *gorm.DB) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	// A plain ":memory:" sqlite DB lives only as long as its connection; force a
	// single pooled connection so every query in this test sees the same schema.
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AIProvider{}, &model.AIGatewayRouterEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	router := NewRouterManager(rdb, db, nil, log.NewStdLogger(testWriter{t}))
	uc := NewGatewayUseCase(db, rdb, nil, nil, router, nil, nil,
		&conf.AI{}, &conf.System{EncryptionKey: testEncryptionKey[:32]}, log.NewStdLogger(testWriter{t}))
	return uc, mr, db
}

func seedProviderWithProbe(t *testing.T, uc *GatewayUseCase, db *gorm.DB, id uint, providerType string, baseURL string, probeEnabled bool) {
	t.Helper()
	encKey, err := pkg.EncryptAES("test-key", []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cfgJSON, _ := json.Marshal(breakerConfig{ActiveProbeEnabled: probeEnabled, ActiveProbeIntervalSec: 1})
	p := &model.AIProvider{
		Name: fmt.Sprintf("p%d", id), BaseURL: baseURL, ProviderType: providerType, APIKey: encKey,
		IsEnabled: true, Weight: 100, Models: datatypes.JSON([]byte(`[]`)),
		BreakerConfig: datatypes.JSON(cfgJSON),
	}
	p.ID = id
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
}

func TestBuildHealthProbeRequest_Dialects(t *testing.T) {
	cases := []struct {
		name       string
		provider   model.AIProvider
		wantMethod string
		wantPath   string
		wantHeader string
	}{
		{
			name:       "anthropic",
			provider:   model.AIProvider{BaseURL: "https://api.anthropic.com", ProviderType: model.ProviderTypeAnthropic},
			wantMethod: http.MethodGet, wantPath: "/v1/models", wantHeader: "x-api-key",
		},
		{
			name:       "gemini",
			provider:   model.AIProvider{BaseURL: "https://generativelanguage.googleapis.com", ProviderType: model.ProviderTypeGemini},
			wantMethod: http.MethodGet, wantPath: "/v1beta/models", wantHeader: "x-goog-api-key",
		},
		{
			name:       "openai_compatible",
			provider:   model.AIProvider{BaseURL: "https://api.openai.com/v1", ProviderType: model.ProviderTypeOpenAICompatible},
			wantMethod: http.MethodGet, wantPath: "/v1/models", wantHeader: "Authorization",
		},
		{
			name:       "azure_openai host-root fallback",
			provider:   model.AIProvider{BaseURL: "https://res.openai.azure.com/openai/deployments/gpt4", ProviderType: model.ProviderTypeAzureOpenAI},
			wantMethod: http.MethodGet, wantPath: "/", wantHeader: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &providerEntry{provider: tc.provider, apiKey: "secret"}
			req, err := buildHealthProbeRequest(context.Background(), entry)
			if err != nil {
				t.Fatalf("buildHealthProbeRequest: %v", err)
			}
			if req.Method != tc.wantMethod {
				t.Fatalf("method = %s, want %s", req.Method, tc.wantMethod)
			}
			if req.URL.Path != tc.wantPath {
				t.Fatalf("path = %s, want %s", req.URL.Path, tc.wantPath)
			}
			if tc.wantHeader != "" && req.Header.Get(tc.wantHeader) == "" {
				t.Fatalf("expected header %s to be set", tc.wantHeader)
			}
		})
	}
}

func TestParseBreakerConfig_DefaultsInterval(t *testing.T) {
	p := &model.AIProvider{BreakerConfig: datatypes.JSON([]byte(`{"activeProbeEnabled":true}`))}
	cfg := parseBreakerConfig(p)
	if !cfg.ActiveProbeEnabled {
		t.Fatal("expected activeProbeEnabled true")
	}
	if cfg.ActiveProbeIntervalSec != activeProbeDefaultIntervalSec {
		t.Fatalf("interval = %d, want default %d", cfg.ActiveProbeIntervalSec, activeProbeDefaultIntervalSec)
	}
}

// TestProbeProviderRecoversOpenBreaker exercises the exact gap active probing
// closes: a provider stuck open with no live traffic still recovers, driven
// only by the timer-fed TryPass/ReportResult pair against a fake upstream.
func TestProbeProviderRecoversOpenBreaker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	uc, mr, db := newTestGatewayForProbes(t)
	seedProviderWithProbe(t, uc, db, 1, model.ProviderTypeOpenAICompatible, upstream.URL, true)
	ctx := context.Background()

	// Force the breaker open, then rewind the cooldown so TryPass will hand
	// out a half-open probe slot immediately.
	for i := 0; i < breakerFailThreshold; i++ {
		uc.router.ReportResult(ctx, 1, AttemptRetryableError)
	}
	uc.router.resetStateCache()
	if uc.router.TryPass(ctx, 1) {
		t.Fatal("breaker should be open")
	}
	past := time.Now().Add(-time.Duration(breakerCooldownSec+5) * time.Second).Unix()
	mr.HSet(breakerKey(1), "opened_at", intToStr(past))
	uc.router.resetStateCache()

	// Drive enough probes to satisfy breakerProbeSuccesses and close the breaker —
	// exactly what the sweep loop would do on successive ticks with zero real traffic.
	for i := 0; i < breakerProbeSuccesses; i++ {
		uc.probeProvider(ctx, 1)
		uc.router.resetStateCache()
	}
	if got := uc.router.StateOf(ctx, 1); got != model.BreakerStateClosed {
		t.Fatalf("state after probe successes = %q, want closed", got)
	}
}

func TestSweepActiveProbes_SkipsClosedAndDisabled(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	uc, _, db := newTestGatewayForProbes(t)
	seedProviderWithProbe(t, uc, db, 1, model.ProviderTypeOpenAICompatible, upstream.URL, true)  // closed by default: skipped
	seedProviderWithProbe(t, uc, db, 2, model.ProviderTypeOpenAICompatible, upstream.URL, false) // disabled: skipped even if open
	ctx := context.Background()
	for i := 0; i < breakerFailThreshold; i++ {
		uc.router.ReportResult(ctx, 2, AttemptRetryableError)
	}
	uc.router.resetStateCache()

	uc.sweepActiveProbes(ctx, map[uint]time.Time{})
	time.Sleep(100 * time.Millisecond) // probes are fired via `go`

	if hits != 0 {
		t.Fatalf("expected zero probes (closed provider 1, disabled provider 2), got %d", hits)
	}
}
