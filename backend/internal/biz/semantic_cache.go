package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adcwb/ai-gateway/internal/biz/vectorindex"
	"github.com/adcwb/ai-gateway/internal/data/model"
)

// Semantic cache (docs/design/07-caching-strategies.md P2, "Two caches, one
// interface"): a cosine-similarity match against a pluggable vectorindex.Index,
// additive to and independent of the exact-match cache in respcache.go. Off by
// default; requires both a per-key opt-in (keyCacheConfig.SemanticEnabled) and
// a gateway-wide embedding provider/model (ai_settings, see settings.go) — the
// latter is deliberately an operator setting, not a per-key one, since it's
// infrastructure (which model generates embeddings), not a traffic policy.

const semanticEmbeddingTimeout = 5 * time.Second

// semanticCacheState carries the request's embedding + scope from the lookup
// phase to the store phase, so a miss doesn't re-embed the same text twice.
type semanticCacheState struct {
	embedding []float32
	scope     string
	cfg       keyCacheConfig
}

// resolveEmbeddingConfig reads the operator-designated embedding provider
// from ai_settings (Settings console page, docs/design/08-web-console.md).
// Empty/zero means semantic cache is disabled gateway-wide.
func (uc *GatewayUseCase) resolveEmbeddingConfig(ctx context.Context) (providerID uint, modelName string, dim int, ok bool) {
	idStr := uc.getSetting(ctx, model.SettingKeyCacheEmbeddingProviderID)
	modelName = uc.getSetting(ctx, model.SettingKeyCacheEmbeddingModel)
	dimStr := uc.getSetting(ctx, model.SettingKeyCacheEmbeddingDim)
	if idStr == "" || modelName == "" {
		return 0, "", 0, false
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return 0, "", 0, false
	}
	dim, _ = strconv.Atoi(dimStr)
	if dim <= 0 {
		dim = 1536 // text-embedding-3-small's dimensionality; the common default
	}
	return uint(id), modelName, dim, true
}

// vectorIndexFor lazily constructs the Redis-backed vector index the first
// time semantic cache is actually used, at the dimensionality the operator
// configured — avoids caring about embedding dim at wire-construction time,
// when no embedding model may be configured yet.
func (uc *GatewayUseCase) vectorIndexFor(dim int) vectorindex.Index {
	if uc.vectorIndex != nil && uc.vectorIndexDim == dim {
		return uc.vectorIndex
	}
	if uc.rdb == nil {
		return nil
	}
	uc.vectorIndex = vectorindex.NewRedisIndex(uc.rdb, dim)
	uc.vectorIndexDim = dim
	return uc.vectorIndex
}

// generateEmbedding calls the configured embedding provider through the
// gateway's own outbound dialect machinery (protocol.go's buildUpstreamRequest)
// — the same "dogfooding" pattern the design calls for for LLM-judge-style
// internal calls: real provider credentials, real HTTP call, cost recorded
// via the existing usage-rollup path. Scope decision: this internal call is
// NOT run through the full ProxyRequest pipeline (no guardrails/quota/audit
// row of its own) — it is an infrastructure call generating a cache key, not
// user-facing traffic, and looping back through the public proxy path would
// add real latency/complexity for no observable benefit. Its cost is still
// recorded (RecordUsage) so it's visible in usage rollups.
func (uc *GatewayUseCase) generateEmbedding(ctx context.Context, tenantID uint, text string) ([]float32, error) {
	providerID, modelName, _, ok := uc.resolveEmbeddingConfig(ctx)
	if !ok {
		return nil, fmt.Errorf("semantic cache: no embedding provider configured")
	}
	entry, err := uc.loadProviderDirect(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("semantic cache: load embedding provider: %w", err)
	}
	reqBody, err := json.Marshal(map[string]interface{}{"model": modelName, "input": text})
	if err != nil {
		return nil, err
	}
	ectx, cancel := context.WithTimeout(ctx, semanticEmbeddingTimeout)
	defer cancel()
	httpReq, err := buildUpstreamRequest(ectx, entry, http.MethodPost, "/embeddings", reqBody, false)
	if err != nil {
		return nil, fmt.Errorf("semantic cache: build embedding request: %w", err)
	}
	resp, err := newProxyClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("semantic cache: embedding call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("semantic cache: embedding provider returned %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || len(parsed.Data) == 0 {
		return nil, fmt.Errorf("semantic cache: malformed embedding response")
	}
	if uc.billing != nil && parsed.Usage.PromptTokens > 0 {
		costMicro := uc.billing.CostMicro(ctx, "CNY", providerID, modelName, parsed.Usage.PromptTokens, 0, 0, 0)
		uc.billing.RecordUsage(tenantID, 0, providerID, modelName, parsed.Usage.PromptTokens, 0, 0, costMicro, costMicro, false)
	}
	return parsed.Data[0].Embedding, nil
}

// extractEmbeddingText pulls the text to embed out of a chat/completions or
// embeddings request body: the last user message, or the embeddings
// endpoint's own "input" string. Multimodal content (array-of-parts) and
// non-string inputs are deliberately not handled — the request just skips
// semantic caching for that one call, same fail-safe posture as the rest of
// this feature.
func extractEmbeddingText(body []byte) (string, bool) {
	var withMessages struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &withMessages); err == nil {
		for i := len(withMessages.Messages) - 1; i >= 0; i-- {
			if withMessages.Messages[i].Role != "user" {
				continue
			}
			if text := strings.TrimSpace(withMessages.Messages[i].Content); text != "" {
				return text, true
			}
		}
	}
	var withInput struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(body, &withInput); err == nil && strings.TrimSpace(withInput.Input) != "" {
		return withInput.Input, true
	}
	return "", false
}

