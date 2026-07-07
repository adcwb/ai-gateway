package biz

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// These exercise anthropicResponseWriter/responsesResponseWriter in
// isolation (bypassing ProxyRequest/GatewayUseCase, which need a full
// DB+Redis+router harness) — the wrapper is what's genuinely new here; the
// translation functions it calls are already covered by
// protocol_anthropic_inbound_test.go / protocol_responses_test.go.

func TestAnthropicResponseWriter_NonStreamingSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	aw := newAnthropicResponseWriter(rec, "claude-sonnet")

	// Mirrors exactly what ProxyRequest's identity non-stream branch does.
	aw.Header().Set("Content-Type", "application/json")
	aw.WriteHeader(200)
	aw.Write([]byte(`{"id":"chatcmpl-1","choices":[{"finish_reason":"stop","message":{"content":"hi"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	aw.Close()

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if m["type"] != "message" || m["role"] != "assistant" {
		t.Fatalf("expected Anthropic-shape body, got: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestAnthropicResponseWriter_ErrorPathTranslated(t *testing.T) {
	rec := httptest.NewRecorder()
	aw := newAnthropicResponseWriter(rec, "claude-sonnet")

	// Mirrors ProxyRequest's PII-block / quota-exceeded inline JSON error paths.
	aw.Header().Set("Content-Type", "application/json")
	aw.WriteHeader(429)
	aw.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error","code":"QUOTA_EXCEEDED"}}`))
	aw.Close()

	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	var m struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Type != "error" || m.Error.Type != "rate_limit_error" || m.Error.Message != "rate limited" {
		t.Fatalf("expected Anthropic-shape error, got: %s", rec.Body.String())
	}
}

func TestAnthropicResponseWriter_Streaming(t *testing.T) {
	rec := httptest.NewRecorder()
	aw := newAnthropicResponseWriter(rec, "claude-sonnet")

	// Mirrors ProxyRequest's SSE branches: sets event-stream Content-Type,
	// WriteHeader(200), then many Write calls as upstream chunks arrive.
	aw.Header().Set("Content-Type", "text/event-stream")
	aw.WriteHeader(200)
	writeSSEChunk(aw, `{"choices":[{"delta":{"content":"Hi"},"finish_reason":null}]}`)
	writeSSEChunk(aw, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	aw.Write([]byte("data: [DONE]\n\n"))
	aw.Close()

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"event: message_start", `"text":"Hi"`, "event: message_stop"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in streamed output:\n%s", want, out)
		}
	}
}

func writeSSEChunk(w interface{ Write([]byte) (int, error) }, data string) {
	var buf bytes.Buffer
	buf.WriteString("data: ")
	buf.WriteString(data)
	buf.WriteString("\n\n")
	w.Write(buf.Bytes())
}
