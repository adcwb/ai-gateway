package model

import (
	"time"

	"gorm.io/datatypes"
)

// AIVideoJob shadows an upstream video-generation job (docs/superpowers/
// specs/2026-07-09-video-generation-phase2-design.md), mirroring AIProxyFile's
// shape: the gateway stores no video bytes, only enough bookkeeping to route
// a later GET (status/content) or DELETE by id back to the provider that
// actually holds the job — required because those follow-up calls carry no
// model field, only a path parameter. Unlike AIBatchJob there is no
// SettledAt: this phase carries no billing, so there is nothing to settle
// and therefore no background poller.
type AIVideoJob struct {
	ID           string         `gorm:"column:id;primaryKey;type:varchar(64)" json:"id"` // upstream video job id
	VirtualKeyID uint           `gorm:"not null;index" json:"virtualKeyId"`
	ProviderID   uint           `gorm:"not null;index" json:"providerId"`
	Model        string         `gorm:"type:varchar(128)" json:"model"`
	RawUpstream  datatypes.JSON `gorm:"column:raw_upstream_json;type:json" json:"-"`
	CreatedAt    time.Time      `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
}

func (AIVideoJob) TableName() string { return "ai_video_jobs" }
