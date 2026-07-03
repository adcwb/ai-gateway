package biz

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

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
