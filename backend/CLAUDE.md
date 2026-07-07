# CLAUDE.md — backend/

Guidance for working on the Go gateway. Repo-wide context lives in the root `CLAUDE.md`; console conventions in `frontend/CLAUDE.md`.

## Technology stack

- **Go 1.25** (bumped from 1.23 when the OpenTelemetry SDK was added; `go-version-file: backend/go.mod` in CI tracks it automatically) · **Kratos v2** (HTTP lifecycle + logging) · **Wire** (compile-time DI)
- **GORM** with MySQL (default) / PostgreSQL / SQLite (`glebarez`, pure-Go, demo only) — selected by `database.driver`
- **Redis** (go-redis v9): quotas, concurrency slots, circuit breaker, billing gate, response cache, key-cache L2, pub/sub invalidation
- **Prometheus** client on a dedicated listener; **Elasticsearch** optional for audit bodies
- Tests: **miniredis** + in-memory SQLite — the whole suite runs offline (`go test ./...`)

## Build & run

```bash
go build -o server ./cmd/server
./server -conf configs/config.yaml
go test ./...                    # no external services needed
wire ./cmd/server                # after changing any ProviderSet
```

`go test -race` does not work on this Windows machine (race runtime DLL issue, exit 0xc0000139); race runs in CI on Linux.

**Wire caveat:** `cmd/server/wire_gen.go` is hand-maintained in wire's output shape. When adding a constructor: add it to the `ProviderSet` in `biz.go`/`data.go`/`server.go`, update `wire.go`, and mirror the change manually in `wire_gen.go` (or run the wire tool).

## Request flow (proxy path)

`middleware/virtual_key_auth.go` (Bearer sk-vk-* → SHA-256 → L1/L2/DB key resolve → enabled/expiry/IP checks → quota `CheckAndReserve` + concurrency slot)
→ `service/gateway.go` → `biz.GatewayUseCase.ProxyRequest`:

1. **PII/guardrails** — `applyPIIPolicy` (`pii.go` + `pii_engine.go`): detectors + injection signatures, block/redact/log per `AIPIIPolicy`
2. **Model resolution** — mapping (exact→regex) → allowed-model whitelist → provider pool (`resolveTargetModel`)
3. **Sticky session** — breaker-aware: pinned provider with open breaker yields *without clearing* the record
4. **Model-aware quota** — `enforceModelQuota`
5. **Billing gate** — `billing.Admit`: suspension check + Redis freeze of the price estimate (fail-open without Redis); 402 on rejection
6. **Response cache** — exact-match lookup (`respcache.go`); hit ⇒ synthetic replay + hit-policy billing, skip upstream entirely
7. **Attempt loop** — candidates from `router.Candidates` (primary first, then priority tier + weighted random; mapping hits do NOT fail over); per attempt: `TryPass` (breaker) → `buildUpstreamRequest` (protocol adapter) → retryable statuses (429/5xx before any client byte) fail over, max 3 attempts
8. **Response** — anthropic providers get response/SSE translation (`protocol.go`); others stream/copy as-is
9. **Settlement** — token/credit quota commit, billing `Settle` (refund over-freeze, async ledger), `RecordUsage` daily rollup, audit enqueue, metrics, cache store (non-stream 200s only)

## Key components (one file ≈ one concern)

| File | Owns |
| --- | --- |
| `biz/gateway.go` | key CRUD, proxy orchestration, model resolution, audit writes |
| `biz/router.go` | `RouterManager`: breaker Lua state machine (`ai:gw:cb:*`), weighted candidates, `RouterEvent` persistence |
| `biz/quota.go` | `QuotaManager`: sliding-window Lua rate limits, concurrency ZSET, credit commit |
| `biz/billing.go` | `BillingManager`: accounts, freeze/settle, double-entry ledger (idempotent), grace→suspension, budget alerts, usage rollup, balance resync |
| `biz/pricing.go` | sell-side price tables (exact→regex), multi-currency credit rates |
| `biz/credits.go` | upstream **cost** pricing (per-million), `calcCredits` |
| `biz/protocol.go` | outbound dialects: openai_compatible (identity fast path), azure_openai, anthropic (incl. SSE translation) |
| `biz/respcache.go` | exact cache: normalized digest, synthetic stream replay, hit-billing policies |
| `biz/pii_engine.go` | detectors (checksum-validated) + injection signatures |
| `biz/tenant.go` | tenants/projects, default-tenant bootstrap + legacy backfill |
| `biz/audit.go` | batched async audit workers, ES spill/retry |
| `biz/key_cache.go` | L1 sync.Map + L2 Redis + pub/sub invalidation (`ai:gw:key:invalidate`) |
| `observability/` | Prometheus instrument set (cardinality rule: never per-key labels), `ReadyChecker`, OTel `SetupTracing`/`Tracer` (D05) |
| `middleware/` | `virtual_key_auth.go` (proxy), `admin_auth.go` (static admin token, constant-time), `tracing.go` (`aigw.request` root span) |
| `console/` | `embed.FS` of the built frontend; placeholder committed, `make embed` refreshes |

## Code standards (Kratos conventions — enforced in review)

### Layering

| Layer | Package | Responsibility |
| ----- | ------- | -------------- |
| **biz** | `internal/biz/` | Business rules, domain errors. No HTTP encoding, no layer-crossing imports. |
| **service** | `internal/service/` | HTTP decode → biz call → HTTP encode. No business logic; branch only on error types. |
| **data** | `internal/data/` | DB/Redis init, GORM models. |

### Errors

Business errors are `kerrors` sentinels in `internal/biz/errors.go` — never `fmt.Errorf` across layer boundaries:

