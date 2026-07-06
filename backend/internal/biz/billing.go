package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/observability"
)

// BillingManager implements the P1 commercial loop
// (docs/design/03-billing-and-monetization.md): per-tenant balance accounts,
// an append-only double-entry ledger, a Redis freeze→settle gate on the proxy
// path, grace-period suspension, budget alerts and daily usage aggregation.
//
// Billing is opt-in: no enabled account for a tenant ⇒ the gateway behaves
// exactly as before. If Redis billing state is unavailable the gate fails
// OPEN (traffic passes, reconciled later) per design principle 6.
type BillingManager struct {
	db      *gorm.DB
	rdb     *redis.Client
	metrics *observability.Metrics
	sysCfg  *conf.System
	logger  *log.Helper

	ledgerQ chan ledgerTask
	usageQ  chan usageTask

	acctCache sync.Map // tenantID → acctCacheEntry
}

func NewBillingManager(db *gorm.DB, rdb *redis.Client, metrics *observability.Metrics, sysCfg *conf.System, logger log.Logger) *BillingManager {
	return &BillingManager{
		db:      db,
		rdb:     rdb,
		metrics: metrics,
		sysCfg:  sysCfg,
		logger:  log.NewHelper(logger),
		ledgerQ: make(chan ledgerTask, 4096),
		usageQ:  make(chan usageTask, 4096),
	}
}

const (
	billBalanceKeyFmt = "ai:gw:bill:bal:%d"   // headroom = balance + credit_limit (micro)
	billAlertKeyFmt   = "ai:gw:bill:alert:%d" // budget-alert dedup
	billResyncEvery   = 60 * time.Second
	billAcctCacheTTL  = 2 * time.Second
	billFreezeFloor   = 1024 // min estimated completion tokens when max_tokens absent
)

type acctCacheEntry struct {
	acct      *model.AIBillingAccount // nil = no enabled account (billing off)
	expiresAt time.Time
}

type ledgerTask struct {
	accountID   uint
	entryType   string
	amountMicro int64
	idemKey     string
	refType     string
	refID       string
	remark      string
}

type usageTask struct {
	day        string
	tenantID   uint
	keyID      uint
	providerID uint
	model      string
	prompt     int64
	completion int64
	cacheRead  int64
	costMicro  int64
	priceMicro int64
	cacheHit   bool
}

// Start launches the async ledger/usage workers and the balance resync loop.
func (bm *BillingManager) Start(ctx context.Context) {
	go bm.ledgerWorker(ctx)
	go bm.usageWorker(ctx)
	go bm.resyncLoop(ctx)
}

// -----------------------------------------------------------------------------
// Admission + freeze → settle (hot path)
// -----------------------------------------------------------------------------

// FreezeHandle carries an in-flight freeze from admission to settlement.
type FreezeHandle struct {
	Account  *model.AIBillingAccount
	EstMicro int64
}

// freezeScript atomically checks headroom and reserves the estimate.
// Returns 1 = frozen, 0 = insufficient, -1 = no balance key (resync pending ⇒ fail open).
var freezeScript = redis.NewScript(`
local bal = redis.call('GET', KEYS[1])
if not bal then return -1 end
bal = tonumber(bal)
local est = tonumber(ARGV[1])
if bal < est then return 0 end
redis.call('DECRBY', KEYS[1], est)
return 1
`)

// AccountForTenant returns the enabled billing account for a tenant, briefly
// cached; nil means billing is disabled for this tenant.
func (bm *BillingManager) AccountForTenant(ctx context.Context, tenantID uint) *model.AIBillingAccount {
	if tenantID == 0 {
		return nil
	}
	if v, ok := bm.acctCache.Load(tenantID); ok {
		e := v.(acctCacheEntry)
		if time.Now().Before(e.expiresAt) {
			return e.acct
		}
	}
	var acct model.AIBillingAccount
	err := bm.db.WithContext(ctx).Where("tenant_id = ? AND is_enabled = ?", tenantID, true).First(&acct).Error
	var out *model.AIBillingAccount
	if err == nil {
		out = &acct
	}
	bm.acctCache.Store(tenantID, acctCacheEntry{acct: out, expiresAt: time.Now().Add(billAcctCacheTTL)})
	return out
}

