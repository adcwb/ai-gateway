package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// DefaultTenantName is auto-created on startup; pre-tenancy deployments keep
// working with zero new mandatory concepts (docs/design/04-multi-tenancy-and-auth.md).
const DefaultTenantName = "default"

// AITenant is the top of the tenancy hierarchy: it owns the billing account,
// the price table binding and (future) users.
type AITenant struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name        string         `gorm:"type:varchar(64);not null;uniqueIndex" json:"name"`
	DisplayName string         `gorm:"type:varchar(128)" json:"displayName"`
	Status      string         `gorm:"type:varchar(16);default:active" json:"status"` // active / suspended
	Settings    datatypes.JSON `gorm:"type:json" json:"settings"`
}

func (AITenant) TableName() string { return "ai_tenants" }

// AIProject groups virtual keys under a tenant for cost attribution and
// quota-template inheritance.
type AIProject struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	TenantID      uint           `gorm:"not null;index;uniqueIndex:idx_project_tenant_name" json:"tenantId"`
	Name          string         `gorm:"type:varchar(64);not null;uniqueIndex:idx_project_tenant_name" json:"name"`
	QuotaTemplate datatypes.JSON `gorm:"type:json" json:"quotaTemplate"`
	Description   string         `gorm:"type:varchar(256)" json:"description"`
}

func (AIProject) TableName() string { return "ai_projects" }
