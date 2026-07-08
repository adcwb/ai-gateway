package dto

import "encoding/json"

// CreatePIIPolicyReq creates a guardrail/PII policy
// (docs/design/06-security-and-guardrails.md). Either the legacy single-engine
// fields (RuleConfig + Action) or the pluggable CheckerChain (or both — the
// chain runs in addition to the legacy engine when both are set) may be used.
type CreatePIIPolicyReq struct {
	Name         string          `json:"name"`
	Enabled      *bool           `json:"enabled"`
	Action       string          `json:"action"`
	IsDefault    bool            `json:"isDefault"`
	RuleConfig   json.RawMessage `json:"ruleConfig"`
	Description  string          `json:"description"`
	CheckerChain json.RawMessage `json:"checkerChain"`
	FailMode     string          `json:"failMode"`
}

// UpdatePIIPolicyReq updates a policy; nil fields are left unchanged.
type UpdatePIIPolicyReq struct {
	ID           uint            `json:"id"`
	Name         *string         `json:"name"`
	Enabled      *bool           `json:"enabled"`
	Action       *string         `json:"action"`
	IsDefault    *bool           `json:"isDefault"`
	RuleConfig   json.RawMessage `json:"ruleConfig"`
	Description  *string         `json:"description"`
	CheckerChain json.RawMessage `json:"checkerChain"`
	FailMode     *string         `json:"failMode"`
}
