package model

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	PIIActionBlock  = "block"
	PIIActionRedact = "redact"
	PIIActionLog    = "log"
)

type AIPIIPolicy struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name        string         `gorm:"type:varchar(128);not null" json:"name"`
	Enabled     bool           `gorm:"default:true" json:"enabled"`
	Action      string         `gorm:"type:varchar(16);not null;default:'block'" json:"action"`
	IsDefault   *bool          `gorm:"column:is_default" json:"isDefault"`
	RuleConfig  datatypes.JSON `gorm:"type:json" json:"ruleConfig"`
	Description string         `gorm:"type:varchar(256)" json:"description"`

	// CheckerChain generalizes this policy into the pluggable guardrail
	// pipeline (docs/design/06-security-and-guardrails.md P2): an ordered
	// []{"name","settings"} list. Empty/absent preserves the exact legacy
	// behavior above (single pii_rules engine, RuleConfig + Action) — this
	// column is purely additive, no existing policy's behavior changes.
	CheckerChain datatypes.JSON `gorm:"column:checker_chain;type:json" json:"checkerChain"`
	// FailMode is "open" (default) or "closed" — see guardrail.ChainOption.FailOpen.
	FailMode string `gorm:"column:fail_mode;type:varchar(8);default:'open'" json:"failMode"`

	BoundKeyCount int64 `gorm:"-" json:"boundKeyCount"`
}

func (AIPIIPolicy) TableName() string { return "ai_pii_policies" }

func (p AIPIIPolicy) MarshalJSON() ([]byte, error) {
	type Alias AIPIIPolicy
	isDefault := p.IsDefault != nil && *p.IsDefault
	return json.Marshal(struct {
		Alias
		IsDefault bool `json:"isDefault"`
	}{
		Alias:     Alias(p),
		IsDefault: isDefault,
	})
}