// Admit gates a request: suspended (or grace-expired) accounts are rejected,
// then the price estimate is frozen against the Redis headroom mirror.
// Returns (nil, nil) when billing is disabled for the tenant.
func (bm *BillingManager) Admit(ctx context.Context, tenantID uint, providerID uint, modelName string, body []byte) (*FreezeHandle, error) {
	acct := bm.AccountForTenant(ctx, tenantID)
	if acct == nil {
		return nil, nil
	}

	switch acct.Status {
	case model.BillingStatusSuspended:
		return nil, ErrBillingSuspended
	case model.BillingStatusGrace:
		if acct.GraceUntil != nil && time.Now().After(*acct.GraceUntil) {
			bm.setStatus(ctx, acct, model.BillingStatusSuspended, nil)
			return nil, ErrBillingSuspended
		}
		// in grace: admit without freezing further debt enforcement beyond headroom
	}

	est := bm.estimateMicro(ctx, acct, providerID, modelName, body)
	if est <= 0 {
		return &FreezeHandle{Account: acct, EstMicro: 0}, nil // nothing priceable to freeze
	}
	if bm.rdb == nil {
		return &FreezeHandle{Account: acct, EstMicro: 0}, nil // fail open
	}
	res, err := freezeScript.Run(ctx, bm.rdb, []string{fmt.Sprintf(billBalanceKeyFmt, acct.ID)}, est).Int()
	if err != nil {
		bm.logger.Warnf("billing: freeze Redis 不可用，失败开放 acct=%d err=%v", acct.ID, err)
		return &FreezeHandle{Account: acct, EstMicro: 0}, nil
	}
	switch res {
	case 0:
		if bm.metrics != nil {
			bm.metrics.BillingRejections.WithLabelValues("insufficient_balance").Inc()
		}
		return nil, ErrInsufficientBalance
	case -1:
		// balance mirror missing: seed it from DB and admit this request
		bm.seedBalance(ctx, acct)
		return &FreezeHandle{Account: acct, EstMicro: 0}, nil
	default:
		return &FreezeHandle{Account: acct, EstMicro: est}, nil
	}
}

// Settle releases the freeze delta and enqueues the ledger deduction.
// actualMicro may be zero (free request / no pricing).
func (bm *BillingManager) Settle(ctx context.Context, h *FreezeHandle, requestID string, actualMicro int64, refID, remark string) {
	if h == nil || h.Account == nil {
		return
	}
	if bm.rdb != nil && (h.EstMicro != 0 || actualMicro != 0) {
		delta := h.EstMicro - actualMicro // refund over-freeze (or charge under-freeze)
		if delta != 0 {
			bm.rdb.IncrBy(ctx, fmt.Sprintf(billBalanceKeyFmt, h.Account.ID), delta)
		}
	}
	if actualMicro <= 0 {
		return
	}
	select {
	case bm.ledgerQ <- ledgerTask{
		accountID:   h.Account.ID,
		entryType:   model.LedgerEntryDeduct,
		amountMicro: -actualMicro,
		idemKey:     "req:" + requestID,
		refType:     "audit_log",
		refID:       refID,
		remark:      remark,
	}:
	default:
		bm.logger.Errorf("billing: ledger 队列已满，丢弃结算 acct=%d req=%s micro=%d — 等待对账修复", h.Account.ID, requestID, actualMicro)
	}
}

// ReleaseFreeze refunds an unused freeze (request failed before any charge).
func (bm *BillingManager) ReleaseFreeze(ctx context.Context, h *FreezeHandle) {
	if h == nil || h.Account == nil || h.EstMicro == 0 || bm.rdb == nil {
		return
	}
	bm.rdb.IncrBy(ctx, fmt.Sprintf(billBalanceKeyFmt, h.Account.ID), h.EstMicro)
}

