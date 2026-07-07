package biz

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz/bedrock"
)

// Bedrock outbound dialect (docs/design/02-protocol-adapters.md): v1 scope is
// Anthropic Claude models on Bedrock's native Invoke API only — other Bedrock
// model families (Titan/Llama/Mistral/Nova) have mutually incompatible invoke
// body shapes and are out of scope until there's demand for them.
//
// The invoke request body is Anthropic Messages JSON (built via the existing
// openAIToAnthropicRequest) minus "model"/"stream" (redundant with the URL
// path and the sync-vs-stream endpoint choice) plus a required top-level
// "anthropic_version". The sync invoke response body IS native Anthropic
// Messages JSON, so anthropicToOpenAIResponse is reused unchanged; the
// streaming endpoint wraps the same native Anthropic SSE event JSON inside
// AWS's binary event-stream framing, unwrapped by translateBedrockStream
// below before handing off to the also-unchanged translateAnthropicStream.

type bedrockCredentialBundle struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
}

// parseBedrockCredentials decodes the AK/SK/session-token bundle that a
// bedrock-type AIProvider stores JSON-encoded in its single encrypted APIKey
// column (docs/design/02-protocol-adapters.md ADR addendum: no new secret
// columns — this reuses the existing AES-256-GCM-at-rest pipeline).
func parseBedrockCredentials(rawAPIKey string) (bedrock.Credentials, error) {
	var b bedrockCredentialBundle
	if err := json.Unmarshal([]byte(rawAPIKey), &b); err != nil {
		return bedrock.Credentials{}, fmt.Errorf("bedrock provider APIKey must be JSON {accessKeyId,secretAccessKey,sessionToken}: %w", err)
	}
	if b.AccessKeyID == "" || b.SecretAccessKey == "" {
		return bedrock.Credentials{}, fmt.Errorf("bedrock provider APIKey missing accessKeyId/secretAccessKey")
	}
	return bedrock.Credentials{
		AccessKeyID:     b.AccessKeyID,
		SecretAccessKey: b.SecretAccessKey,
		SessionToken:    b.SessionToken,
	}, nil
}

// buildBedrockRequest builds a signed Bedrock InvokeModel(WithResponseStream)
// request from an OpenAI-shape chat body.
func buildBedrockRequest(ctx context.Context, entry *providerEntry, cfg adapterConfig, sendBody []byte, isStream bool) (*http.Request, error) {
	anthBody, err := openAIToAnthropicRequest(sendBody, isStream)
	if err != nil {
		return nil, fmt.Errorf("bedrock request translation: %w", err)
	}
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(anthBody, &bodyMap); err != nil {
		return nil, fmt.Errorf("bedrock request translation: %w", err)
	}
	modelID, _ := bodyMap["model"].(string)
	if modelID == "" {
		return nil, fmt.Errorf("bedrock request translation: missing model id")
	}
	delete(bodyMap, "model")
	delete(bodyMap, "stream")
	bodyMap["anthropic_version"] = "bedrock-2023-05-31"
	finalBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("bedrock request translation: %w", err)
	}

	creds, err := parseBedrockCredentials(entry.apiKey)
	if err != nil {
		return nil, err
	}

	action := "invoke"
	if isStream {
		action = "invoke-with-response-stream"
	}
	url := fmt.Sprintf("%s/model/%s/%s", strings.TrimRight(entry.provider.BaseURL, "/"), modelID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(finalBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if isStream {
		req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	}
	bedrock.SignRequest(req, finalBody, cfg.Region, "bedrock", creds, time.Now())
	return req, nil
}

// translateBedrockStream unwraps Bedrock's AWS event-stream binary framing
// (each frame's payload is {"bytes": base64(nativeAnthropicEventJSON)}) into
// plain "event: <type>\ndata: <json>\n\n" SSE text fed through a pipe, then
// hands off to the existing, already-tested translateAnthropicStream —
// avoiding a second, parallel implementation of the Anthropic SSE→OpenAI
// chunk state machine for what is, once unwrapped, identical event JSON.
func translateBedrockStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for {
			msg, err := bedrock.ReadMessage(body)
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(pw, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
				}
				return
			}
			if msg.Headers[":event-type"] != "chunk" {
				continue // :message-type "exception"/other control frames are not chunk data
			}
			var envelope struct {
				Bytes string `json:"bytes"`
			}
			if json.Unmarshal(msg.Payload, &envelope) != nil || envelope.Bytes == "" {
				continue
			}
			decoded, derr := base64.StdEncoding.DecodeString(envelope.Bytes)
			if derr != nil {
				continue
			}
			var typed struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(decoded, &typed) != nil || typed.Type == "" {
				continue
			}
			fmt.Fprintf(pw, "event: %s\ndata: %s\n\n", typed.Type, decoded)
		}
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	return translateAnthropicStream(w, scanner, modelName)
}
