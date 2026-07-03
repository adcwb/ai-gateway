package biz

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

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

// applyPIIPolicy runs the rule-based PII engine (P1-6,
// docs/design/06-security-and-guardrails.md) for the key's bound policy —
// falling back to the default policy — and applies its action:
//
//	block  → request rejected (caller writes the 400)
//	redact → request body rewritten with type-preserving masks
//	log    → findings recorded in audit only, body untouched
func (uc *GatewayUseCase) applyPIIPolicy(ctx context.Context, key *model.AIVirtualKey, body []byte) (context.Context, piiOutput) {
	policy := uc.resolvePIIPolicy(ctx, key)
	if policy == nil || !policy.Enabled {
		return ctx, piiOutput{Blocked: false, NewBody: body}
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
