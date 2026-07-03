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
