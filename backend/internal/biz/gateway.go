package biz

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/observability"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

// GatewayUseCase is the core gateway business logic with all injected dependencies.
type GatewayUseCase struct {
	db      *gorm.DB
	rdb     *redis.Client
	quota   *QuotaManager
	audit   *AuditWorker
	router  *RouterManager
	billing *BillingManager
	metrics *observability.Metrics
	aiConf  *conf.AI
	sysCfg  *conf.System
	logger  *log.Helper
	rawLog  log.Logger
}

// NewGatewayUseCase constructs a GatewayUseCase via Wire DI.
func NewGatewayUseCase(
	db *gorm.DB,
	rdb *redis.Client,
	quota *QuotaManager,
	audit *AuditWorker,
	router *RouterManager,
	billing *BillingManager,
	metrics *observability.Metrics,
	aiConf *conf.AI,
	sysCfg *conf.System,
	logger log.Logger,
) *GatewayUseCase {
	return &GatewayUseCase{
		db:      db,
		rdb:     rdb,
		quota:   quota,
		audit:   audit,
		router:  router,
		billing: billing,
		metrics: metrics,
		aiConf:  aiConf,
		sysCfg:  sysCfg,
		logger:  log.NewHelper(logger),
		rawLog:  logger,
	}
}

type providerEntry struct {
	provider model.AIProvider
	apiKey   string
}

// =============================================================================
// 虚拟 Key 管理
// =============================================================================

// CreateVirtualKey 创建虚拟 Key
func (uc *GatewayUseCase) CreateVirtualKey(ctx context.Context, req dto.CreateVirtualKeyReq, creatorID uint) (dto.CreateVirtualKeyResp, error) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return dto.CreateVirtualKeyResp{}, ErrKeyGenerationFailed
	}
	plainKey := "sk-vk-" + hex.EncodeToString(rawBytes)

	sum := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(sum[:])
	keyPrefix := plainKey[:16]

	plainKeyEncrypted, err := pkg.EncryptAES(plainKey, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return dto.CreateVirtualKeyResp{}, ErrEncryptionFailed
	}

	ipWhitelist, err := NormalizeIPWhitelist(req.IPWhitelist, req.IPWhitelistEnabled)
	if err != nil {
		return dto.CreateVirtualKeyResp{}, err
	}

	vk := model.AIVirtualKey{
		Name:               req.Name,
		KeyHash:            keyHash,
		KeyPrefix:          keyPrefix,
		PlainKeyEncrypted:  plainKeyEncrypted,
		ProviderID:         req.ProviderID,
		BaseURL:            req.BaseURL,
		AllowedModels:      datatypes.JSON(req.AllowedModels),
		DailyTokenQuota:    req.DailyTokenQuota,
		HourlyTokenQuota:   req.HourlyTokenQuota,
		HourlyReqQuota:     req.HourlyReqQuota,
		MaxConcurrency:     req.MaxConcurrency,
		PIIPolicyID:        req.PIIPolicyID,
		IPWhitelistEnabled: req.IPWhitelistEnabled,
		IPWhitelist:        datatypes.JSON(ipWhitelist),
		IsEnabled:          true,
		ExpiresAt:          req.ExpiresAt,
		ProjectID:          req.ProjectID,
		ProjectName:        req.ProjectName,
		EnvID:              req.EnvID,
		DailyPointQuota:    req.DailyPointQuota,
		HourlyPointQuota:   req.HourlyPointQuota,
		CreatedBy:          creatorID,
		Description:        req.Description,
		TenantID:           req.TenantID,
		ProjectRefID:       req.ProjectRefID,
	}
	if vk.TenantID == 0 { // attach to the default tenant so billing always has an owner
		var defTenant model.AITenant
		if err := uc.db.WithContext(ctx).Where("name = ?", model.DefaultTenantName).First(&defTenant).Error; err == nil {
			vk.TenantID = defTenant.ID
		}
	}
	if err := uc.db.WithContext(ctx).Create(&vk).Error; err != nil {
		return dto.CreateVirtualKeyResp{}, err
	}

	return dto.CreateVirtualKeyResp{
		ID:        vk.ID,
		Name:      vk.Name,
		KeyPrefix: vk.KeyPrefix,
		PlainKey:  plainKey,
	}, nil
}

// RevealVirtualKey 解密并返回虚拟 Key 明文
func (uc *GatewayUseCase) RevealVirtualKey(ctx context.Context, id uint) (dto.RevealVirtualKeyResp, error) {
	var vk model.AIVirtualKey
	if err := uc.db.WithContext(ctx).First(&vk, id).Error; err != nil {
		return dto.RevealVirtualKeyResp{}, ErrVirtualKeyNotFound
	}
	if vk.PlainKeyEncrypted == "" {
		return dto.RevealVirtualKeyResp{}, ErrKeyPlaintextNotStored
	}
	plainKey, err := pkg.DecryptAES(vk.PlainKeyEncrypted, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return dto.RevealVirtualKeyResp{}, ErrDecryptionFailed
	}
	return dto.RevealVirtualKeyResp{ID: vk.ID, Name: vk.Name, PlainKey: plainKey}, nil
}

