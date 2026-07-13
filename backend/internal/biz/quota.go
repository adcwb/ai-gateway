package biz

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

const (
	slidingWindowDuration = time.Hour
	slidingWindowBucket   = time.Minute
	slotTTLSeconds        = 900
	maxWaitAttempts       = 25
	waitInterval          = 200 * time.Millisecond
)

var acquireSlotScript = redis.NewScript(`
local now = redis.call('TIME')
local ts = tonumber(now[1]) + tonumber(now[2])/1000000
local expireBefore = ts - tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', expireBefore)
if redis.call('ZCARD', KEYS[1]) < tonumber(ARGV[2]) then
  redis.call('ZADD', KEYS[1], ts, ARGV[1])
  redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]) + 60)
  return 1
end
return 0
`)

var releaseSlotScript = redis.NewScript(`
redis.call('ZREM', KEYS[1], ARGV[1])
return 1
`)

var rollingCheckAddScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local bucket = tonumber(ARGV[3])
local limit = tonumber(ARGV[4])
local amount = tonumber(ARGV[5])
local minBucket = math.floor((now - window) / bucket)
local cur = math.floor(now / bucket)
local sum = 0
local data = redis.call('HGETALL', KEYS[1])
for i = 1, #data, 2 do
  local b = tonumber(data[i])
  if b < minBucket then
    redis.call('HDEL', KEYS[1], data[i])
  else
    sum = sum + tonumber(data[i+1])
  end
end
if sum + amount > limit then
  return -1
end
redis.call('HINCRBY', KEYS[1], cur, amount)
redis.call('PEXPIRE', KEYS[1], (window + bucket) * 1000)
return sum + amount
`)

var rollingSumScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local bucket = tonumber(ARGV[3])
local minBucket = math.floor((now - window) / bucket)
local sum = 0
local data = redis.call('HGETALL', KEYS[1])
for i = 1, #data, 2 do
  local b = tonumber(data[i])
  if b < minBucket then
    redis.call('HDEL', KEYS[1], data[i])
  else
    sum = sum + tonumber(data[i+1])
  end
end
return sum
`)

// QuotaManager manages Redis-based rate limiting and quota enforcement.
type QuotaManager struct {
	rdb    *redis.Client
	db     *gorm.DB
	logger *log.Helper
}

func NewQuotaManager(rdb *redis.Client, db *gorm.DB, logger log.Logger) *QuotaManager {
	return &QuotaManager{
		rdb:    rdb,
		db:     db,
		logger: log.NewHelper(logger),
	}
}

type modelLimits struct {
	perModel    bool
	model       string
	dailyToken  int64
	hourlyToken int64
	hourlyReq   int64
	dailyPoint  float64
	hourlyPoint float64
}

func effectiveLimits(key *model.AIVirtualKey, modelName string) modelLimits {
	for i := range key.ModelQuotas {
		mq := &key.ModelQuotas[i]
		if mq.ModelName == modelName {
			return modelLimits{
				perModel:    true,
				model:       modelName,
				dailyToken:  mq.DailyTokenQuota,
				hourlyToken: mq.HourlyTokenQuota,
				hourlyReq:   mq.HourlyReqQuota,
				dailyPoint:  mq.DailyPointQuota,
				hourlyPoint: mq.HourlyPointQuota,
			}
		}
	}
	return modelLimits{
		perModel:    false,
		model:       modelName,
		dailyToken:  key.DailyTokenQuota,
		hourlyToken: key.HourlyTokenQuota,
		hourlyReq:   key.HourlyReqQuota,
		dailyPoint:  key.DailyPointQuota,
		hourlyPoint: key.HourlyPointQuota,
	}
}

func mlDailyTokenKey(keyID uint, ml modelLimits, day string) string {
	if ml.perModel {
		return fmt.Sprintf("ai:gw:quota:daily:tokens:%d:m:%s:%s", keyID, ml.model, day)
	}
	return fmt.Sprintf("ai:gw:quota:daily:tokens:%d:%s", keyID, day)
}

