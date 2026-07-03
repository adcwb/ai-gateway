package biz

import (
	"bytes"
	"context"
	"strings"
)

type clientAgentCtxKey struct{}

func withClientAgent(ctx context.Context, agent string) context.Context {
	if agent == "" {
		return ctx
	}
	return context.WithValue(ctx, clientAgentCtxKey{}, agent)
}

func clientAgentFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(clientAgentCtxKey{}).(string); ok {
		return v
	}
	return ""
}

const clientAgentMaxLen = 128
const bodyScanLimit = 16 << 10

var uaSignatures = []struct{ marker, name string }{
	{"claude-cli", "Claude Code"},
	{"claude-code", "Claude Code"},
	{"codex", "Codex CLI"},
	{"cline", "Cline"},
	{"roo-cline", "Roo Code"},
	{"roocode", "Roo Code"},
	{"cursor", "Cursor"},
	{"aider", "Aider"},
	{"cherrystudio", "Cherry Studio"},
	{"chatbox", "Chatbox"},
	{"lobehub", "LobeChat"},
	{"lobe-chat", "LobeChat"},
	{"dify", "Dify"},
	{"langchain", "LangChain"},
	{"llama-index", "LlamaIndex"},
	{"llamaindex", "LlamaIndex"},
}

var bodyMarkers = []struct{ marker, name string }{
	{"Hermes Agent Persona", "Hermes Agent"},
	{"You are Claude Code", "Claude Code"},
}

func detectClientAgent(ua string, body []byte) string {
	uaLower := strings.ToLower(strings.TrimSpace(ua))
	if uaLower != "" {
		for _, sig := range uaSignatures {
			if strings.Contains(uaLower, sig.marker) {
				return sig.name
			}
		}
	}

	if len(body) > 0 {
		scan := body
		if len(scan) > bodyScanLimit {
			scan = scan[:bodyScanLimit]
		}
		for _, bm := range bodyMarkers {
			if bytes.Contains(scan, []byte(bm.marker)) {
				return bm.name
			}
		}
	}

	return truncateClientAgent(strings.TrimSpace(ua))
}

func truncateClientAgent(s string) string {
	if len(s) <= clientAgentMaxLen {
		return s
	}
	return strings.ToValidUTF8(s[:clientAgentMaxLen], "")
}
