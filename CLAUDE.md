# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**ai-gateway** is an OpenAI-compatible LLM proxy gateway built in Go. It acts as a central point for managing virtual API keys (credentials), enforcing quotas (tokens, points, concurrency), performing audit logging, and proxying requests to multiple AI providers. The gateway also supports model mapping, IP whitelisting, PII policy integration, and session affinity.

Key capabilities:

- Virtual key management with AES-256 encryption
- Multi-dimensional quota enforcement (daily/hourly tokens, points, request counts, concurrency limits)
- Model-aware per-model quotas
- OpenAI-compatible proxy with request/response transformation
- Comprehensive audit logging (MySQL + optional Elasticsearch)
- Model mapping and remapping
- Session affinity for sticky provider routing
- Local L1 cache + Redis L2 cache for key resolution
- IP whitelist enforcement
- PII detection policy framework

## Technology Stack

- **Language**: Go 1.23.0
- **Framework**: Kratos (go-kratos/kratos/v2) for HTTP server and dependency injection
- **Databases**: MySQL (GORM ORM) for transactional data, Redis for caching and rate limiting
- **Search**: Elasticsearch (optional) for audit log bodies
- **DI Tool**: Google Wire for compile-time dependency injection
- **Key Libraries**:
  - GORM with MySQL driver and datatypes
  - redis/go-redis for caching and Lua script execution
  - elastic/go-elasticsearch for optional audit indexing
  - gopkg.in/yaml.v3 for configuration

## Project Structure

The repository is split into a Go backend and a React frontend (both live in this repo); docs and deploy artifacts sit at the root.

```text
.
├── Dockerfile           # Multi-stage: frontend build → Go build (console embedded) → alpine runtime
├── Makefile             # Root orchestration: all / web / embed / backend / test / docker
├── LICENSE              # MIT
├── README.md            # English readme (README.zh-CN.md is the Chinese mirror)
├── docs/                # Product & design documentation suite (EN + zh-CN mirror)
│   ├── 01-product-vision.md / 02-gap-analysis.md / 03-roadmap.md
│   └── design/01..10-*.md   # Per-capability design docs (ADR style)
├── deploy/
│   ├── compose/docker-compose.yml   # gateway + MySQL + Redis (+ observability profile)
│   ├── prometheus/ · grafana/       # scrape config, provisioning, dashboards
├── .github/workflows/   # ci.yml (vet/test/lint/build), release.yml (binaries + GHCR image)
├── frontend/            # Web console: Vite + React 18 + TypeScript (no UI framework yet)
│   ├── src/api/client.ts    # typed client for the management API (admin token auth)
│   ├── src/i18n.ts          # bilingual (en/zh) dictionary
│   └── src/pages/           # Login, Dashboard, Keys, Providers, Audit
└── backend/             # Go gateway (module github.com/opscenter/ai-gateway)
    ├── cmd/server/
    │   ├── main.go          # Entry point; config + AIGW_* env overrides
    │   ├── wire.go          # Wire DI configuration (DO NOT EDIT directly)
    │   └── wire_gen.go      # Regenerate with: wire ./cmd/server
    ├── configs/config.yaml  # server(+metrics), database(driver+dsn), redis, ai, system(admin_token)
    └── internal/
        ├── biz/             # Business logic layer
        │   ├── gateway.go           # GatewayUseCase: key mgmt, proxy w/ failover loop
        │   ├── router.go            # RouterManager: weighted candidates, circuit breaker (Redis Lua)
        │   ├── quota.go             # QuotaManager: Redis sliding-window rate limiting
        │   ├── audit.go             # AuditWorker: batched async audit to MySQL/ES
        │   ├── provider.go          # Provider CRUD + health (API key encrypted on write)
        │   ├── credits.go           # Token → cost → credits pricing
        │   ├── key_cache.go / sticky_session.go / pii.go / client_*.go
        │   └── dto/                 # Request/response DTOs (gateway.go, provider.go)
        ├── conf/conf.go     # Config structs + ApplyEnvOverrides (AIGW_* vars)
        ├── console/         # embed.FS of the built frontend (placeholder committed;
        │                    #   `make embed` copies frontend/dist here)
        ├── data/            # data.go (driver selection: mysql/postgres/sqlite), model/
        │   └── model/       # AIProvider (+Weight/Priority), AIVirtualKey, audit, quota,
        │                    #   router_event.go (breaker transitions), etc.
        ├── middleware/      # virtual_key_auth.go (proxy), admin_auth.go (management)
        ├── observability/   # Prometheus instrument set + ReadyChecker
        ├── pkg/aes.go       # AES-256-GCM (+ tests)
        ├── server/http.go   # Routes, healthz/readyz, console mount, metrics listener
        └── service/         # HTTP handlers (gateway.go, provider.go)
```