func mlHourlyTokenKey(keyID uint, ml modelLimits) string {
	if ml.perModel {
		return fmt.Sprintf("ai:gw:rl:tokens:%d:m:%s", keyID, ml.model)
	}
	return fmt.Sprintf("ai:gw:rl:tokens:%d", keyID)
}

func mlDailyCreditsKey(keyID uint, ml modelLimits, day string) string {
	if ml.perModel {
		return fmt.Sprintf("ai:gw:quota:daily:credits:%d:m:%s:%s", keyID, ml.model, day)
	}
	return fmt.Sprintf("ai:gw:quota:daily:credits:%d:%s", keyID, day)
}

func mlHourlyCreditsKey(keyID uint, ml modelLimits) string {
	if ml.perModel {
		return fmt.Sprintf("ai:gw:rl:credits:%d:m:%s", keyID, ml.model)
	}
	return fmt.Sprintf("ai:gw:rl:credits:%d", keyID)
}

func mlHourlyReqKey(keyID uint, ml modelLimits) string {
	if ml.perModel {
		return fmt.Sprintf("ai:gw:rl:reqs:%d:m:%s", keyID, ml.model)
	}
	return fmt.Sprintf("ai:gw:rl:reqs:%d", keyID)
}

func hourlyToolCallKey(keyID uint) string {
	return fmt.Sprintf("ai:gw:rl:toolcalls:%d", keyID)
}

func hourlyImageCallKey(keyID uint) string {
	return fmt.Sprintf("ai:gw:rl:imagecalls:%d", keyID)
}

func hourlyAudioCallKey(keyID uint) string {
	return fmt.Sprintf("ai:gw:rl:audiocalls:%d", keyID)
}

func hourlyVideoCallKey(keyID uint) string {
	return fmt.Sprintf("ai:gw:rl:videocalls:%d", keyID)
}

