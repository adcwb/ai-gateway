# 02 · Gap Analysis

> [中文版](zh-CN/02-gap-analysis.md) · Part of the [ai-gateway documentation suite](README.md)

This document is an honest inventory of what ai-gateway does today versus what it must do to serve the three user archetypes in the [Product Vision](01-product-vision.md). Every "current state" claim below was verified against the codebase; file references point at the actual implementation.

**Maturity legend:** ✅ complete · 🟡 partial · ⚪ stub (framework exists, no logic) · 🔴 missing

## Current capability inventory

| Capability | Maturity | Evidence in code |
| --- | --- | --- |
| Virtual key lifecycle (create/rotate/disable/expire, AES-256-GCM at rest, SHA-256 lookup) | ✅ | `internal/biz/gateway.go`, `internal/pkg/aes.go`, `internal/data/model/virtual_key.go` |
| Multi-dimensional quotas (daily/hourly tokens, requests, points, concurrency; per-model overrides) | ✅ | `internal/biz/quota.go` (Redis Lua sliding windows), `virtual_key_model_quota.go` |
| Audit logging (batched async, MySQL + optional ES, separate body table, session grouping) | ✅ | `internal/biz/audit.go` (worker pool, spill queue, retry) |
| OpenAI-compatible proxy (chat, embeddings, rerank, passthrough) | ✅ | `internal/biz/gateway.go`, `internal/server/http.go` |
| Model mapping (exact + regex, provider override) | ✅ | `internal/data/model/model_mapping.go`, `matchModelMapping()` |
| Session affinity (header / prompt_cache_key / content hash) | ✅ | `internal/biz/sticky_session.go` |
| Key resolution caching (L1 sync.Map + L2 Redis + pub/sub invalidation) | ✅ | `internal/biz/key_cache.go` |
| IP whitelisting | ✅ | `internal/middleware/virtual_key_auth.go` |
| Cost calculation (token → CNY → credits) | 🟡 | `internal/biz/credits.go` — calculation only; currency hardcoded to CNY |
| Load balancing | 🟡 | Random pick via `mrand.IntN` (`internal/biz/gateway.go:821,836`); `AIProvider.Weight` exists but is **never read** |
| Provider health | 🔴 | `AIProvider.IsHealthy()` returns `IsEnabled` verbatim (`internal/data/model/provider.go:35`) — no probing, no circuit breaking |
| Failover / retry to another provider | 🔴 | No retry logic on upstream failure anywhere in the proxy path |
| PII detection | ⚪ | `internal/biz/pii.go` — always passes through; action framework (block/redact/log) exists |
| Metrics / tracing / health endpoints | 🔴 | No Prometheus, no OpenTelemetry, no `/healthz` (only indirect go.mod deps) |
| Management-plane authentication | 🔴 | Management API trusts an upstream reverse proxy entirely |
| Multi-tenancy | 🔴 | Keys are a flat namespace; no tenant/project dimension |
| Web console | 🔴 | No UI of any kind |
| Native provider protocols (Anthropic/Gemini/Bedrock) | 🔴 | All providers treated as `openai_compatible` |
| Balance accounts / payments / invoices | 🔴 | Nothing beyond cost calculation |
| Tests / CI / deployment artifacts | 🔴 | Zero `*_test.go` files; no CI config; Dockerfile exists but no compose/Helm |
| Provider API key protection | 🔴 | `AIProvider.APIKey` stored as **plaintext varchar** while virtual keys are AES-encrypted — an inconsistency and a security gap |

The pattern is clear: **the data plane's governance core (keys, quotas, audit) is genuinely strong; everything that makes it resilient (routing), sellable (billing), operable (observability), and adoptable (engineering hygiene) is missing.**

## Gaps by capability domain

Seven domains, ordered roughly by how early they block adoption. Each gap states *who needs it* (archetype from the [vision](01-product-vision.md): platform team / reseller / SaaS team) and *why most deployments hit it*.

### Domain 1 · Traffic routing & resilience → [D01](design/01-routing-and-lb.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Weighted / latency-aware / cost-aware LB strategies | all | Random selection wastes the existing `Weight` field and cannot express "90% to the cheap provider, 10% canary" |
| Automatic failover with retry policy | all | Single-provider outages are weekly events; without failover the gateway *amplifies* blast radius instead of containing it |
| Circuit breaking + passive health checks | all | Retrying into a dead provider adds latency for every user; breakers shed load in milliseconds |
| Fallback chains (provider → provider → degraded model) | platform, SaaS | "gpt-4o → claude-sonnet → local llama" is the canonical resilience pattern |
| Sticky-session vs. breaker interaction | SaaS | Affinity must yield when the pinned provider trips, or sessions hard-fail |

