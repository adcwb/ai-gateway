package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Console-editable settings (docs/design/08-web-console.md module 8): a
// minimal generic key-value store for the one runtime override that's worth
// changing without a redeploy — the billing alert webhook — plus CRUD for
// the existing multi-currency credits-rate table. Scoped deliberately small:
// routing defaults/retry budget and guardrail-chain config stay compile-time
// constants (would otherwise add a DB/Redis read to the hot path for no
// operational benefit at this project's current maturity).

func (uc *GatewayUseCase) getSetting(ctx context.Context, key string) string {
	var s model.AISetting
	if err := uc.db.WithContext(ctx).Where("setting_key = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

// setSetting upserts by key. Assign takes a map, not a struct — a struct's
// zero-value fields (e.g. Value: "" to clear an override) are silently
// skipped by GORM's Updates-on-found path otherwise (the "GORM zero-value
// trap" noted in backend/CLAUDE.md).
func (uc *GatewayUseCase) setSetting(ctx context.Context, key, value string) error {
	return uc.db.WithContext(ctx).
		Where("setting_key = ?", key).
		Assign(map[string]interface{}{"setting_key": key, "value": value}).
		FirstOrCreate(&model.AISetting{}).Error
}

// GetSettings returns the current console-editable settings.
func (uc *GatewayUseCase) GetSettings(ctx context.Context) dto.SettingsResp {
	webhook := uc.getSetting(ctx, model.SettingKeyAlertWebhook)
	usingOverride := webhook != ""
	if webhook == "" && uc.sysCfg != nil {
		webhook = uc.sysCfg.AlertWebhook
	}
	providerID, _ := strconv.ParseUint(uc.getSetting(ctx, model.SettingKeyCacheEmbeddingProviderID), 10, 64)
	dim, _ := strconv.Atoi(uc.getSetting(ctx, model.SettingKeyCacheEmbeddingDim))
	return dto.SettingsResp{
		AlertWebhook:             webhook,
		AlertWebhookIsOverride:   usingOverride,
		CacheEmbeddingProviderID: uint(providerID),
		CacheEmbeddingModel:      uc.getSetting(ctx, model.SettingKeyCacheEmbeddingModel),
		CacheEmbeddingDim:        dim,
	}
}

// UpdateSettings applies a partial update to console-editable settings.
func (uc *GatewayUseCase) UpdateSettings(ctx context.Context, req *dto.UpdateSettingsReq) (dto.SettingsResp, error) {
	if req.AlertWebhook != nil {
		if err := uc.setSetting(ctx, model.SettingKeyAlertWebhook, strings.TrimSpace(*req.AlertWebhook)); err != nil {
			return dto.SettingsResp{}, err
		}
	}
	if req.CacheEmbeddingProviderID != nil {
		if err := uc.setSetting(ctx, model.SettingKeyCacheEmbeddingProviderID, strconv.FormatUint(uint64(*req.CacheEmbeddingProviderID), 10)); err != nil {
			return dto.SettingsResp{}, err
		}
	}
	if req.CacheEmbeddingModel != nil {
		if err := uc.setSetting(ctx, model.SettingKeyCacheEmbeddingModel, strings.TrimSpace(*req.CacheEmbeddingModel)); err != nil {
			return dto.SettingsResp{}, err
		}
	}
	if req.CacheEmbeddingDim != nil {
		if err := uc.setSetting(ctx, model.SettingKeyCacheEmbeddingDim, strconv.Itoa(*req.CacheEmbeddingDim)); err != nil {
			return dto.SettingsResp{}, err
		}
	}
	return uc.GetSettings(ctx), nil
}

// TestAlertWebhook sends a synthetic billing_alert_test event to the
// resolved webhook URL and reports whether delivery succeeded. Synchronous
// (unlike sendAlert) because it's an explicit, low-frequency admin action
// that wants an immediate result, not fire-and-forget.
func (uc *GatewayUseCase) TestAlertWebhook(ctx context.Context) error {
	url := uc.resolveAlertWebhookURL(ctx)
	if url == "" {
		return ErrSettingsWebhookNotConfigured
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":       "test",
		"occurredAt": time.Now().Format(time.RFC3339),
	})
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return ErrSettingsWebhookTestFailed.WithMetadata(map[string]string{"err": err.Error()})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ErrSettingsWebhookTestFailed.WithMetadata(map[string]string{"err": err.Error()})
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ErrSettingsWebhookTestFailed.WithMetadata(map[string]string{"status": resp.Status})
	}
	return nil
}

