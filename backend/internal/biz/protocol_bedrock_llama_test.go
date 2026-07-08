package biz

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildLlamaPrompt(t *testing.T) {
	messages := []oaMessage{
		{Role: "user", Content: json.RawMessage(`"Describe the purpose of a 'hello world' program in one line."`)},
	}
	got := buildLlamaPrompt(messages)
	// Matches the exact template AWS's own Llama 3 InvokeModel doc example uses.
	want := "<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\n" +
		"Describe the purpose of a 'hello world' program in one line.<|eot_id|>" +
		"<|start_header_id|>assistant<|end_header_id|>\n\n"
	if got != want {
		t.Fatalf("prompt =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildLlamaRequestBody(t *testing.T) {
	in := []byte(`{"model":"meta.llama3-70b-instruct-v1:0","messages":[{"role":"user","content":"hi"}],"max_tokens":300,"temperature":0.5,"top_p":0.9}`)
	out, err := buildLlamaRequestBody(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m struct {
		Prompt      string  `json:"prompt"`
		MaxGenLen   int     `json:"max_gen_len"`
		Temperature float64 `json:"temperature"`
		TopP        float64 `json:"top_p"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if m.MaxGenLen != 300 || m.Temperature != 0.5 || m.TopP != 0.9 {
		t.Fatalf("params wrong: %+v", m)
	}
	if !strings.Contains(m.Prompt, "<|start_header_id|>user<|end_header_id|>") {
		t.Fatalf("prompt missing user header: %s", m.Prompt)
	}
}

func TestLlamaToOpenAIResponse(t *testing.T) {
	raw := []byte(`{"generation":"hi there","prompt_token_count":8,"generation_token_count":3,"stop_reason":"stop"}`)
	out, prompt, completion, cacheRead, cacheCreation, err := llamaToOpenAIResponse(raw, "meta.llama3-70b-instruct-v1:0")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prompt != 8 || completion != 3 || cacheRead != 0 || cacheCreation != 0 {
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

func TestTranslateLlamaStream(t *testing.T) {
	body := buildBedrockStreamBody(t,
		[]byte(`{"generation":"Hi","prompt_token_count":8}`),
		[]byte(`{"generation":" there","generation_token_count":2,"stop_reason":"stop"}`),
	)
	w := httptest.NewRecorder()
	audit, prompt, completion, _, _, errMsg := translateLlamaStream(w, body, "meta.llama3-70b-instruct-v1:0")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if string(audit) != "Hi there" {
		t.Fatalf("accumulated audit text = %q", audit)
	}
	if prompt != 8 || completion != 2 {
		t.Fatalf("token counts wrong: p=%d c=%d", prompt, completion)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"content":"Hi"`) || !strings.Contains(out, `"content":" there"`) || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing expected SSE content:\n%s", out)
	}
}