// semanticScopeDigest partitions the vector index by tenant + resolved model
// + generation params (NOT by prompt content — the whole point is finding
// semantically different-but-equivalent prompts within the same scope).
// Mirrors respCacheKey's normalization but deliberately excludes message/
// input content.
func semanticScopeDigest(tenantID uint, resolvedModel string, body []byte) (string, bool) {
	var probe struct {
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
		MaxTokens   *int            `json:"max_tokens"`
		Tools       json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false
	}
	canonical, err := json.Marshal(probe)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(append([]byte(fmt.Sprintf("t%d:m%s:", tenantID, resolvedModel)), canonical...))
	return hex.EncodeToString(sum[:]), true
}

// semanticCacheLookup embeds the request text and searches the vector index
// for a near-duplicate within the same scope. Returns (nil, nil, 0) whenever
// semantic cache doesn't apply (disabled, unavailable, non-embeddable
// request) — always a silent miss, per the "cache may never make traffic
// worse" rule. When state is non-nil but hit is nil, the caller should still
// pass state to semanticCacheStore after a real upstream response so this
// request's embedding gets indexed for future lookups.
func (uc *GatewayUseCase) semanticCacheLookup(ctx context.Context, tenantID uint, realModelName string, cacheCfg keyCacheConfig, body []byte) (state *semanticCacheState, hit *cachedResponse, similarity float64) {
	if !cacheCfg.SemanticEnabled {
		return nil, nil, 0
	}
	_, _, dim, ok := uc.resolveEmbeddingConfig(ctx)
	if !ok {
		return nil, nil, 0
	}
	idx := uc.vectorIndexFor(dim)
	if idx == nil || !idx.Available(ctx) {
		return nil, nil, 0
	}
	text, ok := extractEmbeddingText(body)
	if !ok {
		return nil, nil, 0
	}
	scope, ok := semanticScopeDigest(tenantID, realModelName, body)
	if !ok {
		return nil, nil, 0
	}
	vec, err := uc.generateEmbedding(ctx, tenantID, text)
	if err != nil || len(vec) == 0 {
		return nil, nil, 0
	}
	state = &semanticCacheState{embedding: vec, scope: scope, cfg: cacheCfg}

	matches, err := idx.Search(ctx, scope, vec, 1)
	if err != nil || len(matches) == 0 {
		return state, nil, 0
	}
	best := matches[0]
	if float64(best.Score) < cacheCfg.SemanticThreshold {
		return state, nil, float64(best.Score)
	}
	var entry cachedResponse
	if json.Unmarshal(best.Metadata, &entry) != nil {
		return state, nil, float64(best.Score)
	}
	return state, &entry, float64(best.Score)
}

// semanticCacheStore indexes this request's (already-computed) embedding
// against its response, for future semantic hits. Best-effort: failures are
// swallowed, matching cacheStore's posture for the exact cache.
func (uc *GatewayUseCase) semanticCacheStore(ctx context.Context, state *semanticCacheState, entry *cachedResponse) {
	if state == nil || len(state.embedding) == 0 || entry == nil {
		return
	}
	idx := uc.vectorIndexFor(len(state.embedding))
	if idx == nil {
		return
	}
	meta, err := json.Marshal(entry)
	if err != nil {
		return
	}
	sctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = idx.Upsert(sctx, state.scope, generateRequestID(), state.embedding, meta, state.cfg.SemanticTTLSec)
}