// CheckAndReserve checks quota and reserves concurrency slot before a request.
func (q *QuotaManager) CheckAndReserve(ctx context.Context, key *model.AIVirtualKey) (requestID string, err error) {
	rdb := q.rdb
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	skipModelDims := len(key.ModelQuotas) > 0

	// 1. Sliding window: hourly request count
	if !skipModelDims && key.HourlyReqQuota > 0 {
		reqsKey := fmt.Sprintf("ai:gw:rl:reqs:%d", key.ID)
		n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{reqsKey},
			now.Unix(), windowSecs, bucketSecs, key.HourlyReqQuota, 1).Int64()
		if scriptErr != nil {
			return "", fmt.Errorf("redis error: %w", scriptErr)
		}
		if n < 0 {
			recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimHourlyReq, key.HourlyReqQuota, key.HourlyReqQuota, "")
			return "", fmt.Errorf("每小时请求数已达上限 (%d)", key.HourlyReqQuota)
		}
	}

	// 2. Concurrency slots
	if key.MaxConcurrency > 0 {
		rid := generateRequestID()
		slotKey := fmt.Sprintf("ai:gw:slot:key:%d", key.ID)
		waitKey := fmt.Sprintf("ai:gw:wait:%d", key.ID)

		// rollbackHourlyReq undoes step 1's hourly-request-count reservation —
		// mirrors the rollback steps 3/4 already do on their own failure.
		// Without this, a request that never gets past the concurrency slot
		// (Redis error, or wait-queue timeout) still permanently consumes one
		// unit of HourlyReqQuota, so concurrency contention/Redis hiccups alone
		// can push a key into rate-limiting sooner than its actual
		// accepted-request volume justifies.
		rollbackHourlyReq := func() {
			if !skipModelDims && key.HourlyReqQuota > 0 {
				bucket := strconv.FormatInt(now.Unix()/bucketSecs, 10)
				rdb.HIncrBy(ctx, fmt.Sprintf("ai:gw:rl:reqs:%d", key.ID), bucket, -1)
			}
		}

		acquired := false
		waitIncremented := false
		for i := 0; i < maxWaitAttempts; i++ {
			n, scriptErr := acquireSlotScript.Run(ctx, rdb, []string{slotKey},
				rid, key.MaxConcurrency, slotTTLSeconds).Int()
			if scriptErr != nil {
				if waitIncremented {
					rdb.Decr(ctx, waitKey)
				}
				rollbackHourlyReq()
				return "", fmt.Errorf("redis error: %w", scriptErr)
			}
			if n == 1 {
				acquired = true
				break
			}
			if !waitIncremented {
				rdb.Incr(ctx, waitKey)
				waitIncremented = true
			}
			time.Sleep(waitInterval)
		}

		if waitIncremented {
			rdb.Decr(ctx, waitKey)
		}
		if !acquired {
			recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimConcurrency,
				int64(key.MaxConcurrency), int64(key.MaxConcurrency), model.QuotaReasonWaitTimeout)
			rollbackHourlyReq()
			return "", fmt.Errorf("并发队列等待超时（超过 5s），请稍后重试")
		}
		requestID = rid
	}

	if skipModelDims {
		return requestID, nil
	}

	// 3. Token quota pre-check
	if dim, used, limit, checkErr := q.checkTokenQuotaDetail(ctx, key); checkErr != nil {
		recordTriggerIfNew(ctx, q.db, rdb, key, dim, used, limit, "")
		if requestID != "" {
			q.ReleaseSlot(ctx, key.ID, requestID)
		}
		if key.HourlyReqQuota > 0 {
			bucket := strconv.FormatInt(now.Unix()/bucketSecs, 10)
			rdb.HIncrBy(ctx, fmt.Sprintf("ai:gw:rl:reqs:%d", key.ID), bucket, -1)
		}
		return "", checkErr
	}

	// 4. Credits quota pre-check
	if dim, used, limit, checkErr := q.checkCreditsQuotaDetail(ctx, key); checkErr != nil {
		recordTriggerIfNew(ctx, q.db, rdb, key, dim, used, limit, "")
		if key.HourlyReqQuota > 0 {
			bucket := strconv.FormatInt(now.Unix()/bucketSecs, 10)
			rdb.HIncrBy(ctx, fmt.Sprintf("ai:gw:rl:reqs:%d", key.ID), bucket, -1)
		}
		if requestID != "" {
			q.ReleaseSlot(ctx, key.ID, requestID)
		}
		return "", checkErr
	}

	lazyReleasePassedDims(ctx, q.db, rdb, key)

	return requestID, nil
}

