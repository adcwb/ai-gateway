package biz

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/dto"
	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Sell-side pricing (docs/design/03-billing-and-monetization.md layer 1):
// AIModelItem prices are upstream COST; AIPriceTable holds what the operator
// charges tenants. A model missing from the bound table falls back to cost
// (margin zero) so billing can never silently undercharge to free.

var sellPriceCache sync.Map // "tableID:model" → *sellPriceEntry

type sellPriceEntry struct {
	entry     *modelPriceEntry
	expiresAt time.Time
}

const sellPriceTTL = 5 * time.Minute

// getRatePerCredit generalizes the CNY-only rate lookup to any currency.
func getRatePerCredit(ctx context.Context, db *gorm.DB, rdb *redis.Client, currency string) float64 {
	if currency == "" || currency == "CNY" {
		return getCNYRatePerCredit(ctx, db, rdb)
	}
	redisKey := "ai:gw:credits:rate:" + currency
	if rdb != nil {
		if val, err := rdb.Get(ctx, redisKey).Result(); err == nil {
			if rate, perr := strconv.ParseFloat(val, 64); perr == nil && rate > 0 {
				return rate
			}
		}
	}
	var record model.AICreditsRate
	if err := db.WithContext(ctx).
		Where("currency = ? AND is_enabled = ?", currency, true).
		First(&record).Error; err != nil || record.RatePerCredit <= 0 {
		return 0
	}
	if rdb != nil {
		rdb.Set(ctx, redisKey, strconv.FormatFloat(record.RatePerCredit, 'f', 6, 64), rateCacheTTL)
	}
	return record.RatePerCredit
}

// getSellPriceEntry resolves the sell price for a model under a price table.
// Exact pattern match wins, then regex patterns; nil table or no match falls
// back to the upstream cost entry.
func getSellPriceEntry(ctx context.Context, db *gorm.DB, logger *log.Helper,
	priceTableID *uint, providerID uint, modelName string) *modelPriceEntry {
	if priceTableID == nil || *priceTableID == 0 {
		return getModelPriceEntry(ctx, db, logger, providerID, modelName)
	}
	cacheKey := fmt.Sprintf("%d:%s", *priceTableID, modelName)
	if v, ok := sellPriceCache.Load(cacheKey); ok {
		e := v.(*sellPriceEntry)
		if time.Now().Before(e.expiresAt) {
			return e.entry
		}
	}

	var items []model.AIPriceTableItem
	if err := db.WithContext(ctx).Where("price_table_id = ?", *priceTableID).Find(&items).Error; err != nil {
		return getModelPriceEntry(ctx, db, logger, providerID, modelName)
	}
	var matched *model.AIPriceTableItem
	for i := range items { // exact first
		if items[i].ModelPattern == modelName {
			matched = &items[i]
			break
		}
	}
	if matched == nil {
		for i := range items { // then regex
			re, err := regexp.Compile(items[i].ModelPattern)
			if err == nil && re.MatchString(modelName) {
				matched = &items[i]
				break
			}
		}
	}

	var entry *modelPriceEntry
	if matched != nil {
		entry = &modelPriceEntry{
			inputPrice:     matched.InputPricePerMillion,
			outputPrice:    matched.OutputPricePerMillion,
			cacheReadPrice: matched.CacheReadPerMillion,
			noPricing:      matched.InputPricePerMillion == 0 && matched.OutputPricePerMillion == 0,
			expiresAt:      time.Now().Add(sellPriceTTL),
		}
	} else {
		entry = getModelPriceEntry(ctx, db, logger, providerID, modelName)
	}
	sellPriceCache.Store(cacheKey, &sellPriceEntry{entry: entry, expiresAt: time.Now().Add(sellPriceTTL)})
	return entry
}

// invalidateSellPriceCache drops every cached entry for a table: an item's
// pattern can match model names that were never individually resolved yet,
// so a targeted key delete can't be computed — sweep instead (cache is small:
// bounded by distinct models actually seen per table).
func invalidateSellPriceCache(priceTableID uint) {
	prefix := fmt.Sprintf("%d:", priceTableID)
	sellPriceCache.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, prefix) {
			sellPriceCache.Delete(k)
		}
		return true
	})
}

// -----------------------------------------------------------------------------
// Price table management (docs/design/08-web-console.md module 4)
// -----------------------------------------------------------------------------

// CreatePriceTable creates a named sell-side price table.
func (uc *GatewayUseCase) CreatePriceTable(ctx context.Context, req *dto.CreatePriceTableReq) (*model.AIPriceTable, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, ErrPriceTableInvalid
	}
	currency := req.Currency
	if currency == "" {
		currency = "CNY"
	}
	t := &model.AIPriceTable{Name: strings.TrimSpace(req.Name), Currency: currency}
	if err := uc.db.WithContext(ctx).Create(t).Error; err != nil {
		return nil, ErrPriceTableExists
	}
	return t, nil
}