## Architecture Overview

### Request Flow

1. **Middleware Layer** (`middleware/virtual_key_auth.go`)
   - Extracts `Authorization: Bearer sk-vk-*` token
   - Computes SHA-256 hash of token
   - Resolves virtual key from hash using L1→L2→DB fallback
   - Validates: enabled, not expired, IP whitelist, top-level quota
   - Reserves concurrency slot via Redis Lua script
   - Stores key in request context

2. **Service Layer** (`service/gateway.go`)
   - HTTP handlers for management and proxy routes
   - Decodes JSON requests, delegates to business logic, encodes JSON responses

3. **Business Logic Layer** (`internal/biz/`)
   - `GatewayUseCase`: Orchestrates entire request flow
     - PII policy application (stub)
     - Model resolution (mapping, whitelist fallback, provider pool fallback)
     - Session affinity lookup
     - Per-model quota enforcement
     - Request transformation (model name, stream options, prompt cache key injection)
     - HTTP proxy call to upstream provider
     - Response parsing (token counts, usage)
     - Quota commitment (tokens, points)
     - Audit log enqueuing
   - `QuotaManager`: Sliding-window rate limiting (Redis Lua scripts for atomicity)
   - `AuditWorker`: Batched async audit logging with optional ES spill

4. **Data Access Layer** (`internal/data/`)
   - GORM ORM for MySQL queries
   - Redis client for caching, rate limiting, pub/sub

### Key Design Patterns

**Caching Strategy**:

- L1: In-memory sync.Map (local machine, 60s TTL)
- L2: Redis (5min TTL, shared across instances)
- DB: GORM queries as fallback
- Invalidation: DB update → Redis pub/sub publish → L1 cache delete on all instances

**Rate Limiting**:

- Sliding-window with Redis hash buckets (minute-level granularity)
- Separate counters for tokens, points, requests, concurrency
- Per-model quota overrides supported via `AIVirtualKeyModelQuota` table
- Lua scripts ensure atomicity (check + add in single Redis call)

**Concurrency Control**:

- Sorted set (ZSET) in Redis with expiry-based cleanup
- Request ID generation and slot reservation on entry
- Automatic slot release on request exit (middleware defer)
- Exponential backoff if no slots available

**Audit Logging**:

- Worker pool (4 by default) processing queue
- Batching (100 records per batch, 200ms timeout)
- MySQL writes guaranteed
- Optional Elasticsearch indexing with retry queue fallback
- Request/response bodies stored in separate `audit_log_bodies` table (lazy-loaded)
- File extraction and OSS integration stub

**Model Resolution**:

- Priority: virtual key mapping → allowed model whitelist → provider pool
- Exact match first, then regex pattern matching for mappings
- Random selection as fallback
- Session affinity overrides for cross-provider load balancing

## Configuration

Edit `backend/configs/config.yaml`:

