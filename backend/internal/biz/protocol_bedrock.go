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

	"github.com/adcwb/ai-gateway/internal/biz/bedrock"
)

// Bedrock outbound dialect (docs/design/02-protocol-adapters.md): five model
// families, each with its own mutually incompatible native invoke body shape
// — Anthropic Claude, Amazon Titan Text, Meta Llama, Mistral AI (text-
// completion invoke), and Amazon Nova. bedrockModelFamily detects which one
// a request targets from the model ID (substring match, so a cross-region
// inference-profile prefix like "us."/"eu." in front of the vendor segment
// doesn't need to be stripped first) and buildBedrockRequest/
// bedrockToOpenAIResponse/translateBedrockStream all dispatch on it.
//
// Claude's invoke request body is Anthropic Messages JSON (built via the
// existing openAIToAnthropicRequest) minus "model"/"stream" (redundant with
// the URL path and the sync-vs-stream endpoint choice) plus a required
// top-level "anthropic_version". The sync invoke response body IS native
// Anthropic Messages JSON, so anthropicToOpenAIResponse is reused unchanged;
// the streaming endpoint wraps the same native Anthropic SSE event JSON
// inside AWS's binary event-stream framing, unwrapped by
// translateBedrockStream before handing off to the also-unchanged
// translateAnthropicStream. The other four families' request/response/
// stream-chunk shapes are documented in their own protocol_bedrock_*.go
// files, each confirmed against AWS's official Bedrock documentation.
//
// Scope cuts (see docs/design/02-protocol-adapters.md ADR addendum): the four
// new families are text-only — no tool/function calling (three of them have
// no native tool-calling primitive in their raw invoke API at all; Nova does,
// via toolConfig, but supporting it only for Nova would be asymmetric) and no
// multimodal content, same posture as the existing Claude adapter. Nova's
// native InvokeModelWithResponseStream chunk event shape is a documented
// assumption (confirmed for the Converse API, not independently confirmed
// for native Invoke — no AWS account is available in this environment to
// verify live).

type bedrockFamily string

const (
	bedrockFamilyClaude  bedrockFamily = "claude"
	bedrockFamilyTitan   bedrockFamily = "titan"
	bedrockFamilyLlama   bedrockFamily = "llama"
	bedrockFamilyMistral bedrockFamily = "mistral"
	bedrockFamilyNova    bedrockFamily = "nova"
)

