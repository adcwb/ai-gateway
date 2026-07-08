package extension

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookHook_RoundTripAndSignature(t *testing.T) {
	const secret = "shh"
	var gotSig string
	var gotBody []byte

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-AIGW-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"action":"mutate","patch":{"x":1},"labels":{"k":"v"}}`))
	}))
	defer upstream.Close()

	h := &WebhookHook{HookName: "test", URL: upstream.URL, HMACSecret: secret}
	res, err := h.Handle(context.Background(), Event{Point: PreRequest, TenantID: 1, IR: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != ActionMutate || string(res.Patch) != `{"x":1}` {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Labels["k"] != "v" {
		t.Fatalf("expected labels to round-trip, got %+v", res.Labels)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	want := hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("HMAC signature mismatch: got %s want %s", gotSig, want)
	}
}

func TestWebhookHook_NonOKStatusIsError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	h := &WebhookHook{HookName: "test", URL: upstream.URL}
	if _, err := h.Handle(context.Background(), Event{}); err == nil {
		t.Fatal("expected an error for a non-200 upstream response")
	}
}
