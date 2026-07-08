# D09 · Extensibility & the Agentic Future

> [中文版](../zh-CN/design/09-extensibility.md) · Part of the [ai-gateway documentation suite](../README.md)

| | |
| --- | --- |
| **Phase** | P3 (hook points may land earlier where [D06](06-security-and-guardrails.md) needs them) |
| **Depends on** | [D02 Protocol Adapters](02-protocol-adapters.md) (IR is the extension currency), [D04](04-multi-tenancy-and-auth.md) (plugin config is tenant-scoped) |
| **Depended on by** | community adapters, integrations |

## Context

Every gateway accumulates niche requirements: "log to our SIEM," "custom pricing formula," "call our approval service before expensive models," "sync spend to our ERP." Absorbing these into core makes the core unmaintainable; refusing them makes the project unadoptable. The answer is a small, stable extension surface — and it is also the future-proofing mechanism: when the next protocol era arrives (agentic tool calls, MCP), it should land as *adapters and hooks*, not rewrites. Precedents already in the codebase point the way: guardrail checkers ([D06](06-security-and-guardrails.md)), protocol adapters ([D02](02-protocol-adapters.md)), and payment gateways ([D03](03-billing-and-monetization.md)) are all compile-time registries behind interfaces — extensibility here generalizes that pattern and adds an out-of-process option.

## Hook points

Four, deliberately few — each receives/returns IR-level types, so hooks are dialect-independent:

| Hook | Timing | May | Sync? |
| --- | --- | --- | --- |
| `pre_request` | after auth+guardrails, before routing | mutate request IR, reject (with reason), annotate (labels flow to audit/billing) | sync, deadline-bounded |
| `post_response` | after upstream, before client encode (non-streaming; streaming gets terminal-event only) | mutate response IR, annotate | sync, deadline-bounded |
| `on_audit` | audit entry finalized | consume (read-only) | async |
| `on_billing` | ledger entry committed | consume (read-only) | async |

Rules mirror the guardrail chain: per-hook deadline (default 100 ms sync), per-extension fail-open/fail-closed config, panics contained, invocations counted in metrics and visible in the audit `attempts`-style trail when they mutate or reject.

## Delivery mechanisms (ADR)

- **Context:** how does third-party code attach to those hooks?
- **Options:** (a) Go compile-time registration (import + registry line, rebuild); (b) HTTP webhook out-of-process; (c) WASM in-process sandbox (wazero); (d) Go `plugin` package (.so loading); (e) embedded scripting (Lua/JS).
- **Decision:** ship **(a) + (b)**; evaluate (c) behind real demand. (d) rejected outright — Go plugins are toolchain/version-fragile and Linux-only. (e) rejected — a scripting runtime is a support surface and security surface the project doesn't want.
- **Consequences:**
  - **(a) compile-time** is how *performance-critical and trusted* extensions land (new protocol adapters, vector backends, payment gateways already work this way). Cost: a rebuild. A documented `cmd/server/extensions.go` file gives forks one blessed touch-point, keeping diffs trivial to maintain.
  - **(b) webhook** is how *integration* extensions land with zero rebuild: an extension is a URL + subscribed hooks + HMAC secret (config via management API / console settings). Sync hooks POST the IR envelope and read back `{action: pass|mutate|reject, patch…}`; async hooks are fire-and-batched. This alone covers SIEM export, approval flows, and ERP sync — the majority of real requests.
  - **(c) WASM** would give in-process speed with out-of-process safety, at the cost of an ABI to design and freeze. Revisit when webhook latency measurably blocks a class of adopters (recorded as an open question, not a promise).

## Event bus

`on_audit`/`on_billing` (plus breaker transitions and quota events) generalize into a single internal event stream with pluggable sinks — the async half of extensibility:

- **Sinks:** webhook (batched, HMAC-signed, at-least-once with retry/backoff — reusing the `AuditWorker` spill/retry machinery) and Kafka (optional build; topic per event type). Delivery cursoring in Redis keeps sinks resumable.
- **Envelope:** `{event_type, event_id (ULID), occurred_at, tenant_id, payload}` with schema versioning (`v` field) so consumers survive additive change.
- Consumers this unlocks without core changes: external fiscal invoicing ([D03](03-billing-and-monetization.md) scoped tax out), SIEM/compliance archival, usage-based CRM sync, custom alerting.

