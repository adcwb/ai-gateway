package biz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesToOpenAIChatRequestPlainString(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","input":"hello there","instructions":"be brief","max_output_tokens":100,"stream":true}`)
	out, isStream, err := responsesToOpenAIChatRequest(in)
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
	out, _, err := responsesToOpenAIChatRequest(in)
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

func TestResponsesToOpenAIChatRequestRejectsPreviousResponseID(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_123"}`)
	if _, _, err := responsesToOpenAIChatRequest(in); err == nil {
		t.Fatal("expected previous_response_id to be rejected")
	}
}

func TestResponsesToOpenAIChatRequestRejectsStoreTrue(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","input":"hi","store":true}`)
	if _, _, err := responsesToOpenAIChatRequest(in); err == nil {
		t.Fatal("expected store=true to be rejected")
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
	out := openAIChatToResponses(oa, "gpt-4o")
	var m struct {
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
	if m.Object != "response" || m.Status != "completed" || m.Model != "gpt-4o" {
		t.Fatalf("envelope wrong: %+v", m)
	}
	if len(m.Output) != 1 || m.Output[0].Type != "message" || m.Output[0].Content[0].Text != "hi there" {
		t.Fatalf("output wrong: %+v", m.Output)
	}
	if m.Usage.InputTokens != 5 || m.Usage.OutputTokens != 3 || m.Usage.InputTokensDetails.CachedTokens != 1 || m.Usage.OutputTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("usage wrong: %+v", m.Usage)
	}
}

func TestOpenAIChatToResponsesErrorShape(t *testing.T) {
	oa := []byte(`{"error":{"message":"bad model","code":"MODEL_NOT_ALLOWED"}}`)
	out := openAIChatToResponses(oa, "gpt-4o")
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
	p, c, _, _, errMsg := openAIStreamToResponsesSSE(strings.NewReader(sse), &out, "gpt-4o")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if p != 4 || c != 1 {
		t.Fatalf("usage wrong: p=%d c=%d", p, c)
	}
	text := out.String()
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", `"delta":"Hi"`, "event: response.completed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
}
