package biz

import (
	"context"

	"github.com/adcwb/ai-gateway/internal/biz/guardrail"
)

// piiRulesChecker adapts the existing rule-based engine (pii_engine.go's
// scanPII, unchanged) to the guardrail.Checker interface — the P1 built-in
// checker in the pluggable chain (docs/design/06-security-and-guardrails.md).
// It lives in package biz (not package guardrail) because it depends on
// scanPII/piiDetectors, which stay here to avoid disturbing the existing,
// tested pii_engine_test.go.
type piiRulesChecker struct {
	detectors   map[string]bool
	injection   bool
	fixedAction guardrail.Action // policy.Action, applied uniformly (legacy shape)
}

func newPIIRulesChecker(detectors map[string]bool, injection bool, fixedAction guardrail.Action) *piiRulesChecker {
	return &piiRulesChecker{detectors: detectors, injection: injection, fixedAction: fixedAction}
}

func (c *piiRulesChecker) Name() string         { return "pii_rules" }
func (c *piiRulesChecker) Mode() guardrail.Mode { return guardrail.ModeSync }

func (c *piiRulesChecker) Check(_ context.Context, content *guardrail.Content, _ guardrail.Direction) (guardrail.Finding, error) {
	res := scanPII([]byte(content.Text), c.detectors, c.injection)
	if !res.Found {
		return guardrail.Finding{Action: guardrail.ActionNone}, nil
	}
	return guardrail.Finding{
		Action:   c.fixedAction,
		Types:    res.Types,
		Redacted: string(res.Redacted),
	}, nil
}
