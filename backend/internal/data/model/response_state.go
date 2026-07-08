package model

import (
	"time"

	"gorm.io/datatypes"
)

// AIResponseState is the OpenAI Responses API's server-side conversation
// state (docs/design/02-protocol-adapters.md): a request with store=true
// persists one row here under the response ID actually returned to the
// client, so a later request's previous_response_id can resume the
// conversation. Ownership is scoped to the virtual key that created it — a
// stored conversation can only be continued by the same key, closing off
// any cross-tenant enumeration risk.
type AIResponseState struct {
	ID           uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ResponseID   string         `gorm:"column:response_id;type:varchar(64);uniqueIndex" json:"responseId"`
	VirtualKeyID uint           `gorm:"column:virtual_key_id;index" json:"virtualKeyId"`
	Model        string         `gorm:"column:model;type:varchar(128)" json:"model"`
	Messages     datatypes.JSON `gorm:"column:messages;type:json" json:"messages"`
	CreatedAt    time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	ExpiresAt    time.Time      `gorm:"column:expires_at;index" json:"expiresAt"`
}

func (AIResponseState) TableName() string { return "ai_gateway_responses" }
