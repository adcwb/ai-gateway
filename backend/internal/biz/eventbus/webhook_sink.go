package eventbus

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// WebhookSink batches events, HMAC-signs the batch, and POSTs it — "batched,
// HMAC-signed, at-least-once with retry/backoff" per docs/design/09-
// extensibility.md "Event bus". Retry/backoff itself lives in Bus.sinkPoller
// (the cursor only advances on a successful Deliver); this type just does
// one HTTP call per batch.
type WebhookSink struct {
	SinkName   string
	URL        string
	HMACSecret string
	HTTPClient *http.Client
}

func (s *WebhookSink) Name() string { return s.SinkName }

func (s *WebhookSink) Deliver(ctx context.Context, events []Event) error {
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("eventbus webhook sink %s: encode batch: %w", s.SinkName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("eventbus webhook sink %s: build request: %w", s.SinkName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(s.HMACSecret))
		mac.Write(body)
		req.Header.Set("X-AIGW-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("eventbus webhook sink %s: request failed: %w", s.SinkName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("eventbus webhook sink %s: HTTP %d: %s", s.SinkName, resp.StatusCode, string(snippet))
	}
	return nil
}
