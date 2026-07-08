package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

func doProxyResponses(uc *GatewayUseCase, key *model.AIVirtualKey, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ai/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	uc.ProxyResponses(context.Background(), key, body, w, req)
	return w
}

// TestResponsesStore_ContinueRoundTrip covers the full previous_response_id/
// store flow end-to-end: a stored first turn's history must actually reach
// the upstream server on the second (continued) request.
func TestResponsesStore_ContinueRoundTrip(t *testing.T) {
	uc, db := newTestGatewayForHooks(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}

	var lastUpstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastUpstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"nice to meet you"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	}))
	defer upstream.Close()
	provider := seedHookTestProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1", ProviderID: provider.ID, TenantID: 999}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	first := doProxyResponses(uc, key, []byte(`{"model":"gpt-4o-mini","input":"my name is Alex","store":true}`))
	if first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d: %s", first.Code, first.Body.String())
	}
	var firstResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil || firstResp.ID == "" {
		t.Fatalf("expected a response id in the first reply: %v (%s)", err, first.Body.String())
	}

	var count int64
	db.Model(&model.AIResponseState{}).Where("response_id = ?", firstResp.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected the first turn to be stored under %q, found %d rows", firstResp.ID, count)
	}

	second := doProxyResponses(uc, key, []byte(`{"model":"gpt-4o-mini","input":"what is my name?","previous_response_id":"`+firstResp.ID+`"}`))
	if second.Code != http.StatusOK {
		t.Fatalf("second (continued) request: expected 200, got %d: %s", second.Code, second.Body.String())
	}

	var upstreamReq struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(lastUpstreamBody, &upstreamReq); err != nil {
		t.Fatalf("upstream body not JSON: %v\n%s", err, lastUpstreamBody)
	}
	joined := ""
	for _, m := range upstreamReq.Messages {
		joined += m.Role + ":" + m.Content + "\n"
	}
	if !jsonContains(joined, "my name is Alex") || !jsonContains(joined, "nice to meet you") || !jsonContains(joined, "what is my name?") {
		t.Fatalf("expected the continued request to include the full prior history, got:\n%s", joined)
	}
}

func TestResponsesStore_UnknownPreviousResponseIDRejected(t *testing.T) {
	uc, db := newTestGatewayForHooks(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should never be called for an unresolvable previous_response_id")
	}))
	defer upstream.Close()
	provider := seedHookTestProvider(t, db, sysCfg, upstream.URL)

	key := &model.AIVirtualKey{Name: "k2", ProviderID: provider.ID, TenantID: 999}
	db.Create(key)

	w := doProxyResponses(uc, key, []byte(`{"model":"gpt-4o-mini","input":"hi","previous_response_id":"resp_does_not_exist"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !jsonContains(w.Body.String(), "PREVIOUS_RESPONSE_NOT_FOUND") {
		t.Fatalf("expected a not-found error code, got %s", w.Body.String())
	}
}

func TestResponsesStore_WrongKeyCannotContinue(t *testing.T) {
	uc, db := newTestGatewayForHooks(t)
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	provider := seedHookTestProvider(t, db, sysCfg, upstream.URL)

	keyA := &model.AIVirtualKey{Name: "owner", ProviderID: provider.ID, TenantID: 999}
	keyB := &model.AIVirtualKey{Name: "intruder", ProviderID: provider.ID, TenantID: 999}
	db.Create(keyA)
	db.Create(keyB)

	first := doProxyResponses(uc, keyA, []byte(`{"model":"gpt-4o-mini","input":"secret","store":true}`))
	var firstResp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(first.Body.Bytes(), &firstResp)

	w := doProxyResponses(uc, keyB, []byte(`{"model":"gpt-4o-mini","input":"continue","previous_response_id":"`+firstResp.ID+`"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected a different key's continuation attempt to be rejected, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSweepExpiredResponseStates(t *testing.T) {
	_, db := newTestGatewayForHooks(t)
	expired := model.AIResponseState{ResponseID: "resp_old", VirtualKeyID: 1, ExpiresAt: time.Now().Add(-time.Hour)}
	fresh := model.AIResponseState{ResponseID: "resp_new", VirtualKeyID: 1, ExpiresAt: time.Now().Add(time.Hour)}
	db.Create(&expired)
	db.Create(&fresh)

	sweepExpiredResponseStates(context.Background(), db, log.NewHelper(log.NewStdLogger(testWriter{t})))

	var remaining []model.AIResponseState
	db.Find(&remaining)
	if len(remaining) != 1 || remaining[0].ResponseID != "resp_new" {
		t.Fatalf("expected only the fresh row to survive the sweep, got %+v", remaining)
	}
}
