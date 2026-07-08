package biz

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildTitanRequestBody(t *testing.T) {
	in := []byte(`{"model":"amazon.titan-text-express-v1","messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"},
		{"role":"user","content":"how are you"}
	],"max_tokens":256,"temperature":0.5,"top_p":0.9,"stop":["END"]}`)

	out, err := buildTitanRequestBody(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m struct {
		InputText            string `json:"inputText"`
		TextGenerationConfig struct {
			Temperature   float64  `json:"temperature"`
			TopP          float64  `json:"topP"`
			MaxTokenCount int      `json:"maxTokenCount"`
			StopSequences []string `json:"stopSequences"`
		} `json:"textGenerationConfig"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := "System: be brief\nUser: hi\nBot: hello\nUser: how are you\nBot:"
	if m.InputText != want {
		t.Fatalf("inputText =\n%q\nwant\n%q", m.InputText, want)
	}
	if m.TextGenerationConfig.MaxTokenCount != 256 || m.TextGenerationConfig.Temperature != 0.5 || m.TextGenerationConfig.TopP != 0.9 {
		t.Fatalf("config wrong: %+v", m.TextGenerationConfig)
	}
	if len(m.TextGenerationConfig.StopSequences) != 1 || m.TextGenerationConfig.StopSequences[0] != "END" {
		t.Fatalf("stop sequences wrong: %+v", m.TextGenerationConfig.StopSequences)
	}
}

func TestTitanToOpenAIResponse(t *testing.T) {
	raw := []byte(`{"inputTextTokenCount":10,"results":[{"tokenCount":5,"outputText":"hi there","completionReason":"FINISH"}]}`)
	out, prompt, completion, cacheRead, cacheCreation, err := titanToOpenAIResponse(raw, "amazon.titan-text-express-v1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prompt != 10 || completion != 5 || cacheRead != 0 || cacheCreation != 0 {
		t.Fatalf("token counts wrong: p=%d c=%d cr=%d cc=%d", prompt, completion, cacheRead, cacheCreation)
	}
	var m struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if m.Object != "chat.completion" || m.Choices[0].Message.Content != "hi there" || m.Choices[0].FinishReason != "stop" {
		t.Fatalf("output wrong: %+v", m)
	}
}

func TestTitanToOpenAIResponse_LengthFinish(t *testing.T) {
	raw := []byte(`{"inputTextTokenCount":1,"results":[{"tokenCount":1,"outputText":"x","completionReason":"LENGTH"}]}`)
	out, _, _, _, _, err := titanToOpenAIResponse(raw, "amazon.titan-text-express-v1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !jsonContains(string(out), `"finish_reason":"length"`) {
		t.Fatalf("expected length finish reason, got %s", out)
	}
}

func TestTranslateTitanStream(t *testing.T) {
	body := buildBedrockStreamBody(t,
		[]byte(`{"outputText":"Hi","inputTextTokenCount":10}`),
		[]byte(`{"outputText":" there","totalOutputTextTokenCount":2,"completionReason":"FINISH"}`),
	)
	w := httptest.NewRecorder()
	audit, prompt, completion, cacheRead, cacheCreation, errMsg := translateTitanStream(w, body, "amazon.titan-text-express-v1")
	if errMsg != "" {
		t.Fatalf("unexpected stream error: %s", errMsg)
	}
	if string(audit) != "Hi there" {
		t.Fatalf("accumulated audit text = %q", audit)
	}
	if prompt != 10 || completion != 2 || cacheRead != 0 || cacheCreation != 0 {
		t.Fatalf("token counts wrong: p=%d c=%d cr=%d cc=%d", prompt, completion, cacheRead, cacheCreation)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"content":"Hi"`) || !strings.Contains(out, `"content":" there"`) {
		t.Fatalf("missing deltas in SSE output:\n%s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing terminal chunk/DONE marker:\n%s", out)
	}
}
