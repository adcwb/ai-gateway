package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	kerrors "github.com/go-kratos/kratos/v2/errors"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Model mapping management (docs/design/01-routing-and-lb.md "console UI:
// fallback-chain drag editor") — mirrors mcp_admin.go's/extension_admin.go's
// shape: mutation is platform-admin only (matching every other global-object
// admin resource's current posture; a key-owner-scoped RBAC check, since a
// mapping is really scoped to one tenant's key, is a further increment, not
// attempted here — consistent with this package's existing documented RBAC
// gap on broad tenant-scoped filtering).

// validateMappingModality loads realModelID's catalog row (rejecting an
// unresolvable id, previously unchecked) and, only when that model's
// ModelType isn't the "llm" default, verifies every fallback-chain entry
// resolves to a cataloged, enabled AIModelItem of the identical ModelType.
// llm mappings are exempt: AIModelItem cataloging was never mandatory for
// chat routing (mappingFallbackCandidates/the router's provider pool read
// AIProvider.Models, not AIModelItem) — this must not retroactively
// restrict that existing, working behavior.
func (uc *GatewayUseCase) validateMappingModality(ctx context.Context, realModelID uint, fallbackChain datatypes.JSON) error {
	var realItem model.AIModelItem
	if err := uc.db.WithContext(ctx).First(&realItem, realModelID).Error; err != nil {
		return ErrModelItemNotFound
	}
	if realItem.ModelType == "" || realItem.ModelType == model.ModelTypeLLM {
		return nil
	}
	if len(fallbackChain) == 0 {
		return nil
	}
	var chain []struct {
		ProviderID uint   `json:"providerId"`
		Model      string `json:"model"`
	}
	if err := json.Unmarshal(fallbackChain, &chain); err != nil {
		return nil // malformed chain JSON is a pre-existing, separately-handled concern
	}
	for _, c := range chain {
		if c.ProviderID == 0 || c.Model == "" {
			continue
		}
		var item model.AIModelItem
		if err := uc.db.WithContext(ctx).
			Where("provider_id = ? AND name = ? AND is_enabled = ?", c.ProviderID, c.Model, true).
			First(&item).Error; err != nil {
			return kerrors.BadRequest("MODEL_MAPPING_MODALITY_MISMATCH",
				fmt.Sprintf("fallback entry {providerId:%d, model:%q} is not a cataloged, enabled model (expected type %q)", c.ProviderID, c.Model, realItem.ModelType))
		}
		if item.ModelType != realItem.ModelType {
			return kerrors.BadRequest("MODEL_MAPPING_MODALITY_MISMATCH",
				fmt.Sprintf("fallback entry {providerId:%d, model:%q} is type %q, expected %q", c.ProviderID, c.Model, item.ModelType, realItem.ModelType))
		}
	}
	return nil
}

func (uc *GatewayUseCase) CreateModelMapping(ctx context.Context, req *dto.CreateModelMappingReq) (*model.AIModelMapping, error) {
	virtualModel := strings.TrimSpace(req.VirtualModel)
	if req.VirtualKeyID == 0 || virtualModel == "" || req.RealModelID == 0 {
		return nil, ErrModelMappingInvalid
	}
	if err := uc.validateMappingModality(ctx, req.RealModelID, datatypes.JSON(req.FallbackChain)); err != nil {
		return nil, err
	}
	m := &model.AIModelMapping{
		VirtualKeyID:  req.VirtualKeyID,
		VirtualModel:  virtualModel,
		RealModelID:   req.RealModelID,
		Description:   req.Description,
		FallbackChain: datatypes.JSON(req.FallbackChain),
		IsEnabled:     true,
	}
	if err := uc.db.WithContext(ctx).Create(m).Error; err != nil {
		return nil, ErrModelMappingExists
	}
	return m, nil
}

// ListModelMappings returns every mapping for one virtual key, with its
// RealModel preloaded so the console can show the real model's name without
// a second round trip.
func (uc *GatewayUseCase) ListModelMappings(ctx context.Context, virtualKeyID uint) ([]model.AIModelMapping, error) {
	var list []model.AIModelMapping
	if err := uc.db.WithContext(ctx).Preload("RealModel").
		Where("virtual_key_id = ?", virtualKeyID).Order("id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (uc *GatewayUseCase) UpdateModelMapping(ctx context.Context, req *dto.UpdateModelMappingReq) (*model.AIModelMapping, error) {
	var m model.AIModelMapping
	if err := uc.db.WithContext(ctx).First(&m, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrModelMappingNotFound
		}
		return nil, err
	}
	if req.RealModelID != nil || len(req.FallbackChain) > 0 {
		effectiveRealModelID := m.RealModelID
		if req.RealModelID != nil {
			effectiveRealModelID = *req.RealModelID
		}
		effectiveFallbackChain := m.FallbackChain
		if len(req.FallbackChain) > 0 {
			effectiveFallbackChain = datatypes.JSON(req.FallbackChain)
		}
		if err := uc.validateMappingModality(ctx, effectiveRealModelID, effectiveFallbackChain); err != nil {
			return nil, err
		}
	}

	updates := map[string]interface{}{}
	if req.VirtualModel != nil {
		updates["virtual_model"] = strings.TrimSpace(*req.VirtualModel)
	}
	if req.RealModelID != nil {
		updates["real_model_id"] = *req.RealModelID
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(req.FallbackChain) > 0 {
		updates["fallback_chain"] = datatypes.JSON(req.FallbackChain)
	}
	if len(updates) == 0 {
		return &m, nil
	}
	if err := uc.db.WithContext(ctx).Model(&m).Updates(updates).Error; err != nil {
		return nil, ErrModelMappingExists
	}
	return &m, nil
}

func (uc *GatewayUseCase) DeleteModelMapping(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIModelMapping{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrModelMappingNotFound
	}
	return nil
}
