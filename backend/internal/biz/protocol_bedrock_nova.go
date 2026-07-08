package biz

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Amazon Nova outbound dialect on Bedrock (docs/design/02-protocol-
// adapters.md, confirmed against docs.aws.amazon.com/bedrock/latest/
// userguide/model-card-amazon-nova-*.md and prompt-caching.md): unlike the
// other three new families, Nova's native invoke request is already
// messages-based ({"system":[{"text"}],"messages":[{"role","content":
// [{"text"}]}],"inferenceConfig":{...}}) — no prompt-flattening needed, it
// maps 1:1 from the OpenAI message list.
//
// The sync response shape ({"output":{"message":{"content":[{"text"}]}},
// "stopReason","usage":{"inputTokens","outputTokens"}}) mirrors the Converse
// API's own response envelope (confirmed via the Converse SDK usage pattern
// `response.output.message.content[0].text`), which Nova's native invoke was
// designed alongside. The *streaming* event shape below is the one point of
// real uncertainty in this round: confirmed for ConverseStream
// (messageStart/contentBlockDelta/contentBlockStop/messageStop/metadata),
// assumed — not independently verified — for native
// InvokeModelWithResponseStream, since no AWS account is available in this
// environment to confirm live. Treat as best-effort.

func buildNovaRequestBody(sendBody []byte) ([]byte, error) {
	var in oaChatRequest
	if err := json.Unmarshal(sendBody, &in); err != nil {
		return nil, fmt.Errorf("nova request translation: %w", err)
	}

	var system []map[string]interface{}
	var messages []map[string]interface{}
	for _, m := range in.Messages {
		text := rawContentToText(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case "system", "developer":
			system = append(system, map[string]interface{}{"text": text})
		case "assistant":
			messages = append(messages, map[string]interface{}{"role": "assistant", "content": []map[string]interface{}{{"text": text}}})
		default: // user, tool (no bare tool-result content block in this text-only v1 scope — folded in as user context)
			messages = append(messages, map[string]interface{}{"role": "user", "content": []map[string]interface{}{{"text": text}}})
		}
	}

	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	inferenceConfig := map[string]interface{}{"maxTokens": maxTokens}
	if in.Temperature != nil {
		inferenceConfig["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		inferenceConfig["topP"] = *in.TopP
	}
	if stops := extractStopSequences(in.Stop); len(stops) > 0 {
		inferenceConfig["stopSequences"] = stops
	}

	out := map[string]interface{}{"messages": messages, "inferenceConfig": inferenceConfig}
	if len(system) > 0 {
		out["system"] = system
	}
	return json.Marshal(out)
}

func novaFinishReason(reason string) string {
	if reason == "max_tokens" {
		return "length"
	}
	return "stop"
}

func novaToOpenAIResponse(raw []byte, modelName string) ([]byte, int, int, int, int, error) {
	var in struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string `json:"stopReason"`
		Usage      struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("nova response: %w", err)
	}
	var text strings.Builder
	for _, c := range in.Output.Message.Content {
		text.WriteString(c.Text)
	}
	translated, err := buildOpenAIChatResponse(modelName, "nova-"+modelName, text.String(),
		novaFinishReason(in.StopReason), in.Usage.InputTokens, in.Usage.OutputTokens)
	return translated, in.Usage.InputTokens, in.Usage.OutputTokens, 0, 0, err
}

// translateNovaStream unwraps Bedrock's AWS event-stream framing and
// re-emits Nova's named streaming events (see the best-effort caveat above)
// as real incremental OpenAI chat.completion.chunk frames.
func translateNovaStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
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
		var evt struct {
			ContentBlockDelta *struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"contentBlockDelta"`
			MessageStop *struct {
				StopReason string `json:"stopReason"`
			} `json:"messageStop"`
			Metadata *struct {
				Usage struct {
					InputTokens  int `json:"inputTokens"`
					OutputTokens int `json:"outputTokens"`
				} `json:"usage"`
			} `json:"metadata"`
		}
		if json.Unmarshal(decoded, &evt) != nil {
			continue
		}
		if !roleSent {
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
			roleSent = true
		}
		if evt.ContentBlockDelta != nil && evt.ContentBlockDelta.Delta.Text != "" {
			audit.WriteString(evt.ContentBlockDelta.Delta.Text)
			writeChunk(map[string]interface{}{"content": evt.ContentBlockDelta.Delta.Text}, nil, nil)
		}
		if evt.MessageStop != nil && evt.MessageStop.StopReason != "" {
			finishReason = novaFinishReason(evt.MessageStop.StopReason)
		}
		if evt.Metadata != nil {
			promptTokens = evt.Metadata.Usage.InputTokens
			completionTokens = evt.Metadata.Usage.OutputTokens
		}
	}

	writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
		"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
		"total_tokens": promptTokens + completionTokens,
	})
	finishStream()
	return []byte(audit.String()), promptTokens, completionTokens, 0, 0, errMsg
}
