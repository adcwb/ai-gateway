package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type AIModelItem struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	ProviderID uint   `gorm:"not null;index;uniqueIndex:uk_provider_model" json:"providerId"`
	Name       string `gorm:"type:varchar(128);not null;uniqueIndex:uk_provider_model" json:"name"`

	ModelType     string `gorm:"type:varchar(32);default:llm;index" json:"modelType"`
	ContextWindow int    `gorm:"default:0" json:"contextWindow"`
	IsDefault     bool   `gorm:"default:false;index" json:"isDefault"`
	IsEnabled     bool   `gorm:"default:true;index" json:"isEnabled"`
	Source        string `gorm:"type:varchar(32);default:auto;index" json:"source"`
	LastSyncedAt  *time.Time `gorm:"index" json:"lastSyncedAt"`
	Description   string     `gorm:"type:varchar(256)" json:"description"`

	InputPricePerMillion      float64        `gorm:"column:input_price_per_million;type:decimal(18,6);default:0" json:"inputPricePerMillion"`
	OutputPricePerMillion     float64        `gorm:"column:output_price_per_million;type:decimal(18,6);default:0" json:"outputPricePerMillion"`
	CacheReadPricePerMillion  float64        `gorm:"column:cache_read_price_per_million;type:decimal(18,6);default:0" json:"cacheReadPricePerMillion"`
	CacheWritePricePerMillion float64        `gorm:"column:cache_write_price_per_million;type:decimal(18,6);default:0" json:"cacheWritePricePerMillion"`
	ExtraParams               datatypes.JSON `gorm:"column:extra_params;type:json" json:"extraParams"`
}

func (AIModelItem) TableName() string { return "ai_models" }
