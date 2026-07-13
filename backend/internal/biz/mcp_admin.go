package biz

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/pkg"
)

// MCP server registry management (docs/design/09-extensibility.md "MCP
// gateway" point 1) — mirrors provider.go's CRUD shape: global objects,
// platform-admin managed, credential encrypted at rest.

func (uc *GatewayUseCase) CreateMCPServer(ctx context.Context, req *dto.CreateMCPServerReq) (*model.AIMCPServer, error) {
	name := strings.TrimSpace(req.Name)
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if name == "" || baseURL == "" {
		return nil, ErrMCPServerInvalid
	}
	var encKey string
	if req.APIKey != "" {
		k, err := pkg.EncryptAES(req.APIKey, []byte(uc.sysCfg.EncryptionKey))
		if err != nil {
			return nil, ErrEncryptionFailed
		}
		encKey = k
	}
	s := &model.AIMCPServer{
		Name:        name,
		BaseURL:     baseURL,
		APIKey:      encKey,
		IsEnabled:   true,
		Description: req.Description,
	}
	if err := uc.db.WithContext(ctx).Create(s).Error; err != nil {
		return nil, ErrMCPServerNameExists
	}
	return s, nil
}

func (uc *GatewayUseCase) ListMCPServers(ctx context.Context) ([]model.AIMCPServer, error) {
	var list []model.AIMCPServer
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (uc *GatewayUseCase) UpdateMCPServer(ctx context.Context, req *dto.UpdateMCPServerReq) (*model.AIMCPServer, error) {
	var s model.AIMCPServer
	if err := uc.db.WithContext(ctx).First(&s, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMCPServerNotFound
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
	if req.APIKey != "" {
		encKey, err := pkg.EncryptAES(req.APIKey, []byte(uc.sysCfg.EncryptionKey))
		if err != nil {
			return nil, ErrEncryptionFailed
		}
		updates["api_key"] = encKey
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if len(updates) == 0 {
		return &s, nil
	}
	if err := uc.db.WithContext(ctx).Model(&s).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (uc *GatewayUseCase) DeleteMCPServer(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIMCPServer{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMCPServerNotFound
	}
	return nil
}

// loadMCPServerByName resolves an enabled server by name and decrypts its
// upstream credential, analogous to loadProviderDirect.
func (uc *GatewayUseCase) loadMCPServerByName(ctx context.Context, name string) (*model.AIMCPServer, string, error) {
	var s model.AIMCPServer
	if err := uc.db.WithContext(ctx).Where("name = ? AND is_enabled = ?", name, true).First(&s).Error; err != nil {
		return nil, "", ErrMCPServerNotFound
	}
	if s.APIKey == "" {
		return &s, "", nil
	}
	apiKey, err := pkg.DecryptAES(s.APIKey, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return nil, "", ErrDecryptionFailed
	}
	return &s, apiKey, nil
}
