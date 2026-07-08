package biz

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/opscenter/ai-gateway/internal/biz/extension"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

// -----------------------------------------------------------------------------
// pre_request/post_response hook integration (docs/design/09-extensibility.md):
// exercises ProxyRequest end-to-end (real upstream via httptest, real
// miniredis-backed QuotaManager) with a Dispatcher wired in via
// SetHookDispatcher, asserting the hooks actually change the HTTP response —
// not just unit-testing the Dispatcher in isolation.
// -----------------------------------------------------------------------------

func newTestGatewayForHooks(t *testing.T) (*GatewayUseCase, *gorm.DB) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.AIProvider{}, &model.AIVirtualKey{}, &model.AIModelMapping{}, &model.AIModelItem{},
		&model.AICreditsRate{}, &model.AIGatewayAuditLog{}, &model.AIGatewayAuditLogBody{},
		&model.AIGatewayQuotaEvent{}, &model.AITenant{}, &model.AIResponseState{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	audit := NewAuditWorker(db, nil, nil, &conf.Audit{}, sysCfg, log.NewStdLogger(testWriter{t}))
	audit.Start(context.Background())
	quota := NewQuotaManager(rdb, db, log.NewStdLogger(testWriter{t}))
	uc := NewGatewayUseCase(db, rdb, quota, audit, nil, nil, nil, &conf.AI{AgentTimeoutSec: 5}, sysCfg, log.NewStdLogger(testWriter{t}))
	return uc, db
}

func seedHookTestProvider(t *testing.T, db *gorm.DB, sysCfg *conf.System, baseURL string) *model.AIProvider {
	t.Helper()
	encKey, err := pkg.EncryptAES("test-upstream-key", []byte(sysCfg.EncryptionKey))
	if err != nil {
		t.Fatalf("encrypt api key: %v", err)
	}
	p := &model.AIProvider{Name: "hook-provider", BaseURL: baseURL, ProviderType: model.ProviderTypeOpenAICompatible, APIKey: encKey, IsEnabled: true}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	return p
}

// fixedResultHook always returns the same Result regardless of the event —
// enough to prove the Dispatcher is actually wired into ProxyRequest.
type fixedResultHook struct {
	name   string
	result extension.Result
}

func (h *fixedResultHook) Name() string { return h.name }
func (h *fixedResultHook) Handle(context.Context, extension.Event) (extension.Result, error) {
	return h.result, nil
}

func doProxyRequest(uc *GatewayUseCase, key *model.AIVirtualKey, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ai/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	uc.ProxyRequest(context.Background(), key, body, w, req)
	return w
}

func TestProxyRequest_PreRequestHookRejects(t *testing.T) {
	uc, db := newTestGatewayForHooks(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should never be called once pre_request rejects the request")
	}))
	defer upstream.Close()
	provider := seedHookTestProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1", ProviderID: provider.ID, TenantID: 999}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	dispatcher := extension.NewDispatcher(nil)
	dispatcher.SetHooks([]extension.HookConfig{
		{Hook: &fixedResultHook{name: "rejector", result: extension.Result{Action: extension.ActionReject, Reason: "policy violation"}},
			Points: []extension.HookPoint{extension.PreRequest}, FailOpen: true},
	})
	uc.SetHookDispatcher(dispatcher)

	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	w := doProxyRequest(uc, key, body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !jsonContains(w.Body.String(), "EXTENSION_REJECTED") || !jsonContains(w.Body.String(), "policy violation") {
		t.Fatalf("expected the rejection reason in the response body, got %s", w.Body.String())
	}
}

func TestProxyRequest_PostResponseHookMutatesResponse(t *testing.T) {
	uc, db := newTestGatewayForHooks(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()
	provider := seedHookTestProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k2", ProviderID: provider.ID, TenantID: 999}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	dispatcher := extension.NewDispatcher(nil)
	patched := []byte(`{"hooked":true}`)
	dispatcher.SetHooks([]extension.HookConfig{
		{Hook: &fixedResultHook{name: "mutator", result: extension.Result{Action: extension.ActionMutate, Patch: patched}},
			Points: []extension.HookPoint{extension.PostResponse}, FailOpen: true},
	})
	uc.SetHookDispatcher(dispatcher)

	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	w := doProxyRequest(uc, key, body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(patched) {
		t.Fatalf("expected the post_response hook's patch to be what the client received, got %s", w.Body.String())
	}
}
