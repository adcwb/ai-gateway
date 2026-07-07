package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

// Exact-match response cache (docs/design/07-caching-strategies.md, P2-4).
// Off by default; enabled per key via cache_config. Semantic caching is a
// later phase (requires a vector-capable Redis).
//
// Position in the pipeline: after guardrails (a blocked request is never
// served from cache), before routing (a hit skips upstream, token-quota
// commitment and cost — billed per the key's hit policy instead).

const (
	respCacheKeyFmt     = "ai:gw:cache:x:%s"
	respCacheDefaultTTL = 300 // seconds
	respCacheMaxBody    = 64 << 10
	respCacheLookupWait = 20 * time.Millisecond // budget: a slow cache must not slow misses

	CacheBillingFree     = "free"
	CacheBillingDiscount = "discount"
	CacheBillingFull     = "full"

	semanticCacheDefaultTTL       = 3600 // seconds
	semanticCacheDefaultThreshold = 0.95
)

// keyCacheConfig is the shape of AIVirtualKey.CacheConfig. Semantic fields are
// additive to the exact-cache shape shipped in P2-4 (docs/design/07-caching-
// strategies.md "Two caches, one interface") — both are off by default and
// enabled independently per key.
type keyCacheConfig struct {
	ExactEnabled    bool   `json:"exactEnabled"`
	TTLSec          int    `json:"ttlSec"`
	BillingPolicy   string `json:"billingPolicy"`
	DiscountPercent int    `json:"discountPercent"`

	SemanticEnabled   bool    `json:"semanticEnabled"`
	SemanticThreshold float64 `json:"semanticThreshold"` // cosine similarity, default 0.95
	SemanticTTLSec    int     `json:"semanticTtlSec"`
}

func parseCacheConfig(key *model.AIVirtualKey) (keyCacheConfig, bool) {
	var cfg keyCacheConfig
	if len(key.CacheConfig) == 0 {
		return cfg, false
	}
	if err := json.Unmarshal(key.CacheConfig, &cfg); err != nil {
		return cfg, false
	}
	if cfg.TTLSec <= 0 {
		cfg.TTLSec = respCacheDefaultTTL
	}
	if cfg.BillingPolicy == "" {
		cfg.BillingPolicy = CacheBillingFree
	}
	if cfg.SemanticThreshold <= 0 || cfg.SemanticThreshold > 1 {
		cfg.SemanticThreshold = semanticCacheDefaultThreshold
	}
	if cfg.SemanticTTLSec <= 0 {
		cfg.SemanticTTLSec = semanticCacheDefaultTTL
	}
	// The bool return gates the exact-cache path specifically; semantic
	// cache is gated independently by cfg.SemanticEnabled at its call site,
	// since the two caches operate on different digests/backends.
	return cfg, cfg.ExactEnabled
}

// cachedResponse is the stored IR-level value plus provenance.
type cachedResponse struct {
	Body       json.RawMessage `json:"body"` // complete OpenAI-format response
	Prompt     int             `json:"prompt"`
	Completion int             `json:"completion"`
	CacheRead  int             `json:"cacheRead"`
	ProviderID uint            `json:"providerId"`
	Model      string          `json:"model"`
	CreatedAt  int64           `json:"createdAt"`
}

// cacheableRequest gates what may be cached: chat/embeddings, no tools,
// n<=1, bounded size (docs/design/07-caching-strategies.md cacheability).
func cacheableRequest(path string, body []byte) bool {
	if !strings.Contains(path, "/chat/completions") && !strings.Contains(path, "/embeddings") {
		return false
	}
	if len(body) > respCacheMaxBody {
		return false
	}
	var probe struct {
		Tools json.RawMessage `json:"tools"`
		N     int             `json:"n"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	if len(probe.Tools) > 0 && string(probe.Tools) != "null" {
		return false
	}
	return probe.N <= 1
}

// respCacheKey computes the scope-prefixed normalized digest:
// tenant + resolved model + generation params, minus non-semantic fields.
// Go's map marshaling sorts keys, giving canonical JSON for free.
func respCacheKey(tenantID uint, resolvedModel string, body []byte) (string, bool) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return "", false
	}
	delete(m, "stream")
	delete(m, "stream_options")
	delete(m, "user")
	m["model"] = resolvedModel // scope by the *resolved* model, not the alias
	canonical, err := json.Marshal(m)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(append([]byte(fmt.Sprintf("t%d:", tenantID)), canonical...))
	return hex.EncodeToString(sum[:]), true
}

// cacheLookup returns a stored response or nil. Any error (Redis down, slow
// lookup) is a silent miss — the cache may never make traffic worse.
func cacheLookup(ctx context.Context, rdb *redis.Client, digest string) *cachedResponse {
	if rdb == nil || digest == "" {
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, respCacheLookupWait)
	defer cancel()
	raw, err := rdb.Get(lctx, fmt.Sprintf(respCacheKeyFmt, digest)).Bytes()
	if err != nil {
		return nil
	}
	var entry cachedResponse
	if json.Unmarshal(raw, &entry) != nil {
		return nil
	}
	return &entry
}

// cacheStore persists a successful non-streaming response (best-effort).
func cacheStore(rdb *redis.Client, digest string, entry *cachedResponse, ttlSec int) {
	if rdb == nil || digest == "" || entry == nil {
		return
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rdb.Set(ctx, fmt.Sprintf(respCacheKeyFmt, digest), raw, time.Duration(ttlSec)*time.Second)
}

// writeCachedResponse serves a hit — as JSON, or replayed as a synthetic
// SSE stream when the client asked for streaming (clients built for
// streaming must not break because a cache answered). cacheType is "exact"
// or "semantic" (docs/design/07-caching-strategies.md hit-path header).
func writeCachedResponse(w http.ResponseWriter, entry *cachedResponse, isStream bool, modelName, cacheType string) {
	w.Header().Set("X-AIGW-Cache", "hit-"+cacheType)
	if !isStream {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(entry.Body)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	var full struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(entry.Body, &full) != nil || len(full.Choices) == 0 {
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage json.RawMessage) {
		chunk := map[string]interface{}{
			"id":     full.ID,
			"object": "chat.completion.chunk",
			"model":  modelName,
			"choices": []map[string]interface{}{{
				"index": 0, "delta": delta, "finish_reason": finish,
			}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)
	content := full.Choices[0].Message.Content
	const sliceSize = 120
	for i := 0; i < len(content); i += sliceSize {
		end := i + sliceSize
		if end > len(content) {
			end = len(content)
		}
		writeChunk(map[string]interface{}{"content": content[i:end]}, nil, nil)
	}
	finish := full.Choices[0].FinishReason
	if finish == "" {
		finish = "stop"
	}
	writeChunk(map[string]interface{}{}, finish, full.Usage)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// cacheHitPriceMicro applies the key's hit-billing policy to the sell price
// of the ORIGINAL usage (docs/design/07-caching-strategies.md billing table).
func cacheHitPriceMicro(fullPriceMicro int64, cfg keyCacheConfig) int64 {
	switch cfg.BillingPolicy {
	case CacheBillingFull:
		return fullPriceMicro
	case CacheBillingDiscount:
		pct := cfg.DiscountPercent
		if pct <= 0 || pct > 100 {
			pct = 50
		}
		return fullPriceMicro * int64(pct) / 100
	default:
		return 0
	}
}
