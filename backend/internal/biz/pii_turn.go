package biz

import "encoding/json"

// currentTurnText isolates the text of the conversation's latest user
// message, used to fix a real deadlock: a client following the OpenAI Chat
// Completions convention resends the FULL messages array on every call, but
// both guardrail engines in this file (scanPII in applyPIIPolicy,
// guardrail.Chain in runInboundChain) historically scanned the raw whole-body
// blob as one piece of text. A `block`-action policy that once matched
// something in turn 1 would therefore re-match the same historical text on
// every later turn forever — the user's *new* input has nothing left to fix,
// but the conversation is permanently rejected anyway. Both call sites use
// this helper to re-check a would-be block against only the latest turn's
// own text before honoring it; a match found solely in resent history never
// blocks (see the callers' doc comments for the downgrade behavior).
//
// ok=false means no recognizable OpenAI-shape "messages" array was found
// (the /embeddings "input" field, a non-chat body, or any other shape this
// helper doesn't understand) — callers must fall back to their original,
// unscoped whole-body decision unchanged rather than guess.
func currentTurnText(body []byte) (text string, ok bool) {
	var parsed map[string]interface{}
	if json.Unmarshal(body, &parsed) != nil {
		return "", false
	}
	rawMessages, has := parsed["messages"].([]interface{})
	if !has || len(rawMessages) == 0 {
		return "", false
	}
	for i := len(rawMessages) - 1; i >= 0; i-- {
		m, mok := rawMessages[i].(map[string]interface{})
		if !mok {
			continue
		}
		if role, _ := m["role"].(string); role != "user" {
			continue
		}
		content := m["content"]
		if messageTextBlockCount(content) > 1 {
			// Some clients (observed with Cherry Studio) bundle a leftover
			// fragment from a just-rejected turn together with the user's
			// actual new input into one multi-block message after a block,
			// e.g. content: [{"type":"text","text":"13066914025"},
			//                {"type":"text","text":"ok, new topic"}].
			// There is no reliable way to isolate "genuinely new" text from
			// "residual flagged fragment" here, so this message contributes
			// no text eligible to justify a hard block on its own — an
			// empty string makes the caller's scoped re-check naturally
			// find nothing and downgrade, exactly like pure history would.
			return "", true
		}
		return messageTextGeneric(content), true
	}
	return "", false
}

// messageTextGeneric extracts the scannable text of a parsed message's
// "content" field: a plain string, or (multimodal shape) the joined text of
// every {"type":"text","text":...} block — image/audio/etc. blocks are
// ignored, matching what a text-only PII/prompt-injection detector can even
// act on.
func messageTextGeneric(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var out []byte
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := block["type"].(string); t != "text" {
				continue
			}
			txt, _ := block["text"].(string)
			if txt == "" {
				continue
			}
			if len(out) > 0 {
				out = append(out, '\n')
			}
			out = append(out, txt...)
		}
		return string(out)
	default:
		return ""
	}
}

// messageTextBlockCount reports how many {"type":"text",...} blocks a
// message's multimodal-shape content array has (0 for a plain-string or
// otherwise-typed content).
func messageTextBlockCount(content interface{}) int {
	arr, ok := content.([]interface{})
	if !ok {
		return 0
	}
	n := 0
	for _, item := range arr {
		block, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := block["type"].(string); t == "text" {
			n++
		}
	}
	return n
}
