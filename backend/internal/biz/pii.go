package biz

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// piiAuditInfo stores the PII action and matched types for audit logging.
type piiAuditInfo struct {
	action string
	types  string
}

// piiAuditCtxKey is the context key for synchronous PII audit info.
type piiAuditCtxKey struct{}

// piiAsyncLogKey is the context key for an async PII result channel.
type piiAsyncLogKey struct{}

func piiAuditFromCtx(ctx context.Context) *piiAuditInfo {
	if v, ok := ctx.Value(piiAuditCtxKey{}).(*piiAuditInfo); ok {
		return v
	}
	return nil
}

// piiOutput is the result of PII policy application.
type piiOutput struct {
	Blocked bool
	NewBody []byte
	Types   string
}

// piiPolicyRuleConfig is the shape of AIPIIPolicy.RuleConfig:
//
//	{"detectors": {"cn_id_card": true, ...},   // absent/null = all detectors on
//	 "promptInjection": true}                  // heuristic injection signatures
type piiPolicyRuleConfig struct {
	Detectors       map[string]bool `json:"detectors"`
	PromptInjection bool            `json:"promptInjection"`
}

var piiPolicyCache sync.Map // policyID(uint, 0 = default) → piiPolicyCacheEntry

type piiPolicyCacheEntry struct {
	policy    *model.AIPIIPolicy // nil = no active policy
	expiresAt time.Time
}

const piiPolicyCacheTTL = time.Minute

// applyPIIPolicy is the guardrail pipeline's inbound entry point (P1-6 rule
// engine + P2 pluggable chain, docs/design/06-security-and-guardrails.md).
// When the resolved policy sets CheckerChain, requests run through the
// pluggable multi-checker chain (buildChainForPolicy); otherwise this is
// byte-for-byte the original single-engine behavior — every policy created
// before the chain existed keeps working exactly as it did.
func (uc *GatewayUseCase) applyPIIPolicy(ctx context.Context, key *model.AIVirtualKey, body []byte) (context.Context, piiOutput) {
	policy := uc.resolvePIIPolicy(ctx, key)
	if policy == nil || !policy.Enabled {
		return ctx, piiOutput{Blocked: false, NewBody: body}
	}

	if chain := uc.buildChainForPolicy(policy, uc.tenantNameForKey(ctx, key)); chain != nil {
		return uc.runInboundChain(ctx, key, policy, chain, body)
	}

	var cfg piiPolicyRuleConfig
	if len(policy.RuleConfig) > 0 {
		_ = json.Unmarshal(policy.RuleConfig, &cfg)
	}
	res := scanPII(body, cfg.Detectors, cfg.PromptInjection)
	if !res.Found {
		return ctx, piiOutput{Blocked: false, NewBody: body}
	}

	types := strings.Join(res.Types, ",")
	if uc.metrics != nil {
		for _, t := range res.Types {
			uc.metrics.GuardrailActions.WithLabelValues(t, policy.Action).Inc()
		}
	}

	switch policy.Action {
	case model.PIIActionBlock:
		// A client following the OpenAI Chat Completions convention resends
		// the FULL messages array on every call — scanPII above just scanned
		// the whole raw body as one blob, so a match anywhere in resent
		// history (an earlier turn already audited, possibly already
		// rejected once) would re-trigger this same block forever, even once
		// the user's own new input has nothing left to fix. Re-check scoped
		// to just the latest turn's own text before honoring the block;
		// res.Redacted already has every match (current turn's included, if
		// any) masked in one pass, so it's always safe to forward.
		if curText, scoped := currentTurnText(body); scoped {
			if !scanPII([]byte(curText), cfg.Detectors, cfg.PromptInjection).Found {
				ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionLog, types: types})
				uc.logger.Warnf("PII 命中位于历史消息，非当前轮新输入——不拦截，已脱敏转发 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
				return ctx, piiOutput{Blocked: false, NewBody: res.Redacted, Types: types}
			}
		}
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionBlock, types: types})
		return ctx, piiOutput{Blocked: true, NewBody: body, Types: types}
	case model.PIIActionRedact:
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionRedact, types: types})
		uc.logger.Warnf("PII 检测命中并脱敏 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		return ctx, piiOutput{Blocked: false, NewBody: res.Redacted, Types: types}
	default: // log
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionLog, types: types})
		uc.logger.Warnf("PII 检测命中（仅记录） keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		return ctx, piiOutput{Blocked: false, NewBody: body, Types: types}
	}
}

