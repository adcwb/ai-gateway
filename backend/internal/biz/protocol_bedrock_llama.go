package biz

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Meta Llama outbound dialect on Bedrock (docs/design/02-protocol-
// adapters.md, confirmed against docs.aws.amazon.com/bedrock/latest/
// userguide/model-parameters-meta.md and the Llama 3 InvokeModel example):
// single-string "prompt" built from Llama's own chat template
// (<|begin_of_text|> + <|start_header_id|>{role}<|end_header_id|> per turn),
// "max_gen_len"/"temperature"/"top_p". No native tool-calling in this classic
// invoke shape — text only (see protocol_bedrock.go's scope-cut note).

func buildLlamaPrompt(messages []oaMessage) string {
	var b strings.Builder
	b.WriteString("<|begin_of_text|>")
	for _, m := range messages {
		text := rawContentToText(m.Content)
		if text == "" {
			continue
		}
		role := m.Role
		switch role {
		case "developer":
			role = "system"
		case "tool":
			role = "user" // no native tool role — folded in as user context
		case "system", "assistant", "user":
			// unchanged
		default:
			role = "user"
		}
		fmt.Fprintf(&b, "<|start_header_id|>%s<|end_header_id|>\n\n%s<|eot_id|>", role, text)
	}
	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	return b.String()
}

func buildLlamaRequestBody(sendBody []byte) ([]byte, error) {
	var in oaChatRequest
	if err := json.Unmarshal(sendBody, &in); err != nil {
		return nil, fmt.Errorf("llama request translation: %w", err)
	}
	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens <= 0 {
		maxTokens = 512
	}
	out := map[string]interface{}{"prompt": buildLlamaPrompt(in.Messages), "max_gen_len": maxTokens}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	return json.Marshal(out)
}

func llamaFinishReason(reason string) string {
	if reason == "length" {
		return "length"
	}
	return "stop"
}

func llamaToOpenAIResponse(raw []byte, modelName string) ([]byte, int, int, int, int, error) {
	var in struct {
		Generation           string `json:"generation"`
		PromptTokenCount     int    `json:"prompt_token_count"`
		GenerationTokenCount int    `json:"generation_token_count"`
		StopReason           string `json:"stop_reason"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("llama response: %w", err)
	}
	translated, err := buildOpenAIChatResponse(modelName, "llama-"+modelName, in.Generation,
		llamaFinishReason(in.StopReason), in.PromptTokenCount, in.GenerationTokenCount)
	return translated, in.PromptTokenCount, in.GenerationTokenCount, 0, 0, err
}

// translateLlamaStream unwraps Bedrock's AWS event-stream framing and
// re-emits each Llama chunk ({"generation","prompt_token_count",
// "generation_token_count","stop_reason"}) as a real incremental OpenAI
// chat.completion.chunk.
func translateLlamaStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
	writeChunk, finishStream := newBedrockOpenAIChunkWriter(w, modelName)
	var audit strings.Builder
	promptTokens, completionTokens := 0, 0
	finishReason := "stop"
	roleSent := false
	errMsg := ""

	for {
		decoded, err := bedrockNextChunk(body)
		if err != nil {
			if err != io.EOF {
				errMsg = err.Error()
			}
			break
		}
		var chunk struct {
			Generation           string `json:"generation"`
			PromptTokenCount     int    `json:"prompt_token_count"`
			GenerationTokenCount int    `json:"generation_token_count"`
			StopReason           string `json:"stop_reason"`
		}
		if json.Unmarshal(decoded, &chunk) != nil {
			continue
		}
		if !roleSent {
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
			roleSent = true
		}
		if chunk.Generation != "" {
			audit.WriteString(chunk.Generation)
			writeChunk(map[string]interface{}{"content": chunk.Generation}, nil, nil)
		}
		if chunk.PromptTokenCount > 0 {
			promptTokens = chunk.PromptTokenCount
		}
		if chunk.GenerationTokenCount > 0 {
			completionTokens = chunk.GenerationTokenCount
		}
		if chunk.StopReason != "" {
			finishReason = llamaFinishReason(chunk.StopReason)
		}
	}

	writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
		"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
		"total_tokens": promptTokens + completionTokens,
	})
	finishStream()
	return []byte(audit.String()), promptTokens, completionTokens, 0, 0, errMsg
}
