package biz

import (
	"context"
	"errors"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// PII/guardrail policy management (docs/design/06-security-and-guardrails.md
// "console UI: guardrail-chain builder") — mirrors mcp_admin.go's/
// extension_admin.go's shape: global objects, mutation is platform-admin
// only. Create/Update enforce at most one isDefault=true policy in a
// transaction, since resolvePIIPolicy's fallback query (biz/pii.go) assumes
// exactly one default and has no guard against two existing at once.

func (uc *GatewayUseCase) CreatePIIPolicy(ctx context.Context, req *dto.CreatePIIPolicyReq) (*model.AIPIIPolicy, error) {
	name := strings.TrimSpace(req.Name)
	action := strings.TrimSpace(req.Action)
	if name == "" || action == "" {
		return nil, ErrPIIPolicyInvalid
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	failMode := strings.TrimSpace(req.FailMode)
	if failMode == "" {
		failMode = "open"
	}
	isDefault := req.IsDefault
	p := &model.AIPIIPolicy{
		Name:         name,
		Enabled:      enabled,
		Action:       action,
		IsDefault:    &isDefault,
		RuleConfig:   datatypes.JSON(req.RuleConfig),
		Description:  req.Description,
		CheckerChain: datatypes.JSON(req.CheckerChain),
		FailMode:     failMode,
	}
	err := uc.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if isDefault {
			if err := tx.Model(&model.AIPIIPolicy{}).Where("is_default = ?", true).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(p).Error
	})
	if err != nil {
		return nil, ErrPIIPolicyInvalid
	}
	return p, nil
}

// ListPIIPolicies returns every policy with BoundKeyCount populated from a
// COUNT(*)...GROUP BY on ai_virtual_keys, so the console can show how many
// keys would be affected by editing or deleting a policy.
func (uc *GatewayUseCase) ListPIIPolicies(ctx context.Context) ([]model.AIPIIPolicy, error) {
	var list []model.AIPIIPolicy
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}
	type countRow struct {
		PIIPolicyID uint `gorm:"column:pii_policy_id"`
		Count       int64
	}
	var counts []countRow
	if err := uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Select("pii_policy_id, count(*) as count").
		Where("pii_policy_id IS NOT NULL").
		Group("pii_policy_id").Find(&counts).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]int64, len(counts))
	for _, c := range counts {
		byID[c.PIIPolicyID] = c.Count
	}
	for i := range list {
		list[i].BoundKeyCount = byID[list[i].ID]
	}
	return list, nil
}

func (uc *GatewayUseCase) UpdatePIIPolicy(ctx context.Context, req *dto.UpdatePIIPolicyReq) (*model.AIPIIPolicy, error) {
	var p model.AIPIIPolicy
	if err := uc.db.WithContext(ctx).First(&p, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPIIPolicyNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, ErrPIIPolicyInvalid
		}
		updates["name"] = name
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Action != nil {
		action := strings.TrimSpace(*req.Action)
		if action == "" {
			return nil, ErrPIIPolicyInvalid
		}
		updates["action"] = action
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.FailMode != nil {
		updates["fail_mode"] = *req.FailMode
	}
	if len(req.RuleConfig) > 0 {
		updates["rule_config"] = datatypes.JSON(req.RuleConfig)
	}
	if len(req.CheckerChain) > 0 {
		updates["checker_chain"] = datatypes.JSON(req.CheckerChain)
	}

	err := uc.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if req.IsDefault != nil {
			if *req.IsDefault {
				if err := tx.Model(&model.AIPIIPolicy{}).Where("is_default = ? AND id <> ?", true, p.ID).
					Update("is_default", false).Error; err != nil {
					return err
				}
			}
			updates["is_default"] = *req.IsDefault
		}
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&p).Updates(updates).Error
	})
	if err != nil {
		return nil, ErrPIIPolicyInvalid
	}
	if err := uc.db.WithContext(ctx).First(&p, req.ID).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (uc *GatewayUseCase) DeletePIIPolicy(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIPIIPolicy{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPIIPolicyNotFound
	}
	return nil
}
