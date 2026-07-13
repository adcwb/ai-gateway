package biz

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Protocol adapter layer (docs/design/02-protocol-adapters.md, P2-1/P2-3).
//
// The internal representation is the OpenAI Chat Completions wire format —
// the identity dialect for the vast majority of traffic (fast path: zero
// re-serialization). Non-OpenAI providers get request/response/stream
// translation here. Currently implemented outbound dialects:
//
//	openai_compatible  identity (existing behavior, untouched)
//	azure_openai       same body; api-key header + api-version query
//	anthropic          full request/response/SSE translation
//
// Usage from every dialect is normalized into (prompt, completion, cacheRead)
// so audit, quotas and billing never see dialect-specific shapes.

type adapterConfig struct {
	AnthropicVersion string `json:"anthropicVersion"`
	APIVersion       string `json:"apiVersion"`
	Region           string `json:"region"` // bedrock only
}

func parseAdapterConfig(p *model.AIProvider) adapterConfig {
	cfg := adapterConfig{}
	if len(p.AdapterConfig) > 0 {
		_ = json.Unmarshal(p.AdapterConfig, &cfg)
	}
	if cfg.AnthropicVersion == "" {
		cfg.AnthropicVersion = "2023-06-01"
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "2024-06-01"
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return cfg
}

// buildUpstreamRequest constructs the provider-dialect HTTP request for one
// attempt. openAIPath is the path after /ai/v1 (query included).
func buildUpstreamRequest(ctx context.Context, entry *providerEntry, method, openAIPath string, sendBody []byte, isStream bool) (*http.Request, error) {
	p := entry.provider
	cfg := parseAdapterConfig(&p)

	switch p.ProviderType {
	case model.ProviderTypeAnthropic:
		anthBody, err := openAIToAnthropicRequest(sendBody, isStream)
		if err != nil {
			return nil, fmt.Errorf("anthropic request translation: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/messages", bytes.NewReader(anthBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", entry.apiKey)
		req.Header.Set("anthropic-version", cfg.AnthropicVersion)
		return req, nil

	case model.ProviderTypeGemini:
		gemBody, modelName, _, err := openAIToGeminiRequest(sendBody)
		if err != nil {
			return nil, fmt.Errorf("gemini request translation: %w", err)
		}
		endpoint := ":generateContent"
		if isStream {
			endpoint = ":streamGenerateContent?alt=sse"
		}
		url := fmt.Sprintf("%s/v1beta/models/%s%s", p.BaseURL, modelName, endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(gemBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", entry.apiKey)
		return req, nil

	case model.ProviderTypeBedrock:
		return buildBedrockRequest(ctx, entry, cfg, sendBody, isStream)

	case model.ProviderTypeAzureOpenAI:
		// BaseURL is expected to include /openai/deployments/{deployment}
		path := openAIPath
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path+sep+"api-version="+cfg.APIVersion, bytes.NewReader(sendBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-key", entry.apiKey)
		return req, nil

	default: // openai_compatible — identity dialect
		upstreamPath := rewriteOpenAIPathForProvider(openAIPath, p)
		req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+upstreamPath, bytes.NewReader(sendBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+entry.apiKey)
		return req, nil
	}
}

// -----------------------------------------------------------------------------
// OpenAI → Anthropic request translation
// -----------------------------------------------------------------------------

type oaMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []oaToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaChatRequest struct {
	Model       string          `json:"model"`
	Messages    []oaMessage     `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	MaxComplete int             `json:"max_completion_tokens"`
	Temperature *float64        `json:"temperature"`
	TopP        *float64        `json:"top_p"`
	Stop        json.RawMessage `json:"stop"`
	Tools       []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

// openAIToAnthropicRequest maps the OpenAI chat body onto Anthropic Messages.
// System messages lift into the top-level system field; assistant tool_calls
// become tool_use blocks; role:"tool" results become user tool_result blocks.
func openAIToAnthropicRequest(body []byte, isStream bool) ([]byte, error) {
	var in oaChatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}

	out := map[string]interface{}{
		"model":  in.Model,
		"stream": isStream,
	}
	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens <= 0 {
		maxTokens = 4096 // Anthropic requires max_tokens
	}
	out["max_tokens"] = maxTokens
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.Stop) > 0 {
		var one string
		var many []string
		if json.Unmarshal(in.Stop, &one) == nil {
			out["stop_sequences"] = []string{one}
		} else if json.Unmarshal(in.Stop, &many) == nil && len(many) > 0 {
			out["stop_sequences"] = many
		}
	}
	if len(in.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(in.Tools))
		for _, t := range in.Tools {
			if t.Type != "function" {
				continue
			}
			schema := t.Function.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			tools = append(tools, map[string]interface{}{
				"name":         t.Function.Name,
				"description":  t.Function.Description,
				"input_schema": schema,
			})
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}

	var systemParts []string
	messages := make([]map[string]interface{}, 0, len(in.Messages))
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			systemParts = append(systemParts, rawContentToText(m.Content))
		case "tool":
			messages = append(messages, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     rawContentToText(m.Content),
				}},
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				content := []map[string]interface{}{}
				if txt := rawContentToText(m.Content); txt != "" {
					content = append(content, map[string]interface{}{"type": "text", "text": txt})
				}
				for _, tc := range m.ToolCalls {
					var input json.RawMessage = json.RawMessage(`{}`)
					if json.Valid([]byte(tc.Function.Arguments)) && tc.Function.Arguments != "" {
						input = json.RawMessage(tc.Function.Arguments)
					}
					content = append(content, map[string]interface{}{
						"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input,
					})
				}
				messages = append(messages, map[string]interface{}{"role": "assistant", "content": content})
			} else {
				messages = append(messages, map[string]interface{}{"role": "assistant", "content": rawContentToText(m.Content)})
			}
		default: // user
			messages = append(messages, map[string]interface{}{"role": "user", "content": rawContentToText(m.Content)})
		}
	}
	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n")
	}
	out["messages"] = messages
	return json.Marshal(out)
}

// rawContentToText flattens OpenAI content (string or parts array) to text.
// Multimodal parts other than text are dropped (v1 scope: text + tools).
func rawContentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// -----------------------------------------------------------------------------
// Anthropic → OpenAI response translation (non-streaming)
// -----------------------------------------------------------------------------

type anthUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	CacheReadInput     int `json:"cache_read_input_tokens"`
	CacheCreationInput int `json:"cache_creation_input_tokens"`
}

type anthContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func mapAnthropicStopReason(r string) string {
	switch r {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default: // end_turn, stop_sequence
		return "stop"
	}
}

// anthropicToOpenAIResponse converts a complete Anthropic message into an
// OpenAI chat.completion body and normalized usage. The 5th return value is
// Anthropic's cache_creation_input_tokens (prompt-cache write) — carried
// through the OpenAI-shape usage object as the gateway-internal extension
// field prompt_tokens_details.cache_creation_tokens so a downstream Anthropic
// Messages inbound encoder (protocol_anthropic_inbound.go) can read it back.
func anthropicToOpenAIResponse(body []byte, modelName string) ([]byte, int, int, int, int, error) {
	var in struct {
		ID         string             `json:"id"`
		Model      string             `json:"model"`
		Content    []anthContentBlock `json:"content"`
		StopReason string             `json:"stop_reason"`
		Usage      anthUsage          `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, 0, 0, 0, 0, err
	}

	var text strings.Builder
	var toolCalls []map[string]interface{}
	for _, block := range in.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			toolCalls = append(toolCalls, map[string]interface{}{
				"id": block.ID, "type": "function",
				"function": map[string]interface{}{"name": block.Name, "arguments": args},
			})
		}
	}

	message := map[string]interface{}{"role": "assistant", "content": text.String()}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		if text.Len() == 0 {
			message["content"] = nil
		}
	}
	out := map[string]interface{}{
		"id":      in.ID,
		"object":  "chat.completion",
		"model":   modelName,
		"choices": []map[string]interface{}{{"index": 0, "message": message, "finish_reason": mapAnthropicStopReason(in.StopReason)}},
		"usage": map[string]interface{}{
			"prompt_tokens":     in.Usage.InputTokens,
			"completion_tokens": in.Usage.OutputTokens,
			"total_tokens":      in.Usage.InputTokens + in.Usage.OutputTokens,
			"prompt_tokens_details": map[string]interface{}{
				"cached_tokens":         in.Usage.CacheReadInput,
				"cache_creation_tokens": in.Usage.CacheCreationInput,
			},
		},
	}
	b, err := json.Marshal(out)
	return b, in.Usage.InputTokens, in.Usage.OutputTokens, in.Usage.CacheReadInput, in.Usage.CacheCreationInput, err
}

// -----------------------------------------------------------------------------
// Anthropic SSE → OpenAI chunk stream translation
// -----------------------------------------------------------------------------

// translateAnthropicStream reads Anthropic SSE events and writes OpenAI
// chat.completion.chunk SSE to w. Event-level state machine per
// docs/design/02-protocol-adapters.md: content_block_* map to indexed deltas,
// usage is emitted as a terminal chunk regardless of where it arrived.
// Returns (accumulated text for audit, prompt, completion, cacheRead, cacheCreation, errMsg).
func translateAnthropicStream(w http.ResponseWriter, body *bufio.Scanner, modelName string) ([]byte, int, int, int, int, string) {
	flusher, _ := w.(http.Flusher)
	var audit strings.Builder
	promptTokens, completionTokens, cachedTokens, cacheCreationTokens := 0, 0, 0, 0
	finishReason := "stop"
	streamErr := ""
	msgID := ""
	toolIndex := -1 // OpenAI tool_calls index counter

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		chunk := map[string]interface{}{
			"id":     msgID,
			"object": "chat.completion.chunk",
			"model":  modelName,
			"choices": []map[string]interface{}{{
				"index": 0, "delta": delta, "finish_reason": finish,
			}},
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

	var currentEvent string
	for body.Scan() {
		line := body.Text()
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		switch currentEvent {
		case "message_start":
			var evt struct {
				Message struct {
					ID    string    `json:"id"`
					Usage anthUsage `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil {
				msgID = evt.Message.ID
				promptTokens = evt.Message.Usage.InputTokens
				cachedTokens = evt.Message.Usage.CacheReadInput
				cacheCreationTokens = evt.Message.Usage.CacheCreationInput
			}
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)

		case "content_block_start":
			var evt struct {
				ContentBlock anthContentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil && evt.ContentBlock.Type == "tool_use" {
				toolIndex++
				writeChunk(map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index": toolIndex, "id": evt.ContentBlock.ID, "type": "function",
						"function": map[string]interface{}{"name": evt.ContentBlock.Name, "arguments": ""},
					}},
				}, nil, nil)
			}

		case "content_block_delta":
			var evt struct {
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil {
				continue
			}
			switch evt.Delta.Type {
			case "text_delta":
				audit.WriteString(evt.Delta.Text)
				writeChunk(map[string]interface{}{"content": evt.Delta.Text}, nil, nil)
			case "input_json_delta":
				writeChunk(map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index":    toolIndex,
						"function": map[string]interface{}{"arguments": evt.Delta.PartialJSON},
					}},
				}, nil, nil)
			}

		case "message_delta":
			var evt struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage anthUsage `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil {
				if evt.Delta.StopReason != "" {
					finishReason = mapAnthropicStopReason(evt.Delta.StopReason)
				}
				if evt.Usage.OutputTokens > 0 {
					completionTokens = evt.Usage.OutputTokens
				}
			}

		case "error":
			var evt struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil {
				streamErr = evt.Error.Message
			}

		case "message_stop":
			writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
				"prompt_tokens":     promptTokens,
				"completion_tokens": completionTokens,
				"total_tokens":      promptTokens + completionTokens,
				"prompt_tokens_details": map[string]interface{}{
					"cached_tokens":         cachedTokens,
					"cache_creation_tokens": cacheCreationTokens,
				},
			})
			fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	return []byte(audit.String()), promptTokens, completionTokens, cachedTokens, cacheCreationTokens, streamErr
}