// ListVirtualKeys 分页查询虚拟 Key 列表
func (uc *GatewayUseCase) ListVirtualKeys(ctx context.Context, req dto.ListVirtualKeysReq) ([]model.AIVirtualKey, int64, error) {
	var list []model.AIVirtualKey
	db := uc.db.WithContext(ctx).Model(&model.AIVirtualKey{})
	if req.ProviderID > 0 {
		db = db.Where("provider_id = ?", req.ProviderID)
	}
	if req.IsEnabled != nil {
		db = db.Where("is_enabled = ?", *req.IsEnabled)
	}
	if req.Keyword != "" {
		db = db.Where("name LIKE ?", "%"+req.Keyword+"%")
	}
	if req.ProjectID != nil {
		db = db.Where("project_id = ?", *req.ProjectID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("created_at desc").Limit(req.Limit()).Offset(req.Offset()).Find(&list).Error; err != nil {
		return nil, 0, err
	}

	// 批量回填创建者昵称
	creatorIDs := make([]uint, 0, len(list))
	for _, vk := range list {
		if vk.CreatedBy > 0 {
			creatorIDs = append(creatorIDs, vk.CreatedBy)
		}
	}
	if len(creatorIDs) > 0 {
		var users []struct {
			ID       uint   `gorm:"column:id"`
			NickName string `gorm:"column:nick_name"`
			Avatar   string `gorm:"column:avatar"`
		}
		uc.db.WithContext(ctx).Table("sys_users").
			Select("id, nick_name, avatar").
			Where("id IN ?", creatorIDs).
			Find(&users)
		type userInfo struct{ name, avatar string }
		infoMap := make(map[uint]userInfo, len(users))
		for _, u := range users {
			infoMap[u.ID] = userInfo{u.NickName, u.Avatar}
		}
		for i := range list {
			if info, ok := infoMap[list[i].CreatedBy]; ok {
				list[i].CreatedByName = info.name
				list[i].CreatedByAvatar = info.avatar
			}
		}
	}

	// 批量回填每模型配额
	keyIDs := make([]uint, 0, len(list))
	for _, vk := range list {
		keyIDs = append(keyIDs, vk.ID)
	}
	if len(keyIDs) > 0 {
		var mqs []model.AIVirtualKeyModelQuota
		uc.db.WithContext(ctx).
			Where("virtual_key_id IN ?", keyIDs).
			Order("model_name asc").
			Find(&mqs)
		byKey := make(map[uint][]model.AIVirtualKeyModelQuota, len(keyIDs))
		for _, mq := range mqs {
			byKey[mq.VirtualKeyID] = append(byKey[mq.VirtualKeyID], mq)
		}
		for i := range list {
			list[i].ModelQuotas = byKey[list[i].ID]
		}
	}
	return list, total, nil
}

// VirtualKeyStats 虚拟 Key 概览统计
func (uc *GatewayUseCase) VirtualKeyStats(ctx context.Context) (dto.VirtualKeyStatsResp, error) {
	now := time.Now()
	in7d := now.Add(7 * 24 * time.Hour)
	var row struct {
		Total    int64
		Enabled  int64
		Expiring int64
		Inactive int64
	}
	err := uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Select(`COUNT(*) AS total,
			SUM(CASE WHEN is_enabled = ? AND (expires_at IS NULL OR expires_at > ?) THEN 1 ELSE 0 END) AS enabled,
			SUM(CASE WHEN is_enabled = ? AND expires_at IS NOT NULL AND expires_at > ? AND expires_at <= ? THEN 1 ELSE 0 END) AS expiring,
			SUM(CASE WHEN is_enabled = ? OR (expires_at IS NOT NULL AND expires_at <= ?) THEN 1 ELSE 0 END) AS inactive`,
			true, now, true, now, in7d, false, now).
		Scan(&row).Error
	if err != nil {
		return dto.VirtualKeyStatsResp{}, err
	}
	return dto.VirtualKeyStatsResp{Total: row.Total, Enabled: row.Enabled, Expiring: row.Expiring, Inactive: row.Inactive}, nil
}

// UpdateVirtualKey 更新虚拟 Key 配置
func (uc *GatewayUseCase) UpdateVirtualKey(ctx context.Context, req dto.UpdateVirtualKeyReq) error {
	if req.Name == "" {
		return fmt.Errorf("key 名称不能为空")
	}
	ipWhitelist, err := NormalizeIPWhitelist(req.IPWhitelist, req.IPWhitelistEnabled)
	if err != nil {
		return err
	}
	updates := map[string]interface{}{
		"name":                 req.Name,
		"allowed_models":       req.AllowedModels,
		"pii_policy_id":        req.PIIPolicyID,
		"ip_whitelist_enabled": req.IPWhitelistEnabled,
		"ip_whitelist":         datatypes.JSON(ipWhitelist),
		"expires_at":           req.ExpiresAt,
		"project_id":           req.ProjectID,
		"project_name":         req.ProjectName,
		"env_id":               req.EnvID,
		"description":          req.Description,
	}
	if req.IsEnabled != nil {
		updates["is_enabled"] = *req.IsEnabled
	}
	err = uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Where("id = ?", req.ID).Updates(updates).Error
	if err == nil {
		uc.invalidateKeyCache(ctx, req.ID)
	}
	return err
}

// UpdateVirtualKeyStatus 仅更新虚拟 Key 启用状态
func (uc *GatewayUseCase) UpdateVirtualKeyStatus(ctx context.Context, req dto.UpdateVirtualKeyStatusReq) error {
	res := uc.db.WithContext(ctx).Model(&model.AIVirtualKey{}).
		Where("id = ?", req.ID).Update("is_enabled", *req.IsEnabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("虚拟 Key 不存在")
	}
	uc.invalidateKeyCache(ctx, req.ID)
	return nil
}

// RevokeVirtualKey 撤销虚拟 Key（软删除）
func (uc *GatewayUseCase) RevokeVirtualKey(ctx context.Context, id uint) error {
	err := uc.db.WithContext(ctx).Delete(&model.AIVirtualKey{}, id).Error
	if err == nil {
		uc.invalidateKeyCache(ctx, id)
	}
	return err
}

// GetQuotaConfig 读取虚拟 Key 的配额配置
func (uc *GatewayUseCase) GetQuotaConfig(ctx context.Context, keyID uint) (*dto.QuotaConfigResp, error) {
	var vk model.AIVirtualKey
	if err := uc.db.WithContext(ctx).First(&vk, keyID).Error; err != nil {
		return nil, fmt.Errorf("虚拟 Key 不存在")
	}
	var rows []model.AIVirtualKeyModelQuota
	uc.db.WithContext(ctx).Where("virtual_key_id = ?", keyID).Order("model_name asc").Find(&rows)

	items := make([]dto.QuotaConfigItem, 0, len(rows))
	for i := range rows {
		mq := &rows[i]
		dt, ht, hr, dp, hp := uc.quota.GetModelUsage(ctx, keyID, mq)
		items = append(items, dto.QuotaConfigItem{
			ModelName:        mq.ModelName,
			DailyTokenQuota:  mq.DailyTokenQuota,
			HourlyTokenQuota: mq.HourlyTokenQuota,
			HourlyReqQuota:   mq.HourlyReqQuota,
			DailyPointQuota:  mq.DailyPointQuota,
			HourlyPointQuota: mq.HourlyPointQuota,
			DailyTokenUsed:   dt,
			HourlyTokenUsed:  ht,
			HourlyReqUsed:    hr,
			DailyPointUsed:   dp,
			HourlyPointUsed:  hp,
		})
	}
	return &dto.QuotaConfigResp{
		KeyID:            vk.ID,
		Name:             vk.Name,
		KeyPrefix:        vk.KeyPrefix,
		ProviderID:       vk.ProviderID,
		AllowedModels:    json.RawMessage(vk.AllowedModels),
		DailyTokenQuota:  vk.DailyTokenQuota,
		HourlyTokenQuota: vk.HourlyTokenQuota,
		HourlyReqQuota:   vk.HourlyReqQuota,
		MaxConcurrency:   vk.MaxConcurrency,
		DailyPointQuota:  vk.DailyPointQuota,
		HourlyPointQuota: vk.HourlyPointQuota,
		ModelQuotas:      items,
	}, nil
}

// UpdateQuotaConfig 更新虚拟 Key 配额配置
func (uc *GatewayUseCase) UpdateQuotaConfig(ctx context.Context, req dto.UpdateQuotaConfigReq) error {
	seen := make(map[string]struct{}, len(req.ModelQuotas))
	for _, it := range req.ModelQuotas {
		name := strings.TrimSpace(it.ModelName)
		if name == "" {
			return fmt.Errorf("模型名不能为空")
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("模型 %s 配额重复", name)
		}
		seen[name] = struct{}{}
	}

	err := uc.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.AIVirtualKey{}).Where("id = ?", req.KeyID).Updates(map[string]interface{}{
			"daily_token_quota":  req.DailyTokenQuota,
			"hourly_token_quota": req.HourlyTokenQuota,
			"hourly_req_quota":   req.HourlyReqQuota,
			"max_concurrency":    req.MaxConcurrency,
			"daily_point_quota":  req.DailyPointQuota,
			"hourly_point_quota": req.HourlyPointQuota,
		}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("virtual_key_id = ?", req.KeyID).
			Delete(&model.AIVirtualKeyModelQuota{}).Error; err != nil {
			return err
		}
		if len(req.ModelQuotas) > 0 {
			rows := make([]model.AIVirtualKeyModelQuota, 0, len(req.ModelQuotas))
			for _, it := range req.ModelQuotas {
				rows = append(rows, model.AIVirtualKeyModelQuota{
					VirtualKeyID:     req.KeyID,
					ModelName:        strings.TrimSpace(it.ModelName),
					DailyTokenQuota:  it.DailyTokenQuota,
					HourlyTokenQuota: it.HourlyTokenQuota,
					HourlyReqQuota:   it.HourlyReqQuota,
					DailyPointQuota:  it.DailyPointQuota,
					HourlyPointQuota: it.HourlyPointQuota,
				})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		uc.invalidateKeyCache(ctx, req.KeyID)
	}
	return err
}

// GetKeyQuotaUsage 返回虚拟 Key 的实时配额用量
func (uc *GatewayUseCase) GetKeyQuotaUsage(ctx context.Context, keyID uint) (dto.KeyQuotaUsageResp, error) {
	var vk model.AIVirtualKey
	if err := uc.db.WithContext(ctx).First(&vk, keyID).Error; err != nil {
		return dto.KeyQuotaUsageResp{}, fmt.Errorf("虚拟 Key 不存在")
	}
	dailyTokenUsed, hourlyTokenUsed, hourlyReqUsed, currentConcurrency, dailyPointUsed, hourlyPointUsed := uc.quota.GetUsage(ctx, &vk)
	return dto.KeyQuotaUsageResp{
		KeyID:              vk.ID,
		DailyTokenQuota:    vk.DailyTokenQuota,
		DailyTokenUsed:     dailyTokenUsed,
		HourlyTokenQuota:   vk.HourlyTokenQuota,
		HourlyTokenUsed:    hourlyTokenUsed,
		HourlyReqQuota:     vk.HourlyReqQuota,
		HourlyReqUsed:      hourlyReqUsed,
		MaxConcurrency:     vk.MaxConcurrency,
		CurrentConcurrency: currentConcurrency,
		DailyPointQuota:    vk.DailyPointQuota,
		DailyPointUsed:     dailyPointUsed,
		HourlyPointQuota:   vk.HourlyPointQuota,
		HourlyPointUsed:    hourlyPointUsed,
	}, nil
}

// =============================================================================
// Key 缓存管理
// =============================================================================

// ResolveKeyByHash 通过 SHA-256 hash 解析虚拟 Key（L1→L2→DB）
func (uc *GatewayUseCase) ResolveKeyByHash(ctx context.Context, hash string) (*model.AIVirtualKey, error) {
	if vk := localCacheGet(hash); vk != nil {
		return vk, nil
	}

	cacheKey := "ai:gw:key:" + hash

	if uc.rdb != nil {
		cached, err := uc.rdb.Get(ctx, cacheKey).Bytes()
		if err == nil {
			var vk model.AIVirtualKey
			if json.Unmarshal(cached, &vk) == nil {
				localCacheSet(hash, &vk)
				return &vk, nil
			}
		}
	}

	var vk model.AIVirtualKey
	if err := uc.db.WithContext(ctx).Where("key_hash = ?", hash).First(&vk).Error; err != nil {
		return nil, fmt.Errorf("invalid key")
	}

	var modelQuotas []model.AIVirtualKeyModelQuota
	if err := uc.db.WithContext(ctx).Where("virtual_key_id = ?", vk.ID).Find(&modelQuotas).Error; err == nil {
		vk.ModelQuotas = modelQuotas
	}

	if uc.rdb != nil {
		if data, err := json.Marshal(vk); err == nil {
			uc.rdb.Set(ctx, cacheKey, data, 5*time.Minute)
		}
	}
	localCacheSet(hash, &vk)
	return &vk, nil
}

// invalidateKeyCache 清除指定 Key ID 的 L1/L2 缓存并广播失效消息
func (uc *GatewayUseCase) invalidateKeyCache(ctx context.Context, keyID uint) {
	var vk model.AIVirtualKey
	if err := uc.db.WithContext(ctx).Unscoped().First(&vk, keyID).Error; err != nil {
		return
	}
	hash := vk.KeyHash
	localCacheInvalidate(hash)
	if uc.rdb != nil {
		uc.rdb.Del(ctx, "ai:gw:key:"+hash)
		uc.rdb.Publish(ctx, keyCacheInvalidateCh, hash)
	}
}

// =============================================================================
// 网关代理
// =============================================================================

// ProxyRequest 转发请求到 LLM 提供方
func (uc *GatewayUseCase) ProxyRequest(ctx context.Context, key *model.AIVirtualKey, body []byte, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx = withClientAgent(ctx, detectClientAgent(r.UserAgent(), body))
	ctx = withSessionNative(ctx, extractNativeSessionID(key, r, body))
	isExactModelRequest := isExactModelEndpoint(r.URL.Path)

	// PII 检测（stub：log only，完整检测引擎后续接入）
	ctx, piiOut := uc.applyPIIPolicy(ctx, key, body)
	if piiOut.Blocked {
		uc.writeAuditLog(ctx, key, key.ProviderID, "", body, nil, 0, 0, 0, 0, 0, http.StatusBadRequest, "PII detected: "+piiOut.Types, true, ClientIPFromRequest(r), "openai", 0, 0, "")
		http.Error(w, `{"error":{"message":"Request blocked: sensitive personal information detected","code":"PII_DETECTED"}}`, http.StatusBadRequest)
		return
	}
	body = piiOut.NewBody

	// 解析请求模型 & 统一解析目标模型
	requestedModel := extractModel(body)
	var realModelName string
	var providerID uint
	var mappingActive bool
	var modelErr error
	if isExactModelRequest {
		realModelName, providerID, mappingActive, modelErr = uc.resolveExactTargetModel(ctx, key, requestedModel)
	} else {
		realModelName, providerID, mappingActive, modelErr = uc.resolveTargetModel(ctx, key, requestedModel)
	}
	if modelErr != nil {
		uc.writeAuditLog(ctx, key, key.ProviderID, requestedModel, body, nil, 0, 0, 0, 0, 0, http.StatusBadRequest, modelErr.Error(), false, ClientIPFromRequest(r), "openai", 0, 0, "", requestedModel)
		errMsg, _ := json.Marshal(map[string]interface{}{
			"error": map[string]string{"message": modelErr.Error(), "code": "MODEL_NOT_ALLOWED"},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(errMsg)
		return
	}

	// 会话亲和
	sessionHash := extractSessionHash(key, r, body)
	if !mappingActive && !isExactModelRequest {
		providerID, realModelName, _ = uc.resolveSticky(ctx, sessionHash, realModelName, providerID, requestedModel != "")
	}

	// 模型感知限额
	if uc.enforceModelQuota(ctx, key, providerID, realModelName, requestedModel, body, r, w, "openai") {
		return
	}

	// 计费闸门（P1，docs/design/03-billing-and-monetization.md）：
	// 租户账户停用/余额不足 → 402；否则按估算冻结，响应后按实际结算。
	tenantID := uc.tenantIDForKey(ctx, key)
	requestID := generateRequestID()
	var freeze *FreezeHandle
	if uc.billing != nil {
		var admitErr error
		freeze, admitErr = uc.billing.Admit(ctx, tenantID, providerID, realModelName, body)
		if admitErr != nil {
			if uc.metrics != nil {
				uc.metrics.BillingRejections.WithLabelValues("rejected").Inc()
			}
			uc.writeAuditLog(ctx, key, providerID, realModelName, body, nil, 0, 0, 0, 0, 0, http.StatusPaymentRequired, admitErr.Error(), false, ClientIPFromRequest(r), "openai", 0, 0, "", requestedModel)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(billingErrorBody(admitErr.Error())))
			return
		}
	}

	// 精确响应缓存（P2-4，docs/design/07-caching-strategies.md）：
	// 护栏之后、路由之前；命中即跳过上游与 Token 配额，按命中策略计费。
	cacheCfg, cacheEnabled := parseCacheConfig(key)
	var cacheDigest string
	if cacheEnabled && cacheableRequest(r.URL.Path, body) {
		cacheDigest, _ = respCacheKey(tenantID, realModelName, body)
		if entry := cacheLookup(ctx, uc.rdb, cacheDigest); entry != nil {
			if uc.metrics != nil {
				uc.metrics.CacheRequests.WithLabelValues("exact", "hit").Inc()
			}
			var hitPrice int64
			if freeze != nil && freeze.Account != nil {
				fullPrice := uc.billing.PriceMicro(ctx, freeze.Account, entry.ProviderID, realModelName, entry.Prompt, entry.Completion, entry.CacheRead)
				hitPrice = cacheHitPriceMicro(fullPrice, cacheCfg)
				uc.billing.Settle(ctx, freeze, requestID, hitPrice, "", "cache-hit model="+realModelName)
			}
			if uc.billing != nil {
				uc.billing.RecordUsage(tenantID, key.ID, entry.ProviderID, realModelName, 0, 0, 0, 0, hitPrice, true)
			}
			uc.writeAuditLog(ctx, key, entry.ProviderID, realModelName, body, entry.Body, entry.Prompt, entry.Completion, entry.CacheRead, 0, time.Since(startTime).Milliseconds(), http.StatusOK, "cache-hit-exact", false, ClientIPFromRequest(r), "openai", float64(hitPrice)/model.MicroCreditScale, 0, "", requestedModel)
			writeCachedResponse(w, entry, extractStreamFlag(body), realModelName)
			return
		}
		if uc.metrics != nil {
			uc.metrics.CacheRequests.WithLabelValues("exact", "miss").Inc()
		}
	}

	sendBody := replaceModelInBody(body, realModelName)
	isStream := extractStreamFlag(sendBody)
	sendBody = uc.injectModelExtraParams(ctx, sendBody, providerID, realModelName)
	sendBody = injectStreamUsageOption(sendBody, isStream)
	if !isExactModelRequest {
		sendBody = injectPromptCacheKey(sendBody, sessionHash)
	}

	const aiV1Prefix = "/ai/v1"
	openAIPath := r.URL.Path
	if idx := strings.Index(openAIPath, aiV1Prefix); idx >= 0 {
		openAIPath = openAIPath[idx+len(aiV1Prefix):]
	}
	metricRoute := openAIPath
	if r.URL.RawQuery != "" {
		openAIPath += "?" + r.URL.RawQuery
	}

	client := newProxyClient()

	// 构建故障转移候选列表。命中映射时映射是指令而非提示——不做故障转移。
	// 详见 docs/design/01-routing-and-lb.md。
	var candidates []RouteCandidate
	if mappingActive || uc.router == nil {
		candidates = []RouteCandidate{{ProviderID: providerID, Model: realModelName}}
	} else {
		candidates = uc.router.Candidates(ctx, realModelName, providerID)
		if len(candidates) > maxUpstreamAttempts {
			candidates = candidates[:maxUpstreamAttempts]
		}
	}

	var resp *http.Response
	var selectedProvider *providerEntry
	attemptUsed := 0
	lastStatus := http.StatusBadGateway
	lastErrMsg := "no upstream candidate available"

	for i, cand := range candidates {
		attemptUsed = i
		if uc.router != nil && !uc.router.TryPass(ctx, cand.ProviderID) {
			lastErrMsg = fmt.Sprintf("provider %d circuit open", cand.ProviderID)
			continue
		}
		entry, perr := uc.loadProviderDirect(ctx, cand.ProviderID)
		if perr != nil {
			uc.logger.Errorf("AI 网关加载提供方失败 providerID=%d err=%v", cand.ProviderID, perr)
			lastErrMsg = perr.Error()
			continue
		}

		reqCtx, cancelReq := proxyRequestCtx(ctx, isStream, uc.aiConf)
		// 协议适配层（P2-1）：按提供方方言构建上游请求
		upstreamReq, err := buildUpstreamRequest(reqCtx, entry, r.Method, openAIPath, sendBody, isStream)
		if err != nil {
			cancelReq()
			uc.logger.Errorf("AI 网关构建上游请求失败 provider=%s err=%v", entry.provider.Name, err)
			lastErrMsg = err.Error()
			continue
		}

		attemptResp, reqErr := client.Do(upstreamReq)
		if reqErr != nil {
			cancelReq()
			if uc.router != nil {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			}
			uc.logger.Errorf("AI 网关代理请求失败 provider=%s attempt=%d err=%v", entry.provider.Name, i+1, reqErr)
			lastErrMsg = reqErr.Error()
			continue
		}

		// 可重试状态码且还有候选：关闭响应、上报失败、切换下一提供方。
		// 一旦任何字节写回客户端就不能再转移——这里尚未写出任何内容。
		if IsRetryableStatus(attemptResp.StatusCode) && i < len(candidates)-1 {
			snippet := upstreamErrSnippet(readLimitedBody(attemptResp.Body))
			attemptResp.Body.Close()
			cancelReq()
			if uc.router != nil {
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			}
			uc.logger.Warnf("AI 网关上游可重试错误，转移下一候选 provider=%s status=%d attempt=%d",
				entry.provider.Name, attemptResp.StatusCode, i+1)
			lastStatus = attemptResp.StatusCode
			lastErrMsg = snippet
			continue
		}

		// 提交本次尝试：上报熔断结果
		if uc.router != nil {
			switch {
			case attemptResp.StatusCode == http.StatusUnauthorized || attemptResp.StatusCode == http.StatusForbidden:
				// 上游凭证问题：为该提供方累计失败，但请求本身不再重试
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptFatalError)
			case IsRetryableStatus(attemptResp.StatusCode):
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptRetryableError)
			default:
				uc.router.ReportResult(ctx, cand.ProviderID, AttemptSuccess)
			}
		}
		resp = attemptResp
		selectedProvider = entry
		defer cancelReq()
		break
	}

	if resp == nil || selectedProvider == nil {
		if uc.billing != nil {
			uc.billing.ReleaseFreeze(ctx, freeze)
		}
		http.Error(w, `{"error":{"message":"all upstream providers failed"}}`, http.StatusBadGateway)
		uc.writeAuditLog(ctx, key, providerID, realModelName, body, nil, 0, 0, 0, 0, time.Since(startTime).Milliseconds(), lastStatus, lastErrMsg, false, ClientIPFromRequest(r), "openai", 0, 0, "", requestedModel)
		if uc.metrics != nil {
			uc.metrics.RequestsTotal.WithLabelValues(metricRoute, observability.StatusClass(http.StatusBadGateway)).Inc()
		}
		return
	}
	if attemptUsed > 0 && uc.metrics != nil {
		uc.metrics.FailoverTotal.WithLabelValues(
			fmt.Sprintf("%d", candidates[0].ProviderID),
			fmt.Sprintf("%d", selectedProvider.provider.ID)).Inc()
	}
	providerID = selectedProvider.provider.ID
	defer resp.Body.Close()

	go setStickySession(context.Background(), uc.rdb, sessionHash, selectedProvider.provider.ID, realModelName)

	var respBody []byte
	var streamErr string
	promptTokens, completionTokens, cachedTokens := 0, 0, 0
	respIsStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

	if selectedProvider.provider.ProviderType == model.ProviderTypeAnthropic && resp.StatusCode < 300 {
		// 协议适配（P2-1/P2-3）：Anthropic 响应翻译回 OpenAI 格式，用量归一化。
		// 不透传上游响应头（Content-Length/格式均已改变），由翻译层自建。
		if respIsStream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 64<<10), 4<<20)
			respBody, promptTokens, completionTokens, cachedTokens, streamErr = translateAnthropicStream(w, scanner, realModelName)
		} else {
			raw, _ := io.ReadAll(resp.Body)
			translated, p, c, cached, terr := anthropicToOpenAIResponse(raw, realModelName)
			if terr != nil {
				uc.logger.Errorf("Anthropic 响应翻译失败 provider=%s err=%v", selectedProvider.provider.Name, terr)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte(`{"error":{"message":"upstream response translation failed"}}`))
				respBody = raw
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write(translated)
				respBody = translated
				promptTokens, completionTokens, cachedTokens = p, c, cached
			}
		}
	} else {
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		if isStream && !respIsStream {
			uc.logger.Warnf("请求声明流式但上游返回非 SSE Content-Type contentType=%s provider=%s",
				resp.Header.Get("Content-Type"), selectedProvider.provider.Name)
		}

		if respIsStream {
			respBody, promptTokens, completionTokens, cachedTokens, streamErr = streamProxy(w, resp.Body)
			if promptTokens == 0 && completionTokens == 0 && resp.StatusCode == http.StatusOK {
				p, c, cached := parseUsageFromBody(respBody)
				if p > 0 || c > 0 {
					promptTokens, completionTokens, cachedTokens = p, c, cached
				}
			}
		} else {
			respBody, _ = io.ReadAll(resp.Body)
			w.Write(respBody)
			promptTokens, completionTokens, cachedTokens = parseUsageFromBody(respBody)
		}
	}

	auditErrMsg := ""
	if resp.StatusCode >= 400 {
		auditErrMsg = upstreamErrSnippet(respBody)
		uc.logger.Errorf("AI 网关上游返回错误 provider=%s model=%s statusCode=%d",
			selectedProvider.provider.Name, realModelName, resp.StatusCode)
	} else if streamErr != "" {
		auditErrMsg = streamErr
		uc.logger.Errorf("AI 网关上游以 200+SSE 返回错误事件 provider=%s model=%s error=%s",
			selectedProvider.provider.Name, realModelName, streamErr)
	}

	latency := time.Since(startTime).Milliseconds()
	uc.quota.CommitTokens(ctx, key, realModelName, promptTokens, completionTokens)
	openaiCredits, openaiPrice := uc.quota.CommitCredits(ctx, key, selectedProvider.provider.ID, realModelName, promptTokens, completionTokens, cachedTokens)
	openaiUpstreamID := resp.Header.Get("x-request-id")
	uc.writeAuditLog(ctx, key, selectedProvider.provider.ID, realModelName, body, respBody, promptTokens, completionTokens, cachedTokens, 0, latency, resp.StatusCode, auditErrMsg, false, ClientIPFromRequest(r), "openai", openaiCredits, openaiPrice, openaiUpstreamID, requestedModel)

	// 计费结算 + 用量日聚合（P1-3/P1-5）
	if uc.billing != nil {
		var priceMicro int64
		currency := "CNY"
		if freeze != nil && freeze.Account != nil {
			currency = freeze.Account.Currency
			priceMicro = uc.billing.PriceMicro(ctx, freeze.Account, selectedProvider.provider.ID, realModelName, promptTokens, completionTokens, cachedTokens)
			uc.billing.Settle(ctx, freeze, requestID, priceMicro, openaiUpstreamID, "model="+realModelName)
		}
		costMicro := uc.billing.CostMicro(ctx, currency, selectedProvider.provider.ID, realModelName, promptTokens, completionTokens, cachedTokens)
		if priceMicro == 0 {
			priceMicro = costMicro // no sell-side account: report price at cost
		}
		uc.billing.RecordUsage(tenantID, key.ID, selectedProvider.provider.ID, realModelName, promptTokens, completionTokens, cachedTokens, costMicro, priceMicro, false)
	}

	// 精确缓存写入（仅缓存非流式成功响应；流式响应不重构存储）
	if cacheEnabled && cacheDigest != "" && !respIsStream && resp.StatusCode == http.StatusOK && streamErr == "" {
		cacheStore(uc.rdb, cacheDigest, &cachedResponse{
			Body:       respBody,
			Prompt:     promptTokens,
			Completion: completionTokens,
			CacheRead:  cachedTokens,
			ProviderID: selectedProvider.provider.ID,
			Model:      realModelName,
			CreatedAt:  time.Now().Unix(),
		}, cacheCfg.TTLSec)
	}

	if uc.metrics != nil {
		providerLabel := selectedProvider.provider.Name
		uc.metrics.RequestsTotal.WithLabelValues(metricRoute, observability.StatusClass(resp.StatusCode)).Inc()
		uc.metrics.RequestDuration.WithLabelValues(providerLabel, realModelName).Observe(float64(latency) / 1000)
		if promptTokens > 0 {
			uc.metrics.TokensTotal.WithLabelValues(providerLabel, realModelName, "input").Add(float64(promptTokens))
		}
		if completionTokens > 0 {
			uc.metrics.TokensTotal.WithLabelValues(providerLabel, realModelName, "output").Add(float64(completionTokens))
		}
		if cachedTokens > 0 {
			uc.metrics.TokensTotal.WithLabelValues(providerLabel, realModelName, "cache_read").Add(float64(cachedTokens))
		}
	}
}

