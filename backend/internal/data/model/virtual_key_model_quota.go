package model

import (
	"time"

	"gorm.io/gorm"
)

type AIVirtualKeyModelQuota struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	VirtualKeyID uint   `gorm:"column:virtual_key_id;not null;index;uniqueIndex:uk_vk_model" json:"virtualKeyId"`
	ModelName    string `gorm:"column:model_name;type:varchar(128);not null;uniqueIndex:uk_vk_model" json:"modelName"`

	DailyTokenQuota  int64   `gorm:"column:daily_token_quota;default:0" json:"dailyTokenQuota"`
	HourlyTokenQuota int64   `gorm:"column:hourly_token_quota;default:0" json:"hourlyTokenQuota"`
	HourlyReqQuota   int64   `gorm:"column:hourly_req_quota;default:0" json:"hourlyReqQuota"`
	DailyPointQuota  float64 `gorm:"column:daily_point_quota;type:decimal(18,4);default:0" json:"dailyPointQuota"`
	HourlyPointQuota float64 `gorm:"column:hourly_point_quota;type:decimal(18,4);default:0" json:"hourlyPointQuota"`
}

func (AIVirtualKeyModelQuota) TableName() string { return "ai_virtual_key_model_quotas" }
