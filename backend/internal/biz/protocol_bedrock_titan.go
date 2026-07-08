package biz

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Amazon Titan Text outbound dialect on Bedrock (docs/design/02-protocol-
// adapters.md, confirmed against docs.aws.amazon.com/bedrock/latest/
// userguide/model-parameters-titan-text.md): single-string "inputText" +
// "textGenerationConfig", no native multi-turn concept — messages are
// flattened into the conversational "User: ...\nBot: ..." convention Titan's
// own docs recommend.

func buildTitanPrompt(messages []oaMessage) string {
	var b strings.Builder
	for _, m := range messages {
		text := rawContentToText(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case "system", "developer":
			fmt.Fprintf(&b, "System: %s\n", text)
		case "assistant":
			fmt.Fprintf(&b, "Bot: %s\n", text)
		default: // user, tool (no native tool role — folded in as user context)
			fmt.Fprintf(&b, "User: %s\n", text)
		}
	}
	b.WriteString("Bot:")
	return b.String()
}

func buildTitanRequestBody(sendBody []byte) ([]byte, error) {
	var in oaChatRequest
	if err := json.Unmarshal(sendBody, &in); err != nil {
		return nil, fmt.Errorf("titan request translation: %w", err)
	}
	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	cfg := map[string]interface{}{"maxTokenCount": maxTokens}
	if in.Temperature != nil {
		cfg["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		cfg["topP"] = *in.TopP
	}
	if stops := extractStopSequences(in.Stop); len(stops) > 0 {
		cfg["stopSequences"] = stops
	}
	out := map[string]interface{}{"inputText": buildTitanPrompt(in.Messages), "textGenerationConfig": cfg}
	return json.Marshal(out)
}

func titanFinishReason(reason string) string {
	if reason == "LENGTH" {
		return "length"
	}
	return "stop"
}

func titanToOpenAIResponse(raw []byte, modelName string) ([]byte, int, int, int, int, error) {
	var in struct {
		InputTextTokenCount int `json:"inputTextTokenCount"`
		Results             []struct {
			TokenCount       int    `json:"tokenCount"`
			OutputText       string `json:"outputText"`
			CompletionReason string `json:"completionReason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("titan response: %w", err)
	}
	if len(in.Results) == 0 {
		return nil, 0, 0, 0, 0, fmt.Errorf("titan response: no results")
	}
	result := in.Results[0]
	translated, err := buildOpenAIChatResponse(modelName, "titan-"+modelName, result.OutputText,
		titanFinishReason(result.CompletionReason), in.InputTextTokenCount, result.TokenCount)
	return translated, in.InputTextTokenCount, result.TokenCount, 0, 0, err
}

// translateTitanStream unwraps Bedrock's AWS event-stream framing (via the
// shared bedrockNextChunk) and re-emits each Titan chunk
// ({"outputText","inputTextTokenCount","totalOutputTextTokenCount",
// "completionReason"}) as a real incremental OpenAI chat.completion.chunk.
func translateTitanStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
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
			InputTextTokenCount       int    `json:"inputTextTokenCount"`
			TotalOutputTextTokenCount int    `json:"totalOutputTextTokenCount"`
			OutputText                string `json:"outputText"`
			CompletionReason          string `json:"completionReason"`
		}
		if json.Unmarshal(decoded, &chunk) != nil {
			continue
		}
		if !roleSent {
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
			roleSent = true
		}
		if chunk.OutputText != "" {
			audit.WriteString(chunk.OutputText)
			writeChunk(map[string]interface{}{"content": chunk.OutputText}, nil, nil)
		}
		if chunk.InputTextTokenCount > 0 {
			promptTokens = chunk.InputTextTokenCount
		}
		if chunk.TotalOutputTextTokenCount > 0 {
			completionTokens = chunk.TotalOutputTextTokenCount
		}
		if chunk.CompletionReason != "" {
			finishReason = titanFinishReason(chunk.CompletionReason)
		}
	}

	writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
		"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
		"total_tokens": promptTokens + completionTokens,
	})
	finishStream()
	return []byte(audit.String()), promptTokens, completionTokens, 0, 0, errMsg
}