// ListPriceTables returns all price tables with their items populated.
func (uc *GatewayUseCase) ListPriceTables(ctx context.Context) ([]model.AIPriceTable, error) {
	var tables []model.AIPriceTable
	if err := uc.db.WithContext(ctx).Order("id asc").Find(&tables).Error; err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return tables, nil
	}
	ids := make([]uint, len(tables))
	for i, t := range tables {
		ids[i] = t.ID
	}
	var items []model.AIPriceTableItem
	if err := uc.db.WithContext(ctx).Where("price_table_id IN ?", ids).Order("id asc").Find(&items).Error; err != nil {
		return nil, err
	}
	byTable := map[uint][]model.AIPriceTableItem{}
	for _, it := range items {
		byTable[it.PriceTableID] = append(byTable[it.PriceTableID], it)
	}
	for i := range tables {
		tables[i].Items = byTable[tables[i].ID]
	}
	return tables, nil
}

// UpdatePriceTable applies a partial update to a price table.
func (uc *GatewayUseCase) UpdatePriceTable(ctx context.Context, req *dto.UpdatePriceTableReq) (*model.AIPriceTable, error) {
	var t model.AIPriceTable
	if err := uc.db.WithContext(ctx).First(&t, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPriceTableNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Currency != nil {
		updates["currency"] = *req.Currency
	}
	if len(updates) == 0 {
		return &t, nil
	}
	if err := uc.db.WithContext(ctx).Model(&t).Updates(updates).Error; err != nil {
		return nil, err
	}
	invalidateSellPriceCache(t.ID)
	return &t, nil
}

// DeletePriceTable soft-deletes a price table (its items are left orphaned;
// accounts bound to it fall back to cost per the Layer-1 design).
func (uc *GatewayUseCase) DeletePriceTable(ctx context.Context, id uint) error {
	res := uc.db.WithContext(ctx).Delete(&model.AIPriceTable{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPriceTableNotFound
	}
	invalidateSellPriceCache(id)
	return nil
}

// CreatePriceTableItem adds a model-pattern price row to a table.
func (uc *GatewayUseCase) CreatePriceTableItem(ctx context.Context, req *dto.CreatePriceTableItemReq) (*model.AIPriceTableItem, error) {
	if req.PriceTableID == 0 || strings.TrimSpace(req.ModelPattern) == "" {
		return nil, ErrPriceItemInvalid
	}
	item := &model.AIPriceTableItem{
		PriceTableID:          req.PriceTableID,
		ModelPattern:          strings.TrimSpace(req.ModelPattern),
		InputPricePerMillion:  req.InputPricePerMillion,
		OutputPricePerMillion: req.OutputPricePerMillion,
		CacheReadPerMillion:   req.CacheReadPerMillion,
	}
	if err := uc.db.WithContext(ctx).Create(item).Error; err != nil {
		return nil, err
	}
	invalidateSellPriceCache(req.PriceTableID)
	return item, nil
}

// UpdatePriceTableItem applies a partial update to a price table item.
func (uc *GatewayUseCase) UpdatePriceTableItem(ctx context.Context, req *dto.UpdatePriceTableItemReq) (*model.AIPriceTableItem, error) {
	var item model.AIPriceTableItem
	if err := uc.db.WithContext(ctx).First(&item, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPriceItemNotFound
		}
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.ModelPattern != nil {
		updates["model_pattern"] = strings.TrimSpace(*req.ModelPattern)
	}
	if req.InputPricePerMillion != nil {
		updates["input_price_per_million"] = *req.InputPricePerMillion
	}
	if req.OutputPricePerMillion != nil {
		updates["output_price_per_million"] = *req.OutputPricePerMillion
	}
	if req.CacheReadPerMillion != nil {
		updates["cache_read_per_million"] = *req.CacheReadPerMillion
	}
	if len(updates) > 0 {
		if err := uc.db.WithContext(ctx).Model(&item).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	invalidateSellPriceCache(item.PriceTableID)
	return &item, nil
}

// DeletePriceTableItem removes one price table item.
func (uc *GatewayUseCase) DeletePriceTableItem(ctx context.Context, id uint) error {
	var item model.AIPriceTableItem
	if err := uc.db.WithContext(ctx).First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPriceItemNotFound
		}
		return err
	}
	if err := uc.db.WithContext(ctx).Delete(&model.AIPriceTableItem{}, id).Error; err != nil {
		return err
	}
	invalidateSellPriceCache(item.PriceTableID)
	return nil
}

// TestPattern reports which of the given known model names an exact-or-regex
// pattern matches, using the same whole-string-anchored semantics as model
// mappings (compiledMappingRegex) and price-table item resolution above.
func TestPattern(req *dto.PatternTestReq) dto.PatternTestResp {
	matched := make([]string, 0, len(req.Models))
	isRegex := false
	for _, m := range req.Models {
		if m == req.Pattern {
			matched = append(matched, m)
			continue
		}
	}
	if len(matched) == 0 {
		if re := compiledMappingRegex(req.Pattern); re != nil {
			isRegex = req.Pattern != regexp.QuoteMeta(req.Pattern)
			for _, m := range req.Models {
				if re.MatchString(m) {
					matched = append(matched, m)
				}
			}
		}
	}
	return dto.PatternTestResp{Matched: matched, IsRegex: isRegex}
}
