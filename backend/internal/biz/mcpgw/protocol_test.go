package mcpgw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRequest(t *testing.T) {
	req, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != MethodToolsCall {
		t.Fatalf("unexpected method: %s", req.Method)
	}
}

func TestParseRequest_MissingMethod(t *testing.T) {
	if _, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1}`)); err == nil {
		t.Fatal("expected error for missing method")
	}
}

func TestParseRequest_InvalidJSON(t *testing.T) {
	if _, err := ParseRequest([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseToolCallParams(t *testing.T) {
	req, _ := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{"location":"Seattle"}}}`))
	params, err := ParseToolCallParams(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", params.Name)
	}
	if string(params.Arguments) != `{"location":"Seattle"}` {
		t.Fatalf("unexpected arguments: %s", params.Arguments)
	}
}

func TestParseToolCallParams_MissingName(t *testing.T) {
	req, _ := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	if _, err := ParseToolCallParams(req); err == nil {
		t.Fatal("expected error for missing tool name")
	}
}

func TestErrorResponse(t *testing.T) {
	raw := ErrorResponse(json.RawMessage(`1`), ErrCodeToolNotAllowed, "nope")
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeToolNotAllowed || resp.Error.Message != "nope" {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}
	if string(resp.ID) != "1" {
		t.Fatalf("expected id to round-trip, got %s", resp.ID)
	}
}

func TestClient_Forward_RoundTrip(t *testing.T) {
	var gotSessionID, gotAuth, gotAccept, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("Mcp-Session-Id")
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = buf
		w.Header().Set("Mcp-Session-Id", "srv-session-123")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "upstream-secret", HTTPClient: srv.Client()}
	result, err := client.Forward(context.Background(), "client-session-abc", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSessionID != "client-session-abc" {
		t.Fatalf("expected session id to be forwarded, got %q", gotSessionID)
	}
	if gotAuth != "Bearer upstream-secret" {
		t.Fatalf("expected upstream bearer auth, got %q", gotAuth)
	}
	if gotAccept != "application/json, text/event-stream" {
		t.Fatalf("unexpected Accept header: %q", gotAccept)
	}
	if gotContentType != "application/json" {
		t.Fatalf("unexpected Content-Type header: %q", gotContentType)
	}
	if string(gotBody) != `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` {
		t.Fatalf("unexpected forwarded body: %s", gotBody)
	}
	if result.SessionID != "srv-session-123" {
		t.Fatalf("expected server session id to be captured, got %q", result.SessionID)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", result.StatusCode)
	}
	if result.ContentType != "application/json" {
		t.Fatalf("unexpected content type: %q", result.ContentType)
	}
	var resp Response
	if err := json.Unmarshal(result.Body, &resp); err != nil {
		t.Fatalf("response body did not parse as JSON-RPC: %v", err)
	}
}

func TestClient_Forward_NoAPIKeyOmitsAuthHeader(t *testing.T) {
	var gotAuth string
	sawAuth := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuth = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
	if _, err := client.Forward(context.Background(), "", []byte(`{}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAuth {
		t.Fatalf("expected no Authorization header when APIKey is empty, got %q", gotAuth)
	}
}
