package biz

import (
	"context"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

const keyCacheInvalidateCh = "ai:gw:key:invalidate"

type keyCacheEntry struct {
	vk        *model.AIVirtualKey
	expiredAt time.Time
}

var keyLocalCache sync.Map

const keyLocalCacheTTL = 60 * time.Second

func localCacheGet(hash string) *model.AIVirtualKey {
	v, ok := keyLocalCache.Load(hash)
	if !ok {
		return nil
	}
	entry := v.(keyCacheEntry)
	if time.Now().After(entry.expiredAt) {
		keyLocalCache.Delete(hash)
		return nil
	}
	return entry.vk
}

func localCacheSet(hash string, vk *model.AIVirtualKey) {
	keyLocalCache.Store(hash, keyCacheEntry{
		vk:        vk,
		expiredAt: time.Now().Add(keyLocalCacheTTL),
	})
}

func localCacheInvalidate(hash string) {
	keyLocalCache.Delete(hash)
}

// StartKeyCacheInvalidator starts the Redis Pub/Sub subscription for cross-instance L1 cache invalidation.
func StartKeyCacheInvalidator(ctx context.Context, rdb *redis.Client, logger log.Logger) {
	helper := log.NewHelper(logger)
	go func() {
		backoff := time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if runInvalidationLoop(ctx, rdb, helper) {
				return
			}
			helper.Warnf("AI key cache 订阅断开，等待重连 retry_in=%v", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
}

func runInvalidationLoop(ctx context.Context, rdb *redis.Client, helper *log.Helper) (cancelled bool) {
	pubsub := rdb.Subscribe(ctx, keyCacheInvalidateCh)
	defer pubsub.Close()

	if _, err := pubsub.Receive(ctx); err != nil {
		select {
		case <-ctx.Done():
			return true
		default:
		}
		helper.Warnf("AI key cache 订阅失败: %v", err)
		return false
	}

	helper.Info("AI key cache 跨实例失效订阅已就绪")

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return true
		case msg, ok := <-ch:
			if !ok {
				return false
			}
			localCacheInvalidate(msg.Payload)
		}
	}
}
