package biz

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Gemini outbound dialect (docs/design/02-protocol-adapters.md).
// Endpoint shape: {baseURL}/v1beta/models/{model}:generateContent (sync) or
// :streamGenerateContent?alt=sse (SSE), auth via x-goog-api-key. The model
// lives in the PATH, so request building owns URL construction.

// openAIToGeminiRequest maps the OpenAI chat body onto GenerateContentRequest.
// System messages lift into systemInstruction; assistant tool_calls become
// functionCall parts; role:"tool" results become functionResponse parts.
func openAIToGeminiRequest(body []byte) ([]byte, string, bool, error) {
	var in oaChatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, "", false, err
	}
	var streamProbe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &streamProbe)

	genCfg := map[string]interface{}{}
	if in.Temperature != nil {
		genCfg["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		genCfg["topP"] = *in.TopP
	}
	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens > 0 {
		genCfg["maxOutputTokens"] = maxTokens
	}
	if len(in.Stop) > 0 {
		var one string
		var many []string
		if json.Unmarshal(in.Stop, &one) == nil {
			genCfg["stopSequences"] = []string{one}
		} else if json.Unmarshal(in.Stop, &many) == nil && len(many) > 0 {
			genCfg["stopSequences"] = many
		}
	}

	out := map[string]interface{}{}
	if len(genCfg) > 0 {
		out["generationConfig"] = genCfg
	}
	if len(in.Tools) > 0 {
		decls := make([]map[string]interface{}, 0, len(in.Tools))
		for _, t := range in.Tools {
			if t.Type != "function" {
				continue
			}
			decl := map[string]interface{}{
				"name":        t.Function.Name,
				"description": t.Function.Description,
			}
			if len(t.Function.Parameters) > 0 {
				decl["parameters"] = t.Function.Parameters
			}
			decls = append(decls, decl)
		}
		if len(decls) > 0 {
			out["tools"] = []map[string]interface{}{{"functionDeclarations": decls}}
		}
	}

	var systemParts []string
	contents := make([]map[string]interface{}, 0, len(in.Messages))
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			systemParts = append(systemParts, rawContentToText(m.Content))
		case "tool":
			contents = append(contents, map[string]interface{}{
				"role": "user",
				"parts": []map[string]interface{}{{
					"functionResponse": map[string]interface{}{
						"name":     m.Name,
						"response": map[string]interface{}{"content": rawContentToText(m.Content)},
					},
				}},
			})
		case "assistant":
			parts := []map[string]interface{}{}
			if txt := rawContentToText(m.Content); txt != "" {
				parts = append(parts, map[string]interface{}{"text": txt})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
					args = map[string]interface{}{}
				}
				parts = append(parts, map[string]interface{}{
					"functionCall": map[string]interface{}{"name": tc.Function.Name, "args": args},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]interface{}{"role": "model", "parts": parts})
			}
		default: // user
			contents = append(contents, map[string]interface{}{
				"role":  "user",
				"parts": []map[string]interface{}{{"text": rawContentToText(m.Content)}},
			})
		}
	}
	if len(systemParts) > 0 {
		out["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{{"text": strings.Join(systemParts, "\n")}},
		}
	}
	out["contents"] = contents

	b, err := json.Marshal(out)
	return b, in.Model, streamProbe.Stream, err
}