// CheckModelAwareQuota checks per-model quota after the real model is resolved.
func (q *QuotaManager) CheckModelAwareQuota(ctx context.Context, key *model.AIVirtualKey, modelName string) error {
	rdb := q.rdb
	if rdb == nil {
		return nil
	}
	ml := effectiveLimits(key, modelName)
	now := time.Now()
	day := now.Format("20060102")
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	recordKeyTrigger := func(dim string, used, limit int64) {
		if !ml.perModel {
			recordTriggerIfNew(ctx, q.db, rdb, key, dim, used, limit, "")
		}
	}

	if ml.dailyToken > 0 {
		val, _ := rdb.Get(ctx, mlDailyTokenKey(key.ID, ml, day)).Int64()
		if val >= ml.dailyToken {
			recordKeyTrigger(model.QuotaDimDailyToken, val, ml.dailyToken)
			return fmt.Errorf("模型 %s 每日 Token 配额已耗尽 (%d/%d)", modelName, val, ml.dailyToken)
		}
	}
	if ml.hourlyToken > 0 {
		val, _ := rollingSumScript.Run(ctx, rdb, []string{mlHourlyTokenKey(key.ID, ml)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		if val >= ml.hourlyToken {
			recordKeyTrigger(model.QuotaDimHourlyToken, val, ml.hourlyToken)
			return fmt.Errorf("模型 %s 每小时 Token 配额已耗尽 (%d/%d)", modelName, val, ml.hourlyToken)
		}
	}
	if ml.dailyPoint > 0 {
		val, _ := rdb.Get(ctx, mlDailyCreditsKey(key.ID, ml, day)).Int64()
		limitMicro := int64(ml.dailyPoint * float64(microCreditScale))
		if val >= limitMicro {
			recordKeyTrigger(model.QuotaDimDailyPoint, val, limitMicro)
			return fmt.Errorf("模型 %s 每日 Credits 配额已耗尽", modelName)
		}
	}
	if ml.hourlyPoint > 0 {
		val, _ := rollingSumScript.Run(ctx, rdb, []string{mlHourlyCreditsKey(key.ID, ml)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		limitMicro := int64(ml.hourlyPoint * float64(microCreditScale))
		if val >= limitMicro {
			recordKeyTrigger(model.QuotaDimHourlyPoint, val, limitMicro)
			return fmt.Errorf("模型 %s 每小时 Credits 配额已耗尽", modelName)
		}
	}
	if ml.hourlyReq > 0 {
		n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{mlHourlyReqKey(key.ID, ml)},
			now.Unix(), windowSecs, bucketSecs, ml.hourlyReq, 1).Int64()
		if scriptErr != nil {
			return fmt.Errorf("redis error: %w", scriptErr)
		}
		if n < 0 {
			recordKeyTrigger(model.QuotaDimHourlyReq, ml.hourlyReq, ml.hourlyReq)
			return fmt.Errorf("模型 %s 每小时请求数已达上限 (%d)", modelName, ml.hourlyReq)
		}
	}
	return nil
}

// CheckAndReserveToolCall enforces a key's dedicated MCP tools/call budget
// (QuotaDimToolCall, docs/design/09-extensibility.md), independent of the
// shared HourlyReqQuota that VirtualKeyAuth.ProxyMiddleware already reserves
// for every route including MCP. A zero quota means unlimited.
func (q *QuotaManager) CheckAndReserveToolCall(ctx context.Context, key *model.AIVirtualKey) error {
	if key.HourlyToolCallQuota <= 0 {
		return nil
	}
	rdb := q.rdb
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{hourlyToolCallKey(key.ID)},
		now.Unix(), windowSecs, bucketSecs, key.HourlyToolCallQuota, 1).Int64()
	if scriptErr != nil {
		return fmt.Errorf("redis error: %w", scriptErr)
	}
	if n < 0 {
		recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimToolCall, key.HourlyToolCallQuota, key.HourlyToolCallQuota, "")
		return fmt.Errorf("每小时工具调用次数已达上限 (%d)", key.HourlyToolCallQuota)
	}
	return nil
}

// CheckAndReserveImageCall enforces a key's dedicated images/generations
// budget (QuotaDimImageCall, docs/superpowers/specs/2026-07-09-multimodal-
// media-adapters-design.md), independent of HourlyReqQuota and of
// HourlyAudioCallQuota. A zero quota means unlimited.
func (q *QuotaManager) CheckAndReserveImageCall(ctx context.Context, key *model.AIVirtualKey) error {
	if key.HourlyImageCallQuota <= 0 {
		return nil
	}
	rdb := q.rdb
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{hourlyImageCallKey(key.ID)},
		now.Unix(), windowSecs, bucketSecs, key.HourlyImageCallQuota, 1).Int64()
	if scriptErr != nil {
		return fmt.Errorf("redis error: %w", scriptErr)
	}
	if n < 0 {
		recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimImageCall, key.HourlyImageCallQuota, key.HourlyImageCallQuota, "")
		return fmt.Errorf("每小时图像生成调用次数已达上限 (%d)", key.HourlyImageCallQuota)
	}
	return nil
}

