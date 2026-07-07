package biz

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// OpenAI Responses API inbound codec (docs/design/02-protocol-adapters.md):
// same edge-only translation pattern as the Anthropic Messages codec
// (protocol_anthropic_inbound.go) — decode Responses request → OpenAI
// chat-completions body, encode OpenAI chat response/stream → Responses
// shape. Stateless subset only: previous_response_id chaining and
// store=true (server-side retrieval of a completed response by ID) are
// rejected outright rather than silently ignored, since the gateway does
// not persist Responses state — this was flagged as an open question in
// D02 and is resolved here as "not supported" rather than faked.

type respInputContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type respInputItem struct {
	Type    string          `json:"type,omitempty"` // "message" (default) | "function_call_output"
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Output  string          `json:"output,omitempty"`
}

type respTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions"`
	Tools              []respTool      `json:"tools"`
	Temperature        *float64        `json:"temperature"`
	TopP               *float64        `json:"top_p"`
	MaxOutputTokens    int             `json:"max_output_tokens"`
	Stream             bool            `json:"stream"`
	PreviousResponseID string          `json:"previous_response_id"`
	Store              *bool           `json:"store"`
}

// responsesToOpenAIChatRequest maps a Responses API request onto the OpenAI
// chat-completions body shape.
func responsesToOpenAIChatRequest(body []byte) ([]byte, bool, error) {
	var in responsesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false, err
	}
	if in.PreviousResponseID != "" {
		return nil, false, fmt.Errorf("previous_response_id chaining is not supported by this gateway (no server-side response persistence)")
	}
	if in.Store != nil && *in.Store {
		return nil, false, fmt.Errorf("store=true is not supported by this gateway (no server-side response persistence for later retrieval)")
	}

	out := map[string]interface{}{"model": in.Model, "stream": in.Stream}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if in.MaxOutputTokens > 0 {
		out["max_tokens"] = in.MaxOutputTokens
	}
	if len(in.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(in.Tools))
		for _, t := range in.Tools {
			if t.Type != "" && t.Type != "function" {
				continue
			}
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object"}`)
			}
			tools = append(tools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": t.Name, "description": t.Description, "parameters": params,
				},
			})
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}

	messages := []map[string]interface{}{}
	if in.Instructions != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": in.Instructions})
	}
	inputMsgs, err := responsesInputToOAMessages(in.Input)
	if err != nil {
		return nil, false, err
	}
	messages = append(messages, inputMsgs...)
	out["messages"] = messages

	b, err := json.Marshal(out)
	return b, in.Stream, err
}

// responsesInputToOAMessages handles the Responses API "input" field, which
// is either a plain string (shorthand for one user message) or a list of
// input items (role-bearing messages or function_call_output results).
func responsesInputToOAMessages(raw json.RawMessage) ([]map[string]interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		if plain == "" {
			return nil, nil
		}
		return []map[string]interface{}{{"role": "user", "content": plain}}, nil
	}

	var items []respInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("responses input: %w", err)
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if item.Type == "function_call_output" {
			out = append(out, map[string]interface{}{
				"role": "tool", "tool_call_id": item.CallID, "content": item.Output,
			})
			continue
		}
		role := item.Role
		if role == "" {
			role = "user"
		}
		out = append(out, map[string]interface{}{"role": role, "content": responsesContentToText(item.Content)})
	}
	return out, nil
}

func responsesContentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []respInputContentPart
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "input_text" || p.Type == "output_text" || p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// -----------------------------------------------------------------------------
// OpenAI chat response/stream → Responses API encode
// -----------------------------------------------------------------------------

type oaChatResponseForResponses struct {
	ID      string `json:"id"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []oaToolCall    `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

func responsesErrorBody(message, code string) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{"message": message, "code": code, "type": "invalid_request_error"},
	})
	return b
}

