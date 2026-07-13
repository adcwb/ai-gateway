package biz

import (
	"context"
	"strings"

	"github.com/adcwb/ai-gateway/internal/biz/guardrail"
)

// topicFenceChecker is the standalone `topic_fence` checker (docs/design/
// 06-security-and-guardrails.md P2): a curated list of blocked topic
// phrases, same zero-dependency rule-based approach as pii_rules/
// prompt_injection — a real semantic topic classifier needs an LLM judge or
// the external gRPC engine, both already out of scope elsewhere in this
// design. Like pii_rules_checker, it applies the chain-wide policy.Action
// uniformly rather than a per-checker override.
type topicFenceChecker struct {
	blockedTopics []string // lowercased at construction
	fixedAction   guardrail.Action
}

func newTopicFenceChecker(blockedTopics []string, fixedAction guardrail.Action) *topicFenceChecker {
	lowered := make([]string, 0, len(blockedTopics))
	for _, t := range blockedTopics {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			lowered = append(lowered, t)
		}
	}
	return &topicFenceChecker{blockedTopics: lowered, fixedAction: fixedAction}
}

func (c *topicFenceChecker) Name() string         { return "topic_fence" }
func (c *topicFenceChecker) Mode() guardrail.Mode { return guardrail.ModeSync }

func (c *topicFenceChecker) Check(_ context.Context, content *guardrail.Content, _ guardrail.Direction) (guardrail.Finding, error) {
	if len(c.blockedTopics) == 0 {
		return guardrail.Finding{Action: guardrail.ActionNone}, nil
	}
	lower := strings.ToLower(content.Text)
	for _, topic := range c.blockedTopics {
		if strings.Contains(lower, topic) {
			return guardrail.Finding{Action: c.fixedAction, Types: []string{"topic_fence"}}, nil
		}
	}
	return guardrail.Finding{Action: guardrail.ActionNone}, nil
}
