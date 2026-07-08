package biz

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Mistral AI outbound dialect on Bedrock (docs/design/02-protocol-adapters.md,
// confirmed against docs.aws.amazon.com/bedrock/latest/userguide/
// model-parameters-mistral-text-completion.md): scoped to the text-completion
// invoke API (7B/8x7B/Large-2402), which wraps the prompt in Mistral's own
// "<s>[INST] ... [/INST]" instruction format — not the newer chat-completion
// invoke shape (choices[].message, seen for some newer Mistral models on
// Bedrock), which is a documented scope cut for this round, exactly mirroring
// how the existing Claude adapter scopes out other model families.

// buildMistralPrompt folds any system/developer text into the first user
// turn (Mistral's raw format has no system role) and alternates completed
// user/assistant pairs as "[INST] u [/INST] a</s>", ending with a trailing,
// unclosed "[INST] u [/INST]" for the model to complete.
func buildMistralPrompt(messages []oaMessage) string {
	var systemPrefix string
	var turns []oaMessage
	for _, m := range messages {
		if m.Role == "system" || m.Role == "developer" {
			if t := rawContentToText(m.Content); t != "" {
				if systemPrefix != "" {
					systemPrefix += "\n"
				}
				systemPrefix += t
			}
			continue
		}
		turns = append(turns, m)
	}

	var b strings.Builder
	b.WriteString("<s>")
	pendingSystem := systemPrefix
	for i := 0; i < len(turns); i++ {
		m := turns[i]
		text := rawContentToText(m.Content)
		if m.Role == "assistant" {
			// A leading assistant turn (no user turn came first) shouldn't
			// normally happen; fold it in verbatim as a defensive fallback
			// rather than dropping it.
			fmt.Fprintf(&b, "%s</s>", text)
			continue
		}
		userText := text // user, or tool (no native tool role — folded in as user context)
		if pendingSystem != "" {
			userText = pendingSystem + "\n\n" + userText
			pendingSystem = ""
		}
		fmt.Fprintf(&b, "[INST] %s [/INST]", userText)
		if i+1 < len(turns) && turns[i+1].Role == "assistant" {
			i++
			fmt.Fprintf(&b, " %s</s>", rawContentToText(turns[i].Content))
		}
	}
	return b.String()
}

func buildMistralRequestBody(sendBody []byte) ([]byte, error) {
	var in oaChatRequest
	if err := json.Unmarshal(sendBody, &in); err != nil {
		return nil, fmt.Errorf("mistral request translation: %w", err)
	}
	maxTokens := in.MaxTokens
	if in.MaxComplete > 0 {
		maxTokens = in.MaxComplete
	}
	if maxTokens <= 0 {
		maxTokens = 512
	}
	out := map[string]interface{}{"prompt": buildMistralPrompt(in.Messages), "max_tokens": maxTokens}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if stops := extractStopSequences(in.Stop); len(stops) > 0 {
		out["stop"] = stops
	}
	return json.Marshal(out)
}

func mistralFinishReason(reason string) string {
	if reason == "length" {
		return "length"
	}
	return "stop"
}

// mistralToOpenAIResponse: Mistral's text-completion invoke response carries
// no token-usage fields at all (unlike Titan/Llama/Nova) — completion tokens
// are estimated from output length (same 4-chars-per-token heuristic
// BillingManager.estimateMicro already uses for pre-freeze pricing elsewhere,
// since there's no better source here); prompt tokens are left at 0 since the
// original request text isn't available at response-parse time — a
// documented, honest gap rather than a fabricated total.
func mistralToOpenAIResponse(raw []byte, modelName string) ([]byte, int, int, int, int, error) {
	var in struct {
		Outputs []struct {
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("mistral response: %w", err)
	}
	if len(in.Outputs) == 0 {
		return nil, 0, 0, 0, 0, fmt.Errorf("mistral response: no outputs")
	}
	out := in.Outputs[0]
	completion := len(out.Text) / 4
	translated, err := buildOpenAIChatResponse(modelName, "mistral-"+modelName, out.Text,
		mistralFinishReason(out.StopReason), 0, completion)
	return translated, 0, completion, 0, 0, err
}

// translateMistralStream unwraps Bedrock's AWS event-stream framing and
// re-emits each Mistral chunk ({"outputs":[{"text","stop_reason"}]}) as a
// real incremental OpenAI chat.completion.chunk. Token counts are estimated
// the same way mistralToOpenAIResponse does, for the same reason.
func translateMistralStream(w http.ResponseWriter, body io.Reader, modelName string) ([]byte, int, int, int, int, string) {
	writeChunk, finishStream := newBedrockOpenAIChunkWriter(w, modelName)
	var audit strings.Builder
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
			Outputs []struct {
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"outputs"`
		}
		if json.Unmarshal(decoded, &chunk) != nil || len(chunk.Outputs) == 0 {
			continue
		}
		out := chunk.Outputs[0]
		if !roleSent {
			writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
			roleSent = true
		}
		if out.Text != "" {
			audit.WriteString(out.Text)
			writeChunk(map[string]interface{}{"content": out.Text}, nil, nil)
		}
		if out.StopReason != "" {
			finishReason = mistralFinishReason(out.StopReason)
		}
	}

	completionTokens := audit.Len() / 4
	writeChunk(map[string]interface{}{}, finishReason, map[string]interface{}{
		"prompt_tokens": 0, "completion_tokens": completionTokens, "total_tokens": completionTokens,
	})
	finishStream()
	return []byte(audit.String()), 0, completionTokens, 0, 0, errMsg
}
