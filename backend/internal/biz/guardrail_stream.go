package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/observability"
)

// Streaming outbound guardrail scanning (docs/design/06-security-and-
// guardrails.md's "sliding-window log/terminate-only mode" — previously not
// built at all; streaming responses bypassed applyOutboundGuardrail's chain
// entirely). Once bytes reach the client there is no rewriting them (the
// "Streaming commit rule" in backend/CLAUDE.md), so a block/redact-worthy
// finding can only terminate the stream — stop forwarding further chunks —
// never retroactively redact/block what already went out. A log-only
// finding just records and the stream continues untouched.

// guardrailStreamCap bounds the accumulated-text rescan cost for a
// degenerate huge streamed response: once the visible text exceeds this,
// scanning stops (the rest of the response is allowed through unscanned)
// rather than letting re-scan cost grow unbounded. 64 KiB comfortably covers
// realistic chat responses.
const guardrailStreamCap = 64 << 10

// guardrailStreamWriter wraps the real http.ResponseWriter for a streaming
// response whose key has a resolved policy with a checker chain. It is
// transparent to every existing stream translator (translateGeminiStream/
// translateAnthropicStream/translateBedrockStream/streamProxy) — same
// wrapper-around-http.ResponseWriter pattern as anthropicResponseWriter/
// responsesResponseWriter, including satisfying their http.Flusher type
// assertions.
type guardrailStreamWriter struct {
	real    http.ResponseWriter
	ctx     context.Context
	chain   *guardrail.Chain
	metrics *observability.Metrics

	buf        strings.Builder
	terminated bool
	sawFinding bool
	foundTypes []string
}

func newGuardrailStreamWriter(ctx context.Context, real http.ResponseWriter, chain *guardrail.Chain, metrics *observability.Metrics) *guardrailStreamWriter {
	return &guardrailStreamWriter{real: real, ctx: ctx, chain: chain, metrics: metrics}
}

func (g *guardrailStreamWriter) Header() http.Header { return g.real.Header() }

func (g *guardrailStreamWriter) WriteHeader(status int) { g.real.WriteHeader(status) }

func (g *guardrailStreamWriter) Flush() {
	if f, ok := g.real.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *guardrailStreamWriter) Write(p []byte) (int, error) {
	if g.terminated {
		// Swallow silently: the caller's write loop keeps running to natural
		// upstream completion (a documented, minor inefficiency — not a
		// security gap, since no further bytes reach the client either way).
		return len(p), nil
	}
	if delta := extractSSEDeltaText(p); delta != "" && g.buf.Len() < guardrailStreamCap {
		g.buf.WriteString(delta)
		_, action, findings := g.chain.Run(g.ctx, g.buf.String(), guardrail.DirectionOutbound, func(f guardrail.Finding) {
			if g.metrics != nil {
				for _, ty := range f.Types {
					g.metrics.GuardrailActions.WithLabelValues(ty, string(f.Action)).Inc()
				}
			}
		})
		if action != guardrail.ActionNone {
			g.sawFinding = true
			g.foundTypes = allFindingTypes(findings)
			if g.metrics != nil {
				for _, ty := range g.foundTypes {
					g.metrics.GuardrailActions.WithLabelValues(ty, string(action)).Inc()
				}
			}
		}
		if action == guardrail.ActionBlock || action == guardrail.ActionRedact || action == guardrail.ActionTerminate {
			g.terminated = true
			return len(p), nil
		}
	}
	return g.real.Write(p)
}

// Verdict reports the scan's outcome once the stream has finished, for
// foldGuardrailStreamVerdict to merge into the same audit side-channel
// applyOutboundGuardrail's non-streaming path already populates.
func (g *guardrailStreamWriter) Verdict() (terminated bool, types string) {
	return g.terminated, strings.Join(g.foundTypes, ",")
}

// extractSSEDeltaText parses one or more complete "data: {...}\n\n" SSE
// lines and sums any OpenAI choices[*].delta.content text. Every existing
// streaming call site writes whole lines/events per Write() call (confirmed
// by reading streamProxy and the translate*Stream functions), so no
// cross-call line buffering is needed here.
func extractSSEDeltaText(p []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(p), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		for _, c := range chunk.Choices {
			b.WriteString(c.Delta.Content)
		}
	}
	return b.String()
}

// wrapStreamForGuardrail resolves the key's policy/chain once (same
// resolvePIIPolicy/buildChainForPolicy calls applyOutboundGuardrail already
// makes) and, if one is configured, wraps w so every streamed chunk is
// rescanned. Returns w unchanged (and a nil guardWriter) when no chain
// applies, so the hot path stays a single cache lookup.
func (uc *GatewayUseCase) wrapStreamForGuardrail(ctx context.Context, key *model.AIVirtualKey, w http.ResponseWriter) (http.ResponseWriter, *guardrailStreamWriter) {
	policy := uc.resolvePIIPolicy(ctx, key)
	if policy == nil || !policy.Enabled {
		return w, nil
	}
	chain := uc.buildChainForPolicy(policy, uc.tenantNameForKey(ctx, key))
	if chain == nil {
		return w, nil
	}
	gw := newGuardrailStreamWriter(ctx, w, chain, uc.metrics)
	return gw, gw
}

// foldGuardrailStreamVerdict merges a streaming scan's outcome into the same
// piiAuditCtxKey mechanism the non-streaming path (applyOutboundGuardrail)
// already uses, so writeAuditLog picks it up identically either way.
func (uc *GatewayUseCase) foldGuardrailStreamVerdict(ctx context.Context, gw *guardrailStreamWriter) context.Context {
	if gw == nil {
		return ctx
	}
	terminated, types := gw.Verdict()
	if types == "" {
		return ctx
	}
	action := model.PIIActionLog
	if terminated {
		action = model.PIIActionBlock
		uc.logger.Warnf("guardrail: 流式响应被终止 types=%s", types)
	}
	return context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: action, types: types})
}