```yaml
server:
  http:
    addr: ":8080"
    timeout: 30s
  metrics:
    addr: ":9090"        # Prometheus listener; empty = disabled

database:
  driver: "mysql"         # mysql / postgres / sqlite
  dsn: "user:password@tcp(127.0.0.1:3306)/ai_gateway?charset=utf8mb4&parseTime=True&loc=Local"

redis:
  addr: "127.0.0.1:6379"
  password: ""
  db: 0

ai:
  proxy_timeout_sec: 120
  agent_timeout_sec: 600

system:
  encryption_key: "change-this-to-a-32-byte-secret!"  # Must be 32 bytes for AES-256
  admin_token: ""         # Bearer token for /ai/gateway/*; empty = open (dev only)
```

Every key can be overridden via env vars: `AIGW_HTTP_ADDR`, `AIGW_METRICS_ADDR`, `AIGW_DB_DRIVER`, `AIGW_DB_DSN`, `AIGW_REDIS_ADDR`, `AIGW_REDIS_PASSWORD`, `AIGW_ENCRYPTION_KEY`, `AIGW_ADMIN_TOKEN` (see `conf.ApplyEnvOverrides`).

## Building and Running

All Go commands run from `backend/`; frontend commands from `frontend/`.

### Backend

```bash
cd backend
go build -o server ./cmd/server
./server -conf configs/config.yaml
go test ./...            # offline: miniredis + in-memory SQLite
```

### Frontend (web console)

```bash
cd frontend
npm install
npm run dev              # dev server :5173, proxies /ai to :8080
npm run build            # tsc + vite → frontend/dist
```

### Full single-binary build (console embedded)

```bash
make all                 # root Makefile: web build → copy into backend/internal/console/dist → go build
```

### docker compose

```bash
cd deploy/compose && docker compose up -d
```

### Regenerate Wire DI

After adding new providers in `biz.ProviderSet`, `data.ProviderSet`, `service`, or `server`:

```bash
go install github.com/google/wire/cmd/wire@latest
cd backend && wire ./cmd/server
```

This regenerates `cmd/server/wire_gen.go`. Commit the result (do not edit manually).
Note: `wire_gen.go` is currently hand-maintained in wire's output shape; keep it consistent with `wire.go` when editing by hand.

## API Routes

### Management API (Bearer auth via system.admin_token when configured)

**Virtual Keys**:

- `POST /ai/gateway/key` – Create
- `GET /ai/gateway/key/list` – List with pagination
- `GET /ai/gateway/key/stats` – Statistics
- `PUT /ai/gateway/key` – Update config
- `PUT /ai/gateway/key/status` – Enable/disable
- `DELETE /ai/gateway/key` – Revoke (soft delete)
- `GET /ai/gateway/key/reveal` – Decrypt and return plaintext

**Quotas**:

- `GET /ai/gateway/key/quota-config` – View key quotas
- `PUT /ai/gateway/key/quota-config` – Update quotas + per-model overrides
- `GET /ai/gateway/key/quota-usage` – Real-time usage

**Audit Logs**:

- `GET /ai/gateway/audit/list` – Paginate audit logs with filters
- `GET /ai/gateway/audit/sessions` – Group by session, show aggregates
- `GET /ai/gateway/audit/security-overview` – PII/error stats

**Providers**:

- `POST /ai/gateway/providers` – Register upstream (API key encrypted on write)
- `GET /ai/gateway/providers` – List (API keys never serialized)
- `PUT /ai/gateway/providers` – Partial update (non-empty apiKey re-encrypts)
- `DELETE /ai/gateway/providers?id=` – Soft delete
- `GET /ai/gateway/providers/health` – Live circuit-breaker state per provider

### Proxy API (OpenAI-compatible, authenticated via Bearer sk-vk-*)

- `GET /ai/v1/models` – List models for key
- `POST /ai/v1/chat/completions` – Chat completion
- `POST /ai/v1/embeddings` – Embeddings
- `POST /ai/v1/rerank` – Reranking (DashScope-specific path rewrite)
- Any other `/ai/v1/*` route proxied to upstream

### Ops / Console (no auth)

- `GET /healthz` – Liveness (no dependency checks)
- `GET /readyz` – Readiness (DB + Redis pings, 2s cached; 503 while draining)
- `GET /metrics` – Prometheus, on the separate `server.metrics.addr` listener
- `GET /console/` – Embedded web console SPA

