package biz

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Pluggable guardrail chain construction (docs/design/06-security-and-
// guardrails.md P2): builds a guardrail.Chain from AIPIIPolicy.CheckerChain.
// Only reached when a policy actually sets CheckerChain — every policy
// created before this existed keeps using the legacy single-engine path in
// pii.go untouched.

type checkerConfig struct {
	Name     string          `json:"name"`
	Settings json.RawMessage `json:"settings"`
}

type externalCheckerSettings struct {
	Target    string `json:"target"`
	TimeoutMs int    `json:"timeoutMs"`
}

type piiRulesSettings struct {
	Detectors       map[string]bool `json:"detectors"`
	PromptInjection bool            `json:"promptInjection"`
}

// topicFenceSettings is the shape of the "topic_fence" checker's settings
// object in a policy's checker_chain (docs/design/06-security-and-
// guardrails.md P2): a curated blocklist, matched case-insensitively.
type topicFenceSettings struct {
	BlockedTopics []string `json:"blockedTopics"`
}

var chainCache sync.Map // policyID → chainCacheEntry

type chainCacheEntry struct {
	chain     *guardrail.Chain
	expiresAt time.Time
}

const chainCacheTTL = time.Minute

// buildChainForPolicy parses policy.CheckerChain and constructs (or returns a
// cached) guardrail.Chain. Returns nil if CheckerChain is empty — callers
// fall back to the legacy path.
func (uc *GatewayUseCase) buildChainForPolicy(policy *model.AIPIIPolicy, tenantName string) *guardrail.Chain {
	if len(policy.CheckerChain) == 0 || string(policy.CheckerChain) == "null" {
		return nil
	}
	if v, ok := chainCache.Load(policy.ID); ok {
		e := v.(chainCacheEntry)
		if time.Now().Before(e.expiresAt) {
			return e.chain
		}
	}

	var configs []checkerConfig
	if err := json.Unmarshal(policy.CheckerChain, &configs); err != nil || len(configs) == 0 {
		uc.logger.Warnf("guardrail: 策略 checker_chain 解析失败 policyID=%d err=%v", policy.ID, err)
		return nil
	}

	checkers := make([]guardrail.Checker, 0, len(configs))
	for _, cc := range configs {
		switch cc.Name {
		case "pii_rules":
			var s piiRulesSettings
			if len(cc.Settings) > 0 {
				_ = json.Unmarshal(cc.Settings, &s)
			}
			action := guardrail.Action(policy.Action)
			checkers = append(checkers, newPIIRulesChecker(s.Detectors, s.PromptInjection, action))
		case "prompt_injection":
			checkers = append(checkers, newPromptInjectionChecker(guardrail.Action(policy.Action)))
		case "topic_fence":
			var s topicFenceSettings
			if len(cc.Settings) > 0 {
				_ = json.Unmarshal(cc.Settings, &s)
			}
			checkers = append(checkers, newTopicFenceChecker(s.BlockedTopics, guardrail.Action(policy.Action)))
		case "external":
			var s externalCheckerSettings
			if len(cc.Settings) > 0 {
				_ = json.Unmarshal(cc.Settings, &s)
			}
			if s.Target == "" {
				uc.logger.Warnf("guardrail: external checker 缺少 target，已跳过 policyID=%d", policy.ID)
				continue
			}
			timeout := time.Duration(s.TimeoutMs) * time.Millisecond
			checker, err := guardrail.NewExternalChecker("external", s.Target, timeout, tenantName)
			if err != nil {
				uc.logger.Warnf("guardrail: external checker 初始化失败 policyID=%d err=%v", policy.ID, err)
				continue
			}
			checkers = append(checkers, checker)
		default:
			uc.logger.Warnf("guardrail: 未知 checker %q，已跳过 policyID=%d", cc.Name, policy.ID)
		}
	}
	if len(checkers) == 0 {
		return nil
	}

	failOpen := policy.FailMode != "closed"
	chain := guardrail.NewChain(checkers, guardrail.ChainOption{FailOpen: failOpen}, func(name string, err error) {
		if uc.metrics != nil {
			uc.metrics.GuardrailActions.WithLabelValues(name, "error").Inc()
		}
		uc.logger.Warnf("guardrail: checker 执行失败 checker=%s err=%v", name, err)
	})
	chainCache.Store(policy.ID, chainCacheEntry{chain: chain, expiresAt: time.Now().Add(chainCacheTTL)})
	return chain
}
