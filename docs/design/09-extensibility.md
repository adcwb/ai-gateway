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