// readLimitedBody reads at most 4 KiB of an upstream error body for audit snippets.
func readLimitedBody(r io.Reader) []byte {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return b
}

// WriteRejectionAuditLog 记录在入口被拒绝的请求（无 body 版本）
func (uc *GatewayUseCase) WriteRejectionAuditLog(ctx context.Context, key *model.AIVirtualKey, statusCode int, errMsg, clientIP, protocol string) {
	if key == nil {
		return
	}
	uc.writeAuditLog(ctx, key, key.ProviderID, "", nil, nil, 0, 0, 0, 0, 0,
		statusCode, errMsg, false, clientIP, protocol, 0, 0, "")
}

// loadProviderDirect 从 DB 加载提供方信息并解密 APIKey
func (uc *GatewayUseCase) loadProviderDirect(ctx context.Context, providerID uint) (*providerEntry, error) {
	var p model.AIProvider
	if err := uc.db.WithContext(ctx).First(&p, providerID).Error; err != nil {
		return nil, fmt.Errorf("提供方不存在: %w", err)
	}
	apiKey, err := pkg.DecryptAES(p.APIKey, []byte(uc.sysCfg.EncryptionKey))
	if err != nil {
		return nil, fmt.Errorf("解密 APIKey 失败: %w", err)
	}
	return &providerEntry{provider: p, apiKey: apiKey}, nil
}