### Routing & Failover (docs/design/01-routing-and-lb.md)

- `RouterManager` (`internal/biz/router.go`): candidates = enabled providers offering the resolved model, primary first, fallbacks ordered by `Priority` tier then weighted random (`Weight`; 0 = drained). Model-mapping hits do NOT fail over (mapping is an instruction).
- Circuit breaker per provider in Redis (`ai:gw:cb:{id}`): closed → open after 5 failures/30s → half-open probes after 30s cooldown → closed after 2 probe successes. Redis down ⇒ fail open. Transitions persist to `ai_gateway_router_events`.
- Retry budget: up to 3 candidates; retryable = connect errors, 429/500/502/503/529 **before any byte reaches the client**; 401/403 feed the breaker but never retry the request.
- Sticky sessions yield (without clearing) when the pinned provider's breaker is open.

## Key Concepts

### Virtual Keys (Credentials)

- Format: `sk-vk-<32-byte-hex-random>`
- Stored: SHA-256 hash in DB, encrypted plaintext in DB column
- Use case: Delegate customer-facing credentials, rotate without backend changes
- Lifecycle: Enable/disable, expiry dates, IP whitelisting

### Quotas

- **Dimensions**: Daily tokens, hourly tokens, hourly requests, concurrency, daily/hourly points
- **Scope**: Per-key default, with per-model overrides via `ModelQuotas`
- **Enforcement**: At middleware (entry gate) + at proxy time (model-aware)
- **Trigger**: Commit token counts post-response, record quota events if exceeded

### Model Mapping

- Virtual model name → Real model name + Provider override
- Regex pattern support
- Enable/disable toggle per virtual key
- Solves: Versioning, provider-specific models, load balancing

### Session Affinity

- Derived from: `X-Session-ID` header → `prompt_cache_key` field → content hash (fallback)
- Duration: 1 hour in Redis
- Use: Sticky routing to same provider for cost savings + cache hits
- Scoped per virtual key

### Audit Logs

- Every request logged (success, error, rejection)
- Fields: Key, provider, model, tokens, latency, status, PII action, client IP, protocol
- Request/response bodies optional (lazy-loaded from separate table)
- Files extracted from multipart requests (stub)
- Session tracking via aggregation

### PII Policy

- Currently a **stub** (always passes through)
- Framework in place for async detection channel
- Future: Integration with dedicated PII engine via gRPC/HTTP
- Actions: Block, redact, or log

## Common Development Tasks

### Add a New Quota Dimension

1. Add field to `AIVirtualKey` and `AIVirtualKeyModelQuota` models
2. Add DTO fields to `CreateVirtualKeyReq`, `UpdateQuotaConfigReq`, `QuotaConfigItem`, `KeyQuotaUsageResp`
3. Update `QuotaManager.CheckAndReserve()` and `CommitTokens()` with new Redis script and key derivation
4. Handle in HTTP handlers (`gateway.go` service)
5. Run `go fmt ./...` and `wire ./cmd/server`

### Add a New Provider Type

1. Add `ProviderType` enum logic in `provider.go` (currently all treated as OpenAI-compatible)
2. Update path rewriting in `rewriteOpenAIPathForProvider()` if needed
3. Extend request/response transformation if API differs
4. Test with new provider's models in `model_item.go`

### Modify Request Transformation

Edit `GatewayUseCase.ProxyRequest()`:

- Model name injection: `replaceModelInBody()`
- Stream options: `injectStreamUsageOption()`
- Prompt cache: `injectPromptCacheKey()`
- Extra params: `injectModelExtraParams()`

### Debug Quota Issues

Check Redis state:

```bash
redis-cli
> KEYS "ai:gw:*"          # All gateway keys
> HGETALL ai:gw:rl:tokens:1   # Hourly token buckets for key ID 1
> ZRANGE ai:gw:slot:key:1 0 -1   # Current concurrency slots
```

## Code Standards (Kratos conventions)

