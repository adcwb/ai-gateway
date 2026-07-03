# D07 · Caching Strategies

> [中文版](../zh-CN/design/07-caching-strategies.md) · Part of the [ai-gateway documentation suite](../README.md)

| | |
| --- | --- |
| **Phase** | P2 |
| **Depends on** | [D02 Protocol Adapters](02-protocol-adapters.md) (cache keys/replay operate on the IR), [D03 Billing](03-billing-and-monetization.md) (hit pricing), [D01 Routing](01-routing-and-lb.md) (cache sits before routing) |
| **Depended on by** | — |

## Context

Three cache-like mechanisms already exist, none of which caches *responses*:

- Key-resolution cache (L1/L2, `internal/biz/key_cache.go`) — resolves credentials.
- Session affinity (`sticky_session.go`) + `injectPromptCacheKey()` — makes *provider-side* prompt caches hit more often; the provider still charges (discounted cache-read tokens, already priced by `credits.go`).
- Provider prompt caching itself — upstream's feature, not ours.

What's missing is the layer that eliminates upstream calls entirely: identical or semantically equivalent requests answered from the gateway. For the SaaS archetype (FAQ-shaped traffic, retry storms, template prompts) this is a direct unit-economics lever, and it composes with — not replaces — the affinity machinery above (a semantic *miss* still benefits from prompt-cache-friendly sticky routing).

Position in the pipeline: after guardrails ([D06](06-security-and-guardrails.md) — a blocked request must never be served from cache), before routing (a hit skips routing, quota *token* commitment, and billing settlement in favor of hit-policy billing).

## Two caches, one interface

| | Exact cache | Semantic cache |
| --- | --- | --- |
| Key | SHA-256 over *normalized* IR | Embedding of the normalized prompt text |
| Match | Equality | Cosine similarity ≥ threshold (default 0.95, per-key tunable) |
| Backend | Redis (existing dependency) | Pluggable vector index |
| Default TTL | 5 min | 1 h |
| Risk | None (identical in ⇒ identical answer acceptability) | Wrong-answer risk: mitigated by threshold + opt-in |
| Default state | Off per key, one flag to enable | Off per key, explicit opt-in with threshold |

**Normalization before either key is computed** (on the [D02](02-protocol-adapters.md) IR, dialect-independent): drop non-semantic fields (`stream`, `user`, request IDs), canonicalize JSON ordering, resolve the *virtual* model name (pre-mapping — two keys mapped to the same backend still cache separately only if their resolved model differs). Scope prefix: `tenant:resolved_model:params_digest` where `params_digest` covers generation params (temperature, max_tokens, tools) — a temperature-0.2 answer must not serve a temperature-1.0 request.

Cacheability gate (checked first): no tool calls in flight, no multimodal parts (v1), `n=1`, request under a size ceiling, method is chat/completions or embeddings (embeddings are the *best* cache customers: deterministic and exact-match-friendly).

### Vector backend (ADR)

- **Context:** semantic cache needs ANN search; the project's dependency posture is "MySQL + Redis is a complete deployment."
- **Options:** (a) Redis vector sets (Redis ≥ 8 / Redis Stack); (b) embedded index (HNSW in-process, persisted to disk); (c) external vector DB (Milvus/Qdrant).
- **Decision:** a `VectorIndex` interface with (a) as the shipped default — it rides the existing Redis dependency (version-gated: feature disabled with a clear log if the connected Redis lacks vector support) — and (c) as a P3 community adapter surface. (b) rejected: per-instance indexes diverge across replicas, breaking the stateless principle.
- **Consequences:** semantic cache requires a modern Redis; deployments on old Redis keep exact cache only. Embeddings are generated **through the gateway itself** against an operator-designated embedding provider/key (same dogfooding pattern as the [D06](06-security-and-guardrails.md) LLM-judge): audited, billed at cost, and one config knob (`cache.embedding_model`).

