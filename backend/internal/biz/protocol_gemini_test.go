package biz

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIToGeminiRequest(t *testing.T) {
	in := []byte(`{
		"model": "gemini-2.0-flash",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": null, "tool_calls": [{"id":"t1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SH\"}"}}]},
			{"role": "tool", "tool_call_id": "t1", "name": "get_weather", "content": "sunny"}
		],
		"max_tokens": 256, "temperature": 0.2, "stream": true,
		"tools": [{"type":"function","function":{"name":"get_weather","description":"d","parameters":{"type":"object"}}}]
	}`)
	out, modelName, isStream, err := openAIToGeminiRequest(in)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if modelName != "gemini-2.0-flash" || !isStream {
		t.Fatalf("model/stream: %s %v", modelName, isStream)
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if m["systemInstruction"] == nil {
		t.Fatal("system not lifted into systemInstruction")
	}
	gc := m["generationConfig"].(map[string]interface{})
	if gc["maxOutputTokens"].(float64) != 256 {
		t.Fatal("maxOutputTokens missing")
	}
	contents := m["contents"].([]interface{})
	if len(contents) != 3 { // user, model(functionCall), user(functionResponse)
		t.Fatalf("contents = %d, want 3", len(contents))
	}
	modelTurn := contents[1].(map[string]interface{})
	if modelTurn["role"] != "model" {
		t.Fatalf("assistant must map to model role: %v", modelTurn["role"])
	}
	if m["tools"] == nil {
		t.Fatal("tools not mapped to functionDeclarations")
	}
}

func TestGeminiToOpenAIResponse(t *testing.T) {
	in := []byte(`{
		"candidates": [{
			"content": {"parts": [{"text": "Sunny, "},{"functionCall": {"name": "get_weather", "args": {"city": "SH"}}}]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 9, "cachedContentTokenCount": 3}
	}`)
	out, p, c, cached, err := geminiToOpenAIResponse(in, "virtual-gemini")
	if err != nil {
		t.Fatal(err)
	}
	if p != 20 || c != 9 || cached != 3 {
		t.Fatalf("usage: %d %d %d", p, c, cached)
	}
	var m struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					Function struct{ Name string } `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.Unmarshal(out, &m)
	if m.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("tool_use response must map finish to tool_calls, got %s", m.Choices[0].FinishReason)
	}
	if m.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatal("functionCall not mapped to tool_calls")
	}
}

func TestTranslateGeminiStream(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		``,
	}, "\n")
	rec := httptest.NewRecorder()
	audit, p, c, _, errMsg := translateGeminiStream(rec, bufio.NewScanner(strings.NewReader(sse)), "g")
	if string(audit) != "Hello" || p != 5 || c != 2 || errMsg != "" {
		t.Fatalf("audit=%q p=%d c=%d err=%s", audit, p, c, errMsg)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"content":"Hel"`) || !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("chunks wrong: %s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
		t.Fatal("missing [DONE]")
	}
}

func TestCandidatesLeastLatencyOrdering(t *testing.T) {
	rm, _, db := newTestRouter(t)
	ctx := context.Background()
	seedProvider(t, db, 1, "primary", 100, 0, "m")
	seedProvider(t, db, 2, "slow", 100, 0, "m")
	seedProvider(t, db, 3, "fast", 100, 0, "m")

	rm.ReportLatency(ctx, 2, 2000)
	rm.ReportLatency(ctx, 3, 50)

	for i := 0; i < 10; i++ {
		cands := rm.Candidates(ctx, "m", 1, StrategyLeastLatency)
		if cands[0].ProviderID != 1 {
			t.Fatal("primary must stay first")
		}
		if cands[1].ProviderID != 3 || cands[2].ProviderID != 2 {
			t.Fatalf("least_latency must prefer the fast provider: %+v", cands)
		}
	}
}

func TestLatencyEWMASmoothing(t *testing.T) {
	rm, _, _ := newTestRouter(t)
	ctx := context.Background()
	rm.ReportLatency(ctx, 7, 100)
	rm.ReportLatency(ctx, 7, 200) // ewma = 0.3*200 + 0.7*100 = 130
	got := rm.LatencyEWMA(ctx, 7)
	if got < 129 || got > 131 {
		t.Fatalf("ewma = %v, want ~130", got)
	}
}
