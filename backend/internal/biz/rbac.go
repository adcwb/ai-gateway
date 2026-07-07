package biz

import (
	"context"
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Principal is the resolved management-plane identity (docs/design/04-multi-
// tenancy-and-auth.md): the bootstrap admin token, an admin API key, or an
// OIDC-provisioned user session. All three share this one shape so handlers
// never need to branch on how the caller authenticated.
type Principal struct {
	Kind            string // "bootstrap" | "admin_key" | "session"
	ID              uint   // AIUser.ID or AIAdminKey.ID; 0 for bootstrap
	Name            string // for admin-audit-log attribution
	IsPlatformAdmin bool
	// TenantRoles is empty for a platform admin (every tenant implicitly
	// allowed) and for admin keys (their single TenantID/Role pair is checked
	// directly rather than populating this map).
	TenantRoles map[uint]string
	// AdminKeyTenantID/AdminKeyRole carry an admin key's fixed scope; zero
	// TenantID = platform-wide at that role level.
	AdminKeyTenantID uint
	AdminKeyRole     string
}

const (
	PrincipalKindBootstrap = "bootstrap"
	PrincipalKindAdminKey  = "admin_key"
	PrincipalKindSession   = "session"
)

// BootstrapPrincipal is the synthetic super-admin the static admin_token maps
// to, so P0 (token-only) and P1 (users/RBAC) share one authorization code path.
func BootstrapPrincipal() *Principal {
	return &Principal{Kind: PrincipalKindBootstrap, Name: "bootstrap-token", IsPlatformAdmin: true}
}

// HasRole reports whether the principal satisfies at least minRole for tenantID.
func (p *Principal) HasRole(tenantID uint, minRole string) bool {
	if p == nil {
		return false
	}
	if p.IsPlatformAdmin {
		return true
	}
	switch p.Kind {
	case PrincipalKindAdminKey:
		if p.AdminKeyTenantID != 0 && p.AdminKeyTenantID != tenantID {
			return false
		}
		return model.RoleRank(p.AdminKeyRole) >= model.RoleRank(minRole)
	default:
		role, ok := p.TenantRoles[tenantID]
		if !ok {
			return false
		}
		return model.RoleRank(role) >= model.RoleRank(minRole)
	}
}

// AllowedTenantIDs returns the tenants this principal may act within, or nil
// for a platform admin (meaning "unscoped, allow all" — callers must check
// IsPlatformAdmin first rather than treating nil as "no tenants").
func (p *Principal) AllowedTenantIDs() []uint {
	if p == nil || p.IsPlatformAdmin {
		return nil
	}
	if p.Kind == PrincipalKindAdminKey {
		if p.AdminKeyTenantID == 0 {
			return nil // platform-wide admin key
		}
		return []uint{p.AdminKeyTenantID}
	}
	ids := make([]uint, 0, len(p.TenantRoles))
	for id := range p.TenantRoles {
		ids = append(ids, id)
	}
	return ids
}

type principalCtxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

func PrincipalFromCtx(ctx context.Context) *Principal {
	if v, ok := ctx.Value(principalCtxKey{}).(*Principal); ok {
		return v
	}
	return nil
}

func PrincipalFromRequest(r *http.Request) *Principal {
	return PrincipalFromCtx(r.Context())
}

// RecordAdminAudit writes one operator-activity row (docs/design/04, "every
// state-changing management call writes an operator audit row"). Best-effort:
// a logging failure must never fail the underlying action.
func (uc *GatewayUseCase) RecordAdminAudit(ctx context.Context, p *Principal, tenantID uint, action, entityType, entityID, detail string) {
	recordAdminAudit(ctx, uc.db, uc.logger, p, tenantID, action, entityType, entityID, detail)
}

func recordAdminAudit(ctx context.Context, db *gorm.DB, logger *log.Helper, p *Principal, tenantID uint, action, entityType, entityID, detail string) {
	if db == nil {
		return
	}
	entry := &model.AIAdminAuditLog{
		PrincipalKind: PrincipalKindBootstrap,
		TenantID:      tenantID,
		Action:        action,
		EntityType:    entityType,
		EntityID:      entityID,
		Detail:        detail,
	}
	if p != nil {
		entry.PrincipalKind = p.Kind
		entry.PrincipalID = p.ID
		entry.PrincipalName = p.Name
	}
	if err := db.WithContext(ctx).Create(entry).Error; err != nil {
		logger.Warnf("rbac: 写入管理操作审计失败 action=%s err=%v", action, err)
	}
}