// enforceModelQuota 模型感知限额检查
func (uc *GatewayUseCase) enforceModelQuota(ctx context.Context, key *model.AIVirtualKey,
	providerID uint, realModelName, requestedModel string, body []byte,
	r *http.Request, w http.ResponseWriter, protocol string) bool {
	if len(key.ModelQuotas) == 0 {
		return false
	}
	qErr := uc.quota.CheckModelAwareQuota(ctx, key, realModelName)
	if qErr == nil {
		return false
	}
	uc.writeAuditLog(ctx, key, providerID, realModelName, body, nil, 0, 0, 0, 0, 0,
		http.StatusTooManyRequests, qErr.Error(), false, ClientIPFromRequest(r), protocol, 0, 0, "", requestedModel)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	var b []byte
	if protocol == "anthropic" {
		b, _ = json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "rate_limit_error", "message": qErr.Error()},
		})
	} else {
		b, _ = json.Marshal(map[string]any{
			"error": map[string]string{"message": qErr.Error(), "type": "rate_limit_error", "code": "QUOTA_EXCEEDED"},
		})
	}
	w.Write(b)
	return true
}

// writeAuditLog 异步写入审计日志
func (uc *GatewayUseCase) writeAuditLog(ctx context.Context, key *model.AIVirtualKey, actualProviderID uint, modelName string,
	reqBody, respBody []byte, promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens int,
	latency int64, statusCode int, errMsg string, piiBlocked bool, clientIP string, protocol string,
	pointsConsumed float64, priceConsumed float64, upstreamRequestID string, requestedModel ...string) {

	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}
	reqModel := ""
	if len(requestedModel) > 0 {
		reqModel = requestedModel[0]
	}

	reqStr := string(reqBody)
	respStr := string(respBody)

	const warnThreshold = 1 << 20
	if len(reqStr) > warnThreshold {
		uc.logger.Warnf("AI 审计日志请求体超过 1MiB bytes=%d model=%s", len(reqStr), modelName)
	}
	if len(respStr) > warnThreshold {
		uc.logger.Warnf("AI 审计日志响应体超过 1MiB bytes=%d model=%s", len(respStr), modelName)
	}

	totalTokens := promptTokens + completionTokens + cacheReadTokens + cacheCreationTokens
	if protocol == "openai" {
		totalTokens = promptTokens + completionTokens
	}

	entry := model.AIGatewayAuditLog{
		CreatedAt:           time.Now(),
		VirtualKeyID:        key.ID,
		KeyPrefix:           key.KeyPrefix,
		KeyName:             key.Name,
		ProviderID:          actualProviderID,
		Model:               modelName,
		RequestedModel:      reqModel,
		RequestBody:         reqStr,
		ResponseBody:        respStr,
		PromptTokens:        promptTokens,
		CompletionTokens:    completionTokens,
		CacheReadTokens:     cacheReadTokens,
		CacheCreationTokens: cacheCreationTokens,
		TotalTokens:         totalTokens,
		LatencyMs:           latency,
		StatusCode:          statusCode,
		ErrorMessage:        errMsg,
		PIIBlocked:          piiBlocked,
		ClientIP:            clientIP,
		ClientAgent:         clientAgentFromCtx(ctx),
		Protocol:            protocol,
		PointsConsumed:      pointsConsumed,
		PriceConsumed:       priceConsumed,
		UpstreamRequestID:   upstreamRequestID,
		ProjectID:           key.ProjectID,
		ProjectName:         key.ProjectName,
		EnvID:               key.EnvID,
	}

	entry.SessionID = resolveGatewaySessionID(ctx, uc.rdb, key, reqBody, clientIP)

	piiAction := "none"
	if info := piiAuditFromCtx(ctx); info != nil {
		piiAction = info.action
		entry.PIITypes = info.types
	} else if ch, ok := ctx.Value(piiAsyncLogKey{}).(chan *piiAuditInfo); ok {
		select {
		case info, ok := <-ch:
			if ok && info != nil {
				piiAction = info.action
				entry.PIITypes = info.types
			}
		case <-time.After(200 * time.Millisecond):
			uc.logger.Warn("PII 旁路检测超时，跳过审计记录")
		}
	}
	if piiBlocked && piiAction == "none" {
		piiAction = model.PIIActionBlock
	}
	entry.PIIAction = piiAction

	uc.audit.Enqueue(entry)
}

