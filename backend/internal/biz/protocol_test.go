package biz

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIToAnthropicRequest(t *testing.T) {
	in := []byte(`{
		"model": "claude-sonnet",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": null, "tool_calls": [{"id":"t1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SH\"}"}}]},
			{"role": "tool", "tool_call_id": "t1", "content": "sunny"}
		],
		"max_tokens": 512,
		"temperature": 0.3,
		"stop": ["END"],
		"tools": [{"type":"function","function":{"name":"get_weather","description":"d","parameters":{"type":"object"}}}]
	}`)
	out, err := openAIToAnthropicRequest(in, true)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if m["system"] != "be brief" {
		t.Fatalf("system not lifted: %v", m["system"])
	}
	if m["max_tokens"].(float64) != 512 || m["stream"] != true {
		t.Fatal("max_tokens/stream wrong")
	}
	msgs := m["messages"].([]interface{})
	if len(msgs) != 3 { // user, assistant(tool_use), user(tool_result)
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	asst := msgs[1].(map[string]interface{})
	blocks := asst["content"].([]interface{})
	tu := blocks[0].(map[string]interface{})
	if tu["type"] != "tool_use" || tu["name"] != "get_weather" {
		t.Fatalf("tool_use mapping wrong: %v", tu)
	}
	toolRes := msgs[2].(map[string]interface{})
	trBlocks := toolRes["content"].([]interface{})
	if trBlocks[0].(map[string]interface{})["type"] != "tool_result" {
		t.Fatal("tool_result mapping wrong")
	}
	if _, hasTools := m["tools"]; !hasTools {
		t.Fatal("tools not mapped")
	}
}

func TestOpenAIToAnthropicDefaultMaxTokens(t *testing.T) {
	out, err := openAIToAnthropicRequest([]byte(`{"model":"c","messages":[{"role":"user","content":"x"}]}`), false)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if m["max_tokens"].(float64) != 4096 {
		t.Fatal("anthropic requires max_tokens; default 4096 missing")
	}
}

func TestAnthropicToOpenAIResponse(t *testing.T) {
	in := []byte(`{
		"id": "msg_1", "model": "claude-sonnet",
		"content": [
			{"type": "text", "text": "The weather is "},
			{"type": "tool_use", "id": "t9", "name": "get_weather", "input": {"city": "SH"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 30, "output_tokens": 12, "cache_read_input_tokens": 5, "cache_creation_input_tokens": 3}
	}`)
	out, p, c, cached, cacheCreated, err := anthropicToOpenAIResponse(in, "virtual-claude")
	if err != nil {
		t.Fatal(err)
	}
	if p != 30 || c != 12 || cached != 5 || cacheCreated != 3 {
		t.Fatalf("usage normalization wrong: %d %d %d %d", p, c, cached, cacheCreated)
	}
	var m struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m.Object != "chat.completion" || m.Model != "virtual-claude" {
		t.Fatalf("envelope wrong: %s %s", m.Object, m.Model)
	}
	if m.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("stop_reason mapping: %s", m.Choices[0].FinishReason)
	}
	tc := m.Choices[0].Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" || !strings.Contains(tc.Function.Arguments, "SH") {
		t.Fatalf("tool call mapping: %+v", tc)
	}
}

func TestTranslateAnthropicStream(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"message":{"id":"msg_s","usage":{"input_tokens":10,"cache_read_input_tokens":2}}}`,
		``,
		`event: content_block_start`,
		`data: {"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"delta":{"type":"text_delta","text":"Hello"}}`,
		``,
		`event: content_block_delta`,
		`data: {"delta":{"type":"text_delta","text":" world"}}`,
		``,
		`event: message_delta`,
		`data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	scanner := bufio.NewScanner(strings.NewReader(sse))
	audit, p, c, cached, cacheCreated, errMsg := translateAnthropicStream(rec, scanner, "virtual-claude")

	if string(audit) != "Hello world" {
		t.Fatalf("audit text = %q", audit)
	}
	if p != 10 || c != 7 || cached != 2 || cacheCreated != 0 || errMsg != "" {
		t.Fatalf("usage: %d %d %d %d err=%s", p, c, cached, cacheCreated, errMsg)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"content":"Hello"`) || !strings.Contains(out, `chat.completion.chunk`) {
		t.Fatalf("missing OpenAI chunks: %s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatal("missing finish_reason")
	}
	if !strings.Contains(out, `"completion_tokens":7`) {
		t.Fatal("missing terminal usage chunk")
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
		t.Fatal("missing [DONE] terminator")
	}
}

func TestRespCacheKeyNormalization(t *testing.T) {
	a := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"user":"u1","temperature":0.5}`)
	b := []byte(`{"temperature":0.5,"user":"u2","messages":[{"role":"user","content":"hi"}],"model":"m","stream":false}`)
	ka, ok1 := respCacheKey(1, "real-m", a)
	kb, ok2 := respCacheKey(1, "real-m", b)
	if !ok1 || !ok2 || ka != kb {
		t.Fatalf("field order / stream / user must not change the key: %s vs %s", ka, kb)
	}
	// generation param change ⇒ different key
	c := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":0.9}`)
	kc, _ := respCacheKey(1, "real-m", c)
	if kc == ka {
		t.Fatal("temperature change must change the key")
	}
	// tenant scoping
	kd, _ := respCacheKey(2, "real-m", a)
	if kd == ka {
		t.Fatal("tenant must scope the key")
	}
}

func TestCacheableRequestGates(t *testing.T) {
	if cacheableRequest("/ai/v1/chat/completions", []byte(`{"tools":[{"x":1}]}`)) {
		t.Fatal("tool requests must not be cacheable")
	}
	if cacheableRequest("/ai/v1/chat/completions", []byte(`{"n":3}`)) {
		t.Fatal("n>1 must not be cacheable")
	}
	if !cacheableRequest("/ai/v1/chat/completions", []byte(`{"messages":[]}`)) {
		t.Fatal("plain chat must be cacheable")
	}
	if cacheableRequest("/ai/v1/rerank", []byte(`{}`)) {
		t.Fatal("rerank is not cacheable")
	}
}

func TestCacheHitPricing(t *testing.T) {
	if cacheHitPriceMicro(1000, keyCacheConfig{BillingPolicy: CacheBillingFree}) != 0 {
		t.Fatal("free policy must charge 0")
	}
	if cacheHitPriceMicro(1000, keyCacheConfig{BillingPolicy: CacheBillingFull}) != 1000 {
		t.Fatal("full policy must charge 100%")
	}
	if cacheHitPriceMicro(1000, keyCacheConfig{BillingPolicy: CacheBillingDiscount, DiscountPercent: 30}) != 300 {
		t.Fatal("discount policy must charge the configured percent")
	}
}