// runInboundChain executes the pluggable chain over the raw request body
// text (same "operate on text, not JSON structure" contract as the legacy
// engine — a full IR text-parts extraction is future work, see [D02] IR).
func (uc *GatewayUseCase) runInboundChain(ctx context.Context, key *model.AIVirtualKey, policy *model.AIPIIPolicy, chain *guardrail.Chain, body []byte) (context.Context, piiOutput) {
	finalText, action, findings := chain.Run(ctx, string(body), guardrail.DirectionInbound, func(f guardrail.Finding) {
		if uc.metrics != nil {
			for _, ty := range f.Types {
				uc.metrics.GuardrailActions.WithLabelValues(ty, string(f.Action)).Inc()
			}
		}
	})
	if action == guardrail.ActionNone {
		return ctx, piiOutput{Blocked: false, NewBody: body}
	}
	types := strings.Join(allFindingTypes(findings), ",")
	if uc.metrics != nil {
		for _, ty := range allFindingTypes(findings) {
			uc.metrics.GuardrailActions.WithLabelValues(ty, string(action)).Inc()
		}
	}
	switch action {
	case guardrail.ActionBlock:
		// Same fix as applyPIIPolicy's legacy-engine block case above (see
		// its doc comment): re-check scoped to just the latest turn's own
		// text before honoring a block found by scanning the whole body.
		if curText, scoped := currentTurnText(body); scoped {
			if _, curAction, _ := chain.Run(ctx, curText, guardrail.DirectionInbound, nil); curAction != guardrail.ActionBlock {
				// Every checker in a block-action chain fires with
				// Action==Block uniformly (policy.Action applied chain-wide
				// — see buildChainForPolicy's ADR note), so finalText was
				// never rewritten by Chain.Run's own redact step; fall back
				// to whichever finding actually supplied a masked rewrite
				// (pii_rules always does; a keyword/heuristic checker like
				// prompt_injection/topic_fence has no text-level redaction
				// to offer, so the original text passes through for those —
				// same "rule-grade, not semantic-grade" honesty this
				// pipeline already carries elsewhere).
				forwardText := finalText
				for _, f := range findings {
					if f.Redacted != "" {
						forwardText = f.Redacted
						break
					}
				}
				ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionLog, types: types})
				uc.logger.Warnf("guardrail: 命中位于历史消息，非当前轮新输入——不拦截，已尽力脱敏后转发 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
				return ctx, piiOutput{Blocked: false, NewBody: []byte(forwardText), Types: types}
			}
		}
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionBlock, types: types})
		return ctx, piiOutput{Blocked: true, NewBody: body, Types: types}
	case guardrail.ActionRedact:
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionRedact, types: types})
		uc.logger.Warnf("guardrail: 命中并脱敏 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		return ctx, piiOutput{Blocked: false, NewBody: []byte(finalText), Types: types}
	default: // log
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionLog, types: types})
		uc.logger.Warnf("guardrail: 命中（仅记录） keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		return ctx, piiOutput{Blocked: false, NewBody: body, Types: types}
	}
}

// applyOutboundGuardrail runs the outbound leg of the guardrail chain (docs/
// design/06) against a non-streaming OpenAI-shape response body — purely
// additive: it is a no-op unless the key's resolved policy configures a
// CheckerChain, so no existing deployment sees any behavior change.
func (uc *GatewayUseCase) applyOutboundGuardrail(ctx context.Context, key *model.AIVirtualKey, body []byte) (context.Context, []byte, bool) {
	policy := uc.resolvePIIPolicy(ctx, key)
	if policy == nil || !policy.Enabled {
		return ctx, body, false
	}
	chain := uc.buildChainForPolicy(policy, uc.tenantNameForKey(ctx, key))
	if chain == nil {
		return ctx, body, false
	}
	text := extractAssistantText(body)
	if text == "" {
		return ctx, body, false
	}

	finalText, action, findings := chain.Run(ctx, text, guardrail.DirectionOutbound, func(f guardrail.Finding) {
		if uc.metrics != nil {
			for _, ty := range f.Types {
				uc.metrics.GuardrailActions.WithLabelValues(ty, string(f.Action)).Inc()
			}
		}
	})
	if action == guardrail.ActionNone {
		return ctx, body, false
	}

	types := strings.Join(allFindingTypes(findings), ",")
	if uc.metrics != nil {
		for _, ty := range allFindingTypes(findings) {
			uc.metrics.GuardrailActions.WithLabelValues(ty, string(action)).Inc()
		}
	}
	// Merge with any inbound finding already recorded for this request so the
	// audit row reflects both legs rather than the outbound check clobbering it.
	if existing := piiAuditFromCtx(ctx); existing != nil && existing.types != "" {
		types = existing.types + "," + types
	}

	switch action {
	case guardrail.ActionBlock, guardrail.ActionTerminate:
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionBlock, types: types})
		uc.logger.Warnf("guardrail: 出站响应被拦截 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		blocked, _ := json.Marshal(map[string]interface{}{
			"error": map[string]string{"message": "response blocked by guardrail policy", "code": "GUARDRAIL_BLOCKED"},
		})
		return ctx, blocked, true
	case guardrail.ActionRedact:
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionRedact, types: types})
		uc.logger.Warnf("guardrail: 出站响应已脱敏 keyID=%d policy=%s types=%s", key.ID, policy.Name, types)
		return ctx, replaceAssistantText(body, finalText), false
	default: // log
		ctx = context.WithValue(ctx, piiAuditCtxKey{}, &piiAuditInfo{action: model.PIIActionLog, types: types})
		return ctx, body, false
	}
}

