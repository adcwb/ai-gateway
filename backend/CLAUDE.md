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
| `biz/protocol.go` | outbound dialects: openai_compatible (identity fast path), azure_openai, anthropic (incl. SSE translation); `buildUpstreamRequest` dispatch table |
| `biz/protocol_bedrock.go` + `biz/bedrock/` | outbound bedrock dialect (Anthropic Claude models only): SigV4 signing + AWS event-stream framing (dependency-free package, mirrors `mcpgw`), reuses `openAIToAnthropicRequest`/`anthropicToOpenAIResponse`/`translateAnthropicStream` unchanged |
| `biz/protocol_anthropic_inbound.go` + `biz/anthropic_messages.go` | inbound Anthropic Messages codec: request/response/stream translation functions + `anthropicResponseWriter` (wraps `http.ResponseWriter` so `ProxyRequest` is reused byte-for-byte unchanged) |
| `biz/protocol_responses.go` + `biz/responses_api.go` | inbound OpenAI Responses API codec, same wrapper pattern as the Anthropic Messages codec |
| `biz/batch_proxy.go` + `biz/batch_settlement.go` | Batch/Files API proxy (openai_compatible providers only): passthrough + shadow bookkeeping (`AIProxyFile`/`AIBatchJob`) + `StartBatchSettlementPoller` deferred usage settlement |
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

All `configs/config.yaml` keys overridable via `AIGW_*` env vars (`conf.ApplyEnvOverrides`): `AIGW_HTTP_ADDR`, `AIGW_METRICS_ADDR`, `AIGW_DB_DRIVER`, `AIGW_DB_DSN`, `AIGW_REDIS_ADDR`, `AIGW_REDIS_PASSWORD`, `AIGW_ENCRYPTION_KEY` (exactly 32 bytes), `AIGW_ADMIN_TOKEN` (empty ⇒ open management plane + startup warning), `AIGW_OTLP_ENDPOINT` / `AIGW_OTLP_INSECURE` / `AIGW_TRACE_SAMPLE_RATIO` (tracing, empty endpoint ⇒ disabled), `AIGW_OIDC_ISSUER` / `AIGW_OIDC_CLIENT_ID` / `AIGW_OIDC_CLIENT_SECRET` / `AIGW_OIDC_REDIRECT_URL` / `AIGW_SESSION_SECRET` (SSO, empty issuer ⇒ disabled).

## SSO/OIDC, RBAC, admin API keys (D04)

Three management-plane principal kinds resolve through one `biz.Principal` shape (`internal/biz/rbac.go`), populated by `middleware/admin_auth.go` in this order: the bootstrap `admin_token` (maps to `biz.BootstrapPrincipal()`, a synthetic platform admin — unchanged fast path, every prior deployment/test keeps working exactly as before), an admin API key (`aik-*` bearer, `internal/biz/auth.go` `ResolvePrincipalFromAdminKey`), or a session cookie (`aigw_session`, JWT issued by `AuthUseCase.CompleteLogin` after the OIDC code-flow, verified by `ResolvePrincipalFromSession`). OIDC discovery (`coreos/go-oidc` + `golang.org/x/oauth2`) is lazy — an unreachable issuer never blocks startup. Login/callback/logout/config live on the *unauthenticated* mux (`/ai/gateway/auth/*`); they're how a caller gets a session.

Four fixed roles (`model.RoleOwner/Admin/Member/Viewer`, ranked) checked via `middleware.RequireRole(w, r, tenantID, minRole)` or the route-level `middleware.RequirePlatformAdmin` wrapper (tenantID 0 — global objects). Applied so far to the RBAC table's named actions: key reveal (owner/admin of the key's own tenant), provider/price-table/model-item/credits-rate/settings mutation (platform-admin only), billing recharge/account update (owner/admin of that tenant), and member/admin-key management (owner only). Every list/read endpoint is still open to any authenticated principal — broad tenant-scoped filtering across all of them is the next increment, not yet done.

