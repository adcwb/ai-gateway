package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

// Provider management (docs/design/08-web-console.md forces these endpoints
// into the public API; previously providers were DB-managed only).

// CreateProvider registers an upstream provider, encrypting its API key at rest.
func (uc *GatewayUseCase) CreateProvider(ctx context.Context, req *dto.CreateProviderReq) (*model.AIProvider, error) {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.BaseURL) == "" {
		return nil, ErrProviderInvalid
	}
	if strings.TrimSpace(req.APIKey) == "" {
		return nil, ErrProviderInvalid.WithMetadata(map[string]string{"field": "apiKey"})
	}
	encKey, err := pkg.EncryptAES(req.APIKey, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return nil, ErrEncryptionFailed
	}
	modelsJSON, err := json.Marshal(req.Models)
	if err != nil {
		return nil, ErrProviderInvalid.WithMetadata(map[string]string{"field": "models"})
	}
	providerType := req.ProviderType
	if providerType == "" {
		providerType = "openai_compatible"
	}
	weight := req.Weight
	if weight <= 0 {
		weight = 100
	}
	p := &model.AIProvider{
		Name:         strings.TrimSpace(req.Name),
		BaseURL:      strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		ProviderType: providerType,
		APIKey:       encKey,
		Models:       datatypes.JSON(modelsJSON),
		IsEnabled:    true,
		Weight:       weight,
		Priority:     req.Priority,
		Description:  req.Description,
	}
	if err := uc.db.WithContext(ctx).Create(p).Error; err != nil {
		return nil, ErrProviderNameExists
	}
	return p, nil
}

// ListProviders returns all providers (API keys are never serialized).
func (uc *GatewayUseCase) ListProviders(ctx context.Context) ([]model.AIProvider, error) {
	var list []model.AIProvider
	if err := uc.db.WithContext(ctx).Order("priority asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateProvider applies a partial update; a non-empty APIKey is re-encrypted.
func (uc *GatewayUseCase) UpdateProvider(ctx context.Context, req *dto.UpdateProviderReq) (*model.AIProvider, error) {
	var p model.AIProvider
	if err := uc.db.WithContext(ctx).First(&p, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProviderNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.BaseURL != nil {
		updates["base_url"] = strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
	}
	if req.ProviderType != nil {
		updates["provider_type"] = *req.ProviderType
	}
	if req.APIKey != "" {
		encKey, err := pkg.EncryptAES(req.APIKey, []byte(uc.sysCfg.EncryptionKey))
		if err != nil {
			return nil, ErrEncryptionFailed
		}
		updates["api_key"] = encKey
	}
	if req.Models != nil {
		modelsJSON, err := json.Marshal(*req.Models)
		if err != nil {
			return nil, ErrProviderInvalid.WithMetadata(map[string]string{"field": "models"})
		}
		updates["models"] = datatypes.JSON(modelsJSON)
	}
	if req.Weight != nil {
		updates["weight"] = *req.Weight
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if len(updates) == 0 {
		return &p, nil
	}
	if err := uc.db.WithContext(ctx).Model(&p).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteProvider soft-deletes a provider.
func (uc *GatewayUseCase) DeleteProvider(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIProvider{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// ProviderHealth returns the live breaker state per provider for the console.
func (uc *GatewayUseCase) ProviderHealth(ctx context.Context) ([]dto.ProviderHealthItem, error) {
	providers, err := uc.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]dto.ProviderHealthItem, 0, len(providers))
	for _, p := range providers {
		state := model.BreakerStateClosed
		if uc.router != nil {
			state = uc.router.StateOf(ctx, p.ID)
		}
		items = append(items, dto.ProviderHealthItem{
			ProviderID: p.ID,
			Name:       p.Name,
			State:      state,
			IsEnabled:  p.IsEnabled,
			Weight:     p.Weight,
			Priority:   p.Priority,
		})
	}
	return items, nil
}