// =============================================================================
// 会话亲和
// =============================================================================

func (uc *GatewayUseCase) resolveSticky(ctx context.Context, sessionHash, realModel string, providerID uint, clientPinnedModel bool) (uint, string, bool) {
	if sessionHash == "" {
		return providerID, realModel, false
	}
	rec := getStickySession(ctx, uc.rdb, sessionHash)
	if rec.ProviderID == 0 {
		return providerID, realModel, false
	}
	if rec.ProviderID != providerID {
		clearStickySession(ctx, uc.rdb, sessionHash)
		return providerID, realModel, false
	}
	// 被钉提供方熔断打开时本次让路，但不清除粘性记录 —— 提供方可能在会话
	// TTL 内恢复，清除会造成亲和抖动（docs/design/01-routing-and-lb.md）。
	if uc.router != nil && uc.router.StateOf(ctx, rec.ProviderID) == model.BreakerStateOpen {
		return providerID, realModel, false
	}
	var cnt int64
	uc.db.WithContext(ctx).Model(&model.AIProvider{}).
		Where("id = ? AND is_enabled = ?", rec.ProviderID, true).Count(&cnt)
	if cnt == 0 {
		clearStickySession(ctx, uc.rdb, sessionHash)
		return providerID, realModel, false
	}
	m := realModel
	if !clientPinnedModel && rec.Model != "" {
		m = rec.Model
	}
	return rec.ProviderID, m, true
}

