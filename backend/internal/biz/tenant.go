package biz

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Tenancy (docs/design/04-multi-tenancy-and-auth.md): tenant → project → key.
// Single-tenant mode is the default — a "default" tenant and project are
// created on startup and keys with TenantID 0 resolve to them.

// EnsureTenancyDefaults creates the default tenant/project once and backfills
// legacy keys (tenant_id = 0). Idempotent; called on startup.
func (uc *GatewayUseCase) EnsureTenancyDefaults(ctx context.Context) {
	var tenant model.AITenant
	err := uc.db.WithContext(ctx).Where("name = ?", model.DefaultTenantName).First(&tenant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		tenant = model.AITenant{Name: model.DefaultTenantName, DisplayName: "Default Tenant", Status: "active"}
		if cerr := uc.db.WithContext(ctx).Create(&tenant).Error; cerr != nil {
			uc.logger.Errorf("tenancy: 创建默认租户失败 err=%v", cerr)
			return
		}
		uc.logger.Infof("tenancy: 已创建默认租户 id=%d", tenant.ID)
	} else if err != nil {
		uc.logger.Errorf("tenancy: 查询默认租户失败 err=%v", err)
		return
	}

	var project model.AIProject
	perr := uc.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenant.ID, model.DefaultTenantName).First(&project).Error
	if errors.Is(perr, gorm.ErrRecordNotFound) {
		project = model.AIProject{TenantID: tenant.ID, Name: model.DefaultTenantName, Description: "Default project"}
		if cerr := uc.db.WithContext(ctx).Create(&project).Error; cerr != nil {
			uc.logger.Errorf("tenancy: 创建默认项目失败 err=%v", cerr)
		}
	}

	// Backfill: legacy keys (tenant_id = 0) attach to the default tenant so
	// billing/attribution always has a real tenant to hang off.
	res := uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Where("tenant_id = ?", 0).
		Updates(map[string]interface{}{"tenant_id": tenant.ID, "project_ref_id": project.ID})
	if res.Error == nil && res.RowsAffected > 0 {
		uc.logger.Infof("tenancy: 已将 %d 个存量 Key 挂载到默认租户", res.RowsAffected)
	}
}

// CreateTenant registers a tenant and its (disabled) billing account shell.
func (uc *GatewayUseCase) CreateTenant(ctx context.Context, req *dto.CreateTenantReq) (*model.AITenant, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrTenantInvalid
	}
	t := &model.AITenant{Name: name, DisplayName: req.DisplayName, Status: "active"}
	if err := uc.db.WithContext(ctx).Create(t).Error; err != nil {
		return nil, ErrTenantNameExists
	}
	// Billing account shell: disabled until the operator opts in.
	acct := &model.AIBillingAccount{TenantID: t.ID, IsEnabled: false, Mode: model.BillingModePrepaid, Currency: "CNY", Status: model.BillingStatusActive}
	if err := uc.db.WithContext(ctx).Create(acct).Error; err != nil {
		uc.logger.Warnf("tenancy: 创建租户账户壳失败 tenantID=%d err=%v", t.ID, err)
	}
	return t, nil
}

// ListTenants returns all tenants with their billing account summary attached.
func (uc *GatewayUseCase) ListTenants(ctx context.Context) ([]dto.TenantItem, error) {
	var tenants []model.AITenant
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&tenants).Error; err != nil {
		return nil, err
	}
	var accounts []model.AIBillingAccount
	uc.db.WithContext(ctx).Find(&accounts)
	acctByTenant := make(map[uint]*model.AIBillingAccount, len(accounts))
	for i := range accounts {
		acctByTenant[accounts[i].TenantID] = &accounts[i]
	}
	var counts []struct {
		TenantID uint
		N        int64
	}
	uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Select("tenant_id as tenant_id, count(*) as n").Group("tenant_id").Scan(&counts)
	keysByTenant := map[uint]int64{}
	for _, c := range counts {
		keysByTenant[c.TenantID] = c.N
	}

	items := make([]dto.TenantItem, 0, len(tenants))
	for _, t := range tenants {
		item := dto.TenantItem{AITenant: t, KeyCount: keysByTenant[t.ID]}
		if a := acctByTenant[t.ID]; a != nil {
			item.Account = a
		}
		items = append(items, item)
	}
	return items, nil
}

// CreateProject adds a project under a tenant.
func (uc *GatewayUseCase) CreateProject(ctx context.Context, req *dto.CreateProjectReq) (*model.AIProject, error) {
	if req.TenantID == 0 || strings.TrimSpace(req.Name) == "" {
		return nil, ErrTenantInvalid
	}
	var cnt int64
	uc.db.WithContext(ctx).Model(&model.AITenant{}).Where("id = ?", req.TenantID).Count(&cnt)
	if cnt == 0 {
		return nil, ErrTenantNotFound
	}
	p := &model.AIProject{TenantID: req.TenantID, Name: strings.TrimSpace(req.Name), Description: req.Description}
	if err := uc.db.WithContext(ctx).Create(p).Error; err != nil {
		return nil, ErrTenantNameExists
	}
	return p, nil
}

// ListProjects lists projects, optionally filtered by tenant.
func (uc *GatewayUseCase) ListProjects(ctx context.Context, tenantID uint) ([]model.AIProject, error) {
	q := uc.db.WithContext(ctx).Order("id asc")
	if tenantID > 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var list []model.AIProject
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// tenantIDForKey resolves a key's tenant (0 → default tenant, cached).
func (uc *GatewayUseCase) tenantIDForKey(ctx context.Context, key *model.AIVirtualKey) uint {
	if key.TenantID > 0 {
		return key.TenantID
	}
	// legacy key not yet backfilled: fall back to the default tenant
	var t model.AITenant
	if err := uc.db.WithContext(ctx).Where("name = ?", model.DefaultTenantName).First(&t).Error; err == nil {
		return t.ID
	}
	return 0
}