## Hit path & streaming replay

Stored value: the IR-level final response (not provider wire bytes), plus original usage and provenance (provider, model, created_at, audit ref). On a hit:

1. Respond via the inbound codec — an Anthropic-dialect client can hit an entry created by an OpenAI-dialect client; storing IR (not wire format) is what makes this work.
2. If the client requested `stream=true`, the encoder **replays the cached completion as a synthetic stream** (chunked on token-ish boundaries with zero artificial delay) — clients built for streaming must not break when a cache answers.
3. Audit entry is written as always, marked `cache_hit=exact|semantic` with a reference to the source entry — provenance principle 7 applies to cached answers too.
4. Response header `X-AIGW-Cache: hit-exact|hit-semantic|miss` (suppressible) for client observability.

Failure containment: cache lookup errors (Redis down, index timeout > 20 ms budget) ⇒ silent miss, request proceeds normally. The cache may never make traffic worse.

## Cache-aware billing

Per-key policy, enforced at settlement ([D03](03-billing-and-monetization.md)):

| Policy | Hit charged as | Use case |
| --- | --- | --- |
| `free` (default) | 0 | internal platform: cache savings passed through |
| `discount` | configurable % of the sell price of the *original* usage | reseller: margin on infrastructure value |
| `full` | 100% of sell price | reseller maximizing margin; upstream cost is still 0 |

Quota interaction: hits consume **request-count and concurrency** quotas (they are real requests) but not **token** quotas by default (no upstream tokens moved) — flag per key for resellers who want token-quota parity. Metrics: `aigw_cache_requests_total{cache_type,outcome}` ([D05](05-observability.md)); the Economics dashboard shows "upstream cost avoided" = sum of original-usage cost on hits.

## Invalidation & controls

- TTL is the primary mechanism (short by default; correctness beats hit rate).
- Manual: management API `DELETE /ai/gateway/cache?scope=key|model|tenant` (flush by scope prefix) — the operator's escape hatch after a model/policy change.
- Automatic: model mapping changes and guardrail policy changes publish on the existing invalidation pub/sub channel pattern to flush affected scopes.
- Per-request bypass: `Cache-Control: no-cache` request header (honored only when the key has cache enabled; logged in audit).

## Data model & config

No new MySQL tables (cache state is Redis-native, per the stateless principle). Additive columns on `ai_virtual_keys`: `cache_config json` (exact on/off + ttl, semantic on/off + threshold + ttl, billing policy, token-quota flag). Redis keys follow convention: `ai:gw:cache:x:{scope_digest}` (exact entries), `ai:gw:cache:v:{tenant}` (vector index name), `ai:gw:cache:stats:{keyID}` (hit counters for the console).

## Touched code

| Location | Change |
| --- | --- |
| `internal/biz/respcache/` (new) | normalization, exact store, `VectorIndex` interface + Redis impl, replay encoder glue |
| `internal/biz/gateway.go` `ProxyRequest` | lookup after guardrails / before routing; store after successful settlement |
| `internal/biz/billing.go` | hit-policy settlement path |
| `internal/service/gateway.go` + `internal/server/http.go` | cache flush endpoint |
| `configs/config.yaml` / `conf.go` | `cache` block (embedding model, global ceilings) |

## Testing & verification

- Normalization: field-order/whitespace variants collide; any generation-param change does not. Tool-call and multimodal requests bypass.
- Cross-dialect: entry created via OpenAI codec serves an Anthropic-codec request byte-correctly in both sync and synthetic-stream modes.
- Semantic: threshold sweep on a labeled paraphrase corpus documents the precision/hit-rate trade; the default threshold must show ≥ 99% precision on the corpus.
- Failure: Redis stopped mid-load ⇒ zero request failures, all misses.
- P2 exit criterion ([Roadmap](../03-roadmap.md)): ≥ 30% hit rate on the repetitive-workload benchmark with policy-correct billing.