// =============================================================================
// 模型解析
// =============================================================================

func (uc *GatewayUseCase) resolveTargetModel(ctx context.Context, key *model.AIVirtualKey, requestedModel string) (string, uint, bool, error) {
	mapping, err := uc.resolveModelMapping(ctx, key.ID, key.ProviderID, requestedModel)
	if err != nil {
		return "", 0, false, err
	}
	allowed := allowedModelList(key)
	if mapping != nil {
		realName := mapping.RealModel.Name
		if len(allowed) > 0 && !containsString(allowed, realName) {
			return "", 0, false, fmt.Errorf("模型映射命中的真实模型\"%s\"不在该 Key 的允许模型列表中，访问被拒绝", realName)
		}
		return realName, mapping.RealModel.ProviderID, true, nil
	}
	if len(allowed) > 0 {
		if requestedModel != "" && containsString(allowed, requestedModel) {
			return requestedModel, key.ProviderID, false, nil
		}
		picked := allowed[mrand.IntN(len(allowed))]
		uc.logger.Warnf("请求模型不在该 Key 的允许列表，按白名单随机分发 keyID=%d requestedModel=%s picked=%s",
			key.ID, requestedModel, picked)
		return picked, key.ProviderID, false, nil
	}
	providerModels, perr := uc.listEnabledProviderModelNames(ctx, key.ProviderID)
	if perr != nil {
		return "", 0, false, perr
	}
	if requestedModel != "" && containsString(providerModels, requestedModel) {
		return requestedModel, key.ProviderID, false, nil
	}
	if len(providerModels) == 0 {
		return requestedModel, key.ProviderID, false, nil
	}
	picked := providerModels[mrand.IntN(len(providerModels))]
	uc.logger.Warnf("请求模型不在提供方已启用模型列表，按提供方模型池随机分发 keyID=%d requestedModel=%s picked=%s",
		key.ID, requestedModel, picked)
	return picked, key.ProviderID, false, nil
}

func (uc *GatewayUseCase) resolveExactTargetModel(ctx context.Context, key *model.AIVirtualKey, requestedModel string) (string, uint, bool, error) {
	if requestedModel == "" {
		return "", 0, false, fmt.Errorf("请求体缺少 model 字段")
	}
	mapping, err := uc.resolveModelMapping(ctx, key.ID, key.ProviderID, requestedModel)
	if err != nil {
		return "", 0, false, err
	}
	allowed := allowedModelList(key)
	if mapping != nil {
		realName := mapping.RealModel.Name
		if len(allowed) > 0 && !containsString(allowed, realName) {
			return "", 0, false, fmt.Errorf("模型映射命中的真实模型\"%s\"不在该 Key 的允许模型列表中，访问被拒绝", realName)
		}
		return realName, mapping.RealModel.ProviderID, true, nil
	}
	if len(allowed) > 0 && !containsString(allowed, requestedModel) {
		return "", 0, false, fmt.Errorf("模型\"%s\"不在该 Key 的允许模型列表中，访问被拒绝", requestedModel)
	}
	return requestedModel, key.ProviderID, false, nil
}

func (uc *GatewayUseCase) resolveModelMapping(ctx context.Context, keyID, keyProviderID uint, requestedModel string) (*model.AIModelMapping, error) {
	if requestedModel == "" {
		return nil, nil
	}
	var mappings []model.AIModelMapping
	if err := uc.db.WithContext(ctx).
		Where("virtual_key_id = ? AND is_enabled = ?", keyID, true).
		Order("created_at asc, id asc").
		Preload("RealModel").
		Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("查询模型映射失败: %w", err)
	}
	if len(mappings) == 0 {
		return nil, nil
	}
	matched := matchModelMapping(mappings, requestedModel)
	if matched == nil {
		return nil, nil
	}
	if matched.RealModel == nil || matched.RealModel.ID == 0 {
		return nil, fmt.Errorf("模型映射配置异常：真实模型ID(%d)不存在，请联系管理员更新映射配置", matched.RealModelID)
	}
	if !matched.RealModel.IsEnabled {
		return nil, fmt.Errorf("模型映射配置异常：真实模型\"%s\"已被禁用，请联系管理员更新映射配置", matched.RealModel.Name)
	}
	if matched.RealModel.ProviderID != keyProviderID {
		uc.logger.Warnf("模型映射提供方与Key绑定提供方不一致，已拒绝转发keyID=%d mappingID=%d", keyID, matched.ID)
		return nil, fmt.Errorf("模型映射配置异常：真实模型\"%s\"所属提供方与该Key绑定的提供方不一致，访问被拒绝，请联系管理员更新映射配置", matched.RealModel.Name)
	}
	return matched, nil
}

func (uc *GatewayUseCase) listEnabledProviderModelNames(ctx context.Context, providerID uint) ([]string, error) {
	var models []model.AIModelItem
	if err := uc.db.WithContext(ctx).
		Where("provider_id = ? AND is_enabled = ?", providerID, true).
		Find(&models).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	return names, nil
}

// ListGatewayModels 返回虚拟Key可用的模型列表
func (uc *GatewayUseCase) ListGatewayModels(ctx context.Context, key *model.AIVirtualKey) ([]string, error) {
	var mappings []model.AIModelMapping
	if err := uc.db.WithContext(ctx).
		Where("virtual_key_id = ? AND is_enabled = ?", key.ID, true).
		Find(&mappings).Error; err != nil {
		return nil, err
	}
	if len(mappings) > 0 {
		names := make([]string, 0, len(mappings))
		for _, m := range mappings {
			names = append(names, m.VirtualModel)
		}
		return names, nil
	}
	allowed := allowedModelList(key)
	if len(allowed) > 0 {
		return allowed, nil
	}
	return uc.listEnabledProviderModelNames(ctx, key.ProviderID)
}

// =============================================================================
// 审计日志查询
// =============================================================================

