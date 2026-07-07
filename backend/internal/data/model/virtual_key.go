package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type AIVirtualKey struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name              string         `gorm:"type:varchar(128);not null" json:"name"`
	KeyHash           string         `gorm:"type:varchar(64);not null;uniqueIndex" json:"-"`
	KeyPrefix         string         `gorm:"type:varchar(20);not null" json:"keyPrefix"`
	PlainKeyEncrypted string         `gorm:"type:varchar(512)" json:"-"`
	ProviderID        uint           `gorm:"not null;index" json:"providerId"`
	BaseURL           string         `gorm:"type:varchar(512)" json:"baseUrl"`
	AllowedModels     datatypes.JSON `gorm:"type:json" json:"allowedModels"`

	DailyTokenQuota  int64 `gorm:"default:0" json:"dailyTokenQuota"`
	HourlyTokenQuota int64 `gorm:"default:0" json:"hourlyTokenQuota"`
	HourlyReqQuota   int64 `gorm:"default:0" json:"hourlyReqQuota"`
	MaxConcurrency   int   `gorm:"default:0" json:"maxConcurrency"`

	PIIPolicyID        *uint          `gorm:"column:pii_policy_id;index" json:"piiPolicyId"`
	IPWhitelistEnabled bool           `gorm:"column:ip_whitelist_enabled;default:false" json:"ipWhitelistEnabled"`
	IPWhitelist        datatypes.JSON `gorm:"column:ip_whitelist;type:json" json:"ipWhitelist"`

	IsEnabled bool       `gorm:"default:true" json:"isEnabled"`
	ExpiresAt *time.Time `gorm:"index" json:"expiresAt"`
	CreatedBy uint       `gorm:"index" json:"createdBy"`

	CreatedByName   string `gorm:"-" json:"createdByName"`
	CreatedByAvatar string `gorm:"-" json:"createdByAvatar"`

	ProjectID   *string `gorm:"column:project_id;type:varchar(64);index" json:"projectId"`
	ProjectName *string `gorm:"column:project_name;type:varchar(128)" json:"projectName"`
	EnvID       *string `gorm:"column:env_id;type:varchar(64);index" json:"envId"`

	DailyPointQuota  float64 `gorm:"column:daily_point_quota;type:decimal(18,4);default:0" json:"dailyPointQuota"`
	HourlyPointQuota float64 `gorm:"column:hourly_point_quota;type:decimal(18,4);default:0" json:"hourlyPointQuota"`
	Description      string  `gorm:"type:varchar(256)" json:"description"`

	// P1 tenancy: keys attach to a tenant (billing account scope) and
	// optionally a project (cost attribution). Zero = default tenant.
	TenantID     uint `gorm:"column:tenant_id;index;default:0" json:"tenantId"`
	ProjectRefID uint `gorm:"column:project_ref_id;index;default:0" json:"projectRefId"`

	// Routing strategy: "" = weighted (default) / priority / least_latency / least_cost
	RoutingStrategy string `gorm:"column:routing_strategy;type:varchar(24);default:''" json:"routingStrategy"`

	// P2 response cache config: {"exactEnabled":bool,"ttlSec":int,"billingPolicy":"free|discount|full","discountPercent":int}
	CacheConfig datatypes.JSON `gorm:"column:cache_config;type:json" json:"cacheConfig"`

	// P3 MCP tool governance (docs/design/09-extensibility.md): JSON array of
	// tool name strings this key may call. Mirrors AllowedModels semantics —
	// empty/absent = unrestricted (every tool the upstream server exposes).
	ToolWhitelist datatypes.JSON `gorm:"column:tool_whitelist;type:json" json:"toolWhitelist"`

	ModelQuotas []AIVirtualKeyModelQuota `gorm:"-" json:"modelQuotas,omitempty"`
}

func (AIVirtualKey) TableName() string { return "ai_virtual_keys" }
