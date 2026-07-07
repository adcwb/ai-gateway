package model

import "time"

const (
	QuotaDimHourlyToken = "hourly_token"
	QuotaDimDailyToken  = "daily_token"
	QuotaDimHourlyReq   = "hourly_req"
	QuotaDimConcurrency = "concurrency"
	QuotaDimDailyPoint  = "daily_point"
	QuotaDimHourlyPoint = "hourly_point"
	QuotaDimToolCall    = "tool_call"
)

const (
	QuotaEventTriggered = "triggered"
	QuotaEventReleased  = "released"
	QuotaEventReset     = "reset"
)

const (
	QuotaReasonWindowSlide = "window_slide"
	QuotaReasonManualReset = "manual_reset"
	QuotaReasonWaitTimeout = "wait_timeout"
)

type AIGatewayQuotaEvent struct {
	ID        uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`

	VirtualKeyID uint   `gorm:"not null;index" json:"virtualKeyId"`
	KeyPrefix    string `gorm:"type:varchar(20)" json:"keyPrefix"`
	Dimension    string `gorm:"type:varchar(20);index" json:"dimension"`
	EventType    string `gorm:"type:varchar(16);index" json:"eventType"`
	QuotaLimit   int64  `gorm:"default:0" json:"quotaLimit"`
	Used         int64  `gorm:"default:0" json:"used"`
	Reason       string `gorm:"type:varchar(32);default:''" json:"reason"`
	Operator     string `gorm:"type:varchar(64);default:''" json:"operator"`
	Note         string `gorm:"type:varchar(255);default:''" json:"note"`
}

func (AIGatewayQuotaEvent) TableName() string { return "ai_gateway_quota_events" }