This section defines how code should be written in this repository, following Kratos framework design philosophy.

### Error Handling

**Always use `github.com/go-kratos/kratos/v2/errors` for business errors, never `fmt.Errorf`.**

Business error constants live in `internal/biz/errors.go`:

```go
import kerrors "github.com/go-kratos/kratos/v2/errors"

var (
    ErrVirtualKeyNotFound = kerrors.NotFound("VIRTUAL_KEY_NOT_FOUND", "virtual key not found")
    ErrInvalidIPWhitelist = kerrors.BadRequest("INVALID_IP_WHITELIST", "invalid IP whitelist")
)
```

Kratos errors carry an HTTP status `Code` and a machine-readable `Reason`. When adding new errors:

- `kerrors.BadRequest(reason, msg)` → 400 — caller sent invalid data
- `kerrors.NotFound(reason, msg)` → 404 — resource does not exist
- `kerrors.Forbidden(reason, msg)` → 403 — auth passed but access denied
- `kerrors.New(429, reason, msg)` → rate limit / quota exceeded
- `kerrors.InternalServer(reason, msg)` → 500 — unexpected internal failure

Attach extra context with `.WithMetadata()`, not by formatting strings into the message:

```go
return ErrInvalidIPEntry.WithMetadata(map[string]string{"entry": s})
```

**Never wrap infrastructure errors into ad-hoc strings.** Surface the typed sentinel; let the caller distinguish with `kerrors.IsNotFound(err)`.

In the service layer, use `failWithErr(w, err)` (defined in `service/gateway.go`) for all biz-layer errors. It calls `kerrors.FromError(err)` to extract the correct HTTP status code automatically. Only use `failWith(w, code, msg)` when the status is known statically (e.g., `decodeJSON` returning 400).

### Layer Responsibilities

Strictly follow the three-layer separation Kratos enforces:

| Layer | Package | Responsibility |
| ----- | ------- | -------------- |
| **biz** | `internal/biz/` | Business rules, domain errors, repo interfaces. No HTTP, no SQL. |
| **service** | `internal/service/` | HTTP decode → biz call → HTTP encode. No business logic here. |
| **data** | `internal/data/` | DB/Redis access. Implement biz interfaces. Return raw errors (GORM/Redis). |

- `biz` must not import `service` or `data` (dependency inversion — biz defines interfaces, data implements them)
- `service` must not contain `if/else` business logic; branch only on error types
- `data` translates DB errors to biz-layer typed errors where meaningful (e.g., `gorm.ErrRecordNotFound` → `biz.ErrXxxNotFound`)

### Logging

Inject `log.Logger` via Wire; create `*log.Helper` at the struct level:

```go
type GatewayUseCase struct {
    logger *log.Helper
}

func NewGatewayUseCase(logger log.Logger) *GatewayUseCase {
    return &GatewayUseCase{logger: log.NewHelper(logger)}
}
```

Log levels:

- `logger.Infof(...)` — lifecycle events (start, stop, config loaded)
- `logger.Debugf(...)` — per-request diagnostic detail (disabled in production)
- `logger.Warnf(...)` — recoverable abnormal states (cache miss, retry, fallback)
- `logger.Errorf(...)` — unexpected failures that need investigation

Do not log inside tight loops or hot paths. Audit-log calls go through `AuditWorker`, not `log.Helper`.

### Wire Dependency Injection

All structs are wired via `cmd/server/wire.go`. Rules:

- Every new struct with dependencies gets a `NewXxx(deps...) *Xxx` constructor
- Add the constructor to the relevant `ProviderSet` in `biz/biz.go`, `data/data.go`, or `server/server.go`
- Run `wire ./cmd/server` after any change to a `ProviderSet` — the generated `wire_gen.go` must be committed
- Never instantiate a dependency manually inside a constructor; always receive it as a parameter

### HTTP Response Envelope

Management API (`/ai/gateway/...`) uses the internal envelope:

