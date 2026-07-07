package biz

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Anthropic Messages inbound codec (docs/design/02-protocol-adapters.md):
// translates at the two edges only — request decode Anthropic Messages →
// OpenAI-shape body, response/stream encode OpenAI-shape → Anthropic
// Messages — reusing the existing OpenAI-shape pivot ("IR") and every
// existing outbound dialect completely unchanged. v1 scope matches the
// existing outbound anthropic adapter's own documented limitation: text +
// tool content only, multimodal blocks (image/document) are dropped.

type anthMsgContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type anthMsgMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthMsgTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthMessagesRequest struct {
	Model         string           `json:"model"`
	System        json.RawMessage  `json:"system"`
	Messages      []anthMsgMessage `json:"messages"`
	MaxTokens     int              `json:"max_tokens"`
	Temperature   *float64         `json:"temperature"`
	TopP          *float64         `json:"top_p"`
	StopSequences []string         `json:"stop_sequences"`
	Tools         []anthMsgTool    `json:"tools"`
	Stream        bool             `json:"stream"`
}

// anthropicSystemToText handles Anthropic's "system" field, which is either a
// plain string or a list of {"type":"text","text":...} blocks.
func anthropicSystemToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthMsgContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}

// anthropicMessagesToOpenAIRequest maps an Anthropic Messages request onto the
// OpenAI chat-completions body shape the rest of the gateway already speaks.
func anthropicMessagesToOpenAIRequest(body []byte) ([]byte, bool, error) {
	var in anthMessagesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, false, err
	}

	out := map[string]interface{}{
		"model":  in.Model,
		"stream": in.Stream,
	}
	if in.MaxTokens > 0 {
		out["max_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.StopSequences) > 0 {
		out["stop"] = in.StopSequences
	}
	if len(in.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(in.Tools))
		for _, t := range in.Tools {
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			tools = append(tools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  schema,
				},
			})
		}
		out["tools"] = tools
	}

	messages := []map[string]interface{}{}
	if sys := anthropicSystemToText(in.System); sys != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": sys})
	}
	for _, m := range in.Messages {
		msgs, err := anthropicMessageToOAMessages(m)
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, msgs...)
	}
	out["messages"] = messages

	b, err := json.Marshal(out)
	return b, in.Stream, err
}

// anthropicMessageToOAMessages converts one Anthropic message (whose content
// is either a plain string or a list of content blocks) into zero or more
// OpenAI-shape messages. A user message mixing text and tool_result blocks
// splits into a user message (text) plus one "tool" message per tool_result;
// an assistant message's text and tool_use blocks combine into a single
// OpenAI assistant message (content + tool_calls), matching OpenAI's own
// single-message-per-turn shape.
func anthropicMessageToOAMessages(m anthMsgMessage) ([]map[string]interface{}, error) {
	var plain string
	if json.Unmarshal(m.Content, &plain) == nil {
		if plain == "" {
			return nil, nil
		}
		return []map[string]interface{}{{"role": m.Role, "content": plain}}, nil
	}

	var blocks []anthMsgContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("anthropic message content: %w", err)
	}

	if m.Role == "assistant" {
		var text strings.Builder
		var toolCalls []map[string]interface{}
		for _, blk := range blocks {
			switch blk.Type {
			case "text":
				text.WriteString(blk.Text)
			case "tool_use":
				input := blk.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				toolCalls = append(toolCalls, map[string]interface{}{
					"id": blk.ID, "type": "function",
					"function": map[string]interface{}{"name": blk.Name, "arguments": string(input)},
				})
			}
		}
		msg := map[string]interface{}{"role": "assistant", "content": text.String()}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
			if text.Len() == 0 {
				msg["content"] = nil
			}
		}
		return []map[string]interface{}{msg}, nil
	}

	// role == "user": text blocks accumulate into one user message; each
	// tool_result becomes its own "tool" message (flushing pending text first
	// so ordering is preserved).
	var out []map[string]interface{}
	var pendingText strings.Builder
	flush := func() {
		if pendingText.Len() > 0 {
			out = append(out, map[string]interface{}{"role": "user", "content": pendingText.String()})
			pendingText.Reset()
		}
	}
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			pendingText.WriteString(blk.Text)
		case "tool_result":
			flush()
			out = append(out, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": blk.ToolUseID,
				"content":      toolResultContentToText(blk.Content),
			})
		}
	}
	flush()
	return out, nil
}

// toolResultContentToText flattens an Anthropic tool_result's content, which
// is either a plain string or a list of blocks (text blocks only in v1 scope).
func toolResultContentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthMsgContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}

// -----------------------------------------------------------------------------
// OpenAI → Anthropic Messages response encode (non-streaming)
// -----------------------------------------------------------------------------

func mapOpenAIFinishToAnthropicStop(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default: // stop, content_filter, ""
		return "end_turn"
	}
}