// CheckAndReserveAudioCall enforces a key's dedicated audio budget
// (QuotaDimAudioCall), shared by audio/speech and audio/transcriptions —
// independent of HourlyReqQuota and of HourlyImageCallQuota. A zero quota
// means unlimited.
func (q *QuotaManager) CheckAndReserveAudioCall(ctx context.Context, key *model.AIVirtualKey) error {
	if key.HourlyAudioCallQuota <= 0 {
		return nil
	}
	rdb := q.rdb
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{hourlyAudioCallKey(key.ID)},
		now.Unix(), windowSecs, bucketSecs, key.HourlyAudioCallQuota, 1).Int64()
	if scriptErr != nil {
		return fmt.Errorf("redis error: %w", scriptErr)
	}
	if n < 0 {
		recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimAudioCall, key.HourlyAudioCallQuota, key.HourlyAudioCallQuota, "")
		return fmt.Errorf("每小时音频调用次数已达上限 (%d)", key.HourlyAudioCallQuota)
	}
	return nil
}

// CheckAndReserveVideoCall enforces a key's dedicated video-generation
// submission budget (QuotaDimVideoCall) — consumed only on job creation
// (POST /ai/v1/videos), never on status polling, content download, or
// delete. Independent of HourlyReqQuota/HourlyImageCallQuota/
// HourlyAudioCallQuota. A zero quota means unlimited.
func (q *QuotaManager) CheckAndReserveVideoCall(ctx context.Context, key *model.AIVirtualKey) error {
	if key.HourlyVideoCallQuota <= 0 {
		return nil
	}
	rdb := q.rdb
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	n, scriptErr := rollingCheckAddScript.Run(ctx, rdb, []string{hourlyVideoCallKey(key.ID)},
		now.Unix(), windowSecs, bucketSecs, key.HourlyVideoCallQuota, 1).Int64()
	if scriptErr != nil {
		return fmt.Errorf("redis error: %w", scriptErr)
	}
	if n < 0 {
		recordTriggerIfNew(ctx, q.db, rdb, key, model.QuotaDimVideoCall, key.HourlyVideoCallQuota, key.HourlyVideoCallQuota, "")
		return fmt.Errorf("每小时视频生成调用次数已达上限 (%d)", key.HourlyVideoCallQuota)
	}
	return nil
}

// ReleaseSlot releases a concurrency slot after a request completes.
func (q *QuotaManager) ReleaseSlot(ctx context.Context, keyID uint, requestID string) {
	if requestID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	slotKey := fmt.Sprintf("ai:gw:slot:key:%d", keyID)
	releaseSlotScript.Run(ctx, q.rdb, []string{slotKey}, requestID) //nolint:errcheck
}

// CommitTokens records actual token usage after a request completes.
func (q *QuotaManager) CommitTokens(ctx context.Context, key *model.AIVirtualKey, modelName string, promptTokens, completionTokens int) {
	total := int64(promptTokens + completionTokens)
	if total == 0 {
		return
	}

	ml := effectiveLimits(key, modelName)
	if ml.dailyToken <= 0 && ml.hourlyToken <= 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now()
	day := now.Format("20060102")
	rdb := q.rdb
	bucketSecs := int64(slidingWindowBucket / time.Second)

	_, err := rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		if ml.dailyToken > 0 {
			k := mlDailyTokenKey(key.ID, ml, day)
			pipe.IncrBy(ctx, k, total)
			pipe.Expire(ctx, k, 25*time.Hour)
		}
		if ml.hourlyToken > 0 {
			hk := mlHourlyTokenKey(key.ID, ml)
			bucket := strconv.FormatInt(now.Unix()/bucketSecs, 10)
			pipe.HIncrBy(ctx, hk, bucket, total)
			pipe.PExpire(ctx, hk, slidingWindowDuration+slidingWindowBucket)
		}
		return nil
	})
	if err != nil {
		q.logger.Errorf("提交 token 配额到 Redis 失败 keyID=%d err=%v", key.ID, err)
	}
}