## MCP gateway

The agentic bet. MCP (Model Context Protocol) is becoming the standard way agents reach tools; tool traffic is the next thing platform teams will need to govern exactly as they govern model traffic today — same auth, same quota, same audit questions ("which agent called which tool with what arguments?").

Scope for P3:

1. **MCP proxying:** the gateway exposes virtual MCP server endpoints (Streamable HTTP transport); each maps to a registered upstream MCP server (`ai_mcp_servers`: name, transport config, auth). Clients authenticate with the same `sk-vk-*` keys — one credential system for models *and* tools.
2. **Tool-call governance:** per-key tool allowlists (mirroring model whitelists), argument-level guardrail checks (the [D06](06-security-and-guardrails.md) chain runs on tool arguments/results — injection often arrives *through tool results*), and quota dimensions extended with `QuotaDimToolCall`.
3. **Audit:** tool calls land in the audit center as first-class entries (server, tool, arguments digest, result digest, latency, agent session) — satisfying the P3 exit criterion that tool calls are visible and quota-bound.
4. **Agent sessions:** the existing session-affinity identity (`resolveGatewaySessionID`) extends to group an agent's model calls *and* tool calls into one auditable session — the console's session view then tells the whole agent story.

Explicitly not in scope: authoring/hosting MCP servers (the gateway governs, it does not implement tools), and A2A-style agent-to-agent protocols until they stabilize (the adapter architecture is the insurance policy).

## Future protocol posture

Standing policy, so each new API era is a bounded task rather than a crisis: any new provider dialect = one `OutboundAdapter`; any new client-facing surface = one `InboundCodec` + route; any new governance dimension = quota-dimension constant + audit columns (both designed additive). The IR grows additively; `Extensions` bags absorb what the IR doesn't model yet. Batch API and Files API proxying (roadmap P3-4) follow this recipe: async-job passthrough with job-ID mapping and deferred usage settlement on batch completion.

## Data model & config

| Table | Purpose |
| --- | --- |
| `ai_extensions` | registered webhook extensions: name, url, hooks json, hmac secret (encrypted), fail_mode, tenant scope, is_enabled |
| `ai_mcp_servers` | upstream MCP registrations |
| `ai_event_cursors` | per-sink delivery positions |
| `ai_virtual_keys` | `tool_whitelist json` (additive) |

## Touched code

| Location | Change |
| --- | --- |
| `internal/biz/extension/` (new) | hook dispatcher, webhook client, registry |
| `internal/biz/eventbus/` (new) | stream + sinks |
| `internal/biz/mcp/` (new) | MCP proxy, tool governance |
| `internal/biz/gateway.go` | four hook call-sites |
| `internal/server/http.go` | MCP transport routes, extension mgmt API |

## Testing & verification

- Hook semantics: deadline, fail-mode, panic containment, mutate/reject round-trips (table tests shared with the guardrail chain — same contract).
- Webhook extension conformance kit: a tiny reference extension (repo `examples/extensions/`) that CI runs against the compose stack — doubles as the community template.
- Event bus: at-least-once under sink outage (kill the sink mid-stream, assert resume without loss from cursor).
- MCP: scripted agent session (model call + 2 tool calls) appears as one audit session with tool entries; disallowed tool rejected; argument-guardrail block verified.

## Implementation notes (ADR addendum)

What shipped is the **MCP gateway** slice only (proxying + tool governance) — the generic hook dispatcher (`internal/biz/extension/`), the event bus (`internal/biz/eventbus/`), and `ai_extensions`/`ai_event_cursors` were explicitly out of scope for this round and remain 🔴.