```json
{ "code": 0,          "data": { ... },  "msg": "ok" }   // success
{ "code": "REASON",   "msg": "human message" }           // error (HTTP status from kerrors)
```

Proxy API (`/ai/v1/...`) uses OpenAI-compatible format — **do not wrap proxy responses** in the internal envelope. The middleware `writeJSONError` uses `{"error":{"message":"..."}}` for proxy auth errors; this is intentional.

### Context Propagation

Pass `context.Context` as the first argument to every function that touches I/O (DB, Redis, HTTP). Use typed context-key structs (never bare strings/integers):

```go
type virtualKeyCtxKey struct{}

func WithVirtualKey(ctx context.Context, key *model.AIVirtualKey) context.Context {
    return context.WithValue(ctx, virtualKeyCtxKey{}, key)
}
```

### Naming Conventions

#### Files

`snake_case.go`. One primary concern per file. Helper files follow `<domain>_<aspect>.go`:

- `quota.go` → `QuotaManager`
- `key_cache.go` → key resolution cache logic
- `client_ip.go`, `client_agent.go` → single-concern helpers

#### Types and Structs

**GORM models** (`internal/data/model/`):

- Core AI-domain tables: `AI<Entity>` → `AIVirtualKey`, `AIProvider`, `AIModelItem`, `AIPIIPolicy`
- Gateway-specific tables: `AIGateway<Entity>` → `AIGatewayAuditLog`, `AIGatewayQuotaEvent`
- Table names (via `TableName()`): `ai_<entity>` or `ai_gateway_<entity>` in `snake_case` plural

**DTO types** (`internal/biz/dto/`):

- Requests: `<Verb><Domain>Req` → `CreateVirtualKeyReq`, `ListVirtualKeysReq`
- Responses: `<Verb><Domain>Resp` → `CreateVirtualKeyResp`, `VirtualKeyStatsResp`

**Business structs**: `<Domain><Role>` → `GatewayUseCase`, `QuotaManager`, `AuditWorker`

**Context key types**: unexported `<noun>CtxKey struct{}` — never use bare strings or ints as context keys:

```go
type virtualKeyCtxKey struct{}
type sessionNativeCtxKey struct{}
```

#### Constants

**Unexported constants** — `camelCase`, grouped in `const ()` blocks by domain:

| Suffix | Meaning | Examples |
| ------ | ------- | ------- |
| `TTL` | cache / slot lifetime | `auditSessionGapTTL`, `stickySessionTTL`, `keyLocalCacheTTL` |
| `Timeout` / `Interval` | timing | `auditBatchTimeout`, `auditResyncInterval` |
| `Size` / `Count` | capacity | `auditBatchSize`, `auditBatchWorkerCount` |
| `Attempts` | retry limit | `maxWaitAttempts`, `esMaxRetryAttempts` |
| `Fmt` | format string for Redis keys | `auditOpeningKeyFmt`, `stickyKeyFmt` |
| (bare) | channel names, index names | `keyCacheInvalidateCh`, `esAuditIndexName` |

**Exported constants** — `PascalCase` with a domain-prefix group (acts like an enum namespace):

```go
// Quota dimensions
QuotaDimHourlyToken, QuotaDimDailyToken, QuotaDimHourlyReq, QuotaDimConcurrency

// Quota event states
QuotaEventTriggered, QuotaEventReleased, QuotaEventReset

// Quota reset reasons
QuotaReasonWindowSlide, QuotaReasonManualReset, QuotaReasonWaitTimeout

// PII action values
PIIActionBlock, PIIActionRedact, PIIActionLog
```

Pattern: `<Domain><Category>` where category is the distinguishing qualifier.

**Biz error vars** — `Err<Domain><Condition>`:

```go
ErrVirtualKeyNotFound, ErrKeyPlaintextNotStored, ErrInvalidIPWhitelist
```

Error `Reason` strings: `SCREAMING_SNAKE_CASE` matching the var name without the `Err` prefix.

#### Variables

**Redis Lua scripts** — camelCase ending in `Script`:

