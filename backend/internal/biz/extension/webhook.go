package extension

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

// WebhookHook calls an operator-run HTTP endpoint for pre_request/
// post_response (docs/design/09-extensibility.md "Delivery mechanisms" (b)
// webhook: "an extension is a URL + subscribed hooks + HMAC secret... Sync
// hooks POST the IR envelope and read back {action, patch...}"). This repo
// is the client only — see examples/extensions/webhook-logger for a
// reference server implementing the other side of this contract.
type WebhookHook struct {
	HookName   string
	URL        string
	HMACSecret string
	HTTPClient *http.Client
}

type webhookRequestBody struct {
	Point     HookPoint         `json:"point"`
	TenantID  uint              `json:"tenantId"`
	RequestID string            `json:"requestId"`
	IR        json.RawMessage   `json:"ir"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type webhookResponseBody struct {
	Action Action            `json:"action"`
	Patch  json.RawMessage   `json:"patch,omitempty"`
	Reason string            `json:"reason,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (h *WebhookHook) Name() string { return h.HookName }

func (h *WebhookHook) Handle(ctx context.Context, ev Event) (Result, error) {
	body, err := json.Marshal(webhookRequestBody{
		Point: ev.Point, TenantID: ev.TenantID, RequestID: ev.RequestID,
		IR: ev.IR, Labels: ev.Labels,
	})
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: encode request: %w", h.HookName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: build request: %w", h.HookName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(h.HMACSecret))
		mac.Write(body)
		req.Header.Set("X-AIGW-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	client := h.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: request failed: %w", h.HookName, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("extension %s: read response: %w", h.HookName, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("extension %s: HTTP %d: %s", h.HookName, resp.StatusCode, string(respBody))
	}

	var parsed webhookResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Result{}, fmt.Errorf("extension %s: invalid response body: %w", h.HookName, err)
	}
	return Result{Action: parsed.Action, Patch: parsed.Patch, Reason: parsed.Reason, Labels: parsed.Labels}, nil
}