// CommitCredits calculates and records Credits usage after a request completes.
func (q *QuotaManager) CommitCredits(ctx context.Context, key *model.AIVirtualKey,
	providerID uint, modelName string, promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int) (credits float64, priceCNY float64) {

	price := getModelPriceEntry(ctx, q.db, q.logger, providerID, modelName)
	if price.noPricing {
		return 0, 0
	}

	ratePerCredit := getCNYRatePerCredit(ctx, q.db, q.rdb)
	var microCredits int64
	credits, microCredits, priceCNY = calcCredits(price, promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens, ratePerCredit)

	ml := effectiveLimits(key, modelName)
	if (ml.dailyPoint > 0 || ml.hourlyPoint > 0) && microCredits > 0 {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		now := time.Now()
		day := now.Format("20060102")
		rdb := q.rdb
		bucketSecs := int64(slidingWindowBucket / time.Second)

		_, err := rdb.Pipelined(wctx, func(pipe redis.Pipeliner) error {
			if ml.dailyPoint > 0 {
				k := mlDailyCreditsKey(key.ID, ml, day)
				pipe.IncrBy(wctx, k, microCredits)
				pipe.Expire(wctx, k, 25*time.Hour)
			}
			if ml.hourlyPoint > 0 {
				hk := mlHourlyCreditsKey(key.ID, ml)
				bucket := strconv.FormatInt(now.Unix()/bucketSecs, 10)
				pipe.HIncrBy(wctx, hk, bucket, microCredits)
				pipe.PExpire(wctx, hk, slidingWindowDuration+slidingWindowBucket)
			}
			return nil
		})
		if err != nil {
			q.logger.Errorf("提交 Credits 配额到 Redis 失败 keyID=%d err=%v", key.ID, err)
		}
	}
	return credits, priceCNY
}

// GetUsage returns real-time quota usage for a virtual key.
func (q *QuotaManager) GetUsage(ctx context.Context, key *model.AIVirtualKey) (
	dailyTokenUsed, hourlyTokenUsed, hourlyReqUsed, currentConcurrency int64,
	dailyPointUsed, hourlyPointUsed float64,
) {
	now := time.Now()
	rdb := q.rdb
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	dailyTokenUsed, _ = rdb.Get(ctx, fmt.Sprintf("ai:gw:quota:daily:tokens:%d:%s", key.ID, now.Format("20060102"))).Int64()
	hourlyTokenUsed, _ = rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:tokens:%d", key.ID)},
		now.Unix(), windowSecs, bucketSecs).Int64()
	hourlyReqUsed, _ = rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:reqs:%d", key.ID)},
		now.Unix(), windowSecs, bucketSecs).Int64()
	currentConcurrency, _ = rdb.ZCard(ctx, fmt.Sprintf("ai:gw:slot:key:%d", key.ID)).Result()

	if key.DailyPointQuota > 0 {
		rawDaily, _ := rdb.Get(ctx, fmt.Sprintf("ai:gw:quota:daily:credits:%d:%s", key.ID, now.Format("20060102"))).Int64()
		dailyPointUsed = float64(rawDaily) / float64(microCreditScale)
	}
	if key.HourlyPointQuota > 0 {
		rawHourly, _ := rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:credits:%d", key.ID)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		hourlyPointUsed = float64(rawHourly) / float64(microCreditScale)
	}
	return
}