- **Package split**: `internal/biz/mcpgw/` (new) holds the JSON-RPC 2.0 message shapes and a `Client` that forwards one message to an upstream Streamable HTTP MCP server — dependency-free w.r.t. `biz`, the same split as `guardrail`/`vectorindex`. Governance (whitelists, the guardrail chain, quota, audit) lives in `biz` (`mcp_admin.go` CRUD, `mcp_proxy.go` the handler) as the consumer.
- **Transport coverage**: only single (non-batched) JSON-RPC messages over **POST** are proxied. GET (the transport's optional server-initiated SSE stream) returns 405; DELETE (session termination) returns 204 with no server-side state to clean up — this is a stateless proxy, sessions are just an opaque `Mcp-Session-Id` mirrored to/from the upstream server. Batched (`[]`) JSON-RPC requests are rejected by `mcpgw.ParseRequest` (single-object unmarshal fails against an array) — real MCP clients overwhelmingly send one message per POST, and per-message governance/audit fan-out for a batch was judged not worth the complexity here.
- **Credential + auth model**: agents authenticate with the same `sk-vk-*` virtual keys as model traffic, via the exact same `middleware.VirtualKeyAuth.ProxyMiddleware` — "one credential system for models and tools" is implemented literally (same middleware instance, same top-level request-count quota reservation), not just credential-format parity. `ai_mcp_servers` (new table) registers upstream servers the same way `ai_providers` registers model providers: global objects, platform-admin-only mutation, optional bearer credential encrypted at rest with the same `pkg/aes.go` helper.
- **Tool governance**: `AIVirtualKey.ToolWhitelist` (new additive JSON column) mirrors `AllowedModels`' exact semantics — empty/absent = every tool the upstream exposes is allowed. Disallowed tools are rejected on `tools/call` (JSON-RPC error `-32001`, no upstream call made) **and** filtered out of `tools/list` responses, so an agent doesn't even see tools it can't call.
- **Argument/result guardrail scanning reuses the D06 chain verbatim** — the same `resolvePIIPolicy` → `buildChainForPolicy` → `guardrail.Chain.Run` path model prompts/responses go through, run against a tool call's `arguments` JSON (inbound) and a JSON `CallToolResult.content` block's text (outbound). This only activates for policies that configure `checker_chain` (the pluggable path) — a policy still on the legacy single-engine path (`RuleConfig`+`Action`, no chain) is not consulted for MCP traffic, since `mcpGuardrailScan` calls `buildChainForPolicy` directly rather than falling back to `scanPII`. Blocked arguments never reach the upstream server (JSON-RPC error `-32002`); blocked results are replaced with a `[blocked]` content block and `isError: true` rather than relaying the tool's actual output. Redaction rewrite only handles the common single-text-block result shape — a multi-block `content` array is left unrewritten (the finding is still recorded) since redacted text can't be unambiguously remapped onto multiple original blocks.
- **Quota**: tool calls consume the key's existing top-level request-count quota (the same `CheckAndReserve` reservation `VirtualKeyAuth.ProxyMiddleware` already does for every route it wraps) — a dedicated `QuotaDimToolCall` with its own Redis Lua-script bucket, as the design's point 2 calls for, was **not** built. This is a real scope cut: tool-call traffic and model-call traffic currently share one quota counter per key, not independent budgets.
- **Audit reuses the existing `ai_gateway_audit_logs` table** rather than a parallel MCP-specific one: `protocol="mcp"`, and the `model` column is overloaded to carry `"<serverName>"` (non-tool-call methods) or `"<serverName>/<toolName>"` (`tools/call`) — visible today in the console's existing Audit page without any new UI. `resolveGatewaySessionID` (unchanged) still runs, so tool calls group into the same session heuristic as model calls, though no dedicated "agent session" concept (design point 4) was built beyond that reuse.

### Batch + Files API proxy (added this round)

The "Future protocol posture" section above sketches this as a one-line recipe ("async-job passthrough with job-ID mapping and deferred usage settlement on batch completion") with no data model — unlike the MCP slice, this was designed from scratch this round rather than following a pre-existing spec.

- **Scope**: `openai_compatible` providers only. Anthropic's separate Message Batches API is not translated — a future addition, not a redesign, following this same recipe again.
- **Provider selection**: Files/Batches requests carry no `model` field, so they can't reuse model-mapping to pick a provider. `POST /ai/v1/files` and `POST /ai/v1/batches` require an `X-AIGW-Provider` header (provider name); subsequent `GET`/`DELETE`-by-id calls resolve the provider from a local shadow row instead of repeating the header, since a file/batch is a provider-scoped resource once created.
- **Data model (new, additive)**: `ai_proxy_files` (`AIProxyFile`) and `ai_batch_jobs` (`AIBatchJob`) — shadow bookkeeping only, no file bytes stored. `AIBatchJob.SettledAt` doubles as the settlement poller's work-queue predicate (`WHERE settled_at IS NULL`).
- **Deferred settlement**: `GatewayUseCase.StartBatchSettlementPoller` (60s ticker, same shape as D01's active health probes) polls non-terminal jobs' status; once `completed`, fetches the output file exactly once, sums usage per model across every JSONL result line (a batch's lines could in principle target different models), and settles one aggregate charge per model via `BillingManager.Settle` at OpenAI's published 50% batch discount. There is no prior `Admit`/freeze at submission time (usage is unknowable until the batch actually runs), so settlement constructs a zero-estimate `FreezeHandle` and lets `Settle`'s estimate-vs-actual delta do a pure debit.
- **Scoped out**: incremental settlement while a batch is still `in_progress`, per-result-line audit rows (one aggregate row per batch instead), and retrying an output-file fetch failure beyond the next sweep (left unsettled and retried next tick — "fail open on economics," the project's standing rule). Console UI for Batch/Files (provider selection, job status) remains API-only.
- **Live-verified** end-to-end against a scratch gateway instance and a hand-written mock MCP upstream (Node `http` server): `tools/list` filtering, an allowed `tools/call` forwarding correctly with the upstream's exact response body, a disallowed tool rejected with `-32001` **without ever reaching the upstream** (confirmed via a call counter in the mock server), and all three calls landing in the audit log with correct `protocol`/`model`/`statusCode`/bodies. Not exercised against a real third-party MCP server (e.g. an official reference server) or a client that uses GET/SSE or session termination.

### MCP gateway round 2: batching, GET/SSE push, dedicated quota (closes 3 of the round-1 scope cuts)

Picking back up the three gaps the round-1 addendum above called out — batched JSON-RPC, GET/SSE, and `QuotaDimToolCall` — plus console UI, which was always out of scope for an API-only slice:

- **Batched (`[]`) JSON-RPC requests are now supported.** `mcpgw.ParseBatch` detects a leading `[` and decodes into `[]*Request`, capped at `mcpMaxBatchSize = 20` messages (oversized batches are rejected whole, before any forwarding, to bound the hot path). `HandleMCPRequest`'s per-message body (whitelist → quota → guardrail → forward → response guardrail → audit) was factored into `handleOneMCPMessage`, called once per message — so a batch gets full per-message governance and one audit row per message, the fan-out the round-1 addendum said wasn't worth the complexity at the time. A message with no `id` (a JSON-RPC notification) still runs governance/forwarding/audit but contributes no entry to the response array (a lone notification gets HTTP 202 with no body, matching the Streamable HTTP spec).
- **GET/SSE server push is now proxied**, not 405'd. `handleMCPStream` forwards the client's GET to the upstream server (mirroring `Mcp-Session-Id`) and relays the response byte-for-byte with a flush after every chunk (`mcpgw.Client.ForwardStream`). This is a raw passthrough — no per-message governance runs on server-pushed content (there's no discrete JSON-RPC request/response pair to apply a whitelist or guardrail chain to); one audit row is written per connection lifetime instead of per message.
- **`QuotaDimToolCall` shipped** as `AIVirtualKey.HourlyToolCallQuota` (additive column, 0 = unlimited, same sliding-window Lua script as every other rolling quota) — `QuotaManager.CheckAndReserveToolCall` runs on every `tools/call` message, independent of the shared request-count quota `VirtualKeyAuth.ProxyMiddleware` already reserves for every route. Exceeding it returns JSON-RPC error `-32003`.
- **Console UI** landed for both gaps this addendum's round-1 text flagged: an MCP Servers admin page (`frontend/src/pages/McpServers.tsx`, CRUD mirroring the Providers page) and two new fields on the key create form (tool whitelist as a comma-separated list, hourly tool-call quota).
- **Still not built**: the generic hook dispatcher/event bus (a separate P3 item, never in this slice's scope), multi-block tool-result redaction rewrite, and an "agent session" concept beyond the existing `resolveGatewaySessionID` reuse.

### Hook dispatcher + event bus + WASM sandbox (this round)

The item the previous two rounds explicitly deferred. Per an explicit request, this round also builds the WASM sandbox ((c) in "Delivery mechanisms" above) despite that section's own hedge ("evaluate behind real demand... not a promise") — so all three delivery mechanisms (compile-time, webhook, WASM) exist for `pre_request`/`post_response`.

- **Two mechanisms, not one.** Re-reading "Hook points" and "Event bus" together: `pre_request`/`post_response` are synchronous, deadline-bounded, mutate/reject-capable — these go through a new `internal/biz/extension.Dispatcher` (compile-time hooks + `ai_extensions` webhook/WASM rows), architecturally identical to `guardrail.Chain`. `on_audit`/`on_billing` generalize directly into a new `internal/biz/eventbus.Bus` (no separate Hook interface — just `Bus.Publish(eventType, tenantID, payload)`) with pluggable sinks. The two packages don't depend on each other.
- **`internal/biz/extension/`** (dependency-free w.r.t. `biz`, like `guardrail`): `Dispatcher.RunSync` mirrors `guardrail.Chain.Run`'s exact semantics — per-hook deadline (default 100ms), fail-open/closed, panic containment via a buffered-channel + `select` (a timed-out hook's goroutine is abandoned, not killed — Go has no preemptive goroutine cancellation — but `context.WithTimeout` cascades into `WebhookHook`'s `http.NewRequestWithContext` and `WasmHook`'s `wazero.RuntimeConfig.WithCloseOnContextDone`, so both real implementations do stop promptly), first `Reject` short-circuits, `Mutate` patches compose (each hook sees the previous one's output). `Register`/`CompiledHooks` (`registry.go`) is the "one blessed touch-point" — wired from the new `cmd/server/extensions.go`, empty by default.
- **WASM ABI is deliberately the smallest thing that works**, and is now a real compatibility surface, documented as such in `wasm.go`'s doc comment: guest exports `alloc(size u32) -> ptr u32` and `handle(ptr u32, len u32) -> packed u64` (`packed = resultPtr<<32 | resultLen`); host writes the JSON `Event` into guest memory via `alloc`, calls `handle`, reads the JSON `Result` back. One `wazero.CompiledModule` per configured extension, a fresh isolated `api.Module` instantiated per call (wazero modules aren't safe for concurrent `Call`). No host-imported callback surface — a guest cannot call back into the gateway mid-execution; revisit if real plugins need it. Verification note: no `wat2wasm`/TinyGo toolchain is available in this environment, so `extension/wasm_test.go` exercises the ABI against a **hand-assembled minimal WASM binary** (a Go `[]byte` literal implementing just `alloc`/`handle`/a data segment) rather than a realistic compiled guest — sufficient to prove the host-side plumbing, not a stand-in for testing against a real guest toolchain's output.
- **`internal/biz/eventbus/`** (dependency-free w.r.t. `biz`, but does talk to `gorm`/`data/model` directly, like `vectorindex` talks to Redis): `Bus.Publish` is a non-blocking channel send (mirrors `BillingManager.Settle`'s `ledgerQ` — drop + log on a full queue, never adds latency to the request path). A batch-insert worker drains that channel into a new durable table, `ai_event_log` (mirrors `AuditWorker.processBatch`'s batch-then-flush shape); one poller goroutine per configured `Sink` then reads log rows after its own cursor (`ai_event_cursors`), delivers a batch, and only advances the cursor on success — at-least-once, with exponential backoff on failure (mirrors `audit.go`'s `esRetryWorker`).
  - **ADR correction**: this document's "Event bus" section says "cursoring in Redis," but its own "Data model & config" table lists `ai_event_cursors` as a table, not a Redis key. This round follows the table, pairing it with the durable `ai_event_log` table — a pure in-memory channel (or a channel + Redis cursor with no durable log to resume *from*) cannot actually satisfy this same document's own testing requirement ("kill the sink mid-stream, assert resume without loss from cursor") across a process restart. `eventbus/bus_test.go`'s `TestBus_CursorSurvivesRestart` verifies exactly this: a second `Bus` instance reading the same database, after the first is torn down, resumes from the saved cursor with no gap and no duplicate.
  - **Sinks shipped**: `WebhookSink` (batched, HMAC-signed, matches the design doc's "batched, HMAC-signed, at-least-once with retry/backoff") and `KafkaSink` (`github.com/segmentio/kafka-go`, topic per event type — `audit`/`billing`). Both are opt-in via `conf.Extensions` (`event_webhook_url`/`event_webhook_secret`, `kafka_brokers`) — infra-level settings that must live in config rather than a DB row, unlike `ai_extensions`.
- **Wired into the request/billing path**: `pre_request` runs in `ProxyRequest` right after the PII/guardrail check, before model resolution (reject → 4xx + audit; mutate → replaces the request body; labels merge into a new `hookLabelsCtxKey` context bag mirroring the existing `reasoningTokensCtxKey` side-channel, and land in a new additive `AIGatewayAuditLog.HookLabels` column). `post_response` mutate-capable calls sit right after the two existing `applyOutboundGuardrail` call sites (translated-dialect and identity/plain non-streaming responses) — the only two places bytes haven't reached the client yet. Streaming gets exactly one **terminal, annotate-only** call after the stream completes (`runTerminalPostResponseHook`) — a hook that tries to `Mutate` there is logged and ignored, since the "Streaming commit rule" makes rewriting already-sent bytes impossible. `on_audit` publishes from `writeAuditLog` right before the existing `uc.audit.Enqueue`; `on_billing` publishes from `BillingManager.applyLedger` right after the ledger transaction commits (not on an idempotent replay).
- **`GatewayUseCase`/`BillingManager` wiring uses setters, not constructor params**: `SetHookDispatcher`/`SetEventBus` are called once from `cmd/server/wire_gen.go`, rather than adding two more positional parameters to `NewGatewayUseCase` (already ten) and `NewBillingManager` — both are nil-safe (every call site guards `if uc.hooks != nil`/`if uc.eventBus != nil`), so this needed zero changes to the dozen existing test files that construct a `GatewayUseCase` positionally. Same pattern `vectorIndex` already uses for its own optional, post-construction field.
- **Admin API**: `ai_extensions` CRUD (`internal/biz/extension_admin.go`, mirrors `mcp_admin.go`) at `/ai/gateway/extensions`, platform-admin gated. Create/Update/Delete all call `reloadHookDispatcher`, which reloads compile-time hooks + every enabled DB row (decrypting webhook HMAC secrets, compiling WASM modules) and atomically swaps the `Dispatcher`'s hook set — no restart needed to add/change a webhook or WASM hook.
- **Reference kit**: `examples/extensions/webhook-logger/` — its own standalone Go module (repo root has no `go.mod`), a minimal HTTP server verifying `X-AIGW-Signature` and always answering `{"action":"pass"}`, satisfying both the hook-dispatcher webhook contract and the event-bus webhook-sink contract (the sink ignores the body of a 2xx response). Has its own `go test` (extracting the handler into a directly-testable function) — not wired into a docker-compose/CI job, which would be a larger infra change than implementing the feature.
- **Scoped out**: console UI for `ai_extensions` (API-only — console work is a separate, later round); breaker-transition and quota-trigger events flowing through the event bus (only the two named hook points, `on_audit`/`on_billing`, were requested — "plus breaker transitions and quota events" in this document's event-bus prose remains a documented follow-on, not silently dropped).
