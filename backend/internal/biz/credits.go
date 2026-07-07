package biz

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

type modelPriceEntry struct {
	inputPrice      float64
	outputPrice     float64
	cacheReadPrice  float64
	cacheWritePrice float64
	noPricing       bool
	expiresAt       time.Time
}

var modelPriceCache sync.Map

const (
	modelPriceTTL    = 5 * time.Minute
	rateCacheTTL     = 10 * time.Minute
	microCreditScale = 1_000_000
)

func getModelPriceEntry(ctx context.Context, db *gorm.DB, logger *log.Helper, providerID uint, modelName string) *modelPriceEntry {
	key := fmt.Sprintf("%d:%s", providerID, modelName)

	if v, ok := modelPriceCache.Load(key); ok {
		entry := v.(*modelPriceEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry
		}
	}

	var m model.AIModelItem
	err := db.WithContext(ctx).
		Select("input_price_per_million, output_price_per_million, cache_read_price_per_million, cache_write_price_per_million").
		Where("provider_id = ? AND name = ?", providerID, modelName).
		First(&m).Error
	if err != nil {
		logger.Debugf("credits: model price not found provider=%d model=%s err=%v", providerID, modelName, err)
		entry := &modelPriceEntry{noPricing: true, expiresAt: time.Now().Add(modelPriceTTL)}
		modelPriceCache.Store(key, entry)
		return entry
	}

	entry := &modelPriceEntry{
		inputPrice:      m.InputPricePerMillion,
		outputPrice:     m.OutputPricePerMillion,
		cacheReadPrice:  m.CacheReadPricePerMillion,
		cacheWritePrice: m.CacheWritePricePerMillion,
		noPricing:       m.InputPricePerMillion == 0,
		expiresAt:       time.Now().Add(modelPriceTTL),
	}
	modelPriceCache.Store(key, entry)
	return entry
}

// invalidateModelPriceCache drops the cached cost entry so a console edit to
// AIModelItem pricing takes effect immediately rather than waiting out modelPriceTTL.
func invalidateModelPriceCache(providerID uint, modelName string) {
	modelPriceCache.Delete(fmt.Sprintf("%d:%s", providerID, modelName))
}

func getCNYRatePerCredit(ctx context.Context, db *gorm.DB, rdb *redis.Client) float64 {
	const redisKey = "ai:gw:credits:rate:CNY"

	if rdb != nil {
		val, err := rdb.Get(ctx, redisKey).Result()
		if err == nil {
			if rate, parseErr := strconv.ParseFloat(val, 64); parseErr == nil && rate > 0 {
				return rate
			}
		}
	}

	var record model.AICreditsRate
	dbErr := db.WithContext(ctx).
		Where("currency = ? AND is_enabled = ?", "CNY", true).
		First(&record).Error
	if dbErr != nil || record.RatePerCredit <= 0 {
		return 0
	}

	rate := record.RatePerCredit
	if rdb != nil {
		rdb.Set(ctx, redisKey, strconv.FormatFloat(rate, 'f', 6, 64), rateCacheTTL)
	}
	return rate
}

func invalidateCNYRateCache(ctx context.Context, rdb *redis.Client) {
	if rdb != nil {
		rdb.Del(ctx, "ai:gw:credits:rate:CNY")
	}
}

// calcCredits prices normalized usage. cacheWriteTokens (Anthropic's
// cache_creation_input_tokens — always 0 for dialects that don't report a
// distinct cache-write count) is priced via AIModelItem.CacheWritePricePerMillion,
// which existed as a column since D02 but was never read by this function
// until the Anthropic Messages/Bedrock inbound work needed it end-to-end.
func calcCredits(price *modelPriceEntry, promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int, ratePerCredit float64) (credits float64, microCredits int64, costCNY float64) {
	if price.noPricing || price.inputPrice == 0 {
		return 0, 0, 0
	}

	cacheReadPrice := price.cacheReadPrice
	if cacheReadPrice == 0 {
		cacheReadPrice = price.inputPrice
	}
	cacheWritePrice := price.cacheWritePrice
	if cacheWritePrice == 0 {
		cacheWritePrice = price.inputPrice
	}

	costCNY = (float64(promptTokens)*price.inputPrice +
		float64(completionTokens)*price.outputPrice +
		float64(cacheReadTokens)*cacheReadPrice +
		float64(cacheWriteTokens)*cacheWritePrice) / 1_000_000

	if ratePerCredit > 0 {
		credits = costCNY / ratePerCredit
		microCredits = int64(math.Round(credits * float64(microCreditScale)))
	}
	return
}
