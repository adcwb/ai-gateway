package biz

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildMistralPrompt_SingleTurn(t *testing.T) {
	messages := []oaMessage{
		{Role: "user", Content: json.RawMessage(`"Describe the purpose of a 'hello world' program in one line."`)},
	}
	got := buildMistralPrompt(messages)
	want := "<s>[INST] Describe the purpose of a 'hello world' program in one line. [/INST]"
	if got != want {
		t.Fatalf("prompt =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildMistralPrompt_SystemFoldedAndMultiTurn(t *testing.T) {
	messages := []oaMessage{
		{Role: "system", Content: json.RawMessage(`"be brief"`)},
		{Role: "user", Content: json.RawMessage(`"hi"`)},
		{Role: "assistant", Content: json.RawMessage(`"hello"`)},
		{Role: "user", Content: json.RawMessage(`"how are you"`)},
	}
	got := buildMistralPrompt(messages)
	want := "<s>[INST] be brief\n\nhi [/INST] hello</s>[INST] how are you [/INST]"
	if got != want {
		t.Fatalf("prompt =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildMistralRequestBody(t *testing.T) {
	in := []byte(`{"model":"mistral.mistral-7b-instruct-v0:2","messages":[{"role":"user","content":"hi"}],"max_tokens":300,"temperature":0.5,"stop":"END"}`)
	out, err := buildMistralRequestBody(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m struct {
		Prompt      string   `json:"prompt"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature float64  `json:"temperature"`
		Stop        []string `json:"stop"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if m.MaxTokens != 300 || m.Temperature != 0.5 {
		t.Fatalf("params wrong: %+v", m)
	}
	if len(m.Stop) != 1 || m.Stop[0] != "END" {
		t.Fatalf("stop wrong: %+v", m.Stop)
	}
	if !strings.Contains(m.Prompt, "[INST] hi [/INST]") {
		t.Fatalf("prompt wrong: %s", m.Prompt)
	}
}

func TestMistralToOpenAIResponse(t *testing.T) {
	raw := []byte(`{"outputs":[{"text":"hi there","stop_reason":"stop"}]}`)
	out, prompt, completion, cacheRead, cacheCreation, err := mistralToOpenAIResponse(raw, "mistral.mistral-7b-instruct-v0:2")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prompt != 0 || cacheRead != 0 || cacheCreation != 0 {
		t.Fatalf("expected prompt/cache tokens to be 0 (Mistral reports none), got p=%d cr=%d cc=%d", prompt, cacheRead, cacheCreation)
	}
	if completion != len("hi there")/4 {
		t.Fatalf("expected estimated completion tokens, got %d", completion)
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

func TestTranslateMistralStream(t *testing.T) {
	body := buildBedrockStreamBody(t,
		[]byte(`{"outputs":[{"text":"Hi"}]}`),
		[]byte(`{"outputs":[{"text":" there","stop_reason":"stop"}]}`),
	)
	w := httptest.NewRecorder()
	audit, prompt, completion, _, _, errMsg := translateMistralStream(w, body, "mistral.mistral-7b-instruct-v0:2")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if string(audit) != "Hi there" {
		t.Fatalf("accumulated audit text = %q", audit)
	}
	if prompt != 0 || completion != len("Hi there")/4 {
		t.Fatalf("token counts wrong: p=%d c=%d", prompt, completion)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"content":"Hi"`) || !strings.Contains(out, `"content":" there"`) || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing expected SSE content:\n%s", out)
	}
}
