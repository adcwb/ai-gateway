# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/) and versions follow [SemVer](https://semver.org/) (v0.x until the P1 milestone completes — see [docs/03-roadmap.md](docs/03-roadmap.md)).

## [Unreleased]

### Added — P1 "Commercial Loop" (core) + P2 "Differentiation" (core)

**P1 — tenancy, billing, attribution, PII** (`docs/design/03,04,06`):

- **Tenancy** (P1-1): `ai_tenants` / `ai_projects` tables; a `default` tenant/project is auto-created on startup and legacy keys are backfilled; keys carry `tenant_id`/`project_ref_id`; tenant & project management APIs (`/ai/gateway/tenants`, `/ai/gateway/projects`).
- **Balance billing** (P1-3): opt-in per-tenant billing accounts (prepaid/postpaid, credit limit), append-only double-entry ledger with idempotency keys, Redis freeze→settle gate on the proxy path (estimate from `max_tokens`, over-freeze refunded at settlement, fail-open when Redis is down), grace-period → suspension state machine (`402 BILLING_SUSPENDED` / `INSUFFICIENT_BALANCE`), budget alerts at a configurable low watermark, periodic balance-mirror resync. APIs: `/ai/gateway/billing/recharge|account|ledger`.
- **Sell-side pricing** (P1-4): `ai_price_tables` decouple what tenants pay from upstream cost (exact + regex model patterns); credit rates generalized to any currency.
- **Usage attribution** (P1-5): `ai_usage_dailies` pre-aggregation (tenant × key × provider × model × day, tokens + cost + price + cache hits) maintained asynchronously; `/ai/gateway/stats/overview` and `/stats/timeseries` power the console.
- **Rule-based PII engine** (P1-6): the `pii.go` stub is now real — CN resident ID (with GB 11643 checksum), CN mobile, bank card (Luhn), email, IPv4, API-key/secret detectors plus a prompt-injection signature heuristic; block / redact (type-preserving masks) / log actions honored per `AIPIIPolicy` binding with default-policy fallback.

**P2 — protocol adapters, response cache**:

- **Anthropic outbound adapter** (P2-1/P2-3): providers with `provider_type: anthropic` are called natively — OpenAI→Messages request translation (system lifting, tool_use/tool_result mapping, required `max_tokens`), response translation back to OpenAI format, and full SSE stream translation (indexed tool-call deltas, terminal usage chunk, `[DONE]`); usage normalized (input/output/cache-read) into audit, quotas and billing.
- **Azure OpenAI outbound adapter**: `api-key` auth + `api-version` query (configurable via the new `adapter_config` provider column).
- **Exact-match response cache** (P2-4): opt-in per key (`cache_config`), normalized request digest (field order / `stream` / `user` insensitive, tenant + resolved-model scoped), synthetic SSE replay for streaming clients, `X-AIGW-Cache` header, cache-aware billing (`free`/`discount`/`full` hit policies), silent-miss failure containment.

**Console**: new Tenants page (tenant/project creation, billing summary), Billing page (balance card, enable/disable billing, recharge, ledger), 7-day usage section on the dashboard (requests/tokens/billed credits/cache hits/top models).

### Not yet implemented (designed, tracked for later)

Users/RBAC and OIDC (P1-2/P2-9 — admin token remains the single principal), payment gateways/subscriptions/invoices (P2-7), inbound Anthropic Messages endpoint and Gemini/Bedrock adapters, semantic cache, external PII engine, OpenTelemetry tracing (P2-6).

### Added — P0 "Open-Source Ready" milestone

- **Routing & resilience** (`docs/design/01-routing-and-lb.md`): `RouterManager` with weighted candidate ordering (activates `AIProvider.Weight`), priority tiers, automatic retry/failover across providers (up to 3 attempts, nothing retried after bytes reach the client), Redis-shared circuit breaker (closed → open → half-open with probe slots), breaker-aware session affinity, breaker transition events persisted to `ai_gateway_router_events`.
- **Observability** (`docs/design/05-observability.md`): Prometheus metrics on a dedicated listener (`aigw_requests_total`, latency histograms, token counters, upstream attempts, failovers, breaker state), `/healthz` and `/readyz` (dependency pings, drain-aware), Grafana overview dashboard + compose observability profile.
- **Management-plane auth** (`docs/design/04-multi-tenancy-and-auth.md`): static admin bearer token (`system.admin_token` / `AIGW_ADMIN_TOKEN`) guarding all `/ai/gateway/*` routes; constant-time comparison; loud warning when unset.
- **Provider management API**: CRUD + `GET /ai/gateway/providers/health` with live breaker state; provider API keys encrypted (AES-256-GCM) on write.
- **Multi-database**: `database.driver` selects MySQL (default), PostgreSQL, or SQLite (demo tier).
- **Web console** (`frontend/`, `docs/design/08-web-console.md`): React + TypeScript SPA — login, dashboard with provider health, keys, providers, audit views; bilingual (en/zh); embedded into the binary at `/console/` via `embed.FS`.
- **Config**: `AIGW_*` environment overrides for all secrets and endpoints.
- **Engineering**: unit tests (breaker state machine, candidate ordering, pricing math, AES round-trip) running fully offline; GitHub Actions CI (vet, race tests, lint, frontend build, docker build) and release workflow (multi-platform binaries + GHCR multi-arch image); one-command docker-compose stack; issue/PR templates; CONTRIBUTING/SECURITY docs.

### Changed

- Repository restructured into `backend/` (Go) and `frontend/` (console) with docs at the root.
- `/ai/gateway/*` now requires the admin token when configured (**breaking** for previously open deployments — set `AIGW_ADMIN_TOKEN` and send it as a Bearer header).
- Upstream failures no longer propagate immediately: retryable errors (connect errors, 429/5xx before first byte) fail over to the next healthy provider.

### Documentation

- Full bilingual product & design suite under `docs/`: vision, gap analysis, phased roadmap (P0–P3), and ten design documents (routing, protocol adapters, billing, multi-tenancy, observability, security/guardrails, caching, console, extensibility/MCP, deployment).
