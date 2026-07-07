package biz

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Model catalog management (docs/design/08-web-console.md module 4): per
// provider, upstream cost pricing consumed by credits.go/getModelPriceEntry
// and referenced by model_mapping.go's RealModelID. Previously DB-managed
// only — this forces the missing endpoints into the public API.

// CreateModelItem registers a model's cost pricing under a provider.
func (uc *GatewayUseCase) CreateModelItem(ctx context.Context, req *dto.CreateModelItemReq) (*model.AIModelItem, error) {
	if req.ProviderID == 0 || strings.TrimSpace(req.Name) == "" {
		return nil, ErrModelItemInvalid
	}
	modelType := req.ModelType
	if modelType == "" {
		modelType = "llm"
	}
	m := &model.AIModelItem{
		ProviderID:                req.ProviderID,
		Name:                      strings.TrimSpace(req.Name),
		ModelType:                 modelType,
		ContextWindow:             req.ContextWindow,
		IsDefault:                 req.IsDefault,
		IsEnabled:                 true,
		Source:                    "manual",
		Description:               req.Description,
		InputPricePerMillion:      req.InputPricePerMillion,
		OutputPricePerMillion:     req.OutputPricePerMillion,
		CacheReadPricePerMillion:  req.CacheReadPricePerMillion,
		CacheWritePricePerMillion: req.CacheWritePricePerMillion,
	}
	if err := uc.db.WithContext(ctx).Create(m).Error; err != nil {
		return nil, ErrModelItemExists
	}
	return m, nil
}

// ListModelItems returns model items, optionally filtered by provider.
func (uc *GatewayUseCase) ListModelItems(ctx context.Context, providerID uint) ([]model.AIModelItem, error) {
	q := uc.db.WithContext(ctx).Order("provider_id asc, name asc")
	if providerID != 0 {
		q = q.Where("provider_id = ?", providerID)
	}
	var list []model.AIModelItem
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateModelItem applies a partial update to a model item's catalog/pricing fields.
func (uc *GatewayUseCase) UpdateModelItem(ctx context.Context, req *dto.UpdateModelItemReq) (*model.AIModelItem, error) {
	var m model.AIModelItem
	if err := uc.db.WithContext(ctx).First(&m, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrModelItemNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.ModelType != nil {
		updates["model_type"] = *req.ModelType
	}
	if req.ContextWindow != nil {
		updates["context_window"] = *req.ContextWindow
	}
	if req.IsDefault != nil {
		updates["is_default"] = *req.IsDefault
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.InputPricePerMillion != nil {
		updates["input_price_per_million"] = *req.InputPricePerMillion
	}
	if req.OutputPricePerMillion != nil {
		updates["output_price_per_million"] = *req.OutputPricePerMillion
	}
	if req.CacheReadPricePerMillion != nil {
		updates["cache_read_price_per_million"] = *req.CacheReadPricePerMillion
	}
	if req.CacheWritePricePerMillion != nil {
		updates["cache_write_price_per_million"] = *req.CacheWritePricePerMillion
	}
	if len(updates) == 0 {
		return &m, nil
	}
	if err := uc.db.WithContext(ctx).Model(&m).Updates(updates).Error; err != nil {
		return nil, err
	}
	invalidateModelPriceCache(m.ProviderID, m.Name)
	return &m, nil
}

// DeleteModelItem soft-deletes a model item.
func (uc *GatewayUseCase) DeleteModelItem(ctx context.Context, id uint) error {
	var m model.AIModelItem
	if err := uc.db.WithContext(ctx).First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrModelItemNotFound
		}
		return err
	}
	if err := uc.db.WithContext(ctx).Delete(&model.AIModelItem{}, id).Error; err != nil {
		return err
	}
	invalidateModelPriceCache(m.ProviderID, m.Name)
	return nil
}
