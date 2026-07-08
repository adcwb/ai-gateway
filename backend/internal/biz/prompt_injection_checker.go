package biz

import (
	"context"
	"strings"

	"github.com/opscenter/ai-gateway/internal/biz/guardrail"
)

// promptInjectionChecker is the standalone `prompt_injection` checker (docs/
// design/06-security-and-guardrails.md P2): the same heuristic signature list
// pii_rules' legacy `promptInjection` flag already used
// (promptInjectionSignatures, pii_engine.go), now usable on its own in a
// chain without needing pii_rules too. LLM-judge escalation remains P2/out
// of scope, same as the legacy flag. Like pii_rules_checker, it applies the
// chain-wide policy.Action uniformly rather than a per-checker override.
type promptInjectionChecker struct {
	fixedAction guardrail.Action
}

func newPromptInjectionChecker(fixedAction guardrail.Action) *promptInjectionChecker {
	return &promptInjectionChecker{fixedAction: fixedAction}
}

func (c *promptInjectionChecker) Name() string         { return "prompt_injection" }
func (c *promptInjectionChecker) Mode() guardrail.Mode { return guardrail.ModeSync }

func (c *promptInjectionChecker) Check(_ context.Context, content *guardrail.Content, _ guardrail.Direction) (guardrail.Finding, error) {
	lower := strings.ToLower(content.Text)
	for _, sig := range promptInjectionSignatures {
		if strings.Contains(lower, sig) {
			return guardrail.Finding{Action: c.fixedAction, Types: []string{"prompt_injection"}}, nil
		}
	}
	return guardrail.Finding{Action: guardrail.ActionNone}, nil
}