func (uc *GatewayUseCase) resolveAlertWebhookURL(ctx context.Context) string {
	if v := uc.getSetting(ctx, model.SettingKeyAlertWebhook); v != "" {
		return v
	}
	if uc.sysCfg != nil {
		return uc.sysCfg.AlertWebhook
	}
	return ""
}

// resolveAlertWebhook mirrors resolveAlertWebhookURL for BillingManager,
// which owns its own sendAlert path and DB handle.
func (bm *BillingManager) resolveAlertWebhook(ctx context.Context) string {
	var s model.AISetting
	if err := bm.db.WithContext(ctx).Where("setting_key = ?", model.SettingKeyAlertWebhook).First(&s).Error; err == nil && s.Value != "" {
		return s.Value
	}
	if bm.sysCfg != nil {
		return bm.sysCfg.AlertWebhook
	}
	return ""
}

// -----------------------------------------------------------------------------
// Credits rates (existing ai_credits_rates table; previously DB-managed only)
// -----------------------------------------------------------------------------

// CreateCreditsRate registers a currency's CNY-equivalent credit rate.
func (uc *GatewayUseCase) CreateCreditsRate(ctx context.Context, req *dto.CreateCreditsRateReq) (*model.AICreditsRate, error) {
	if strings.TrimSpace(req.Currency) == "" || req.RatePerCredit <= 0 {
		return nil, ErrCreditsRateInvalid
	}
	r := &model.AICreditsRate{
		Currency:      strings.ToUpper(strings.TrimSpace(req.Currency)),
		RatePerCredit: req.RatePerCredit,
		IsEnabled:     true,
		Description:   req.Description,
	}
	if err := uc.db.WithContext(ctx).Create(r).Error; err != nil {
		return nil, ErrCreditsRateExists
	}
	return r, nil
}

// ListCreditsRates returns all configured currency rates.
func (uc *GatewayUseCase) ListCreditsRates(ctx context.Context) ([]model.AICreditsRate, error) {
	var list []model.AICreditsRate
	if err := uc.db.WithContext(ctx).Order("currency asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateCreditsRate applies a partial update and invalidates the Redis-cached
// rate (getRatePerCredit/getCNYRatePerCredit) so the change is live immediately.
func (uc *GatewayUseCase) UpdateCreditsRate(ctx context.Context, req *dto.UpdateCreditsRateReq) (*model.AICreditsRate, error) {
	var r model.AICreditsRate
	if err := uc.db.WithContext(ctx).First(&r, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCreditsRateNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.RatePerCredit != nil {
		if *req.RatePerCredit <= 0 {
			return nil, ErrCreditsRateInvalid
		}
		updates["rate_per_credit"] = *req.RatePerCredit
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(updates) > 0 {
		if err := uc.db.WithContext(ctx).Model(&r).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	if uc.rdb != nil {
		uc.rdb.Del(ctx, "ai:gw:credits:rate:"+r.Currency)
	}
	uc.db.WithContext(ctx).First(&r, req.ID)
	return &r, nil
}

// DeleteCreditsRate removes a currency rate.
func (uc *GatewayUseCase) DeleteCreditsRate(ctx context.Context, id uint) error {
	var r model.AICreditsRate
	if err := uc.db.WithContext(ctx).First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCreditsRateNotFound
		}
		return err
	}
	if err := uc.db.WithContext(ctx).Delete(&model.AICreditsRate{}, id).Error; err != nil {
		return err
	}
	if uc.rdb != nil {
		uc.rdb.Del(ctx, "ai:gw:credits:rate:"+r.Currency)
	}
	return nil
}
