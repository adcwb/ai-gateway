package model

import (
	"time"

	"gorm.io/gorm"
)

type AICreditsRate struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Currency      string  `gorm:"type:varchar(8);not null;uniqueIndex" json:"currency"`
	RatePerCredit float64 `gorm:"type:decimal(18,6);not null" json:"ratePerCredit"`
	IsEnabled     bool    `gorm:"default:true" json:"isEnabled"`
	Description   string  `gorm:"type:varchar(256)" json:"description"`
}

func (AICreditsRate) TableName() string { return "ai_credits_rates" }
