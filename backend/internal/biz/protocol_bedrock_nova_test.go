package biz

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildNovaRequestBody(t *testing.T) {
	in := []byte(`{"model":"amazon.nova-pro-v1:0","messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"}
	],"max_tokens":300,"temperature":0.5,"top_p":0.9,"stop":["END"]}`)
	out, err := buildNovaRequestBody(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
		InferenceConfig struct {
			MaxTokens     int      `json:"maxTokens"`
			Temperature   float64  `json:"temperature"`
			TopP          float64  `json:"topP"`
			StopSequences []string `json:"stopSequences"`
		} `json:"inferenceConfig"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(m.System) != 1 || m.System[0].Text != "be brief" {
		t.Fatalf("system wrong: %+v", m.System)
	}
	if len(m.Messages) != 2 || m.Messages[0].Role != "user" || m.Messages[0].Content[0].Text != "hi" ||
		m.Messages[1].Role != "assistant" || m.Messages[1].Content[0].Text != "hello" {
		t.Fatalf("messages wrong: %+v", m.Messages)
	}
	if m.InferenceConfig.MaxTokens != 300 || m.InferenceConfig.Temperature != 0.5 || m.InferenceConfig.TopP != 0.9 {
		t.Fatalf("inferenceConfig wrong: %+v", m.InferenceConfig)
	}
	if len(m.InferenceConfig.StopSequences) != 1 || m.InferenceConfig.StopSequences[0] != "END" {
		t.Fatalf("stopSequences wrong: %+v", m.InferenceConfig.StopSequences)
	}
}

func TestNovaToOpenAIResponse(t *testing.T) {
	raw := []byte(`{"output":{"message":{"role":"assistant","content":[{"text":"hi there"}]}},"stopReason":"end_turn","usage":{"inputTokens":10,"outputTokens":4,"totalTokens":14}}`)
	out, prompt, completion, cacheRead, cacheCreation, err := novaToOpenAIResponse(raw, "amazon.nova-pro-v1:0")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prompt != 10 || completion != 4 || cacheRead != 0 || cacheCreation != 0 {
		t.Fatalf("token counts wrong: p=%d c=%d cr=%d cc=%d", prompt, completion, cacheRead, cacheCreation)
	}
	var m struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	json.Unmarshal(out, &m)
	if m.Choices[0].Message.Content != "hi there" || m.Choices[0].FinishReason != "stop" {
		t.Fatalf("output wrong: %+v", m)
	}
}

func TestNovaToOpenAIResponse_MaxTokensFinish(t *testing.T) {
	raw := []byte(`{"output":{"message":{"content":[{"text":"x"}]}},"stopReason":"max_tokens","usage":{"inputTokens":1,"outputTokens":1}}`)
	out, _, _, _, _, err := novaToOpenAIResponse(raw, "amazon.nova-pro-v1:0")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !jsonContains(string(out), `"finish_reason":"length"`) {
		t.Fatalf("expected length finish reason, got %s", out)
	}
}

func TestTranslateNovaStream(t *testing.T) {
	body := buildBedrockStreamBody(t,
		[]byte(`{"contentBlockDelta":{"delta":{"text":"Hi"}}}`),
		[]byte(`{"contentBlockDelta":{"delta":{"text":" there"}}}`),
		[]byte(`{"messageStop":{"stopReason":"end_turn"}}`),
		[]byte(`{"metadata":{"usage":{"inputTokens":10,"outputTokens":2}}}`),
	)
	w := httptest.NewRecorder()
	audit, prompt, completion, _, _, errMsg := translateNovaStream(w, body, "amazon.nova-pro-v1:0")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if string(audit) != "Hi there" {
		t.Fatalf("accumulated audit text = %q", audit)
	}
	if prompt != 10 || completion != 2 {
		t.Fatalf("token counts wrong: p=%d c=%d", prompt, completion)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"content":"Hi"`) || !strings.Contains(out, `"content":" there"`) ||
		!strings.Contains(out, `"finish_reason":"stop"`) || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing expected SSE content:\n%s", out)
	}
}