```go
var acquireSlotScript = redis.NewScript(`...`)
var rollingCheckAddScript = redis.NewScript(`...`)
```

**Caches** — camelCase ending in `Cache` (for `sync.Map` / map caches):

```go
var modelPriceCache sync.Map
var mappingRegexCache sync.Map
```

**Lookup slices/maps** — descriptive camelCase:

```go
var uaSignatures = []struct{ marker, name string }{...}
var sessionHeaderNames = []string{...}
var extraParamsReservedKeys = map[string]struct{}{...}
```

#### Functions

**Constructors**: `New<Type>(deps...) *Type`

**Context helpers**:

```go
// unexported: with<Noun> + <noun>FromCtx
func withSessionNative(ctx, id) context.Context
func sessionNativeFromCtx(ctx) string

// exported: With<Noun> + <Noun>FromRequest/FromCtx
func WithVirtualKey(ctx, key) context.Context
func VirtualKeyFromRequest(r) *model.AIVirtualKey
```

**Input processing**: `extract<Thing>()`, `parse<Thing>()`, `normalize<Thing>()`

```go
extractBearerToken(r), extractNativeSessionID(key, r, body)
parseIPWhitelist(raw), parseIPEntry(entry)
NormalizeIPWhitelist(raw, requireNonEmpty)  // exported = public API
```

**Derivation / minting**: `resolve<Thing>()`, `mint<Thing>()`, `hash<Thing>()`

```go
resolveGatewaySessionID(ctx, rdb, key, body, ip)
mintSessionID(keyID, agent)
hashSessionKey(s)
```

#### Struct Tags

GORM column names use `snake_case`; JSON field names use `camelCase`:

```go
ProviderID uint `gorm:"not null;index"         json:"providerId"`
CreatedAt  time `gorm:"column:created_at;..."  json:"createdAt"`
KeyHash    string `gorm:"uniqueIndex"           json:"-"`       // hidden from API
ModelQuotas []... `gorm:"-"                    json:"modelQuotas,omitempty"` // computed, not in DB
```

#### Redis Key Format

All keys use prefix `ai:gw:` followed by domain and qualifier segments, separated by `:`:

```text
ai:gw:key:invalidate          # pub/sub invalidation channel
ai:gw:rl:tokens:<keyID>       # hourly token rate-limit hash
ai:gw:slot:key:<keyID>        # concurrency slot ZSET
ai:gw:sticky:<hash>           # sticky session record
ai:gw:osess:<hash>            # opening-session affinity key
ai:gw:audit:spill             # ES spill queue
```

Parameterized keys stored as `const` format strings ending in `Fmt`:

```go
const auditOpeningKeyFmt = "ai:gw:osess:%s"
const stickyKeyFmt       = "ai:gw:sticky:%s"
```

### go.mod Module Name

The module is declared as `github.com/opscenter/ai-gateway`. All internal imports must use this prefix. If you see `github.com/adcwb/ai-gateway` in go.mod, that is a stale name — update it back to `github.com/opscenter/ai-gateway`.

## Important Notes

1. **Encryption Key**: The `encryption_key` in config must be exactly 32 bytes. Pad or trim as needed. Used for AES-256-GCM.

2. **Database Migrations**: `data.NewDB()` calls `autoMigrate()` on all models. Tables created automatically on startup.

3. **Redis Pub/Sub**: Key cache invalidation publishes to `ai:gw:key:invalidate` channel. Listeners (all instances) delete from L1 cache on message.

4. **Async Audit**: `AuditWorker` runs 4 independent goroutines. If ES unavailable, logs spill to Redis queue and retry. Manual drain possible on startup.

5. **Wire Builds Compile-Time**: Do not call provider functions directly; Wire resolves dependencies at build time only. Always regenerate after adding providers.

6. **Chinese Comments**: Code contains Chinese comments. Key terms:
   - 虚拟 Key = Virtual Key (credential)
   - 提供方 = Provider (LLM backend)
   - 配额 = Quota
   - 审计 = Audit
   - 缓存 = Cache