// Gemini response shapes shared by sync + stream.
type geminiCandidate struct {
	Content struct {
		Parts []struct {
			Text         string `json:"text"`
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		} `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata"`
}

func mapGeminiFinishReason(r string) string {
	switch strings.ToUpper(r) {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST":
		return "content_filter"
	default: // STOP, FINISH_REASON_UNSPECIFIED, ""
		return "stop"
	}
}

// geminiToOpenAIResponse converts a complete GenerateContentResponse into an
// OpenAI chat.completion body and normalized usage.
func geminiToOpenAIResponse(body []byte, modelName string) ([]byte, int, int, int, error) {
	var in geminiResponse
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, 0, 0, 0, err
	}
	var text strings.Builder
	var toolCalls []map[string]interface{}
	finish := "stop"
	if len(in.Candidates) > 0 {
		cand := in.Candidates[0]
		finish = mapGeminiFinishReason(cand.FinishReason)
		for i, part := range cand.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				args := "{}"
				if len(part.FunctionCall.Args) > 0 {
					args = string(part.FunctionCall.Args)
				}
				toolCalls = append(toolCalls, map[string]interface{}{
					"id": fmt.Sprintf("call_gemini_%d", i), "type": "function",
					"function": map[string]interface{}{"name": part.FunctionCall.Name, "arguments": args},
				})
			}
		}
	}
	message := map[string]interface{}{"role": "assistant", "content": text.String()}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		if text.Len() == 0 {
			message["content"] = nil
		}
		finish = "tool_calls"
	}
	p, c, cached := 0, 0, 0
	if in.UsageMetadata != nil {
		p, c, cached = in.UsageMetadata.PromptTokenCount, in.UsageMetadata.CandidatesTokenCount, in.UsageMetadata.CachedContentTokenCount
	}
	out := map[string]interface{}{
		"id":      "gemini-" + modelName,
		"object":  "chat.completion",
		"model":   modelName,
		"choices": []map[string]interface{}{{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]interface{}{
			"prompt_tokens":     p,
			"completion_tokens": c,
			"total_tokens":      p + c,
			"prompt_tokens_details": map[string]interface{}{
				"cached_tokens": cached,
			},
		},
	}
	b, err := json.Marshal(out)
	return b, p, c, cached, err
}

// translateGeminiStream reads Gemini SSE (streamGenerateContent?alt=sse) and
// writes OpenAI chat.completion.chunk SSE. Each event is a full
// GenerateContentResponse carrying a text delta; usageMetadata rides on the
// trailing chunks. Returns (audit text, prompt, completion, cacheRead, errMsg).
func translateGeminiStream(w http.ResponseWriter, body *bufio.Scanner, modelName string) ([]byte, int, int, int, string) {
	flusher, _ := w.(http.Flusher)
	var audit strings.Builder
	promptTokens, completionTokens, cachedTokens := 0, 0, 0
	finishReason := "stop"
	roleSent := false
	toolIndex := -1

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		chunk := map[string]interface{}{
			"id":     "gemini-" + modelName,
			"object": "chat.completion.chunk",
			"model":  modelName,
			"choices": []map[string]interface{}{{
				"index": 0, "delta": delta, "finish_reason": finish,
			}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	for body.Scan() {
		line := body.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var evt geminiResponse
		if json.Unmarshal([]byte(data), &evt) != nil {
			continue
		}
		if !roleSent {
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
			roleSent = true
		}
		if len(evt.Candidates) > 0 {
			cand := evt.Candidates[0]
			if cand.FinishReason != "" {
				finishReason = mapGeminiFinishReason(cand.FinishReason)
			}
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					audit.WriteString(part.Text)
					writeChunk(map[string]interface{}{"content": part.Text}, nil, nil)
				}
				if part.FunctionCall != nil {
					toolIndex++
					args := "{}"
					if len(part.FunctionCall.Args) > 0 {
						args = string(part.FunctionCall.Args)
					}
					finishReason = "tool_calls"
					writeChunk(map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": toolIndex, "id": fmt.Sprintf("call_gemini_%d", toolIndex), "type": "function",
							"function": map[string]interface{}{"name": part.FunctionCall.Name, "arguments": args},
						}},
					}, nil, nil)
				}
			}
		}
		if evt.UsageMetadata != nil {
			promptTokens = evt.UsageMetadata.PromptTokenCount
			completionTokens = evt.UsageMetadata.CandidatesTokenCount
			cachedTokens = evt.UsageMetadata.CachedContentTokenCount
		}
	}

	writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      promptTokens + completionTokens,
		"prompt_tokens_details": map[string]interface{}{
			"cached_tokens": cachedTokens,
		},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return []byte(audit.String()), promptTokens, completionTokens, cachedTokens, ""
}