func (q *QuotaManager) checkTokenQuotaDetail(ctx context.Context, key *model.AIVirtualKey) (dim string, used, limit int64, err error) {
	now := time.Now()
	rdb := q.rdb

	if key.DailyTokenQuota > 0 {
		k := fmt.Sprintf("ai:gw:quota:daily:tokens:%d:%s", key.ID, now.Format("20060102"))
		val, _ := rdb.Get(ctx, k).Int64()
		if val >= key.DailyTokenQuota {
			return model.QuotaDimDailyToken, val, key.DailyTokenQuota,
				fmt.Errorf("每日 Token 配额已耗尽 (%d/%d)", val, key.DailyTokenQuota)
		}
	}

	if key.HourlyTokenQuota > 0 {
		windowSecs := int64(slidingWindowDuration / time.Second)
		bucketSecs := int64(slidingWindowBucket / time.Second)
		val, _ := rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:tokens:%d", key.ID)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		if val >= key.HourlyTokenQuota {
			return model.QuotaDimHourlyToken, val, key.HourlyTokenQuota,
				fmt.Errorf("每小时 Token 配额已耗尽 (%d/%d)", val, key.HourlyTokenQuota)
		}
	}

	return "", 0, 0, nil
}

// GetModelUsage reads real-time usage for a per-model quota row (for the quota management page).
func (q *QuotaManager) GetModelUsage(ctx context.Context, keyID uint, mq *model.AIVirtualKeyModelQuota) (
	dailyTokenUsed, hourlyTokenUsed, hourlyReqUsed int64, dailyPointUsed, hourlyPointUsed float64) {
	rdb := q.rdb
	if rdb == nil {
		return
	}
	ml := modelLimits{perModel: true, model: mq.ModelName}
	now := time.Now()
	day := now.Format("20060102")
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)

	dailyTokenUsed, _ = rdb.Get(ctx, mlDailyTokenKey(keyID, ml, day)).Int64()
	hourlyTokenUsed, _ = rollingSumScript.Run(ctx, rdb, []string{mlHourlyTokenKey(keyID, ml)},
		now.Unix(), windowSecs, bucketSecs).Int64()
	hourlyReqUsed, _ = rollingSumScript.Run(ctx, rdb, []string{mlHourlyReqKey(keyID, ml)},
		now.Unix(), windowSecs, bucketSecs).Int64()
	rawDaily, _ := rdb.Get(ctx, mlDailyCreditsKey(keyID, ml, day)).Int64()
	dailyPointUsed = float64(rawDaily) / float64(microCreditScale)
	rawHourly, _ := rollingSumScript.Run(ctx, rdb, []string{mlHourlyCreditsKey(keyID, ml)},
		now.Unix(), windowSecs, bucketSecs).Int64()
	hourlyPointUsed = float64(rawHourly) / float64(microCreditScale)
	return
}

func (q *QuotaManager) checkCreditsQuotaDetail(ctx context.Context, key *model.AIVirtualKey) (dim string, used, limit int64, err error) {
	if key.DailyPointQuota == 0 && key.HourlyPointQuota == 0 {
		return "", 0, 0, nil
	}

	now := time.Now()
	rdb := q.rdb

	if key.DailyPointQuota > 0 {
		k := fmt.Sprintf("ai:gw:quota:daily:credits:%d:%s", key.ID, now.Format("20060102"))
		val, _ := rdb.Get(ctx, k).Int64()
		limitMicro := int64(key.DailyPointQuota * float64(microCreditScale))
		if val >= limitMicro {
			return model.QuotaDimDailyPoint, val, limitMicro,
				fmt.Errorf("每日 Credits 配额已耗尽 (%.4f/%.4f)",
					float64(val)/float64(microCreditScale), key.DailyPointQuota)
		}
	}

	if key.HourlyPointQuota > 0 {
		windowSecs := int64(slidingWindowDuration / time.Second)
		bucketSecs := int64(slidingWindowBucket / time.Second)
		val, _ := rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:credits:%d", key.ID)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		limitMicro := int64(key.HourlyPointQuota * float64(microCreditScale))
		if val >= limitMicro {
			return model.QuotaDimHourlyPoint, val, limitMicro,
				fmt.Errorf("每小时 Credits 配额已耗尽 (%.4f/%.4f)",
					float64(val)/float64(microCreditScale), key.HourlyPointQuota)
		}
	}

	return "", 0, 0, nil
}