// PriceMicro computes the sell-side micro-credit price of a usage triple for
// an account (its price table + currency rate).
func (bm *BillingManager) PriceMicro(ctx context.Context, acct *model.AIBillingAccount, providerID uint, modelName string, prompt, completion, cacheRead int) int64 {
	if acct == nil {
		return 0
	}
	entry := getSellPriceEntry(ctx, bm.db, bm.logger, acct.PriceTableID, providerID, modelName)
	rate := getRatePerCredit(ctx, bm.db, bm.rdb, acct.Currency)
	_, micro, _ := calcCredits(entry, prompt, completion, cacheRead, rate)
	return micro
}

// CostMicro computes the upstream cost in micro-credits (for margin reports).
func (bm *BillingManager) CostMicro(ctx context.Context, currency string, providerID uint, modelName string, prompt, completion, cacheRead int) int64 {
	entry := getModelPriceEntry(ctx, bm.db, bm.logger, providerID, modelName)
	rate := getRatePerCredit(ctx, bm.db, bm.rdb, currency)
	_, micro, _ := calcCredits(entry, prompt, completion, cacheRead, rate)
	return micro
}

// estimateMicro prices a worst-case guess: prompt ≈ len(body)/4 tokens,
// completion = max_tokens (or a floor). Over-freeze is refunded at settle.
func (bm *BillingManager) estimateMicro(ctx context.Context, acct *model.AIBillingAccount, providerID uint, modelName string, body []byte) int64 {
	maxTokens := extractMaxTokens(body)
	if maxTokens <= 0 {
		maxTokens = billFreezeFloor
	}
	promptEst := len(body) / 4
	return bm.PriceMicro(ctx, acct, providerID, modelName, promptEst, maxTokens, 0)
}

