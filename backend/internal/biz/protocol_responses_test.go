package biz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesToOpenAIChatRequestPlainString(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","input":"hello there","instructions":"be brief","max_output_tokens":100,"stream":true}`)
	out, isStream, err := responsesToOpenAIChatRequest(in, nil)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !isStream {
		t.Fatal("stream flag not propagated")
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if m["model"] != "gpt-4o" || m["max_tokens"].(float64) != 100 {
		t.Fatalf("model/max_tokens wrong: %v", m)
	}
	msgs := m["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user): %v", len(msgs), msgs)
	}
	sys := msgs[0].(map[string]interface{})
	if sys["role"] != "system" || sys["content"] != "be brief" {
		t.Fatalf("system message wrong: %v", sys)
	}
	user := msgs[1].(map[string]interface{})
	if user["role"] != "user" || user["content"] != "hello there" {
		t.Fatalf("user message wrong: %v", user)
	}
}

func TestResponsesToOpenAIChatRequestFunctionCallOutput(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","input":[
		{"role":"user","content":[{"type":"input_text","text":"weather in SH?"}]},
		{"type":"function_call_output","call_id":"c1","output":"sunny"}
	]}`)
	out, _, err := responsesToOpenAIChatRequest(in, nil)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	msgs := m["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(msgs), msgs)
	}
	toolMsg := msgs[1].(map[string]interface{})
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "c1" || toolMsg["content"] != "sunny" {
		t.Fatalf("function_call_output mapping wrong: %v", toolMsg)
	}
}

// TestResponsesToOpenAIChatRequestPrependsPriorMessages covers
// previous_response_id continuation (docs/design/02-protocol-adapters.md):
// resolving the ID itself is responses_api.go's job (DB + key ownership),
// but once resolved, this function must prepend the prior turns before the
// new instructions/input.
func TestResponsesToOpenAIChatRequestPrependsPriorMessages(t *testing.T) {
	prior := []map[string]interface{}{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "hello"},
	}
	in := []byte(`{"model":"gpt-4o","input":"how are you"}`)
	out, _, err := responsesToOpenAIChatRequest(in, prior)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	msgs := m["messages"].([]interface{})
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3 (2 prior + 1 new): %v", len(msgs), msgs)
	}
	if msgs[0].(map[string]interface{})["content"] != "hi" || msgs[1].(map[string]interface{})["content"] != "hello" {
		t.Fatalf("prior messages not prepended in order: %v", msgs)
	}
	if msgs[2].(map[string]interface{})["content"] != "how are you" {
		t.Fatalf("new input not appended after prior messages: %v", msgs)
	}
}

func TestOpenAIChatToResponsesSuccess(t *testing.T) {
	oa := []byte(`{
		"id": "chatcmpl-1",
		"choices": [{"finish_reason":"stop","message":{"content":"hi there"}}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 3,
			"prompt_tokens_details": {"cached_tokens": 1},
			"completion_tokens_details": {"reasoning_tokens": 2}}
	}`)
	out, assistantMsg := openAIChatToResponses(oa, "gpt-4o", "resp_test123")
	var m struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if m.ID != "resp_test123" {
		t.Fatalf("expected the caller-minted response ID to be used, got %q", m.ID)
	}
	if m.Object != "response" || m.Status != "completed" || m.Model != "gpt-4o" {
		t.Fatalf("envelope wrong: %+v", m)
	}
	if len(m.Output) != 1 || m.Output[0].Type != "message" || m.Output[0].Content[0].Text != "hi there" {
		t.Fatalf("output wrong: %+v", m.Output)
	}
	if m.Usage.InputTokens != 5 || m.Usage.OutputTokens != 3 || m.Usage.InputTokensDetails.CachedTokens != 1 || m.Usage.OutputTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("usage wrong: %+v", m.Usage)
	}
	if assistantMsg["role"] != "assistant" || assistantMsg["content"] != "hi there" {
		t.Fatalf("assistantMsg (for storage) wrong: %+v", assistantMsg)
	}
}

func TestOpenAIChatToResponsesErrorShape(t *testing.T) {
	oa := []byte(`{"error":{"message":"bad model","code":"MODEL_NOT_ALLOWED"}}`)
	out, assistantMsg := openAIChatToResponses(oa, "gpt-4o", "resp_test123")
	var m struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(out, &m)
	if m.Error.Message != "bad model" {
		t.Fatalf("error translation wrong: %+v", m)
	}
	if assistantMsg != nil {
		t.Fatalf("expected no assistantMsg (nothing to store) for an error response, got %+v", assistantMsg)
	}
}

func TestOpenAIStreamToResponsesSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var out strings.Builder
	p, c, _, _, errMsg, assistantMsg := openAIStreamToResponsesSSE(strings.NewReader(sse), &out, "gpt-4o", "resp_test123")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if p != 4 || c != 1 {
		t.Fatalf("usage wrong: p=%d c=%d", p, c)
	}
	text := out.String()
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", `"delta":"Hi"`, "event: response.completed", `"id":"resp_test123"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
	if assistantMsg["role"] != "assistant" || assistantMsg["content"] != "Hi" {
		t.Fatalf("assistantMsg (for storage) wrong: %+v", assistantMsg)
	}
}
