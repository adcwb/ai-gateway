package biz

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

const (
	quotaSweepInterval = 30 * time.Second
	limitedIndexKey    = "ai:gw:rl:limited:index"
)

func guardKey(dim string, keyID uint) string {
	return fmt.Sprintf("ai:gw:rl:limitstate:%s:%d", dim, keyID)
}

func limitMember(dim string, keyID uint) string {
	return fmt.Sprintf("%s:%d", dim, keyID)
}

func parseLimitMember(m string) (dim string, keyID uint, ok bool) {
	i := strings.LastIndex(m, ":")
	if i <= 0 || i == len(m)-1 {
		return "", 0, false
	}
	id, err := strconv.ParseUint(m[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[:i], uint(id), true
}

func limitGuardTTL(dim string) time.Duration {
	switch dim {
	case model.QuotaDimDailyToken:
		return 26 * time.Hour
	case model.QuotaDimConcurrency:
		return 20 * time.Minute
	default:
		return 2 * time.Hour
	}
}

func dimensionLimit(key *model.AIVirtualKey, dim string) int64 {
	switch dim {
	case model.QuotaDimHourlyToken:
		return key.HourlyTokenQuota
	case model.QuotaDimDailyToken:
		return key.DailyTokenQuota
	case model.QuotaDimHourlyReq:
		return key.HourlyReqQuota
	case model.QuotaDimConcurrency:
		return int64(key.MaxConcurrency)
	}
	return 0
}

func dimensionUsage(ctx context.Context, rdb *redis.Client, keyID uint, dim string) int64 {
	now := time.Now()
	windowSecs := int64(slidingWindowDuration / time.Second)
	bucketSecs := int64(slidingWindowBucket / time.Second)
	switch dim {
	case model.QuotaDimHourlyToken:
		v, _ := rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:tokens:%d", keyID)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		return v
	case model.QuotaDimHourlyReq:
		v, _ := rollingSumScript.Run(ctx, rdb, []string{fmt.Sprintf("ai:gw:rl:reqs:%d", keyID)},
			now.Unix(), windowSecs, bucketSecs).Int64()
		return v
	case model.QuotaDimDailyToken:
		v, _ := rdb.Get(ctx, fmt.Sprintf("ai:gw:quota:daily:tokens:%d:%s", keyID, now.Format("20060102"))).Int64()
		return v
	case model.QuotaDimConcurrency:
		v, _ := rdb.ZCard(ctx, fmt.Sprintf("ai:gw:slot:key:%d", keyID)).Result()
		return v
	}
	return 0
}

func dimensionResetKeys(keyID uint, dim string) []string {
	switch dim {
	case model.QuotaDimHourlyToken:
		return []string{fmt.Sprintf("ai:gw:rl:tokens:%d", keyID)}
	case model.QuotaDimHourlyReq:
		return []string{fmt.Sprintf("ai:gw:rl:reqs:%d", keyID)}
	case model.QuotaDimDailyToken:
		return []string{fmt.Sprintf("ai:gw:quota:daily:tokens:%d:%s", keyID, time.Now().Format("20060102"))}
	case model.QuotaDimConcurrency:
		return []string{fmt.Sprintf("ai:gw:slot:key:%d", keyID)}
	}
	return nil
}

func configuredDims(key *model.AIVirtualKey) []string {
	dims := make([]string, 0, 4)
	for _, d := range []string{
		model.QuotaDimHourlyToken, model.QuotaDimDailyToken,
		model.QuotaDimHourlyReq, model.QuotaDimConcurrency,
	} {
		if dimensionLimit(key, d) > 0 {
			dims = append(dims, d)
		}
	}
	return dims
}

func recordQuotaEvent(ctx context.Context, db *gorm.DB, logger *log.Helper, ev model.AIGatewayQuotaEvent) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := db.WithContext(ctx).Create(&ev).Error; err != nil {
		logger.Errorf("写配额限额事件失败 keyID=%d dim=%s type=%s err=%v",
			ev.VirtualKeyID, ev.Dimension, ev.EventType, err)
	}
}

func recordTriggerIfNew(ctx context.Context, db *gorm.DB, rdb *redis.Client, key *model.AIVirtualKey, dim string, used, limit int64, reason string) {
	if rdb == nil {
		return
	}
	ok, err := rdb.SetNX(ctx, guardKey(dim, key.ID), time.Now().Unix(), limitGuardTTL(dim)).Result()
	if err != nil || !ok {
		return
	}
	rdb.SAdd(ctx, limitedIndexKey, limitMember(dim, key.ID))
	helper := log.NewHelper(log.DefaultLogger)
	recordQuotaEvent(ctx, db, helper, model.AIGatewayQuotaEvent{
		VirtualKeyID: key.ID,
		KeyPrefix:    key.KeyPrefix,
		Dimension:    dim,
		EventType:    model.QuotaEventTriggered,
		QuotaLimit:   limit,
		Used:         used,
		Reason:       reason,
	})
}

func releaseIfActive(ctx context.Context, db *gorm.DB, rdb *redis.Client, keyID uint, keyPrefix, dim string, used, limit int64, reason, operator string) {
	if rdb == nil {
		return
	}
	removed, err := rdb.Del(ctx, guardKey(dim, keyID)).Result()
	if err != nil || removed == 0 {
		return
	}
	rdb.SRem(ctx, limitedIndexKey, limitMember(dim, keyID))
	helper := log.NewHelper(log.DefaultLogger)
	recordQuotaEvent(ctx, db, helper, model.AIGatewayQuotaEvent{
		VirtualKeyID: keyID,
		KeyPrefix:    keyPrefix,
		Dimension:    dim,
		EventType:    model.QuotaEventReleased,
		QuotaLimit:   limit,
		Used:         used,
		Reason:       reason,
		Operator:     operator,
	})
}

func lazyReleasePassedDims(ctx context.Context, db *gorm.DB, rdb *redis.Client, key *model.AIVirtualKey) {
	if rdb == nil {
		return
	}
	dims := configuredDims(key)
	if len(dims) == 0 {
		return
	}
	pipe := rdb.Pipeline()
	existCmds := make(map[string]*redis.IntCmd, len(dims))
	for _, d := range dims {
		existCmds[d] = pipe.Exists(ctx, guardKey(d, key.ID))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return
	}
	for _, d := range dims {
		if existCmds[d].Val() == 0 {
			continue
		}
		limit := dimensionLimit(key, d)
		used := dimensionUsage(ctx, rdb, key.ID, d)
		if limit <= 0 || used < limit {
			releaseIfActive(ctx, db, rdb, key.ID, key.KeyPrefix, d, used, limit, model.QuotaReasonWindowSlide, "")
		}
	}
}

// StartQuotaReleaseSweeper starts the background sweeper that detects when quota limits are lifted.
func StartQuotaReleaseSweeper(ctx context.Context, db *gorm.DB, rdb *redis.Client, logger log.Logger) {
	helper := log.NewHelper(logger)
	go func() {
		ticker := time.NewTicker(quotaSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepLimitStates(ctx, db, rdb, helper)
			}
		}
	}()
}