func extractMaxTokens(body []byte) int {
	var probe struct {
		MaxTokens           int `json:"max_tokens"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0
	}
	if probe.MaxCompletionTokens > 0 {
		return probe.MaxCompletionTokens
	}
	return probe.MaxTokens
}

// -----------------------------------------------------------------------------
// Ledger worker (MySQL is the source of truth; Redis is the gate)
// -----------------------------------------------------------------------------

func (bm *BillingManager) ledgerWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-bm.ledgerQ:
			bm.applyLedger(task)
		}
	}
}

// applyLedger writes one ledger row + balance update in a transaction,
// idempotent on idemKey, then evaluates suspension and budget alerts.
func (bm *BillingManager) applyLedger(task ledgerTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var after *model.AIBillingAccount
	err := bm.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var acct model.AIBillingAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acct, task.accountID).Error; err != nil {
			return err
		}
		var dup int64
		tx.Model(&model.AIBillingLedger{}).Where("idempotency_key = ?", task.idemKey).Count(&dup)
		if dup > 0 {
			return nil // replay: already applied
		}
		newBalance := acct.BalanceMicro + task.amountMicro
		entry := &model.AIBillingLedger{
			AccountID:         acct.ID,
			EntryType:         task.entryType,
			AmountMicro:       task.amountMicro,
			BalanceAfterMicro: newBalance,
			IdempotencyKey:    task.idemKey,
			RefType:           task.refType,
			RefID:             task.refID,
			Remark:            task.remark,
		}
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		if err := tx.Model(&acct).Update("balance_micro", newBalance).Error; err != nil {
			return err
		}
		acct.BalanceMicro = newBalance
		after = &acct
		return nil
	})
	if err != nil {
		bm.logger.Errorf("billing: ledger 落库失败 acct=%d idem=%s err=%v", task.accountID, task.idemKey, err)
		return
	}
	if after != nil {
		bm.postBalanceChecks(ctx, after)
	}
}

// postBalanceChecks handles suspension transitions and budget alerts after a
// balance change (docs/design/03-billing-and-monetization.md suspension policy).
func (bm *BillingManager) postBalanceChecks(ctx context.Context, acct *model.AIBillingAccount) {
	headroom := acct.BalanceMicro + acct.CreditLimitMicro

	switch {
	case headroom <= 0 && acct.Status == model.BillingStatusActive:
		if acct.GraceHours > 0 {
			until := time.Now().Add(time.Duration(acct.GraceHours) * time.Hour)
			bm.setStatus(ctx, acct, model.BillingStatusGrace, &until)
			bm.logger.Warnf("billing: 账户进入宽限期 acct=%d tenant=%d until=%s", acct.ID, acct.TenantID, until.Format(time.RFC3339))
		} else {
			bm.setStatus(ctx, acct, model.BillingStatusSuspended, nil)
			bm.logger.Warnf("billing: 账户已欠费停用 acct=%d tenant=%d", acct.ID, acct.TenantID)
		}
		bm.sendAlert("account_"+acct.Status, acct)
	case headroom > 0 && acct.Status != model.BillingStatusActive:
		bm.setStatus(ctx, acct, model.BillingStatusActive, nil)
		bm.logger.Infof("billing: 账户恢复 active acct=%d tenant=%d", acct.ID, acct.TenantID)
	}

	if acct.LowWatermarkMicro > 0 && acct.BalanceMicro < acct.LowWatermarkMicro && bm.rdb != nil {
		ok, _ := bm.rdb.SetNX(ctx, fmt.Sprintf(billAlertKeyFmt, acct.ID), "1", time.Hour).Result()
		if ok {
			bm.logger.Warnf("billing: 预算告警 acct=%d tenant=%d balance=%.4f < watermark=%.4f (credits)",
				acct.ID, acct.TenantID,
				float64(acct.BalanceMicro)/model.MicroCreditScale,
				float64(acct.LowWatermarkMicro)/model.MicroCreditScale)
			if bm.metrics != nil {
				bm.metrics.BillingRejections.WithLabelValues("budget_alert").Inc()
			}
			bm.sendAlert("budget_alert", acct)
		}
	}
}

func (bm *BillingManager) setStatus(ctx context.Context, acct *model.AIBillingAccount, status string, graceUntil *time.Time) {
	updates := map[string]interface{}{"status": status, "grace_until": graceUntil}
	if err := bm.db.WithContext(ctx).Model(&model.AIBillingAccount{}).Where("id = ?", acct.ID).Updates(updates).Error; err != nil {
		bm.logger.Errorf("billing: 更新账户状态失败 acct=%d err=%v", acct.ID, err)
		return
	}
	acct.Status = status
	acct.GraceUntil = graceUntil
	bm.acctCache.Delete(acct.TenantID)
}

// -----------------------------------------------------------------------------
// Recharge & account management (management API)
// -----------------------------------------------------------------------------

// Recharge credits an account synchronously (manual/admin recharge; payment
// gateways enqueue through the same path with order-number idempotency keys).
func (bm *BillingManager) Recharge(ctx context.Context, tenantID uint, credits float64, idemKey, refType, remark string) (*model.AIBillingAccount, error) {
	if credits <= 0 {
		return nil, ErrBillingInvalidAmount
	}
	var acct model.AIBillingAccount
	if err := bm.db.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&acct).Error; err != nil {
		return nil, ErrBillingAccountNotFound
	}
	if idemKey == "" {
		idemKey = fmt.Sprintf("recharge:%d:%d", tenantID, time.Now().UnixNano())
	}
	bm.applyLedger(ledgerTask{
		accountID:   acct.ID,
		entryType:   model.LedgerEntryRecharge,
		amountMicro: int64(math.Round(credits * model.MicroCreditScale)),
		idemKey:     idemKey,
		refType:     refType,
		remark:      remark,
	})
	bm.db.WithContext(ctx).First(&acct, acct.ID)
	bm.acctCache.Delete(tenantID)
	bm.seedBalance(ctx, &acct)
	return &acct, nil
}

// UpdateAccount applies operator changes (enable, mode, limits, watermark, price table).
func (bm *BillingManager) UpdateAccount(ctx context.Context, tenantID uint, updates map[string]interface{}) (*model.AIBillingAccount, error) {
	var acct model.AIBillingAccount
	if err := bm.db.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&acct).Error; err != nil {
		return nil, ErrBillingAccountNotFound
	}
	if len(updates) > 0 {
		if err := bm.db.WithContext(ctx).Model(&acct).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	bm.db.WithContext(ctx).First(&acct, acct.ID)
	bm.acctCache.Delete(tenantID)
	bm.seedBalance(ctx, &acct)
	return &acct, nil
}

// ListLedger pages an account's ledger, newest first.
func (bm *BillingManager) ListLedger(ctx context.Context, tenantID uint, page, pageSize int) ([]model.AIBillingLedger, int64, error) {
	var acct model.AIBillingAccount
	if err := bm.db.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&acct).Error; err != nil {
		return nil, 0, ErrBillingAccountNotFound
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var total int64
	bm.db.WithContext(ctx).Model(&model.AIBillingLedger{}).Where("account_id = ?", acct.ID).Count(&total)
	var rows []model.AIBillingLedger
	err := bm.db.WithContext(ctx).Where("account_id = ?", acct.ID).
		Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// -----------------------------------------------------------------------------
// Balance mirror maintenance
// -----------------------------------------------------------------------------

// seedBalance writes the authoritative headroom into the Redis gate.
func (bm *BillingManager) seedBalance(ctx context.Context, acct *model.AIBillingAccount) {
	if bm.rdb == nil {
		return
	}
	headroom := acct.BalanceMicro + acct.CreditLimitMicro
	bm.rdb.Set(ctx, fmt.Sprintf(billBalanceKeyFmt, acct.ID), strconv.FormatInt(headroom, 10), 0)
}

// resyncLoop periodically restores the Redis headroom mirror from MySQL,
// bounding any drift from crashes between freeze and settle.
func (bm *BillingManager) resyncLoop(ctx context.Context) {
	ticker := time.NewTicker(billResyncEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var accounts []model.AIBillingAccount
			if err := bm.db.WithContext(ctx).Where("is_enabled = ?", true).Find(&accounts).Error; err != nil {
				continue
			}
			for i := range accounts {
				bm.seedBalance(ctx, &accounts[i])
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Usage aggregation (P1-5)
// -----------------------------------------------------------------------------

// RecordUsage enqueues one request's attribution into the daily rollup.
func (bm *BillingManager) RecordUsage(tenantID, keyID, providerID uint, modelName string, prompt, completion, cacheRead int, costMicro, priceMicro int64, cacheHit bool) {
	task := usageTask{
		day:        time.Now().Format("2006-01-02"),
		tenantID:   tenantID,
		keyID:      keyID,
		providerID: providerID,
		model:      modelName,
		prompt:     int64(prompt),
		completion: int64(completion),
		cacheRead:  int64(cacheRead),
		costMicro:  costMicro,
		priceMicro: priceMicro,
		cacheHit:   cacheHit,
	}
	select {
	case bm.usageQ <- task:
	default: // rollup is best-effort; never block the hot path
	}
}

func (bm *BillingManager) usageWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-bm.usageQ:
			bm.upsertUsage(t)
		}
	}
}

func (bm *BillingManager) upsertUsage(t usageTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cacheHits := int64(0)
	if t.cacheHit {
		cacheHits = 1
	}
	row := &model.AIUsageDaily{
		Day: t.day, TenantID: t.tenantID, KeyID: t.keyID, ProviderID: t.providerID, Model: t.model,
		Requests: 1, PromptTokens: t.prompt, CompletionTokens: t.completion, CacheReadTokens: t.cacheRead,
		CostMicro: t.costMicro, PriceMicro: t.priceMicro, CacheHits: cacheHits,
	}
	err := bm.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}, {Name: "tenant_id"}, {Name: "key_id"}, {Name: "provider_id"}, {Name: "model"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"requests":          gorm.Expr("requests + 1"),
			"prompt_tokens":     gorm.Expr("prompt_tokens + ?", t.prompt),
			"completion_tokens": gorm.Expr("completion_tokens + ?", t.completion),
			"cache_read_tokens": gorm.Expr("cache_read_tokens + ?", t.cacheRead),
			"cost_micro":        gorm.Expr("cost_micro + ?", t.costMicro),
			"price_micro":       gorm.Expr("price_micro + ?", t.priceMicro),
			"cache_hits":        gorm.Expr("cache_hits + ?", cacheHits),
		}),
	}).Create(row).Error
	if err != nil {
		bm.logger.Warnf("billing: 用量日聚合失败 day=%s key=%d err=%v", t.day, t.keyID, err)
	}
}

// UsageOverview aggregates the last N days for the console dashboard.
func (bm *BillingManager) UsageOverview(ctx context.Context, tenantID uint, days int) (map[string]interface{}, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
	q := bm.db.WithContext(ctx).Model(&model.AIUsageDaily{}).Where("day >= ?", since)
	if tenantID > 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var totals struct {
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		CostMicro        int64
		PriceMicro       int64
		CacheHits        int64
	}
	if err := q.Select("COALESCE(SUM(requests),0) as requests, COALESCE(SUM(prompt_tokens),0) as prompt_tokens, COALESCE(SUM(completion_tokens),0) as completion_tokens, COALESCE(SUM(cost_micro),0) as cost_micro, COALESCE(SUM(price_micro),0) as price_micro, COALESCE(SUM(cache_hits),0) as cache_hits").
		Scan(&totals).Error; err != nil {
		return nil, err
	}

	type modelRow struct {
		Model      string `json:"model"`
		Requests   int64  `json:"requests"`
		PriceMicro int64  `json:"priceMicro"`
	}
	var topModels []modelRow
	mq := bm.db.WithContext(ctx).Model(&model.AIUsageDaily{}).Where("day >= ?", since)
	if tenantID > 0 {
		mq = mq.Where("tenant_id = ?", tenantID)
	}
	mq.Select("model, SUM(requests) as requests, SUM(price_micro) as price_micro").
		Group("model").Order("requests desc").Limit(10).Scan(&topModels)

	return map[string]interface{}{
		"days":             days,
		"requests":         totals.Requests,
		"promptTokens":     totals.PromptTokens,
		"completionTokens": totals.CompletionTokens,
		"costCredits":      float64(totals.CostMicro) / model.MicroCreditScale,
		"priceCredits":     float64(totals.PriceMicro) / model.MicroCreditScale,
		"cacheHits":        totals.CacheHits,
		"topModels":        topModels,
	}, nil
}

// UsageTimeseries returns per-day rows for charts.
func (bm *BillingManager) UsageTimeseries(ctx context.Context, tenantID uint, days int) ([]map[string]interface{}, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
	q := bm.db.WithContext(ctx).Model(&model.AIUsageDaily{}).Where("day >= ?", since)
	if tenantID > 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	type row struct {
		Day              string
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		CostMicro        int64
		PriceMicro       int64
	}
	var rows []row
	if err := q.Select("day, SUM(requests) as requests, SUM(prompt_tokens) as prompt_tokens, SUM(completion_tokens) as completion_tokens, SUM(cost_micro) as cost_micro, SUM(price_micro) as price_micro").
		Group("day").Order("day asc").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]interface{}{
			"day":              r.Day,
			"requests":         r.Requests,
			"promptTokens":     r.PromptTokens,
			"completionTokens": r.CompletionTokens,
			"costCredits":      float64(r.CostMicro) / model.MicroCreditScale,
			"priceCredits":     float64(r.PriceMicro) / model.MicroCreditScale,
		})
	}
	return out, nil
}

// billingErrorBody renders the OpenAI-style error for 402 responses.
func billingErrorBody(msg string) string {
	return `{"error":{"message":"` + strings.ReplaceAll(msg, `"`, `'`) + `","type":"billing_error","code":"BILLING_REJECTED"}}`
}

// sendAlert POSTs a billing event to the configured webhook (best-effort,
// async, 5s timeout). Event types: budget_alert / account_grace / account_suspended.
func (bm *BillingManager) sendAlert(eventType string, acct *model.AIBillingAccount) {
	if bm.sysCfg == nil || strings.TrimSpace(bm.sysCfg.AlertWebhook) == "" {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type":           eventType,
		"tenantId":       acct.TenantID,
		"accountId":      acct.ID,
		"balanceCredits": float64(acct.BalanceMicro) / model.MicroCreditScale,
		"status":         acct.Status,
		"occurredAt":     time.Now().Format(time.RFC3339),
	})
	url := bm.sysCfg.AlertWebhook
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			bm.logger.Warnf("billing: 告警 webhook 投递失败 type=%s err=%v", eventType, err)
			return
		}
		resp.Body.Close()
	}()
}
