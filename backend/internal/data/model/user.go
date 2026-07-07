package model

import (
	"time"

	"gorm.io/gorm"
)

// Tenant membership roles (docs/design/04-multi-tenancy-and-auth.md RBAC
// table), ordered least → most privileged. Deliberately a fixed set, not a
// permission-matrix engine.
const (
	RoleViewer = "viewer"
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// RoleRank orders roles for >= comparisons; unknown roles rank below viewer.
func RoleRank(role string) int {
	switch role {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleMember:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// AIUser is a console principal, JIT-provisioned on first OIDC login (the
// project deliberately skips local password accounts — SSO is introduced
// directly, see backend/CLAUDE.md). IsPlatformAdmin bypasses per-tenant role
// checks entirely (the synthetic super-admin the bootstrap token also maps to).
type AIUser struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Email           string     `gorm:"type:varchar(256);not null;uniqueIndex" json:"email"`
	DisplayName     string     `gorm:"type:varchar(128)" json:"displayName"`
	OIDCSubject     string     `gorm:"column:oidc_subject;type:varchar(256);uniqueIndex" json:"-"`
	IsPlatformAdmin bool       `gorm:"default:false" json:"isPlatformAdmin"`
	IsEnabled       bool       `gorm:"default:true" json:"isEnabled"`
	LastLoginAt     *time.Time `gorm:"index" json:"lastLoginAt"`
}

func (AIUser) TableName() string { return "ai_users" }

// AIUserTenantRole is the membership edge: one user may belong to many
// tenants, each with its own role.
type AIUserTenantRole struct {
	ID        uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`

	UserID   uint   `gorm:"not null;uniqueIndex:idx_user_tenant" json:"userId"`
	TenantID uint   `gorm:"not null;uniqueIndex:idx_user_tenant;index" json:"tenantId"`
	Role     string `gorm:"type:varchar(16);not null" json:"role"`
}

func (AIUserTenantRole) TableName() string { return "ai_user_tenant_roles" }

// AIAdminKey is a machine principal for automation/CI — hashed and encrypted
// exactly like AIVirtualKey (SHA-256 lookup + AES-encrypted plaintext, shown
// once). TenantID 0 = platform-wide (role applies across every tenant).
type AIAdminKey struct {
	ID        uint           `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`

	Name              string     `gorm:"type:varchar(128);not null" json:"name"`
	KeyHash           string     `gorm:"type:varchar(64);not null;uniqueIndex" json:"-"`
	KeyPrefix         string     `gorm:"type:varchar(20);not null" json:"keyPrefix"`
	PlainKeyEncrypted string     `gorm:"type:varchar(512)" json:"-"`
	TenantID          uint       `gorm:"index;default:0" json:"tenantId"` // 0 = platform-wide
	Role              string     `gorm:"type:varchar(16);not null;default:viewer" json:"role"`
	IsEnabled         bool       `gorm:"default:true" json:"isEnabled"`
	LastUsedAt        *time.Time `gorm:"index" json:"lastUsedAt"`
}

func (AIAdminKey) TableName() string { return "ai_admin_keys" }

// AIAdminAuditLog is the operator "activity log" — separate from gateway
// traffic audit (ai_gateway_audit_logs): who changed what in the management
// plane. Written for every state-changing management call.
type AIAdminAuditLog struct {
	ID        uint      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index" json:"createdAt"`

	PrincipalKind string `gorm:"type:varchar(16);not null" json:"principalKind"` // bootstrap / admin_key / session
	PrincipalID   uint   `gorm:"index" json:"principalId"`
	PrincipalName string `gorm:"type:varchar(128)" json:"principalName"`
	TenantID      uint   `gorm:"index" json:"tenantId"`
	Action        string `gorm:"type:varchar(64);not null" json:"action"`
	EntityType    string `gorm:"type:varchar(64)" json:"entityType"`
	EntityID      string `gorm:"type:varchar(64)" json:"entityId"`
	Detail        string `gorm:"type:varchar(1024)" json:"detail"`
}

func (AIAdminAuditLog) TableName() string { return "ai_admin_audit_logs" }