func sweepLimitStates(ctx context.Context, db *gorm.DB, rdb *redis.Client, helper *log.Helper) {
	defer func() {
		if r := recover(); r != nil {
			helper.Errorf("配额解除扫描器 panic: %v", r)
		}
	}()
	if rdb == nil {
		return
	}
	sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	members, err := rdb.SMembers(sweepCtx, limitedIndexKey).Result()
	if err != nil || len(members) == 0 {
		return
	}
	for _, m := range members {
		dim, keyID, ok := parseLimitMember(m)
		if !ok {
			rdb.SRem(sweepCtx, limitedIndexKey, m)
			continue
		}
		if exists, _ := rdb.Exists(sweepCtx, guardKey(dim, keyID)).Result(); exists == 0 {
			rdb.SRem(sweepCtx, limitedIndexKey, m)
			continue
		}
		var vk model.AIVirtualKey
		if err := db.WithContext(sweepCtx).First(&vk, keyID).Error; err != nil {
			releaseIfActive(sweepCtx, db, rdb, keyID, "", dim, 0, 0, model.QuotaReasonWindowSlide, "")
			continue
		}
		limit := dimensionLimit(&vk, dim)
		used := dimensionUsage(sweepCtx, rdb, keyID, dim)
		if limit <= 0 || used < limit {
			releaseIfActive(sweepCtx, db, rdb, keyID, vk.KeyPrefix, dim, used, limit, model.QuotaReasonWindowSlide, "")
		}
	}
}

func resolveResetDims(dim string) []string {
	all := []string{
		model.QuotaDimHourlyToken, model.QuotaDimDailyToken,
		model.QuotaDimHourlyReq, model.QuotaDimConcurrency,
	}
	if dim == "" || dim == "all" {
		return all
	}
	for _, d := range all {
		if d == dim {
			return []string{dim}
		}
	}
	return nil
}
