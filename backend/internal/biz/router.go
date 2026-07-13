package biz

import (
	"context"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/adcwb/ai-gateway/internal/data/model"
	"github.com/adcwb/ai-gateway/internal/observability"
)

// RouterManager implements provider selection and circuit breaking
// (docs/design/01-routing-and-lb.md). Breaker state lives in Redis so all
// gateway instances share one view; a short local micro-cache keeps the hot
// path cheap. When Redis is unavailable the breaker fails OPEN-circuit-less
// (all providers considered healthy) per design principle 6.
type RouterManager struct {
	rdb     *redis.Client
	db      *gorm.DB
	metrics *observability.Metrics
	logger  *log.Helper

	stateCache sync.Map // providerID → breakerCacheEntry
	modelIndex atomic.Pointer[modelIndexEntry]
}

func NewRouterManager(rdb *redis.Client, db *gorm.DB, metrics *observability.Metrics, logger log.Logger) *RouterManager {
	return &RouterManager{
		rdb:     rdb,
		db:      db,
		metrics: metrics,
		logger:  log.NewHelper(logger),
	}
}

// Breaker tuning (docs/design/01-routing-and-lb.md defaults).
const (
	breakerFailThreshold  = 5
	breakerWindowSec      = 30
	breakerCooldownSec    = 30
	breakerProbeQuota     = 3
	breakerProbeSuccesses = 2

	breakerKeyFmt        = "ai:gw:cb:%d"
	breakerStateCacheTTL = 1500 * time.Millisecond
	candidateCacheTTL    = 10 * time.Second
	maxUpstreamAttempts  = 3
)

type breakerCacheEntry struct {
	state     string
	expiresAt time.Time
}

// modelIndexEntry is a gateway-wide provider→model inverted index, rebuilt
// with one full table scan (not one scan per distinct requested model — see
// providersForModel).
type modelIndexEntry struct {
	byModel   map[string][]model.AIProvider
	expiresAt time.Time
}

// AttemptOutcome classifies one upstream attempt for breaker accounting.
type AttemptOutcome string

const (
	AttemptSuccess        AttemptOutcome = "success"
	AttemptRetryableError AttemptOutcome = "retryable_error"
	AttemptFatalError     AttemptOutcome = "fatal_error"
)

// tryPassScript decides atomically whether a request may hit a provider.
// Returns: 1 = allow (closed), 2 = allow as half-open probe, 0 = deny (open).
// Hash fields: state, fail_count, window_start, opened_at, probe_inflight, probe_ok.
var tryPassScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local cooldown = tonumber(ARGV[2])
local probeQuota = tonumber(ARGV[3])
local state = redis.call('HGET', key, 'state')
if not state or state == 'closed' then
    return 1
end
if state == 'open' then
    local openedAt = tonumber(redis.call('HGET', key, 'opened_at') or '0')
    if now - openedAt < cooldown then
        return 0
    end
    -- cooldown elapsed: transition to half-open and take the first probe slot
    redis.call('HSET', key, 'state', 'half_open', 'probe_inflight', 1, 'probe_ok', 0)
    return 2
end
-- half_open: hand out limited probe slots
local inflight = tonumber(redis.call('HGET', key, 'probe_inflight') or '0')
if inflight < probeQuota then
    redis.call('HINCRBY', key, 'probe_inflight', 1)
    return 2
end
return 0
`)

// reportResultScript feeds one attempt outcome into the state machine.
// Returns {prevState, newState} — the caller previously fetched prevState
// via a separate StateOf Redis round-trip before running this script; folding
// it into the script's own read of the 'state' field removes that extra hop.
var reportResultScript = redis.NewScript(`
local key = KEYS[1]
local ok = ARGV[1] == '1'
local now = tonumber(ARGV[2])
local windowSec = tonumber(ARGV[3])
local threshold = tonumber(ARGV[4])
local probeSuccesses = tonumber(ARGV[5])
local state = redis.call('HGET', key, 'state')
if not state then state = 'closed' end
local prevState = state

if state == 'half_open' then
    if ok then
        local okCount = tonumber(redis.call('HINCRBY', key, 'probe_ok', 1))
        if okCount >= probeSuccesses then
            redis.call('HSET', key, 'state', 'closed', 'fail_count', 0, 'window_start', now, 'probe_inflight', 0, 'probe_ok', 0)
            return {prevState, 'closed'}
        end
        return {prevState, 'half_open'}
    end
    redis.call('HSET', key, 'state', 'open', 'opened_at', now, 'probe_inflight', 0, 'probe_ok', 0)
    return {prevState, 'open'}
end

if state == 'open' then
    return {prevState, 'open'}
end

