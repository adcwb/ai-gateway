package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"github.com/adcwb/ai-gateway/internal/conf"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// -----------------------------------------------------------------------------
// Pure governance helpers: whitelist, content-block extraction, tools/list
// filtering — no DB/HTTP needed.
// -----------------------------------------------------------------------------

func TestToolWhitelist_EmptyMeansUnrestricted(t *testing.T) {
	key := &model.AIVirtualKey{}
	if !isToolAllowed(key, "anything") {
		t.Fatal("expected an absent whitelist to allow every tool")
	}
	key.ToolWhitelist = datatypes.JSON(`["search","fetch"]`)
	if !isToolAllowed(key, "search") {
		t.Fatal("expected a whitelisted tool to be allowed")
	}
	if isToolAllowed(key, "delete_everything") {
		t.Fatal("expected a non-whitelisted tool to be rejected")
	}
}

func TestExtractAndReplaceToolResultText(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"call 13800001234 now"}]`)
	if got := extractToolResultText(content); got != "call 13800001234 now" {
		t.Fatalf("unexpected extracted text: %q", got)
	}
	rewritten := replaceToolResultText(content, "call *** now")
	if got := extractToolResultText(rewritten); got != "call *** now" {
		t.Fatalf("unexpected rewritten text: %q", got)
	}
}

func TestReplaceToolResultText_MultiBlockLeftUntouched(t *testing.T) {
	content := json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)
	rewritten := replaceToolResultText(content, "redacted")
	if string(rewritten) != string(content) {
		t.Fatalf("expected multi-block content to be left untouched, got %s", rewritten)
	}
}

func TestFilterToolsList(t *testing.T) {
	key := &model.AIVirtualKey{ToolWhitelist: datatypes.JSON(`["search"]`)}
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search"},{"name":"delete_everything"}]}}`)
	filtered := filterToolsList(key, body)
	var rpcResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(filtered, &rpcResp); err != nil {
		t.Fatalf("unmarshal filtered response: %v", err)
	}
	if len(rpcResp.Result.Tools) != 1 || rpcResp.Result.Tools[0].Name != "search" {
		t.Fatalf("expected only the whitelisted tool to survive, got %+v", rpcResp.Result.Tools)
	}
}

func TestFilterToolsList_UnrestrictedPassesThrough(t *testing.T) {
	key := &model.AIVirtualKey{}
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search"},{"name":"anything"}]}}`)
	if string(filterToolsList(key, body)) != string(body) {
		t.Fatal("expected an unrestricted key's tools/list response to pass through unchanged")
	}
}

// -----------------------------------------------------------------------------
// HandleMCPRequest integration: real fake upstream MCP server + real (async)
// AuditWorker + real guardrail chain, mirroring the harness pattern already
// used for the PII/guardrail pipeline (pii_pipeline_test.go).
// -----------------------------------------------------------------------------

