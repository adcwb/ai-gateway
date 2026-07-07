package model

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ProviderModel struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

type AIProvider struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name         string         `gorm:"type:varchar(64);not null;uniqueIndex" json:"name"`
	BaseURL      string         `gorm:"type:varchar(256);not null" json:"baseUrl"`
	ProviderType string         `gorm:"type:varchar(64);default:openai_compatible" json:"providerType"`
	APIKey       string         `gorm:"type:varchar(512);not null" json:"-"`
	Models       datatypes.JSON `gorm:"type:json" json:"models"`
	IsEnabled    bool           `gorm:"default:true" json:"isEnabled"`
	Weight       int            `gorm:"default:100" json:"weight"`
	Priority     int            `gorm:"default:0" json:"priority"` // lower = preferred tier for fallback ordering
	Description  string         `gorm:"type:varchar(256)" json:"description"`
	LastSyncedAt *time.Time     `gorm:"index" json:"lastSyncedAt"`

	// AdapterConfig carries dialect-specific settings
	// (docs/design/02-protocol-adapters.md), e.g.
	//   anthropic:    {"anthropicVersion": "2023-06-01"}
	//   azure_openai: {"apiVersion": "2024-06-01"}
	AdapterConfig datatypes.JSON `gorm:"column:adapter_config;type:json" json:"adapterConfig"`

	// BreakerConfig carries per-provider circuit-breaker overrides
	// (docs/design/01-routing-and-lb.md). Currently only the active health
	// probe toggle/interval are read from it; failure threshold/cooldown/probe
	// quota remain global constants in router.go.
	//   {"activeProbeEnabled": true, "activeProbeIntervalSec": 30}
	BreakerConfig datatypes.JSON `gorm:"column:breaker_config;type:json" json:"breakerConfig"`
}

// Provider dialect identifiers handled by the protocol adapter layer.
const (
	ProviderTypeOpenAICompatible = "openai_compatible"
	ProviderTypeAnthropic        = "anthropic"
	ProviderTypeAzureOpenAI      = "azure_openai"
	ProviderTypeGemini           = "gemini"
)

func (AIProvider) TableName() string { return "ai_providers" }

func (p *AIProvider) IsHealthy() bool { return p.IsEnabled }

func (p *AIProvider) ParseModels() ([]ProviderModel, error) {
	var models []ProviderModel
	if err := json.Unmarshal([]byte(p.Models), &models); err != nil {
		return nil, err
	}
	return models, nil
}

func (p *AIProvider) DefaultModelName() string {
	models, err := p.ParseModels()
	if err != nil || len(models) == 0 {
		return ""
	}
	for _, m := range models {
		if m.IsDefault {
			return m.Name
		}
	}
	return models[0].Name
}
