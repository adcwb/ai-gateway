package biz

import (
	"context"
	"errors"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Model mapping management (docs/design/01-routing-and-lb.md "console UI:
// fallback-chain drag editor") — mirrors mcp_admin.go's/extension_admin.go's
// shape: mutation is platform-admin only (matching every other global-object
// admin resource's current posture; a key-owner-scoped RBAC check, since a
// mapping is really scoped to one tenant's key, is a further increment, not
// attempted here — consistent with this package's existing documented RBAC
// gap on broad tenant-scoped filtering).

func (uc *GatewayUseCase) CreateModelMapping(ctx context.Context, req *dto.CreateModelMappingReq) (*model.AIModelMapping, error) {
	virtualModel := strings.TrimSpace(req.VirtualModel)
	if req.VirtualKeyID == 0 || virtualModel == "" || req.RealModelID == 0 {
		return nil, ErrModelMappingInvalid
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