// bedrockModelFamily classifies a Bedrock model ID by vendor segment.
func bedrockModelFamily(modelID string) bedrockFamily {
	switch {
	case strings.Contains(modelID, "amazon.titan"):
		return bedrockFamilyTitan
	case strings.Contains(modelID, "meta.llama"):
		return bedrockFamilyLlama
	case strings.Contains(modelID, "mistral."):
		return bedrockFamilyMistral
	case strings.Contains(modelID, "amazon.nova"):
		return bedrockFamilyNova
	default:
		return bedrockFamilyClaude
	}
}

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
// request from an OpenAI-shape chat body, dispatching the body construction
// by model family — everything after that (signing, URL, headers) is shared.
func buildBedrockRequest(ctx context.Context, entry *providerEntry, cfg adapterConfig, sendBody []byte, isStream bool) (*http.Request, error) {
	modelID := extractModel(sendBody)

	var finalBody []byte
	var err error
	switch bedrockModelFamily(modelID) {
	case bedrockFamilyTitan:
		finalBody, err = buildTitanRequestBody(sendBody)
	case bedrockFamilyLlama:
		finalBody, err = buildLlamaRequestBody(sendBody)
	case bedrockFamilyMistral:
		finalBody, err = buildMistralRequestBody(sendBody)
	case bedrockFamilyNova:
		finalBody, err = buildNovaRequestBody(sendBody)
	default:
		finalBody, modelID, err = buildClaudeBedrockRequestBody(sendBody, isStream)
	}
	if err != nil {
		return nil, err
	}
	if modelID == "" {
		return nil, fmt.Errorf("bedrock request translation: missing model id")
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

// buildClaudeBedrockRequestBody is the original (pre-multi-family) Claude
// Bedrock body construction, extracted unchanged: Anthropic Messages JSON
// (via openAIToAnthropicRequest) minus "model"/"stream" plus a required
// "anthropic_version".
func buildClaudeBedrockRequestBody(sendBody []byte, isStream bool) (finalBody []byte, modelID string, err error) {
	anthBody, err := openAIToAnthropicRequest(sendBody, isStream)
	if err != nil {
		return nil, "", fmt.Errorf("bedrock request translation: %w", err)
	}
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(anthBody, &bodyMap); err != nil {
		return nil, "", fmt.Errorf("bedrock request translation: %w", err)
	}
	modelID, _ = bodyMap["model"].(string)
	if modelID == "" {
		return nil, "", fmt.Errorf("bedrock request translation: missing model id")
	}
	delete(bodyMap, "model")
	delete(bodyMap, "stream")
	bodyMap["anthropic_version"] = "bedrock-2023-05-31"
	finalBody, err = json.Marshal(bodyMap)
	if err != nil {
		return nil, "", fmt.Errorf("bedrock request translation: %w", err)
	}
	return finalBody, modelID, nil
}

// bedrockToOpenAIResponse dispatches a Bedrock InvokeModel sync response to
// the right family parser. Signature matches anthropicToOpenAIResponse's
// (cacheCreation included) since gateway.go unpacks all Bedrock traffic —
// whichever family — through the same six return values.
func bedrockToOpenAIResponse(raw []byte, modelName string) (translated []byte, prompt, completion, cacheRead, cacheCreation int, err error) {
	switch bedrockModelFamily(modelName) {
	case bedrockFamilyTitan:
		return titanToOpenAIResponse(raw, modelName)
	case bedrockFamilyLlama:
		return llamaToOpenAIResponse(raw, modelName)
	case bedrockFamilyMistral:
		return mistralToOpenAIResponse(raw, modelName)
	case bedrockFamilyNova:
		return novaToOpenAIResponse(raw, modelName)
	default:
		return anthropicToOpenAIResponse(raw, modelName)
	}
}

// translateBedrockStream unwraps Bedrock's AWS event-stream binary framing
// and dispatches to the right family's streaming translator.
func translateBedrockStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
	switch bedrockModelFamily(modelName) {
	case bedrockFamilyTitan:
		return translateTitanStream(w, body, modelName)
	case bedrockFamilyLlama:
		return translateLlamaStream(w, body, modelName)
	case bedrockFamilyMistral:
		return translateMistralStream(w, body, modelName)
	case bedrockFamilyNova:
		return translateNovaStream(w, body, modelName)
	default:
		return translateClaudeBedrockStream(w, body, modelName)
	}
}

// bedrockNextChunk reads one AWS event-stream frame and returns the decoded
// inner chunk JSON (the SDK-level {"bytes": base64(...)} envelope every
// Bedrock InvokeModelWithResponseStream frame carries, regardless of model
// family, already unwrapped) — shared by all five families' stream
// translators. Returns (nil, nil) for a frame the caller should just skip
// (a non-"chunk" control frame, or one that doesn't decode), and (nil,
// io.EOF) at the natural end of the stream.
func bedrockNextChunk(body io.Reader) ([]byte, error) {
	for {
		msg, err := bedrock.ReadMessage(body)
		if err != nil {
			return nil, err
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
		return decoded, nil
	}
}

// translateClaudeBedrockStream is the original (pre-multi-family) Claude
// stream translation, extracted unchanged: unwrap into plain
// "event: <type>\ndata: <json>\n\n" SSE text fed through a pipe, then hand
// off to the existing, already-tested translateAnthropicStream — avoiding a
// second, parallel implementation of the Anthropic SSE→OpenAI chunk state
// machine for what is, once unwrapped, identical event JSON.
func translateClaudeBedrockStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for {
			decoded, err := bedrockNextChunk(body)
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(pw, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
				}
				return
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

// -----------------------------------------------------------------------------
// Shared helpers used by the four new (Titan/Llama/Mistral/Nova) family files
// -----------------------------------------------------------------------------

// extractStopSequences handles OpenAI's "stop" field, which may be a single
// string or an array of strings — same shape openAIToAnthropicRequest
// already parses inline; factored out here since four new call sites need it.
func extractStopSequences(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// buildOpenAIChatResponse builds a minimal OpenAI chat.completion body —
// shared by all four new families' sync response parsers, which all reduce
// to "one piece of assistant text + a finish reason + token counts" (none of
// them support tool calls in this gateway's v1 scope).
func buildOpenAIChatResponse(modelName, id, content, finishReason string, promptTokens, completionTokens int) ([]byte, error) {
	out := map[string]interface{}{
		"id": id, "object": "chat.completion", "model": modelName,
		"choices": []map[string]interface{}{{
			"index":         0,
			"message":       map[string]interface{}{"role": "assistant", "content": content},
			"finish_reason": finishReason,
		}},
		"usage": map[string]interface{}{
			"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
			"total_tokens": promptTokens + completionTokens,
		},
	}
	return json.Marshal(out)
}

// newBedrockOpenAIChunkWriter returns a writeChunk closure matching the exact
// chat.completion.chunk shape translateGeminiStream already uses, plus a
// finishStream closure that writes the terminal "data: [DONE]" marker —
// shared by the four new families' stream translators, all of which produce
// a flat text delta per chunk (unlike Claude/Gemini's richer event-typed
// state machines).
func newBedrockOpenAIChunkWriter(w http.ResponseWriter, modelName string) (writeChunk func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}), finishStream func()) {
	flusher, _ := w.(http.Flusher)
	writeChunk = func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		chunk := map[string]interface{}{
			"id": "bedrock-" + modelName, "object": "chat.completion.chunk", "model": modelName,
			"choices": []map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	finishStream = func() {
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	return
}