// openAIChatToResponses converts a complete OpenAI chat.completion body into
// a Responses API response, tracking reasoning tokens through so a client on
// a real reasoning-capable OpenAI upstream sees a non-zero breakdown
// (previously always 0 — the identity dialect never parsed this field until
// the Responses API entrance needed it, see parseUsageFromBody).
func openAIChatToResponses(oaBody []byte, requestedModel string) []byte {
	if msg, code, isErr := openAIJSONIsError(oaBody); isErr {
		return responsesErrorBody(msg, code)
	}
	var in oaChatResponseForResponses
	if err := json.Unmarshal(oaBody, &in); err != nil || len(in.Choices) == 0 {
		return responsesErrorBody("malformed upstream response", "api_error")
	}
	choice := in.Choices[0]

	var output []map[string]interface{}
	var textContent string
	_ = json.Unmarshal(choice.Message.Content, &textContent)
	if textContent != "" {
		output = append(output, map[string]interface{}{
			"type": "message", "id": "msg_" + in.ID, "role": "assistant", "status": "completed",
			"content": []map[string]interface{}{{"type": "output_text", "text": textContent, "annotations": []interface{}{}}},
		})
	}
	for _, tc := range choice.Message.ToolCalls {
		output = append(output, map[string]interface{}{
			"type": "function_call", "id": "fc_" + tc.ID, "call_id": tc.ID,
			"name": tc.Function.Name, "arguments": tc.Function.Arguments,
		})
	}
	if output == nil {
		output = []map[string]interface{}{}
	}

	cachedTokens, reasoningTokens := 0, 0
	if in.Usage.PromptTokensDetails != nil {
		cachedTokens = in.Usage.PromptTokensDetails.CachedTokens
	}
	if in.Usage.CompletionTokensDetails != nil {
		reasoningTokens = in.Usage.CompletionTokensDetails.ReasoningTokens
	}

	out := map[string]interface{}{
		"id": "resp_" + in.ID, "object": "response", "status": "completed", "model": requestedModel,
		"output": output,
		"usage": map[string]interface{}{
			"input_tokens":          in.Usage.PromptTokens,
			"output_tokens":         in.Usage.CompletionTokens,
			"total_tokens":          in.Usage.PromptTokens + in.Usage.CompletionTokens,
			"input_tokens_details":  map[string]interface{}{"cached_tokens": cachedTokens},
			"output_tokens_details": map[string]interface{}{"reasoning_tokens": reasoningTokens},
		},
	}
	b, _ := json.Marshal(out)
	return b
}

// openAIStreamToResponsesSSE mirrors openAIStreamToAnthropicSSE
// (protocol_anthropic_inbound.go) for the Responses API. Event coverage is
// intentionally scoped to the commonly-consumed subset — response.created,
// response.output_text.delta, response.function_call_arguments.delta,
// response.completed — not the full Responses API event taxonomy (which also
// has per-item added/done events, reasoning-summary events, etc.); noted as
// an explicit scope limit rather than claimed spec parity.
func openAIStreamToResponsesSSE(r io.Reader, w io.Writer, requestedModel string) (promptTokens, completionTokens, cacheReadTokens, reasoningTokens int, errMsg string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)

	respID := "resp_" + requestedModel
	started := false
	var textBuilder strings.Builder
	var toolCalls []map[string]interface{}
	finishReason := "stop"

	writeEvt := func(eventType string, data map[string]interface{}) {
		data["type"] = eventType
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
	}
	ensureStarted := func() {
		if started {
			return
		}
		started = true
		writeEvt("response.created", map[string]interface{}{
			"response": map[string]interface{}{"id": respID, "object": "response", "status": "in_progress", "model": requestedModel},
		})
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CompletionTokensDetails *struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if e, _, isErr := openAIJSONIsError([]byte(payload)); isErr {
			errMsg = e
			continue
		}
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				promptTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				completionTokens = chunk.Usage.CompletionTokens
			}
			if chunk.Usage.PromptTokensDetails != nil {
				cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			if chunk.Usage.CompletionTokensDetails != nil {
				reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		ensureStarted()

		if choice.Delta.Content != "" {
			textBuilder.WriteString(choice.Delta.Content)
			writeEvt("response.output_text.delta", map[string]interface{}{
				"item_id": respID, "output_index": 0, "content_index": 0, "delta": choice.Delta.Content,
			})
		}
		for i, tc := range choice.Delta.ToolCalls {
			if tc.Function.Name != "" {
				toolCalls = append(toolCalls, map[string]interface{}{
					"type": "function_call", "id": "fc_" + tc.ID, "call_id": tc.ID,
					"name": tc.Function.Name, "arguments": "",
				})
			}
			if tc.Function.Arguments != "" && i < len(toolCalls) {
				toolCalls[i]["arguments"] = toolCalls[i]["arguments"].(string) + tc.Function.Arguments
				writeEvt("response.function_call_arguments.delta", map[string]interface{}{
					"item_id": toolCalls[i]["id"], "delta": tc.Function.Arguments,
				})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}
	}

	output := []map[string]interface{}{}
	if textBuilder.Len() > 0 {
		output = append(output, map[string]interface{}{
			"type": "message", "id": respID, "role": "assistant", "status": "completed",
			"content": []map[string]interface{}{{"type": "output_text", "text": textBuilder.String(), "annotations": []interface{}{}}},
		})
	}
	output = append(output, toolCalls...)
	_ = finishReason

	writeEvt("response.completed", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response", "status": "completed", "model": requestedModel,
			"output": output,
			"usage": map[string]interface{}{
				"input_tokens": promptTokens, "output_tokens": completionTokens,
				"total_tokens":          promptTokens + completionTokens,
				"input_tokens_details":  map[string]interface{}{"cached_tokens": cacheReadTokens},
				"output_tokens_details": map[string]interface{}{"reasoning_tokens": reasoningTokens},
			},
		},
	})
	return
}
