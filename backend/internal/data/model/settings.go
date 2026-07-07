package model

import "time"

// AISetting is a minimal generic key-value store for console-editable runtime
// settings that would otherwise only be changeable via config.yaml/env
// (docs/design/08-web-console.md module 8) — currently just the alert
// webhook override. Not on any hot path: read only when an alert fires or the
// settings page loads.
type AISetting struct {
	Key       string    `gorm:"column:setting_key;type:varchar(64);primaryKey" json:"key"`
	Value     string    `gorm:"column:value;type:varchar(512)" json:"value"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (AISetting) TableName() string { return "ai_settings" }

const SettingKeyAlertWebhook = "alert_webhook"

// Semantic cache embedding settings (docs/design/07-caching-strategies.md
// "Vector backend (ADR)"): embeddings are generated through the gateway
// itself against an operator-designated provider/model, configured here
// rather than in config.yaml so it can change without a redeploy.
const (
	SettingKeyCacheEmbeddingProviderID = "cache_embedding_provider_id"
	SettingKeyCacheEmbeddingModel      = "cache_embedding_model"
	SettingKeyCacheEmbeddingDim        = "cache_embedding_dim"
)
