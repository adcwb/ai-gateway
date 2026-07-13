package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/adcwb/ai-gateway/internal/biz/dto"
	"github.com/adcwb/ai-gateway/internal/biz/extension"
	"github.com/adcwb/ai-gateway/internal/conf"
	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/pkg"
)

// Extension registry management (docs/design/09-extensibility.md "Delivery
// mechanisms") — mirrors mcp_admin.go's shape: global objects, platform-admin
// managed, secret encrypted at rest. Create/Update/Delete all end by calling
// reloadHookDispatcher so a running gateway picks up the change without a
// restart.

func (uc *GatewayUseCase) CreateExtension(ctx context.Context, req *dto.CreateExtensionReq) (*model.AIExtension, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || (req.Kind != model.ExtensionKindWebhook && req.Kind != model.ExtensionKindWasm) || len(req.Hooks) == 0 {
		return nil, ErrExtensionInvalid
	}
	var encSecret string
	if req.HMACSecret != "" {
		s, err := pkg.EncryptAES(req.HMACSecret, []byte(uc.sysCfg.EncryptionKey))
		if err != nil {
			return nil, ErrEncryptionFailed
		}
		encSecret = s
	}
	failMode := req.FailMode
	if failMode == "" {
		failMode = model.ExtensionFailModeOpen
	}
	e := &model.AIExtension{
		Name: name, Kind: req.Kind, Hooks: datatypes.JSON(req.Hooks),
		URL: req.URL, HMACSecret: encSecret, WasmPath: req.WasmPath,
		FailMode: failMode, TenantID: req.TenantID, TimeoutMs: req.TimeoutMs,
		IsEnabled: true,
	}
	if err := uc.db.WithContext(ctx).Create(e).Error; err != nil {
		return nil, ErrExtensionNameExists
	}
	uc.reloadHookDispatcher(ctx)
	return e, nil
}

func (uc *GatewayUseCase) ListExtensions(ctx context.Context) ([]model.AIExtension, error) {
	var list []model.AIExtension
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (uc *GatewayUseCase) UpdateExtension(ctx context.Context, req *dto.UpdateExtensionReq) (*model.AIExtension, error) {
	var e model.AIExtension
	if err := uc.db.WithContext(ctx).First(&e, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrExtensionNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if len(req.Hooks) > 0 {
		updates["hooks"] = datatypes.JSON(req.Hooks)
	}
	if req.URL != nil {
		updates["url"] = *req.URL
	}
	if req.HMACSecret != "" {
		encSecret, err := pkg.EncryptAES(req.HMACSecret, []byte(uc.sysCfg.EncryptionKey))
		if err != nil {
			return nil, ErrEncryptionFailed
		}
		updates["hmac_secret"] = encSecret
	}
	if req.WasmPath != nil {
		updates["wasm_path"] = *req.WasmPath
	}
	if req.FailMode != nil {
		updates["fail_mode"] = *req.FailMode
	}
	if req.TenantID != nil {
		updates["tenant_id"] = *req.TenantID
	}
	if req.TimeoutMs != nil {
		updates["timeout_ms"] = *req.TimeoutMs
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if len(updates) > 0 {
		if err := uc.db.WithContext(ctx).Model(&e).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	uc.reloadHookDispatcher(ctx)
	return &e, nil
}

func (uc *GatewayUseCase) DeleteExtension(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIExtension{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrExtensionNotFound
	}
	uc.reloadHookDispatcher(ctx)
	return nil
}

// -----------------------------------------------------------------------------
// Dispatcher construction/reload
// -----------------------------------------------------------------------------

// NewExtensionDispatcher builds a Dispatcher, loading every enabled
// AIExtension row (webhook/WASM) plus every compile-time-registered hook
// (extension.CompiledHooks, see cmd/server/extensions.go). Called once at
// startup; extension_admin.go's CRUD handlers call reloadHookDispatcher
// afterward to hot-reload without a restart.
func NewExtensionDispatcher(db *gorm.DB, sysCfg *conf.System, logger log.Logger) *extension.Dispatcher {
	helper := log.NewHelper(logger)
	d := extension.NewDispatcher(func(hookName string, point extension.HookPoint, err error) {
		helper.Warnf("extension: hook 执行失败 hook=%s point=%s err=%v", hookName, point, err)
	})
	loadHooksInto(context.Background(), d, db, sysCfg, helper)
	return d
}

// reloadHookDispatcher re-reads ai_extensions and swaps the Dispatcher's
// hook set. uc.hooks is nil when no Dispatcher was wired (SetHookDispatcher
// never called, e.g. in most existing tests) — a no-op in that case.
func (uc *GatewayUseCase) reloadHookDispatcher(ctx context.Context) {
	if uc.hooks == nil {
		return
	}
	loadHooksInto(ctx, uc.hooks, uc.db, uc.sysCfg, uc.logger)
}

func loadHooksInto(ctx context.Context, d *extension.Dispatcher, db *gorm.DB, sysCfg *conf.System, logger *log.Helper) {
	configs := extension.CompiledHooks()

	var rows []model.AIExtension
	if err := db.WithContext(ctx).Where("is_enabled = ?", true).Find(&rows).Error; err != nil {
		logger.Errorf("extension: 加载 ai_extensions 失败 err=%v", err)
		d.SetHooks(configs)
		return
	}

	for _, row := range rows {
		var points []extension.HookPoint
		var pointNames []string
		if err := json.Unmarshal(row.Hooks, &pointNames); err != nil {
			logger.Errorf("extension: 解析 hooks 字段失败 name=%s err=%v", row.Name, err)
			continue
		}
		for _, p := range pointNames {
			points = append(points, extension.HookPoint(p))
		}

		var hook extension.Hook
		switch row.Kind {
		case model.ExtensionKindWebhook:
			secret := ""
			if row.HMACSecret != "" {
				s, err := pkg.DecryptAES(row.HMACSecret, []byte(sysCfg.EncryptionKey))
				if err != nil {
					logger.Errorf("extension: 解密 HMAC secret 失败 name=%s err=%v", row.Name, err)
					continue
				}
				secret = s
			}
			hook = &extension.WebhookHook{HookName: row.Name, URL: row.URL, HMACSecret: secret, HTTPClient: newProxyClient()}
		case model.ExtensionKindWasm:
			wh, err := extension.NewWasmHook(ctx, row.Name, row.WasmPath)
			if err != nil {
				logger.Errorf("extension: 加载 wasm 模块失败 name=%s err=%v", row.Name, err)
				continue
			}
			hook = wh
		default:
			continue
		}

		deadline := time.Duration(row.TimeoutMs) * time.Millisecond
		configs = append(configs, extension.HookConfig{
			Hook: hook, Points: points, Deadline: deadline,
			FailOpen: row.FailMode != model.ExtensionFailModeClosed,
			TenantID: row.TenantID,
		})
	}

	d.SetHooks(configs)
}
