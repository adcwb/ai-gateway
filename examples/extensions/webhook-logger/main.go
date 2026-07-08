// Command webhook-logger is a reference implementation of the webhook side
// of docs/design/09-extensibility.md "Delivery mechanisms" (b): it's the
// simplest possible server that satisfies the contract ai-gateway's
// internal/biz/extension.WebhookHook (pre_request/post_response) and
// internal/biz/eventbus.WebhookSink (on_audit/on_billing) both speak —
// verify the X-AIGW-Signature HMAC-SHA256 header, log what arrived, and
// always answer "pass". Point a real extension/event-sink integration at a
// copy of this and replace the logging with your own logic (SIEM export,
// approval flow, ERP sync, ...).
//
// Run: WEBHOOK_SECRET=changeme PORT=8090 go run .
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	addr := ":" + envOr("PORT", "8090")

	http.HandleFunc("/", handler(secret))

	log.Printf("webhook-logger listening on %s (signature check %s)", addr, enabledLabel(secret))
	log.Fatal(http.ListenAndServe(addr, nil))
}

// handler builds the request handler with secret bound in — factored out of
// main() so it's directly testable via httptest without a real listener.
func handler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		if secret != "" && !validSignature(secret, body, r.Header.Get("X-AIGW-Signature")) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		log.Printf("webhook-logger: received %d bytes: %s", len(body), string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// {"action":"pass"} satisfies both the hook-dispatcher contract
		// (extension.WebhookHook expects {action, patch?, labels?, reason?})
		// and is simply ignored by the event-bus sink contract (that one is
		// fire-and-forget: any 2xx status means "delivered").
		w.Write([]byte(`{"action":"pass"}`))
	}
}

func validSignature(secret string, body []byte, got string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func enabledLabel(secret string) string {
	if secret == "" {
		return "disabled (WEBHOOK_SECRET unset)"
	}
	return "enabled"
}