```go
ErrProviderNotFound = kerrors.NotFound("PROVIDER_NOT_FOUND", "provider not found")
ErrBillingSuspended = kerrors.New(402, "BILLING_SUSPENDED", "account suspended: insufficient balance")
```

Service handlers use `failWithErr(w, err)` (extracts HTTP status via `kerrors.FromError`); `failWith(w, code, msg)` only for statically-known statuses. Attach context via `.WithMetadata()`, not string formatting. `Reason` strings are `SCREAMING_SNAKE_CASE` matching the var name sans `Err`.

### Naming

- Files: `snake_case.go`, one concern; GORM models `AI<Entity>` / `AIGateway<Entity>`, tables `ai_*` via `TableName()`
- DTOs: `<Verb><Domain>Req/Resp` in `internal/biz/dto/`
- Context keys: unexported `<noun>CtxKey struct{}` — never strings
- Redis: prefix `ai:gw:`, parameterized keys as `const ...Fmt` format strings; Lua scripts `var xxxScript = redis.NewScript(...)`
- Constants: unexported camelCase grouped by domain (`...TTL`, `...Timeout`, `...Size`, `...Fmt`); exported enums `<Domain><Category>` (`QuotaDimHourlyToken`, `BillingStatusGrace`, `PIIActionBlock`)
- Constructors `New<Type>(deps...)`; context helpers `With<Noun>` / `<Noun>FromCtx`; processing `extract/parse/normalize/resolve/mint<Thing>`

### Hard rules

1. **Hot path is sacred** — no blocking I/O added to `ProxyRequest` beyond the existing Redis calls; heavy work goes through async workers (`AuditWorker`/`BillingManager` queues are the template).
2. **Migrations are additive** — new columns/tables only; register models in `data.autoMigrate`. Watch the **GORM zero-value trap**: `default:` column tags override zero-value struct fields on `Create` (bit us twice in tests — use explicit `Update` when seeding zeros).
3. **Fail open on economics, fail closed on security** — Redis loss degrades quotas/breaker/billing to pass-through with loud logs; auth failures always reject.
4. **Streaming commit rule** — once any byte reaches the client, no failover, no retry, no rewrite.
5. **Never serialize secrets** — provider/virtual keys are AES-256-GCM at rest (`pkg/aes.go`), `json:"-"` on the columns, decrypt only into in-memory snapshots.
6. Logging via injected `log.Helper`: Info = lifecycle, Warn = recoverable/fallback, Error = needs investigation; never log in per-token loops. Chinese log/comment text is fine (project convention).

### Adding a quota dimension / provider dialect / detector

- **Quota dimension**: field on `AIVirtualKey` + `AIVirtualKeyModelQuota` → DTOs → `QuotaManager.CheckAndReserve`/`CommitTokens` Redis script + key derivation → handlers.
- **Outbound dialect**: constant in `model/provider.go` → branch in `buildUpstreamRequest` (+ response/stream translation if non-OpenAI shape) → `adapter_config` for dialect settings. Usage must normalize to (prompt, completion, cacheRead).
- **PII detector**: append to `piiDetectors` in `pii_engine.go` with a checksum validator where the identifier defines one; add positive + negative (invalid-checksum) test cases.

## Environment & config

All `configs/config.yaml` keys overridable via `AIGW_*` env vars (`conf.ApplyEnvOverrides`): `AIGW_HTTP_ADDR`, `AIGW_METRICS_ADDR`, `AIGW_DB_DRIVER`, `AIGW_DB_DSN`, `AIGW_REDIS_ADDR`, `AIGW_REDIS_PASSWORD`, `AIGW_ENCRYPTION_KEY` (exactly 32 bytes), `AIGW_ADMIN_TOKEN` (empty ⇒ open management plane + startup warning), `AIGW_OTLP_ENDPOINT` / `AIGW_OTLP_INSECURE` / `AIGW_TRACE_SAMPLE_RATIO` (tracing, empty endpoint ⇒ disabled).

## OpenTelemetry tracing (D05)

`internal/observability/tracing.go`: `SetupTracing` (called directly from `cmd/server/main.go`, not through Wire — it's a process-global concern like the logger) builds an OTLP/gRPC exporter + `ParentBased` ratio sampler when `observability.otlp_endpoint` is set; otherwise the global no-op `TracerProvider` stays in place and every `observability.Tracer.Start(...)` call is a no-op. Span topology: `aigw.request` (root, `middleware/tracing.go`) → `aigw.auth` (`virtual_key_auth.go`) / `aigw.route` / `aigw.upstream` per attempt / `aigw.settle` (all in `biz/gateway.go`), plus an async `aigw.audit.persist` span **linked** (not parented) to each batched request via the `trace_id`/`span_id` columns now on `ai_gateway_audit_logs`. Force-sample debugging: header `X-AIGW-Trace-Force` compared against `system.admin_token`.

## Active health probes (D01)

`internal/biz/health_probe.go`: `GatewayUseCase.StartActiveHealthProbes` (launched from `StartBackgroundWorkers`) ticks every 10s and, for any enabled provider with `breaker_config.activeProbeEnabled=true` whose breaker is **not** closed, calls the same `RouterManager.TryPass`/`ReportResult` pair a real attempt would against a lightweight dialect-appropriate request — closing the gap where a provider ranked behind enough healthy candidates never gets another live attempt (and so never recovers) even after its outage clears. Off by default per provider; toggle via `activeProbeEnabled`/`activeProbeIntervalSec` on the provider create/update API.

## Known gaps in this package (see root CLAUDE.md "Feature status")

SSO/OIDC (user system intentionally skipped; admin token is the only principal), tenant-scoped management queries, payment gateways/subscriptions/invoices, inbound Anthropic/Responses endpoints, Bedrock adapter (needs SigV4), semantic cache, external PII engine, audit-body encryption, ES config wiring (client is nil in wire_gen).