// ListAuditLogs 分页查询审计日志
func (uc *GatewayUseCase) ListAuditLogs(ctx context.Context, req dto.ListAuditLogsReq) ([]model.AIGatewayAuditLog, int64, error) {
	var list []model.AIGatewayAuditLog
	db := uc.applyAuditLogFilters(
		uc.db.WithContext(ctx).Model(&model.AIGatewayAuditLog{}),
		req.AuditLogFilter,
	)
	if req.SessionID != "" {
		db = db.Where("COALESCE(NULLIF(session_id,''), CONCAT('log-', id)) = ?", req.SessionID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "created_at desc"
	if req.SessionID != "" {
		order = "created_at asc"
	}
	if err := db.Order(order).Limit(req.Limit()).Offset(req.Offset()).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	uc.fillBodiesFromDB(ctx, list)
	uc.fillFilesFromDB(ctx, list)
	return list, total, nil
}

// ListAuditSessions 审计日志按会话聚合分页查询
func (uc *GatewayUseCase) ListAuditSessions(ctx context.Context, req dto.ListAuditSessionsReq) ([]dto.AuditSessionSummary, int64, error) {
	filtered := func() *gorm.DB {
		return uc.applyAuditLogFilters(
			uc.db.WithContext(ctx).Model(&model.AIGatewayAuditLog{}),
			req.AuditLogFilter,
		)
	}

	var total int64
	if err := filtered().Select("COUNT(DISTINCT " + auditSessionExpr + ")").Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	selectAgg := auditSessionExpr + " AS session_id, " +
		"MIN(created_at) AS first_at, MAX(created_at) AS last_at, COUNT(*) AS req_count, " +
		"SUM(prompt_tokens) AS prompt_tokens, SUM(completion_tokens) AS completion_tokens, SUM(total_tokens) AS total_tokens, " +
		"SUM(points_consumed) AS points_consumed, SUM(price_consumed) AS price_consumed, " +
		"MAX(key_name) AS key_name, " +
		"MAX(client_agent) AS client_agent, MAX(protocol) AS protocol, MAX(model) AS model"

	var sessions []dto.AuditSessionSummary
	if err := filtered().
		Select(selectAgg).
		Group(auditSessionExpr).
		Order("last_at DESC").
		Limit(req.Limit()).Offset(req.Offset()).
		Scan(&sessions).Error; err != nil {
		return nil, 0, err
	}
	if len(sessions) == 0 {
		return sessions, total, nil
	}

	keys := make([]string, 0, len(sessions))
	for _, se := range sessions {
		keys = append(keys, se.SessionID)
	}
	sub := filtered().Select(auditSessionExpr + " AS session_id, status_code, " +
		"ROW_NUMBER() OVER (PARTITION BY " + auditSessionExpr + " ORDER BY created_at DESC, id DESC) AS rn")
	type sessFinalStatus struct {
		SessionID  string
		StatusCode int
	}
	var finals []sessFinalStatus
	if err := uc.db.WithContext(ctx).
		Table("(?) AS t", sub).
		Select("t.session_id, t.status_code").
		Where("t.rn = 1 AND t.session_id IN ?", keys).
		Scan(&finals).Error; err != nil {
		uc.logger.Warnf("回填会话最终状态失败，FinalStatusCode 返回 0 err=%v", err)
	}
	statusByID := make(map[string]int, len(finals))
	for _, f := range finals {
		statusByID[f.SessionID] = f.StatusCode
	}
	for i := range sessions {
		sessions[i].FinalStatusCode = statusByID[sessions[i].SessionID]
	}
	return sessions, total, nil
}

// SecurityOverview 审计页安全态势聚合
func (uc *GatewayUseCase) SecurityOverview(ctx context.Context, req dto.SecurityOverviewReq) (dto.SecurityOverviewResp, error) {
	resp := dto.SecurityOverviewResp{TopPIITypes: []dto.PIITypeRank{}, TopErrorModels: []dto.ModelErrorRank{}}
	topN := req.TopN
	if topN <= 0 {
		topN = 5
	}

	filtered := func() *gorm.DB {
		return uc.applyAuditLogFilters(
			uc.db.WithContext(ctx).Model(&model.AIGatewayAuditLog{}),
			req.AuditLogFilter,
		)
	}

	var counts struct {
		Total  int64
		Block  int64
		Redact int64
		Errors int64
	}
	if err := filtered().Select(
		"COUNT(*) AS total, " +
			"COALESCE(SUM(CASE WHEN pii_action = 'block' THEN 1 ELSE 0 END), 0) AS block, " +
			"COALESCE(SUM(CASE WHEN pii_action = 'redact' THEN 1 ELSE 0 END), 0) AS redact, " +
			"COALESCE(SUM(CASE WHEN status_code <> 200 THEN 1 ELSE 0 END), 0) AS errors",
	).Scan(&counts).Error; err != nil {
		return resp, err
	}
	resp.TotalRequests = counts.Total
	resp.BlockCount = counts.Block
	resp.RedactCount = counts.Redact
	resp.ErrorCount = counts.Errors
	if counts.Total > 0 {
		resp.ErrorRate = float64(counts.Errors) / float64(counts.Total)
	}

	var piiRows []struct{ PIITypes string }
	if err := filtered().Where("pii_types <> ''").Select("pii_types").Scan(&piiRows).Error; err != nil {
		return resp, err
	}
	typeCount := make(map[string]int64)
	for _, r := range piiRows {
		for _, t := range strings.Split(r.PIITypes, ",") {
			if t = strings.TrimSpace(t); t != "" {
				typeCount[t]++
			}
		}
	}
	ranks := make([]dto.PIITypeRank, 0, len(typeCount))
	for t, c := range typeCount {
		ranks = append(ranks, dto.PIITypeRank{Type: t, Count: c})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Count != ranks[j].Count {
			return ranks[i].Count > ranks[j].Count
		}
		return ranks[i].Type < ranks[j].Type
	})
	if len(ranks) > topN {
		ranks = ranks[:topN]
	}
	resp.TopPIITypes = ranks

	var errorModels []dto.ModelErrorRank
	if err := filtered().
		Where("status_code <> 200 AND model <> ''").
		Select("model, COUNT(*) AS error_count").
		Group("model").Order("error_count DESC").Limit(topN).
		Scan(&errorModels).Error; err != nil {
		return resp, err
	}
	resp.TopErrorModels = errorModels
	return resp, nil
}

const auditSessionExpr = "COALESCE(NULLIF(session_id,''), CONCAT('log-', id))"

func (uc *GatewayUseCase) applyAuditLogFilters(db *gorm.DB, f dto.AuditLogFilter) *gorm.DB {
	if f.VirtualKeyID > 0 {
		db = db.Where("virtual_key_id = ?", f.VirtualKeyID)
	}
	if f.ProviderID > 0 {
		db = db.Where("provider_id = ?", f.ProviderID)
	}
	if f.Model != "" {
		db = db.Where("model = ?", f.Model)
	}
	if f.Protocol != "" {
		db = db.Where("protocol = ?", f.Protocol)
	}
	if f.PIIAction != "" {
		if f.PIIAction == "none" {
			db = db.Where("pii_action IN ('none', '')")
		} else {
			db = db.Where("pii_action = ?", f.PIIAction)
		}
	}
	switch f.Status {
	case "success":
		db = db.Where("status_code = 200")
	case "error":
		db = db.Where("status_code <> 200")
	}
	if f.ClientAgent != "" {
		db = db.Where("client_agent LIKE ?", "%"+f.ClientAgent+"%")
	}
	if f.PIIBlocked != nil {
		db = db.Where("pii_blocked = ?", *f.PIIBlocked)
	}
	if f.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, f.StartTime); err == nil {
			db = db.Where("created_at >= ?", t)
		}
	}
	if f.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, f.EndTime); err == nil {
			db = db.Where("created_at <= ?", t)
		}
	}
	return db
}

func (uc *GatewayUseCase) fillBodiesFromDB(ctx context.Context, list []model.AIGatewayAuditLog) {
	if len(list) == 0 {
		return
	}
	ids := make([]uint, 0, len(list))
	idxByID := make(map[uint]int, len(list))
	for i, l := range list {
		ids = append(ids, l.ID)
		idxByID[l.ID] = i
	}
	var bodies []model.AIGatewayAuditLogBody
	if err := uc.db.WithContext(ctx).Where("audit_log_id IN ?", ids).Find(&bodies).Error; err != nil {
		uc.logger.Warnf("查询审计日志 body 失败，body 字段返回空 err=%v", err)
		return
	}
	for _, b := range bodies {
		if idx, ok := idxByID[b.AuditLogID]; ok {
			list[idx].RequestBody = b.RequestBody
			list[idx].ResponseBody = b.ResponseBody
		}
	}
}