// applyCacheHitGuardrail re-runs the outbound guardrail chain against a
// cached response before it is replayed to a cache-hit caller. A cache entry
// is served across every request that maps to its digest/scope over its
// whole TTL — it was guardrail-checked (if at all) exactly once, at the
// moment it was first written by the request that produced it. A later
// caller can resolve to a different virtual key (different PII policy) or
// the same key's policy can have been tightened since the entry was cached,
// so skipping this check on a hit would let a cache serve content that
// would be blocked/redacted if generated fresh right now. Returns the
// (possibly redacted) entry to serve, and the JSON error body to send
// instead when the policy blocks it — entry itself is left with its
// original Prompt/Completion/ProviderID intact even when blocked, so the
// caller can still bill/audit against the real cached usage numbers.
func (uc *GatewayUseCase) applyCacheHitGuardrail(ctx context.Context, key *model.AIVirtualKey, entry *cachedResponse) (context.Context, *cachedResponse, []byte) {
	ctx, finalBody, blocked := uc.applyOutboundGuardrail(ctx, key, entry.Body)
	if blocked {
		return ctx, entry, finalBody
	}
	out := *entry
	out.Body = finalBody
	return ctx, &out, nil
}

// extractAssistantText / replaceAssistantText target the OpenAI chat.completion
// shape's choices[0].message.content — the canonical internal representation
// every dialect's response is normalized to before this point runs, so a
// single implementation covers openai_compatible, anthropic, and gemini alike.
func extractAssistantText(body []byte) string {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		return ""
	}
	return parsed.Choices[0].Message.Content
}

func replaceAssistantText(body []byte, newText string) []byte {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}
	choices, ok := parsed["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return body
	}
	choice0, ok := choices[0].(map[string]interface{})
	if !ok {
		return body
	}
	message, ok := choice0["message"].(map[string]interface{})
	if !ok {
		return body
	}
	message["content"] = newText
	out, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return out
}

func allFindingTypes(findings []guardrail.Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Types...)
	}
	return out
}

// tenantNameForKey resolves a display name for the external-checker RPC's
// tenant_name field (best-effort — an empty result is fine, the field is
// informational only).
func (uc *GatewayUseCase) tenantNameForKey(ctx context.Context, key *model.AIVirtualKey) string {
	tenantID := uc.tenantIDForKey(ctx, key)
	var t model.AITenant
	if err := uc.db.WithContext(ctx).Select("name").First(&t, tenantID).Error; err != nil {
		return ""
	}
	return t.Name
}

// resolvePIIPolicy loads the key's bound policy, else the default policy,
// with a short local cache (policies change rarely; the proxy path is hot).
func (uc *GatewayUseCase) resolvePIIPolicy(ctx context.Context, key *model.AIVirtualKey) *model.AIPIIPolicy {
	var cacheID uint // 0 = "default policy" slot
	if key.PIIPolicyID != nil {
		cacheID = *key.PIIPolicyID
	}
	if v, ok := piiPolicyCache.Load(cacheID); ok {
		e := v.(piiPolicyCacheEntry)
		if time.Now().Before(e.expiresAt) {
			return e.policy
		}
	}

	var policy model.AIPIIPolicy
	var found *model.AIPIIPolicy
	if cacheID > 0 {
		if err := uc.db.WithContext(ctx).First(&policy, cacheID).Error; err == nil {
			found = &policy
		}
	} else {
		if err := uc.db.WithContext(ctx).Where("is_default = ? AND enabled = ?", true, true).First(&policy).Error; err == nil {
			found = &policy
		}
	}
	piiPolicyCache.Store(cacheID, piiPolicyCacheEntry{policy: found, expiresAt: time.Now().Add(piiPolicyCacheTTL)})
	return found
}