func newTestGatewayForMCP(t *testing.T) (*GatewayUseCase, *gorm.DB) {
	t.Helper()
	resetGuardrailCaches()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.AIMCPServer{}, &model.AIVirtualKey{}, &model.AIPIIPolicy{}, &model.AITenant{},
		&model.AIGatewayAuditLog{}, &model.AIGatewayAuditLogBody{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.AITenant{Name: model.DefaultTenantName, DisplayName: "Default", Status: "active"}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	audit := NewAuditWorker(db, nil, nil, &conf.Audit{}, sysCfg, log.NewStdLogger(testWriter{t}))
	audit.Start(context.Background())
	uc := NewGatewayUseCase(db, nil, nil, audit, nil, nil, nil, &conf.AI{AgentTimeoutSec: 5}, sysCfg, log.NewStdLogger(testWriter{t}))
	return uc, db
}

func seedMCPServer(t *testing.T, db *gorm.DB, baseURL string) {
	t.Helper()
	if err := db.Create(&model.AIMCPServer{Name: "test-server", BaseURL: baseURL, IsEnabled: true}).Error; err != nil {
		t.Fatalf("seed mcp server: %v", err)
	}
}

// waitForAuditRow polls the audit table (the batch worker flushes every
// 200ms) rather than sleeping a fixed duration — bounded at 2s.
func waitForAuditRow(t *testing.T, db *gorm.DB, model_ string) model.AIGatewayAuditLog {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var row model.AIGatewayAuditLog
		if err := db.Where("model = ?", model_).First(&row).Error; err == nil {
			return row
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for audit row with model=%q", model_)
	return model.AIGatewayAuditLog{}
}

func doMCPRequest(uc *GatewayUseCase, key *model.AIVirtualKey, serverName string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ai/mcp/"+serverName, bytes.NewReader(body))
	w := httptest.NewRecorder()
	uc.HandleMCPRequest(context.Background(), key, serverName, w, req)
	return w
}

func TestHandleMCPRequest_ToolsCallAllowedAndForwarded(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"sunny, 72F"}],"isError":false}}`))
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "k1", ToolWhitelist: datatypes.JSON(`["get_weather"]`)}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{"location":"Seattle"}}}`)
	w := doMCPRequest(uc, key, "test-server", reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got == "" {
		t.Fatal("expected a forwarded response body")
	}

	row := waitForAuditRow(t, db, "test-server/get_weather")
	if row.Protocol != "mcp" {
		t.Fatalf("expected protocol=mcp, got %q", row.Protocol)
	}
	if row.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 in audit row, got %d", row.StatusCode)
	}
}

func TestHandleMCPRequest_ToolNotWhitelistedRejected(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	calledUpstream := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledUpstream = true
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "k2", ToolWhitelist: datatypes.JSON(`["search"]`)}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	w := doMCPRequest(uc, key, "test-server", reqBody)
	if w.Code != http.StatusOK { // JSON-RPC errors still ride HTTP 200 by convention here
		t.Fatalf("unexpected HTTP status: %d", w.Code)
	}
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32001 {
		t.Fatalf("expected a tool-not-allowed JSON-RPC error, got %+v", resp.Error)
	}
	if calledUpstream {
		t.Fatal("expected the upstream server to never be called for a disallowed tool")
	}

	row := waitForAuditRow(t, db, "test-server/delete_everything")
	if row.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 in audit row, got %d", row.StatusCode)
	}
}

func TestHandleMCPRequest_UnknownServerRejected(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	key := &model.AIVirtualKey{Name: "k3"}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	w := doMCPRequest(uc, key, "does-not-exist", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleMCPRequest_ToolsListFiltered(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search"},{"name":"delete_everything"}]}}`))
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "k4", ToolWhitelist: datatypes.JSON(`["search"]`)}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	w := doMCPRequest(uc, key, "test-server", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); jsonContains(got, "delete_everything") {
		t.Fatalf("expected the disallowed tool to be filtered out of tools/list, got %s", got)
	}
	if got := w.Body.String(); !jsonContains(got, "search") {
		t.Fatalf("expected the whitelisted tool to remain, got %s", got)
	}
}

func jsonContains(body, substr string) bool {
	return strings.Contains(body, substr)
}

// -----------------------------------------------------------------------------
// Dedicated QuotaDimToolCall, batched JSON-RPC, GET/SSE stream — the three
// pieces the design doc's ADR addendum originally scoped out of the first
// MCP gateway round (docs/design/09-extensibility.md).
// -----------------------------------------------------------------------------

// newTestGatewayForMCPWithQuota mirrors newTestGatewayForMCP but wires a
// real (miniredis-backed) QuotaManager — needed to exercise
// CheckAndReserveToolCall, which touches Redis once a key sets a non-zero
// HourlyToolCallQuota.
func newTestGatewayForMCPWithQuota(t *testing.T) (*GatewayUseCase, *gorm.DB) {
	t.Helper()
	resetGuardrailCaches()
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
		&model.AIMCPServer{}, &model.AIVirtualKey{}, &model.AIPIIPolicy{}, &model.AITenant{},
		&model.AIGatewayAuditLog{}, &model.AIGatewayAuditLogBody{}, &model.AIGatewayQuotaEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.AITenant{Name: model.DefaultTenantName, DisplayName: "Default", Status: "active"}).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sysCfg := &conf.System{EncryptionKey: testEncryptionKey[:32]}
	audit := NewAuditWorker(db, nil, nil, &conf.Audit{}, sysCfg, log.NewStdLogger(testWriter{t}))
	audit.Start(context.Background())
	quota := NewQuotaManager(rdb, db, log.NewStdLogger(testWriter{t}))
	uc := NewGatewayUseCase(db, rdb, quota, audit, nil, nil, nil, &conf.AI{AgentTimeoutSec: 5}, sysCfg, log.NewStdLogger(testWriter{t}))
	return uc, db
}

