package model

import (
	"time"

	"gorm.io/gorm"
)

type AIModelMapping struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	VirtualKeyID uint   `gorm:"column:virtual_key_id;not null;index;uniqueIndex:uk_vk_virtual_model" json:"virtualKeyId"`
	VirtualModel string `gorm:"column:virtual_model;type:varchar(128);not null;uniqueIndex:uk_vk_virtual_model" json:"virtualModel"`
	RealModelID  uint   `gorm:"column:real_model_id;not null;index" json:"realModelId"`
	IsEnabled    bool   `gorm:"column:is_enabled;default:true" json:"isEnabled"`
	Description  string `gorm:"column:description;type:varchar(256)" json:"description"`

	RealModel *AIModelItem `gorm:"foreignKey:RealModelID" json:"realModel,omitempty"`
}

func (AIModelMapping) TableName() string { return "ai_model_mappings" }