This is the highest-priority gap: a gateway that cannot survive a provider outage is a single point of failure that customers pay to add.

### Domain 2 · Protocol adaptation → [D02](design/02-protocol-adapters.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Outbound: native Anthropic / Gemini / Bedrock / Azure calls | all | The best models are not all behind OpenAI-compatible endpoints; without translation, provider choice is fictional |
| Inbound: Anthropic Messages / Responses API entrances | SaaS, platform | Clients built on Claude SDKs or the newer OpenAI Responses API should not need rewriting |
| Usage normalization (cache tokens, reasoning tokens) | all | Billing and audit need one canonical usage model regardless of upstream dialect |

### Domain 3 · Commercialization → [D03](design/03-billing-and-monetization.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Balance accounts (prepaid/postpaid) with atomic deduction | reseller, platform | Quotas limit *rate*; only balances limit *spend*. Chargeback and resale both need an account |
| Transaction ledger (recharge/deduct/freeze/refund, idempotent) | reseller | Money movement without double-entry provenance is unauditable and unsupportable |
| Multi-currency + tiered/group pricing | reseller | CNY-hardcoding (`credits.go`) blocks every non-CNY deployment; resale margin requires prices decoupled from cost |
| Out-of-credit suspension with grace period | reseller | The enforcement step that makes prepaid real |
| Budget alerts | platform | Finance wants a warning at 80%, not a surprise at 100% |
| Subscription plans, payment gateways, invoices | reseller | The full sell-side loop: this is what turns the gateway into a business platform |

### Domain 4 · Multi-tenancy & access control → [D04](design/04-multi-tenancy-and-auth.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Management-plane authentication | all | "Assume a reverse proxy" is not shippable as open source; the first `docker run` exposes admin APIs to the network |
| Tenant → project → key hierarchy | platform, reseller | Cost attribution, quota inheritance, and isolation all hang off this tree |
| RBAC (owner/admin/member/viewer) | platform, reseller | The person who views audit logs must not be the person who reveals keys |
| OIDC / SSO | platform | Enterprise table stakes for anything with a login page |

### Domain 5 · Observability → [D05](design/05-observability.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Prometheus metrics (`/metrics`) | all | Without request/latency/token/error series, operators fly blind; also the substrate for latency-aware routing |
| `/healthz` `/readyz` | all | Kubernetes and every load balancer require them |
| OpenTelemetry tracing | platform | "Why was this request slow" needs spans across route→upstream→audit |
| Grafana dashboards shipped in-repo | all | Observability that requires assembly doesn't get used |

### Domain 6 · Security & compliance → [D06](design/06-security-and-guardrails.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| PII engine behind the existing stub | platform | The block/redact/log framework in `pii.go` is wired but detects nothing; compliance stories need it real |
| Guardrail pipeline (prompt injection, content safety) | platform, SaaS | Gateways are the natural chokepoint for org-wide AI safety policy |
| Provider API key encryption | all | Upstream keys are plaintext in MySQL today; must match the AES treatment virtual keys already get |
| Audit body encryption option | platform | Prompt bodies are the most sensitive data the gateway stores |

### Domain 7 · Engineering & ecosystem → [D10](design/10-deployment-and-ops.md)

| Gap | Who | Why it matters |
| --- | --- | --- |
| Tests + CI | all | Zero tests means every contribution is a gamble; open source without CI doesn't earn trust |
| docker-compose / Helm | all | Time-to-first-request is the top-of-funnel metric for an infra project |
| PostgreSQL / SQLite support | all | MySQL-only halves the addressable audience; SQLite makes the demo trivial |
| English docs, CONTRIBUTING, OpenAPI spec | all | The difference between a repo and a project |
| Web console → [D08](design/08-web-console.md) | all | Adoption reality: evaluators judge the console before they read the API docs |
| Extensibility hooks / MCP gateway → [D09](design/09-extensibility.md) | future | The escape hatch that keeps niche requirements out of core |

## Reading the gaps as a strategy

Sequencing follows from dependency and adoption logic, not size:

1. **Resilience and operability first (P0)** — routing/failover, metrics, admin auth, deploy artifacts, tests. These make the gateway *safe to run*; nothing else matters if it isn't.
2. **The commercial loop second (P1)** — tenancy, balances, console MVP. These make the gateway *worth running* for the underserved reseller archetype and unlock chargeback for platform teams.
3. **Differentiation third (P2)** — protocol adapters, guardrails, semantic cache, payments. These win comparisons.
4. **Future-proofing continuously (P3)** — plugins, MCP, event bus. These keep the core small while the ecosystem grows.

The phased plan with exit criteria is in the [Roadmap](03-roadmap.md).