type oaChatResponseForAnthropic struct {
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
			CachedTokens        int `json:"cached_tokens"`
			CacheCreationTokens int `json:"cache_creation_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// openAIJSONIsError reports whether an OpenAI-shape body is one of the
// gateway's own error bodies ({"error": {...}}) rather than a success payload
// — every non-streaming error path in gateway.go writes this shape regardless
// of inbound route, so a single structural check dispatches both cases
// uniformly without gateway.go needing to know its caller's wire dialect.
func openAIJSONIsError(body []byte) (msg string, code string, isErr bool) {
	var probe struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Error == nil {
		return "", "", false
	}
	code = probe.Error.Code
	if code == "" {
		code = probe.Error.Type
	}
	return probe.Error.Message, code, true
}

// openAIErrorToAnthropicError re-wraps the gateway's OpenAI-shape error body
// ({"error":{"message","code"/"type"}}) as an Anthropic-shape error body
// ({"type":"error","error":{"type","message"}}).
func openAIErrorToAnthropicError(message, code string) []byte {
	anthType := "api_error"
	switch code {
	case "PII_DETECTED", "GUARDRAIL_BLOCKED":
		anthType = "invalid_request_error"
	case "MODEL_NOT_ALLOWED":
		anthType = "invalid_request_error"
	case "QUOTA_EXCEEDED", "rate_limit_error":
		anthType = "rate_limit_error"
	case "BILLING_SUSPENDED":
		anthType = "permission_error"
	}
	b, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    anthType,
			"message": message,
		},
	})
	return b
}

// openAIResponseToAnthropicMessage converts a complete OpenAI chat.completion
// body (as produced by any outbound dialect, already translated to the
// OpenAI shape) into an Anthropic Messages response. requestedModel is
// echoed back verbatim (Anthropic clients expect to see the model they asked
// for, not the gateway's resolved provider-side name).
func openAIResponseToAnthropicMessage(oaBody []byte, requestedModel string) []byte {
	if msg, code, isErr := openAIJSONIsError(oaBody); isErr {
		return openAIErrorToAnthropicError(msg, code)
	}

	var in oaChatResponseForAnthropic
	if err := json.Unmarshal(oaBody, &in); err != nil || len(in.Choices) == 0 {
		return openAIErrorToAnthropicError("malformed upstream response", "api_error")
	}
	choice := in.Choices[0]

	var content []map[string]interface{}
	var textContent string
	_ = json.Unmarshal(choice.Message.Content, &textContent) // content may be null (tool-call-only turn)
	if textContent != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": textContent})
	}
	for _, tc := range choice.Message.ToolCalls {
		var input json.RawMessage = json.RawMessage(`{}`)
		if json.Valid([]byte(tc.Function.Arguments)) && tc.Function.Arguments != "" {
			input = json.RawMessage(tc.Function.Arguments)
		}
		content = append(content, map[string]interface{}{
			"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input,
		})
	}
	if content == nil {
		content = []map[string]interface{}{}
	}

	cachedTokens, cacheCreationTokens := 0, 0
	if in.Usage.PromptTokensDetails != nil {
		cachedTokens = in.Usage.PromptTokensDetails.CachedTokens
		cacheCreationTokens = in.Usage.PromptTokensDetails.CacheCreationTokens
	}

	out := map[string]interface{}{
		"id":            in.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         requestedModel,
		"content":       content,
		"stop_reason":   mapOpenAIFinishToAnthropicStop(choice.FinishReason),
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                in.Usage.PromptTokens,
			"output_tokens":               in.Usage.CompletionTokens,
			"cache_read_input_tokens":     cachedTokens,
			"cache_creation_input_tokens": cacheCreationTokens,
		},
	}
	b, _ := json.Marshal(out)
	return b
}

// -----------------------------------------------------------------------------
// OpenAI chunk stream → Anthropic Messages SSE encode
// -----------------------------------------------------------------------------

// openAIStreamToAnthropicSSE reads OpenAI chat.completion.chunk SSE (produced
// by any outbound path — translateAnthropicStream/translateGeminiStream's own
// output, translateBedrockStream, or a raw openai_compatible passthrough
// stream) and re-emits it as Anthropic Messages streaming events. It is the
// mirror image of translateAnthropicStream: source and sink are swapped, but
// the event-level (not token-level) state — current content-block type/index,
// tool-call argument accumulation — is the same shape.
func openAIStreamToAnthropicSSE(r io.Reader, w io.Writer, requestedModel string) (promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens int, errMsg string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)

	msgID := "msg_" + requestedModel
	started := false
	blockOpen := false
	blockIsTool := false
	blockIndex := -1
	finishReason := "stop"

	writeEvt := func(event string, data map[string]interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	}
	ensureStarted := func() {
		if started {
			return
		}
		started = true
		writeEvt("message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id": msgID, "type": "message", "role": "assistant", "model": requestedModel,
				"content": []interface{}{}, "stop_reason": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}
	openTextBlock := func() {
		if blockOpen {
			return
		}
		blockOpen, blockIsTool = true, false
		blockIndex++
		writeEvt("content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": blockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		})
	}
	closeBlock := func() {
		if !blockOpen {
			return
		}
		writeEvt("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": blockIndex})
		blockOpen = false
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
						Index    int    `json:"index"`
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
					CachedTokens        int `json:"cached_tokens"`
					CacheCreationTokens int `json:"cache_creation_tokens"`
				} `json:"prompt_tokens_details"`
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
				cacheCreationTokens = chunk.Usage.PromptTokensDetails.CacheCreationTokens
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		ensureStarted()

		if choice.Delta.Content != "" {
			openTextBlock()
			writeEvt("content_block_delta", map[string]interface{}{
				"type": "content_block_delta", "index": blockIndex,
				"delta": map[string]interface{}{"type": "text_delta", "text": choice.Delta.Content},
			})
		}
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Function.Name != "" { // new tool call announced
				closeBlock()
				blockOpen, blockIsTool = true, true
				blockIndex++
				writeEvt("content_block_start", map[string]interface{}{
					"type": "content_block_start", "index": blockIndex,
					"content_block": map[string]interface{}{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]interface{}{}},
				})
			}
			if tc.Function.Arguments != "" && blockIsTool {
				writeEvt("content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
				})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}
	}

	closeBlock()
	writeEvt("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": mapOpenAIFinishToAnthropicStop(finishReason), "stop_sequence": nil},
		"usage": map[string]interface{}{"output_tokens": completionTokens},
	})
	writeEvt("message_stop", map[string]interface{}{"type": "message_stop"})
	return
}