func (uc *GatewayUseCase) fillFilesFromDB(ctx context.Context, list []model.AIGatewayAuditLog) {
	if len(list) == 0 {
		return
	}
	ids := make([]uint, 0, len(list))
	for _, l := range list {
		ids = append(ids, l.ID)
	}
	var files []model.AIGatewayAuditLogFile
	if err := uc.db.WithContext(ctx).
		Where("audit_log_id IN ?", ids).
		Order("audit_log_id asc, part_index asc").
		Find(&files).Error; err != nil {
		uc.logger.Warnf("查询审计日志文件清单失败 err=%v", err)
		return
	}
	m := make(map[uint][]model.AIGatewayAuditLogFile, len(ids))
	for _, f := range files {
		m[f.AuditLogID] = append(m[f.AuditLogID], f)
	}
	for i := range list {
		list[i].Files = m[list[i].ID]
	}
}

// =============================================================================
// 流式代理辅助函数
// =============================================================================

func streamProxy(w http.ResponseWriter, body io.Reader) (respBody []byte, promptTokens, completionTokens, cachedTokens int, streamErr string) {
	flusher, hasFlusher := w.(http.Flusher)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	var collected []byte
	for scanner.Scan() {
		line := scanner.Text()
		raw := line + "\n"
		w.Write([]byte(raw))
		if hasFlusher {
			flusher.Flush()
		}
		collected = append(collected, raw...)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		if streamErr == "" {
			if e := parseStreamChunkError(payload); e != "" {
				streamErr = e
			}
		}
		p, c, cached := parseUsageFromChunk([]byte(payload))
		if p > 0 || c > 0 {
			promptTokens = p
			completionTokens = c
			cachedTokens = cached
		}
	}
	return collected, promptTokens, completionTokens, cachedTokens, streamErr
}

func parseStreamChunkError(payload string) string {
	var chunk struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(payload), &chunk) == nil && len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return string(chunk.Error)
	}
	return ""
}

func parseUsageFromChunk(data []byte) (promptTokens, completionTokens, cachedTokens int) {
	var chunk struct {
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &chunk) == nil && chunk.Usage != nil {
		if chunk.Usage.PromptTokensDetails != nil {
			cachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		return chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, cachedTokens
	}
	return 0, 0, 0
}

func parseUsageFromBody(body []byte) (promptTokens, completionTokens, cachedTokens int) {
	var resp struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return 0, 0, 0
	}
	if resp.Usage.PromptTokensDetails != nil {
		cachedTokens = resp.Usage.PromptTokensDetails.CachedTokens
	}
	if resp.Usage.PromptTokens == 0 && resp.Usage.CompletionTokens == 0 && resp.Usage.TotalTokens > 0 {
		return resp.Usage.TotalTokens, 0, cachedTokens
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cachedTokens
}

func upstreamErrSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	const maxLen = 2048
	if len(s) > maxLen {
		return s[:maxLen] + "...(truncated)"
	}
	return s
}

// =============================================================================
// 请求体变换辅助函数
// =============================================================================

func extractModel(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &req)
	return strings.TrimSpace(req.Model)
}

func extractStreamFlag(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	json.Unmarshal(body, &req)
	return req.Stream
}

func replaceModelInBody(body []byte, realModel string) []byte {
	if realModel == "" {
		return body
	}
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	req["model"] = realModel
	modified, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return modified
}

func injectStreamUsageOption(body []byte, isStream bool) []byte {
	if !isStream {
		return body
	}
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	opts, _ := req["stream_options"].(map[string]interface{})
	if opts == nil {
		opts = make(map[string]interface{})
	}
	opts["include_usage"] = true
	req["stream_options"] = opts
	modified, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return modified
}

func injectPromptCacheKey(body []byte, sessionHash string) []byte {
	if sessionHash == "" {
		return body
	}
	var req map[string]interface{}
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	if v, ok := req["prompt_cache_key"]; ok {
		if s, _ := v.(string); s != "" {
			return body
		}
	}
	req["prompt_cache_key"] = sessionHash
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

var extraParamsReservedKeys = map[string]struct{}{
	"model":          {},
	"messages":       {},
	"input":          {},
	"stream":         {},
	"stream_options": {},
	"tools":          {},
	"tool_choice":    {},
}

func (uc *GatewayUseCase) injectModelExtraParams(ctx context.Context, body []byte, providerID uint, realModelName string) []byte {
	var m model.AIModelItem
	err := uc.db.WithContext(ctx).
		Select("extra_params").
		Where("provider_id = ? AND name = ?", providerID, realModelName).
		First(&m).Error
	if err != nil || len(m.ExtraParams) == 0 {
		return body
	}
	var extra map[string]interface{}
	if json.Unmarshal(m.ExtraParams, &extra) != nil || len(extra) == 0 {
		return body
	}
	var req map[string]interface{}
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	for k, v := range extra {
		if _, reserved := extraParamsReservedKeys[k]; reserved {
			continue
		}
		req[k] = v
	}
	modified, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return modified
}

func isExactModelEndpoint(path string) bool {
	return strings.HasSuffix(path, "/embeddings") || strings.HasSuffix(path, "/rerank")
}

func rewriteOpenAIPathForProvider(openAIPath string, provider model.AIProvider) string {
	path, query, _ := strings.Cut(openAIPath, "?")
	if path == "/rerank" && isDashScopeProvider(provider) {
		path = "/reranks"
	}
	if query != "" {
		return path + "?" + query
	}
	return path
}

func isDashScopeProvider(provider model.AIProvider) bool {
	baseURL := strings.ToLower(provider.BaseURL)
	name := strings.ToLower(provider.Name)
	return strings.Contains(baseURL, "dashscope.aliyuncs.com") ||
		strings.Contains(name, "dashscope") ||
		strings.Contains(name, "bailian") ||
		strings.Contains(name, "百炼")
}

func allowedModelList(key *model.AIVirtualKey) []string {
	if len(key.AllowedModels) == 0 || string(key.AllowedModels) == "null" {
		return nil
	}
	var allowed []string
	if err := json.Unmarshal(key.AllowedModels, &allowed); err != nil {
		return nil
	}
	return allowed
}

// StartBackgroundWorkers launches the key-cache invalidation listener and quota release
// sweeper. Called once during application start (from KratosServer.Start).
func (uc *GatewayUseCase) StartBackgroundWorkers(ctx context.Context) {
	go StartKeyCacheInvalidator(ctx, uc.rdb, uc.rawLog)
	go StartQuotaReleaseSweeper(ctx, uc.db, uc.rdb, uc.rawLog)
	uc.EnsureTenancyDefaults(ctx)
	if uc.billing != nil {
		uc.billing.Start(ctx)
	}
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

var mappingRegexCache sync.Map

// compiledMappingRegex returns a compiled regex for the virtual model pattern (whole-string anchored).
func compiledMappingRegex(virtualModel string) *regexp.Regexp {
	if v, ok := mappingRegexCache.Load(virtualModel); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile("^(?:" + virtualModel + ")$")
	if err != nil {
		mappingRegexCache.Store(virtualModel, (*regexp.Regexp)(nil))
		return nil
	}
	mappingRegexCache.Store(virtualModel, re)
	return re
}

// matchModelMapping matches mappings with exact first, then regex fallback.
func matchModelMapping(mappings []model.AIModelMapping, requestedModel string) *model.AIModelMapping {
	for i := range mappings {
		if mappings[i].VirtualModel == requestedModel {
			return &mappings[i]
		}
	}
	for i := range mappings {
		re := compiledMappingRegex(mappings[i].VirtualModel)
		if re == nil {
			continue
		}
		if re.MatchString(requestedModel) {
			return &mappings[i]
		}
	}
	return nil
}
