package model

import (
	"time"

	"gorm.io/datatypes"
)

// AIProxyFile shadows an uploaded OpenAI Files API object (docs/design/09-extensibility.md,
// D02/D09 Batch+Files passthrough). The gateway does not store file bytes —
// only enough bookkeeping to route a later GET/DELETE by id back to the
// provider that actually holds the file, without the client repeating a
// provider selector on every follow-up call.
type AIProxyFile struct {
	ID           string         `gorm:"column:id;primaryKey;type:varchar(64)" json:"id"` // upstream file id
	VirtualKeyID uint           `gorm:"not null;index" json:"virtualKeyId"`
	ProviderID   uint           `gorm:"not null;index" json:"providerId"`
	Purpose      string         `gorm:"type:varchar(32)" json:"purpose"`
	Filename     string         `gorm:"type:varchar(256)" json:"filename"`
	Bytes        int64          `json:"bytes"`
	RawUpstream  datatypes.JSON `gorm:"column:raw_upstream_json;type:json" json:"-"`
	CreatedAt    time.Time      `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
}

func (AIProxyFile) TableName() string { return "ai_proxy_files" }

// AIBatchJob shadows an OpenAI Batch API job. Status/counts mirror the
// upstream job so the console/API can answer status queries without calling
// upstream on every poll; BatchSettlementPoller (batch_settlement.go) is the
// only writer that transitions Status after creation.
type AIBatchJob struct {
	ID               string         `gorm:"column:id;primaryKey;type:varchar(64)" json:"id"` // upstream batch id
	VirtualKeyID     uint           `gorm:"not null;index" json:"virtualKeyId"`
	ProviderID       uint           `gorm:"not null;index" json:"providerId"`
	InputFileID      string         `gorm:"column:input_file_id;type:varchar(64)" json:"inputFileId"`
	OutputFileID     string         `gorm:"column:output_file_id;type:varchar(64)" json:"outputFileId"`
	ErrorFileID      string         `gorm:"column:error_file_id;type:varchar(64)" json:"errorFileId"`
	Endpoint         string         `gorm:"type:varchar(64)" json:"endpoint"`
	CompletionWindow string         `gorm:"column:completion_window;type:varchar(16)" json:"completionWindow"`
	Status           string         `gorm:"type:varchar(24);index" json:"status"`
	RequestCounts    datatypes.JSON `gorm:"column:request_counts;type:json" json:"requestCounts,omitempty"`
	RawUpstream      datatypes.JSON `gorm:"column:raw_upstream_json;type:json" json:"-"`
	CreatedAt        time.Time      `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`
	CompletedAt      *time.Time     `gorm:"column:completed_at" json:"completedAt"`
	// SettledAt marks when BatchSettlementPoller finished pricing+billing this
	// batch's aggregate usage exactly once; nil means "not settled yet" and is
	// the poller's own work queue predicate — never set for a non-terminal status.
	SettledAt *time.Time `gorm:"column:settled_at" json:"settledAt"`
}

func (AIBatchJob) TableName() string { return "ai_batch_jobs" }

// BatchTerminalStatuses are the OpenAI Batch API statuses after which the
// settlement poller stops polling that job for status updates.
var BatchTerminalStatuses = map[string]bool{
	"completed": true, "failed": true, "expired": true, "cancelled": true,
}