JIT user provisioning: first OIDC login creates `AIUser` (linked to an existing row by email if one predates SSO, e.g. a pre-provisioned platform admin) and upserts an `AIUserTenantRole` in `oidc_default_tenant` (default: the `default` tenant) using the first role-ranked value found in the `groups`/`roles` ID-token claims, falling back to `oidc_default_role`. `ai_admin_audit_logs` records every state-changing management call's principal/action/entity.

## OpenTelemetry tracing (D05)

`internal/observability/tracing.go`: `SetupTracing` (called directly from `cmd/server/main.go`, not through Wire — it's a process-global concern like the logger) builds an OTLP/gRPC exporter + `ParentBased` ratio sampler when `observability.otlp_endpoint` is set; otherwise the global no-op `TracerProvider` stays in place and every `observability.Tracer.Start(...)` call is a no-op. Span topology: `aigw.request` (root, `middleware/tracing.go`) → `aigw.auth` (`virtual_key_auth.go`) / `aigw.route` / `aigw.upstream` per attempt / `aigw.settle` (all in `biz/gateway.go`), plus an async `aigw.audit.persist` span **linked** (not parented) to each batched request via the `trace_id`/`span_id` columns now on `ai_gateway_audit_logs`. Force-sample debugging: header `X-AIGW-Trace-Force` compared against `system.admin_token`.

## Active health probes (D01)

`internal/biz/health_probe.go`: `GatewayUseCase.StartActiveHealthProbes` (launched from `StartBackgroundWorkers`) ticks every 10s and, for any enabled provider with `breaker_config.activeProbeEnabled=true` whose breaker is **not** closed, calls the same `RouterManager.TryPass`/`ReportResult` pair a real attempt would against a lightweight dialect-appropriate request — closing the gap where a provider ranked behind enough healthy candidates never gets another live attempt (and so never recovers) even after its outage clears. Off by default per provider; toggle via `activeProbeEnabled`/`activeProbeIntervalSec` on the provider create/update API.

## Pluggable guardrail pipeline + audit encryption (D06)

`internal/biz/guardrail/` (`checker.go`, `chain.go`, `external.go`) is dependency-free w.r.t. `biz` — `Checker` interface, `Chain` (ordered sync run with per-chain deadline + fail-open/closed, most-severe-action-wins, redact rewrites text for later checkers, block/terminate short-circuits; async checkers dispatched without blocking) and the gRPC `external` checker (`api/guardrail/v1/guardrail.proto`, generated `guardrail.pb.go`/`guardrail_grpc.pb.go`) all live there. `biz` depends on `guardrail`, never the reverse: `internal/biz/pii_rules_checker.go` adapts the existing `scanPII` engine as the `pii_rules` built-in checker.

`AIPIIPolicy` gained `checker_chain json` + `fail_mode varchar(8)` (additive, table not renamed). `applyPIIPolicy` (`pii.go`) tries `buildChainForPolicy` first; a policy with no `checker_chain` configured falls through to the exact original single-engine `scanPII` behavior — the pipeline is opt-in per policy, zero behavior change until an operator configures a chain. `applyOutboundGuardrail` (also `pii.go`) mirrors the chain against the assistant's reply for **non-streaming responses only** (identity + translated anthropic/gemini dialects); `gateway.go`'s non-streaming response path was restructured (`WriteHeader` moved after the guardrail check, `Content-Length` stripped) so a block/redact finding can still change the committed bytes. Streaming outbound scanning (the design's sliding-window `log`/`terminate`-only mode) is **not built** — streaming responses bypass outbound guardrails entirely, same as before this pipeline existed.

Audit body encryption: `audit.encrypt_bodies` (config/env `AIGW_AUDIT_ENCRYPT_BODIES`) AES-GCM-encrypts prompt/response bodies via `AuditWorker.encryptBody` (`system.encryption_key`); ES-bound copies are left blank rather than storing ciphertext when enabled. `gateway.go`'s `decryptAuditBody` best-effort-decrypts and falls back to the raw value, so historical plaintext rows stay readable if encryption is enabled later. See `docs/design/06-security-and-guardrails.md`'s ADR addendum for the full list of scope decisions.

## Semantic cache (D07)

Exact-match cache (`internal/biz/respcache.go`, SHA-256 digest over normalized tenant+resolved-model+params+body) is joined by a cosine-similarity cache sitting right after it in `ProxyRequest`: `internal/biz/semantic_cache.go` embeds the request's last user message via the gateway's own outbound dialect code (`buildUpstreamRequest`, against an operator-designated embedding provider/model configured in Settings — `SettingKeyCacheEmbeddingProviderID/Model/Dim`, not `config.yaml`) and searches a pluggable `vectorindex.Index` (`internal/biz/vectorindex/`, dependency-free w.r.t. `biz` like `guardrail`). Shipped backend: `RedisIndex` over RediSearch `FT.CREATE`/`FT.SEARCH` KNN — capability-detected via a cached `FT._LIST` probe (60s TTL) so a plain (non-search) Redis silently degrades to exact-cache-only, re-checked dynamically rather than once at startup.

Both caches are opt-in per key via `AIVirtualKey.CacheConfig` (`keyCacheConfig` in `respcache.go`: `exactEnabled`/`semanticEnabled` + independent TTL/threshold/billing-policy fields), now actually reachable via the admin API (`CreateVirtualKeyReq`/`UpdateVirtualKeyReq.cacheConfig`, raw JSON pass-through — previously a gap even for the pre-existing exact cache). Semantic scope (`semanticScopeDigest`) partitions by tenant+resolved-model+generation-params **excluding prompt content**, so paraphrases within the same scope can be found as neighbors; one RediSearch index is created lazily per scope. See `docs/design/07-caching-strategies.md`'s ADR addendum for what's scoped out (cache-flush admin endpoint, console UI for cache/embedding config, live verification against a real RediSearch server — none available here).

## MCP gateway (D09)

`internal/biz/mcpgw/` (dependency-free w.r.t. `biz`, like `guardrail`/`vectorindex`): JSON-RPC 2.0 message shapes + a `Client` forwarding one message to an upstream Streamable HTTP MCP server. `internal/biz/mcp_admin.go` (CRUD for `ai_mcp_servers`, mirrors `provider.go`) + `internal/biz/mcp_proxy.go` (`HandleMCPRequest`, the governance/forwarding/audit handler) live in `biz`. Route: `POST /ai/mcp/{serverName}`, authenticated by the **same** `middleware.VirtualKeyAuth.ProxyMiddleware` model traffic uses — one credential system, one top-level request-count quota reservation, for both.

Tool governance: `AIVirtualKey.ToolWhitelist` (additive JSON column, exact semantics of `AllowedModels` — empty = unrestricted) gates `tools/call` (JSON-RPC error `-32001` if disallowed, upstream never called) and filters `tools/list` results. Tool arguments/results run through the **same D06 guardrail chain** model traffic does (`resolvePIIPolicy` → `buildChainForPolicy` → `guardrail.Chain.Run`) — only activates for policies with `checker_chain` configured (the pluggable path), not the legacy single-engine path. Audit reuses `ai_gateway_audit_logs` (`protocol="mcp"`, `model` column holds `server` or `server/tool`) rather than a parallel table — visible in the console's existing Audit page today.

Scoped out this round (see `docs/design/09-extensibility.md` ADR addendum): the generic hook dispatcher and event bus (`internal/biz/extension/`, `internal/biz/eventbus/` — separate P3 items, not requested), batched JSON-RPC requests, GET/SSE server push, a dedicated `QuotaDimToolCall` (tool calls share the key's existing request-count quota), and multi-block tool-result redaction rewrite.

## Inbound Anthropic Messages / Responses API, Bedrock outbound, Batch+Files proxy (D02/D09)

D02 originally proposed a dedicated `internal/biz/protocol/` package with independent `ChatRequest`/`Usage` IR structs and `OutboundAdapter`/`InboundCodec` interfaces. What shipped instead reuses the IR that `protocol.go`'s own doc comment already claimed existed — "the OpenAI Chat Completions wire format" — since every outbound dialect already pivoted through it and it has full test coverage. A new inbound codec only needs two edge translations (request decode, response/stream encode); outbound adapters are untouched. This keeps the fast-path guarantee (OpenAI-in→openai_compatible-out) trivially true — that path is never touched by any of this — at the cost of the IR being OpenAI-shaped rather than a neutral union, judged acceptable since billing/audit only ever cared about the fields `credits.go` already modeled.

**Anthropic Messages** (`protocol_anthropic_inbound.go` + `anthropic_messages.go`): `POST /anthropic/v1/messages`, accepts `x-api-key: sk-vk-*` (added to `middleware.VirtualKeyAuth` alongside the original `Authorization: Bearer`) or Bearer. `anthropicResponseWriter` wraps the real `http.ResponseWriter` — buffers non-streaming bodies whole and translates on `Close()` (success via `openAIResponseToAnthropicMessage`, the gateway's own `{"error":...}` bodies via the same structural check so PII-block/quota-exceeded/billing-402/failover-502 paths all come out Anthropic-shaped for free), pipes streaming bodies through `openAIStreamToAnthropicSSE` in a goroutine — so `ProxyRequest` itself is called completely unmodified (`uc.ProxyRequest(ctx, key, oaBody, anthropicResponseWriterInstance, rr)`), reused byte-for-byte for every existing outbound dialect. v1 scope matches the pre-existing outbound anthropic adapter's own limitation: text + tool content only, multimodal blocks dropped.

**Responses API** (`protocol_responses.go` + `responses_api.go`): `POST /ai/v1/responses` (registered before the `/ai/v1/` catch-all, same pattern as `/ai/v1/models`), same wrapper-around-`ProxyRequest` design as Anthropic Messages. `previous_response_id` and `store:true` are rejected outright (400) rather than silently ignored — the gateway has no server-side Responses-state persistence, so chaining/retrieval genuinely cannot work, and D02 flagged this as an open question rather than a promise. Streaming event coverage is scoped to `response.created`/`response.output_text.delta`/`response.function_call_arguments.delta`/`response.completed` — not the full Responses API event taxonomy.

**Bedrock outbound** (`protocol_bedrock.go` + `internal/biz/bedrock/`, dependency-free w.r.t. `biz` like `mcpgw`/`guardrail`/`vectorindex`): v1 scope is Anthropic Claude models on Bedrock's native Invoke API only (`model.ProviderTypeBedrock`) — Titan/Llama/Mistral/Nova have mutually incompatible invoke body shapes and are out of scope until demand shows. `bedrock.SignRequest` hand-implements AWS SigV4 (no aws-sdk-go dependency; cross-checked in `sigv4_test.go` against an independent Python reference implementation, not just re-derived in Go). `bedrock.ReadMessage` decodes the AWS event-stream binary framing `InvokeModelWithResponseStream` uses. Credentials: `AIProvider.APIKey` (the existing single encrypted-string column) holds JSON `{"accessKeyId","secretAccessKey","sessionToken"}` for `bedrock`-type providers — no new secret columns; region is a non-secret `adapterConfig.region` field like anthropic/azure's dialect settings. The sync invoke response body IS native Anthropic Messages JSON, so `anthropicToOpenAIResponse` is reused as-is; the streaming endpoint's binary frames are unwrapped and re-fed as synthetic SSE text into the **unmodified** `translateAnthropicStream` (`translateBedrockStream`) rather than a second parallel translator.

**Cache-write/reasoning token plumbing** (touched as part of this work, not a separate feature): `anthropicToOpenAIResponse`/`translateAnthropicStream` now also return `cache_creation_input_tokens` (parsed before but silently dropped); `calcCredits` gained a `cacheWriteTokens` parameter priced via `AIModelItem.CacheWritePricePerMillion` (a column that existed since D02 but was never read). `AIGatewayAuditLog.ReasoningTokens` (new additive column, informational only — reasoning tokens are already a subset of `completion_tokens` for pricing, per OpenAI's own usage shape) is populated via a `withReasoningTokens(ctx, n)`/`reasoningTokensFromCtx(ctx)` side-channel (mirrors the existing `withClientAgent` pattern) rather than a 20th positional parameter on `writeAuditLog`'s dozen call sites. The audit `Protocol` field's `totalTokens` accounting (`"openai"` vs everything else) is now driven by the **outbound** provider dialect (anthropic/bedrock ⇒ additive, since those APIs report cache tokens separately from `input_tokens`; everyone else ⇒ subset, since `cached_tokens` there is already included in `prompt_tokens`) instead of always being `"openai"` — this was latent dead code before (nothing ever passed a different value) and is a real accounting bug fix, not scope creep, once real Anthropic-native traffic exists.

**Batch + Files API proxy** (`batch_proxy.go` + `batch_settlement.go`): D09's "Future protocol posture" section sketches this as a one-line recipe ("async-job passthrough with job-ID mapping and deferred usage settlement on batch completion") with no data model — this round designed that data model from scratch rather than following a pre-existing spec. Scope: `openai_compatible` providers only (Anthropic's separate Message Batches API is not translated). Files/Batches carry no `model` field, so provider selection can't reuse model-mapping — `POST /ai/v1/files` and `POST /ai/v1/batches` require an `X-AIGW-Provider` header (provider name); subsequent `GET`/`DELETE`-by-id calls resolve the provider from a local shadow row (`AIProxyFile`/`AIBatchJob`, additive tables, no file bytes stored) instead of repeating the header. `GatewayUseCase.StartBatchSettlementPoller` (from `StartBackgroundWorkers`, 60s ticker, same shape as `StartActiveHealthProbes`) polls non-terminal jobs' status, and once a job reaches `completed`, fetches its output file exactly once, sums usage per model across every JSONL result line, and settles one aggregate charge per model via `BillingManager.Settle` at OpenAI's published 50% batch discount — constructing a zero-estimate `FreezeHandle` since there was no prior `Admit`/freeze at submission time (usage is unknowable until the batch actually runs). Explicitly out of scope: incremental settlement while still `in_progress`, per-line audit rows (one aggregate row per batch), and retrying an output-file fetch failure beyond the next sweep (left unsettled and retried — "fail open on economics").

## Known gaps in this package (see root CLAUDE.md "Feature status")

Broad tenant-scoped list filtering (most list endpoints are role-gated only where explicitly named in the RBAC table, not yet tenant-filtered by default), payment gateways/subscriptions/invoices, `previous_response_id` chaining / `store:true` for the Responses API (no server-side state persistence — rejected outright, not silently ignored), Bedrock support for non-Anthropic model families (Titan/Llama/Mistral/Nova), Anthropic Message Batches API translation (Batch/Files proxy is openai_compatible-only), ES config wiring (client is nil in wire_gen), streaming outbound guardrail scanning, `prompt_injection`/`topic_fence` as standalone checkers, external-checker result caching, cache-flush admin endpoint (D07 — TTL is the only invalidation mechanism), console UI for cache_config/embedding settings (API-only today), console UI for provider `adapter_config`/bedrock credentials and Batch/Files (API-only today), MCP hook dispatcher/event bus/`QuotaDimToolCall`/batched-request support (D09), console UI for MCP servers/tool whitelist (API-only today).