func TestHandleMCPRequest_ToolCallQuotaExceeded(t *testing.T) {
	uc, db := newTestGatewayForMCPWithQuota(t)
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "quota-key", HourlyToolCallQuota: 1}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{}}}`)

	w1 := doMCPRequest(uc, key, "test-server", reqBody)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected first call to succeed, got %d: %s", w1.Code, w1.Body.String())
	}

	w2 := doMCPRequest(uc, key, "test-server", reqBody)
	if w2.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP status on second call: %d", w2.Code)
	}
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32003 {
		t.Fatalf("expected a tool-call-quota-exceeded JSON-RPC error, got %+v (body=%s)", resp.Error, w2.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected the upstream to be called exactly once (quota exhausted before the second forward), got %d", got)
	}
}

func TestHandleMCPRequest_BatchForwarded(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"%s done"}]}}`, string(req.ID), req.Params.Name)
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "batch-key"}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	batchBody := []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{}}},
		{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fetch","arguments":{}}}
	]`)
	w := doMCPRequest(uc, key, "test-server", batchBody)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var responses []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &responses); err != nil {
		t.Fatalf("expected a JSON array batch response, got %s: %v", w.Body.String(), err)
	}
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses in the batch, got %d", len(responses))
	}

	waitForAuditRow(t, db, "test-server/search")
	waitForAuditRow(t, db, "test-server/fetch")
}

func TestHandleMCPRequest_BatchOversizedRejected(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should never be called for an oversized batch")
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "oversized-key"}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	var msgs []string
	for i := 0; i < mcpMaxBatchSize+1; i++ {
		msgs = append(msgs, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i))
	}
	batchBody := []byte("[" + strings.Join(msgs, ",") + "]")

	w := doMCPRequest(uc, key, "test-server", batchBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized batch, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleMCPRequest_GETStreamsUpstreamSSE(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected the proxy to forward a GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress2\"}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	key := &model.AIVirtualKey{Name: "stream-key"}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ai/mcp/test-server", nil)
	w := httptest.NewRecorder()
	uc.HandleMCPRequest(context.Background(), key, "test-server", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected the upstream's content-type to be relayed, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "notifications/progress") || !strings.Contains(body, "notifications/progress2") {
		t.Fatalf("expected both SSE chunks to be relayed, got %q", body)
	}

	waitForAuditRow(t, db, "test-server/stream")
}

func TestHandleMCPRequest_GuardrailBlocksArguments(t *testing.T) {
	uc, db := newTestGatewayForMCP(t)
	calledUpstream := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledUpstream = true
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upstream.Close()
	seedMCPServer(t, db, upstream.URL)

	policy := &model.AIPIIPolicy{
		Name: "block-mobile", Enabled: true, Action: model.PIIActionBlock,
		CheckerChain: datatypes.JSON(`[{"name":"pii_rules","settings":{"detectors":{"cn_mobile":true}}}]`),
	}
	if err := db.Create(policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	key := &model.AIVirtualKey{Name: "k5", PIIPolicyID: &policy.ID}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send_sms","arguments":{"to":"13800001234"}}}`)
	w := doMCPRequest(uc, key, "test-server", reqBody)
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32002 {
		t.Fatalf("expected a guardrail-block JSON-RPC error, got %+v (body=%s)", resp.Error, w.Body.String())
	}
	if calledUpstream {
		t.Fatal("expected the upstream server to never be called once arguments are blocked")
	}
}
