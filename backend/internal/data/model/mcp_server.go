package model

import (
	"time"

	"gorm.io/gorm"
)

// AIMCPServer registers an upstream MCP (Model Context Protocol) server the
// gateway proxies tool traffic to (docs/design/09-extensibility.md "MCP
// gateway"). Mirrors AIProvider's shape deliberately: same lifecycle (global
// object, platform-admin managed), same at-rest credential handling.
type AIMCPServer struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name        string `gorm:"type:varchar(64);not null;uniqueIndex" json:"name"`
	BaseURL     string `gorm:"type:varchar(256);not null" json:"baseUrl"` // upstream Streamable HTTP MCP endpoint
	APIKey      string `gorm:"type:varchar(512)" json:"-"`                // optional bearer credential to upstream, AES-256-GCM at rest
	IsEnabled   bool   `gorm:"default:true" json:"isEnabled"`
	Description string `gorm:"type:varchar(256)" json:"description"`
}

func (AIMCPServer) TableName() string { return "ai_mcp_servers" }
