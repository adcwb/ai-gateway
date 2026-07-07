package biz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicMessagesToOpenAIRequest(t *testing.T) {
	in := []byte(`{
		"model": "claude-sonnet",
		"system": "be brief",
		"messages": [
			{"role": "user", "content": "what's the weather in SH?"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "Let me check."},
				{"type": "tool_use", "id": "t1", "name": "get_weather", "input": {"city": "SH"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "t1", "content": "sunny"}
			]}
		],
		"max_tokens": 512,
		"temperature": 0.3,
		"stop_sequences": ["END"],
		"tools": [{"name": "get_weather", "description": "d", "input_schema": {"type": "object"}}],
		"stream": true
	}`)
	out, isStream, err := anthropicMessagesToOpenAIRequest(in)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !isStream {
		t.Fatal("stream flag not propagated")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if m["model"] != "claude-sonnet" || m["max_tokens"].(float64) != 512 {
		t.Fatalf("model/max_tokens wrong: %v", m)
	}
	msgs := m["messages"].([]interface{})
	// system, user, assistant(tool_use), tool(tool_result)
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %v", len(msgs), msgs)
	}
	sys := msgs[0].(map[string]interface{})
	if sys["role"] != "system" || sys["content"] != "be brief" {
		t.Fatalf("system message wrong: %v", sys)
	}
	asst := msgs[2].(map[string]interface{})
	if asst["role"] != "assistant" {
		t.Fatalf("assistant message role wrong: %v", asst)
	}
	toolCalls := asst["tool_calls"].([]interface{})
	tc := toolCalls[0].(map[string]interface{})
	fn := tc["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Fatalf("tool_use mapping wrong: %v", tc)
	}
	toolMsg := msgs[3].(map[string]interface{})
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "t1" || toolMsg["content"] != "sunny" {
		t.Fatalf("tool_result mapping wrong: %v", toolMsg)
	}
	if _, hasTools := m["tools"]; !hasTools {
		t.Fatal("tools not mapped")
	}
}

func TestOpenAIResponseToAnthropicMessageSuccess(t *testing.T) {
	oa := []byte(`{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"choices": [{"index": 0, "finish_reason": "tool_calls", "message": {
			"content": "The weather is ",
			"tool_calls": [{"id":"t9","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SH\"}"}}]
		}}],
		"usage": {"prompt_tokens": 30, "completion_tokens": 12, "prompt_tokens_details": {"cached_tokens": 5, "cache_creation_tokens": 3}}
	}`)
	out := openAIResponseToAnthropicMessage(oa, "claude-sonnet")
	var m struct {
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if m.Type != "message" || m.Role != "assistant" || m.Model != "claude-sonnet" {
		t.Fatalf("envelope wrong: %+v", m)
	}
	if m.StopReason != "tool_use" {
		t.Fatalf("stop_reason mapping wrong: %s", m.StopReason)
	}
	if len(m.Content) != 2 || m.Content[0].Type != "text" || m.Content[1].Type != "tool_use" || m.Content[1].Name != "get_weather" {
		t.Fatalf("content blocks wrong: %+v", m.Content)
	}
	if m.Usage.InputTokens != 30 || m.Usage.OutputTokens != 12 || m.Usage.CacheReadInputTokens != 5 || m.Usage.CacheCreationInputTokens != 3 {
		t.Fatalf("usage wrong: %+v", m.Usage)
	}
}

func TestOpenAIResponseToAnthropicMessageErrorShape(t *testing.T) {
	oa := []byte(`{"error":{"message":"rate limited","code":"QUOTA_EXCEEDED"}}`)
	out := openAIResponseToAnthropicMessage(oa, "claude-sonnet")
	var m struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if m.Type != "error" || m.Error.Type != "rate_limit_error" || m.Error.Message != "rate limited" {
		t.Fatalf("error translation wrong: %+v", m)
	}
}

func TestOpenAIStreamToAnthropicSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var out strings.Builder
	p, c, cached, _, errMsg := openAIStreamToAnthropicSSE(strings.NewReader(sse), &out, "claude-sonnet")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if p != 10 || c != 2 || cached != 1 {
		t.Fatalf("usage wrong: p=%d c=%d cached=%d", p, c, cached)
	}
	text := out.String()
	for _, want := range []string{"event: message_start", "event: content_block_delta", `"text":"Hello"`, "event: message_stop"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
}