-- closed
if ok then
    redis.call('HSET', key, 'fail_count', 0, 'window_start', now)
    return {prevState, 'closed'}
end
local windowStart = tonumber(redis.call('HGET', key, 'window_start') or '0')
local failCount = tonumber(redis.call('HGET', key, 'fail_count') or '0')
if now - windowStart > windowSec then
    failCount = 0
    redis.call('HSET', key, 'window_start', now)
end
failCount = failCount + 1
redis.call('HSET', key, 'fail_count', failCount)
if failCount >= threshold then
    redis.call('HSET', key, 'state', 'open', 'opened_at', now, 'probe_inflight', 0, 'probe_ok', 0)
    return {prevState, 'open'}
end
return {prevState, 'closed'}
`)

// TryPass reports whether an attempt against providerID may proceed now.
// Redis failure ⇒ allow (fail open).
func (rm *RouterManager) TryPass(ctx context.Context, providerID uint) bool {
	if rm.rdb == nil {
		return true
	}
	key := breakerKey(providerID)
	res, err := tryPassScript.Run(ctx, rm.rdb, []string{key},
		time.Now().Unix(), breakerCooldownSec, breakerProbeQuota).Int()
	if err != nil {
		rm.logger.Warnf("router: TryPass Redis 不可用，失败开放 providerID=%d err=%v", providerID, err)
		return true
	}
	if res == 2 {
		rm.cacheState(providerID, model.BreakerStateHalfOpen)
	}
	return res != 0
}

// ReportResult feeds the breaker after an attempt and records state transitions.
func (rm *RouterManager) ReportResult(ctx context.Context, providerID uint, outcome AttemptOutcome) {
	if rm.metrics != nil {
		rm.metrics.UpstreamAttempts.WithLabelValues(strconv.FormatUint(uint64(providerID), 10), string(outcome)).Inc()
	}
	if rm.rdb == nil {
		return
	}
	ok := "0"
	if outcome == AttemptSuccess {
		ok = "1"
	}
	// reportResultScript now returns {prevState, newState} in one round trip —
	// this used to call StateOf first (a separate HMGET) purely to log/compare
	// against the transition, doubling the Redis hop on every settled attempt.
	res, err := reportResultScript.Run(ctx, rm.rdb, []string{breakerKey(providerID)},
		ok, time.Now().Unix(), breakerWindowSec, breakerFailThreshold, breakerProbeSuccesses).Result()
	if err != nil {
		rm.logger.Warnf("router: ReportResult 写入失败 providerID=%d err=%v", providerID, err)
		return
	}
	pair, isPair := res.([]interface{})
	if !isPair || len(pair) != 2 {
		rm.logger.Warnf("router: ReportResult 返回格式异常 providerID=%d", providerID)
		return
	}
	prev, _ := pair[0].(string)
	newState, _ := pair[1].(string)
	rm.cacheState(providerID, newState)
	if newState != prev {
		rm.recordTransition(providerID, prev, newState, string(outcome))
	}
}

// StateOf returns the current breaker state, via a short-lived local cache.
func (rm *RouterManager) StateOf(ctx context.Context, providerID uint) string {
	if v, hit := rm.stateCache.Load(providerID); hit {
		entry := v.(breakerCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.state
		}
	}
	state := model.BreakerStateClosed
	if rm.rdb != nil {
		vals, err := rm.rdb.HMGet(ctx, breakerKey(providerID), "state", "opened_at").Result()
		if err == nil && len(vals) == 2 && vals[0] != nil {
			state, _ = vals[0].(string)
			// an 'open' breaker whose cooldown elapsed is effectively half-open
			if state == model.BreakerStateOpen {
				if openedAt, ok := vals[1].(string); ok {
					if ts, perr := strconv.ParseInt(openedAt, 10, 64); perr == nil &&
						time.Now().Unix()-ts >= breakerCooldownSec {
						state = model.BreakerStateHalfOpen
					}
				}
			}
		}
	}
	rm.cacheState(providerID, state)
	return state
}

func (rm *RouterManager) cacheState(providerID uint, state string) {
	rm.stateCache.Store(providerID, breakerCacheEntry{state: state, expiresAt: time.Now().Add(breakerStateCacheTTL)})
	if rm.metrics != nil {
		var v float64
		switch state {
		case model.BreakerStateHalfOpen:
			v = 1
		case model.BreakerStateOpen:
			v = 2
		}
		rm.metrics.BreakerState.WithLabelValues(strconv.FormatUint(uint64(providerID), 10)).Set(v)
	}
}

// recordTransition persists a breaker transition asynchronously (best-effort).
func (rm *RouterManager) recordTransition(providerID uint, from, to, reason string) {
	rm.logger.Warnf("router: 熔断状态迁移 providerID=%d %s → %s reason=%s", providerID, from, to, reason)
	if rm.db == nil {
		return
	}
	evt := &model.AIGatewayRouterEvent{ProviderID: providerID, FromState: from, ToState: to, Reason: reason}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rm.db.WithContext(ctx).Create(evt).Error; err != nil {
			rm.logger.Warnf("router: 熔断事件落库失败 providerID=%d err=%v", providerID, err)
		}
	}()
}

func breakerKey(providerID uint) string {
	return "ai:gw:cb:" + strconv.FormatUint(uint64(providerID), 10)
}

// -----------------------------------------------------------------------------
// Candidate resolution & ordering
// -----------------------------------------------------------------------------

// RouteCandidate is one (provider, model) pair the attempt loop may try.
type RouteCandidate struct {
	ProviderID uint
	Model      string
}

// Routing strategies selectable per key via AIVirtualKey.RoutingStrategy.
const (
	StrategyWeighted     = "weighted" // default: priority tier, then weighted random
	StrategyPriority     = "priority" // strict priority ordering (fallback-chain style)
	StrategyLeastLatency = "least_latency"
	StrategyLeastCost    = "least_cost"
)

// Candidates returns an ordered provider list for realModel, primary first,
// remaining fallbacks ordered by the key's routing strategy (activating the
// long-dormant AIProvider.Weight field). Providers whose breaker is open are
// moved to the very end rather than dropped, so a request with no healthy
// candidates still makes one last-resort attempt.
func (rm *RouterManager) Candidates(ctx context.Context, realModel string, primaryProviderID uint, strategy string) []RouteCandidate {
	providers := rm.providersForModel(ctx, realModel)

	type ranked struct {
		id       uint
		priority int
		sortKey  float64 // weighted-random ranking key (higher wins)
		metric   float64 // strategy metric (lower wins): latency ms or cost
		open     bool
	}
	items := make([]ranked, 0, len(providers))
	seenPrimary := false
	for _, p := range providers {
		if p.ID == primaryProviderID {
			seenPrimary = true
			continue // primary is prepended below
		}
		w := p.Weight
		if w <= 0 {
			continue // weight 0 = drained: no fallback traffic
		}
		// Efraimidis–Spirakis weighted random ordering: key = u^(1/w)
		key := math.Pow(mrand.Float64(), 1.0/float64(w))
		item := ranked{
			id:       p.ID,
			priority: p.Priority,
			sortKey:  key,
			open:     rm.StateOf(ctx, p.ID) == model.BreakerStateOpen,
		}
		switch strategy {
		case StrategyLeastLatency:
			item.metric = rm.LatencyEWMA(ctx, p.ID) // unknown = 0 ⇒ optimistic probe
		case StrategyLeastCost:
			item.metric = rm.costPerMillion(ctx, p.ID, realModel)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].open != items[j].open {
			return !items[i].open // healthy first
		}
		switch strategy {
		case StrategyLeastLatency, StrategyLeastCost:
			if items[i].metric != items[j].metric {
				return items[i].metric < items[j].metric
			}
		case StrategyPriority:
			if items[i].priority != items[j].priority {
				return items[i].priority < items[j].priority
			}
			return items[i].sortKey > items[j].sortKey
		}
		if items[i].priority != items[j].priority {
			return items[i].priority < items[j].priority
		}
		return items[i].sortKey > items[j].sortKey
	})

	out := make([]RouteCandidate, 0, len(items)+1)
	if seenPrimary || primaryProviderID != 0 {
		out = append(out, RouteCandidate{ProviderID: primaryProviderID, Model: realModel})
	}
	for _, it := range items {
		out = append(out, RouteCandidate{ProviderID: it.id, Model: realModel})
	}
	if len(out) == 0 {
		out = append(out, RouteCandidate{ProviderID: primaryProviderID, Model: realModel})
	}
	return out
}

// providersForModel lists enabled providers offering realModel via the
// gateway-wide provider→model index (modelIndex), an O(1) map lookup after
// the index is built/refreshed.
func (rm *RouterManager) providersForModel(ctx context.Context, realModel string) []model.AIProvider {
	return rm.providerModelIndex(ctx)[realModel]
}

// providerModelIndex returns the cached provider→model index, rebuilding it
// with a single full table scan when stale. Previously this scan ran once
// per distinct requested model name on every cache miss (O(providers×models)
// each time) even though the underlying provider set is shared across every
// model — a gateway serving N distinct models paid the same full scan N
// times per TTL window. One shared index amortizes that to a single scan.
func (rm *RouterManager) providerModelIndex(ctx context.Context) map[string][]model.AIProvider {
	if entry := rm.modelIndex.Load(); entry != nil && time.Now().Before(entry.expiresAt) {
		return entry.byModel
	}
	var all []model.AIProvider
	if rm.db != nil {
		if err := rm.db.WithContext(ctx).Where("is_enabled = ?", true).Find(&all).Error; err != nil {
			rm.logger.Warnf("router: 查询提供方失败 err=%v", err)
			return nil
		}
	}
	byModel := make(map[string][]model.AIProvider)
	for _, p := range all {
		models, err := p.ParseModels()
		if err != nil {
			continue
		}
		seen := make(map[string]struct{}, len(models))
		for _, m := range models {
			if _, dup := seen[m.Name]; dup {
				continue // a provider listing the same model twice shouldn't duplicate as a candidate
			}
			seen[m.Name] = struct{}{}
			byModel[m.Name] = append(byModel[m.Name], p)
		}
	}
	rm.modelIndex.Store(&modelIndexEntry{byModel: byModel, expiresAt: time.Now().Add(candidateCacheTTL)})
	return byModel
}

// -----------------------------------------------------------------------------
// Latency EWMA (feeds the least_latency strategy and the console)
// -----------------------------------------------------------------------------

const (
	latencyKeyFmt   = "ai:gw:lat:%d"
	latencyEWMAlpha = 0.3
)

// ReportLatency folds one successful attempt's latency into the provider's
// EWMA (Redis-shared; non-atomic read-modify-write is acceptable for a
// smoothed metric).
func (rm *RouterManager) ReportLatency(ctx context.Context, providerID uint, ms int64) {
	if rm.rdb == nil || ms <= 0 {
		return
	}
	key := fmt.Sprintf(latencyKeyFmt, providerID)
	prev, err := rm.rdb.HGet(ctx, key, "ewma_ms").Float64()
	next := float64(ms)
	if err == nil && prev > 0 {
		next = latencyEWMAlpha*float64(ms) + (1-latencyEWMAlpha)*prev
	}
	rm.rdb.HSet(ctx, key, "ewma_ms", next)
}

// LatencyEWMA returns the provider's smoothed latency in ms (0 = unknown,
// which strategies treat optimistically so new providers get probed).
func (rm *RouterManager) LatencyEWMA(ctx context.Context, providerID uint) float64 {
	if rm.rdb == nil {
		return 0
	}
	v, err := rm.rdb.HGet(ctx, fmt.Sprintf(latencyKeyFmt, providerID), "ewma_ms").Float64()
	if err != nil {
		return 0
	}
	return v
}

// costPerMillion sums input+output cost for ranking under least_cost.
func (rm *RouterManager) costPerMillion(ctx context.Context, providerID uint, modelName string) float64 {
	if rm.db == nil {
		return 0
	}
	entry := getModelPriceEntry(ctx, rm.db, rm.logger, providerID, modelName)
	if entry == nil || entry.noPricing {
		return 0
	}
	return entry.inputPrice + entry.outputPrice
}

// -----------------------------------------------------------------------------
// Per-attempt audit trail (docs/design/01-routing-and-lb.md)
// -----------------------------------------------------------------------------

// AttemptRecord captures one upstream attempt for the audit failover trail.
type AttemptRecord struct {
	ProviderID uint   `json:"providerId"`
	Status     int    `json:"status"` // 0 = transport error / breaker skip
	Err        string `json:"err,omitempty"`
	LatencyMs  int64  `json:"latencyMs"`
}

type attemptTrailCtxKey struct{}

// withAttemptTrail installs a mutable attempt recorder on the context.
func withAttemptTrail(ctx context.Context) (context.Context, *[]AttemptRecord) {
	trail := &[]AttemptRecord{}
	return context.WithValue(ctx, attemptTrailCtxKey{}, trail), trail
}

func attemptTrailFromCtx(ctx context.Context) []AttemptRecord {
	if v, ok := ctx.Value(attemptTrailCtxKey{}).(*[]AttemptRecord); ok && v != nil {
		return *v
	}
	return nil
}

// recordAttempt appends one attempt to the context trail (no-op without one).
func recordAttempt(ctx context.Context, rec AttemptRecord) {
	if v, ok := ctx.Value(attemptTrailCtxKey{}).(*[]AttemptRecord); ok && v != nil {
		*v = append(*v, rec)
	}
}

// trimErr bounds error strings stored in the attempt trail.
func trimErr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// IsRetryableStatus reports whether an upstream HTTP status justifies failover
// (docs/design/01-routing-and-lb.md retryable matrix). 401/403 are NOT retried
// but should open the breaker for that provider via a failure report.
func IsRetryableStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 529:
		return true
	}
	return false
}
