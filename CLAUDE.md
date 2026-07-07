# CLAUDE.md

This file provides repo-wide guidance for Claude Code. Directory-specific guidance lives in **`backend/CLAUDE.md`** (Go gateway: architecture, Kratos conventions, hard rules) and **`frontend/CLAUDE.md`** (console: stack, page patterns, i18n rules) — read the one matching the files you are changing.

## What this project is

**ai-gateway** is a self-hosted, OpenAI-compatible AI traffic control plane in Go: virtual keys, multi-dimensional quotas, audit logging, token accounting, balance-based billing, and multi-provider routing with failover — one binary. Three target users (see `docs/01-product-vision.md`): platform teams (internal cost control), API resellers (prepaid resale), SaaS teams (embedded LLM features).

## Repository layout

```text
├── backend/    # Go gateway (module github.com/opscenter/ai-gateway) — see backend/CLAUDE.md
├── frontend/   # React+TS web console (Vite) — see frontend/CLAUDE.md
├── docs/       # Product & design suite, EN authoritative + docs/zh-CN mirror
│   ├── 01-product-vision.md · 02-gap-analysis.md · 03-roadmap.md (P0–P3, exit criteria)
│   └── design/01..10-*.md   # per-capability designs, ADR style
├── deploy/     # compose stack (deploy/compose), Prometheus, Grafana provisioning
├── .github/    # ci.yml (vet/test/lint/frontend/docker), release.yml (binaries + GHCR)
├── Dockerfile  # multi-stage: console build → embed → Go build → alpine
└── Makefile    # root orchestration: all / web / embed / backend / test / docker
```

## Cross-cutting invariants (override feature velocity)

1. **No breaking changes to `/ai/v1/*`** — OpenAI compatibility is a public contract.
2. **Migrations are additive** — `autoMigrate` only; destructive changes need a design-doc decision first.
3. **Hot-path budget** — anything > ~2 ms p99 on the proxy path runs async or is opt-in.
4. **Docs ship with code** — if implementation diverges from `docs/design/*`, update the design in the same PR (append ADR entries; don't rewrite old decisions).
5. **Bilingual parity** — user-facing docs and console strings land in en + zh together.
6. **Headless first** — every capability is an API before it is a screen; the console uses zero private endpoints.

## Build / test quickstart

```bash
make all            # console build → embed → single binary (backend/server)
cd backend && go test ./...     # fully offline (miniredis + in-memory SQLite)
cd frontend && npm run build    # tsc strict + vite
cd deploy/compose && docker compose up -d
```

Local caveat: `go test -race` fails on this Windows machine (race-runtime DLL, exit 0xc0000139) — race coverage comes from CI (Linux).

## Feature status (what exists vs what doesn't)

Maturity: ✅ implemented + tested · 🟡 partial · 🔴 designed only (see the design doc)

| Capability | Status | Notes / where |
| --- | --- | --- |
| Virtual keys, quotas, audit, model mapping, sticky sessions, IP whitelist | ✅ | P0 inherited core |
| Weighted LB + failover + circuit breaker + strategies + active health probes | ✅ | `biz/router.go`: weighted / priority / least_latency (Redis EWMA) / least_cost per key; per-mapping `fallback_chain`; per-attempt audit trail. `biz/health_probe.go`: opt-in per-provider active probing for idle-period breaker recovery (D01) |
| Metrics `/metrics`, `/healthz`, `/readyz`, Grafana dashboard, OTel tracing | ✅ | tracing opt-in via `observability.otlp_endpoint` (empty = disabled, zero overhead); span topology + force-sample header per D05 |
| Admin-token management auth | ✅ | User system intentionally skipped — SSO/OIDC to be introduced directly (D04) |
| Tenants → projects → keys, default-tenant bootstrap | ✅ | project `quota_template` inheritance ✅; tenant-scoped list filtering 🔴 (admin token = platform admin) |
| Balance billing: accounts, ledger, freeze→settle, grace/suspension, budget alerts | ✅ | alert webhook ✅ (`AIGW_ALERT_WEBHOOK`); email channel, payment gateways / subscriptions / invoices 🔴 (D03 L4) |
| Price tables + multi-currency rates | ✅ | console editor UI 🔴 |
| Usage daily rollup + stats endpoints | ✅ | console charts for timeseries 🔴 |
| Rule-based PII engine (block/redact/log) + injection heuristic | ✅ | pluggable checker chain, external engine (gRPC), outbound/stream scanning, audit-body encryption 🔴 (D06) |
| Protocol adapters | 🟡 | outbound anthropic + gemini (incl. SSE) + azure_openai ✅; Bedrock, inbound Anthropic Messages & Responses API 🔴 (D02) |
| Exact response cache + hit billing | ✅ | semantic cache 🔴 (D07); streaming responses are not cached (by design, revisit) |
| Web console | ✅ | key/provider/model-pricing management, usage charts, audit body/session/security views, settings, Playwright E2E ✅ (D08); RBAC/SSO, fallback-chain drag editor, guardrail-chain builder remain out of scope (no user system) |
| Multi-DB (mysql/postgres/sqlite) | ✅ | CI includes a PostgreSQL+Redis boot smoke job |
| Deployment | ✅ | compose + Helm chart + `doctor`/`rekey` CLI (D10); K8s operator deferred by design |
| Engineering | ✅ | tests+CI+release, `api/openapi.yaml`, coverage regression gate (target: 60% on biz), `sync-models` endpoint |
| MCP gateway, plugins/hooks, event bus, Batch/Files APIs | 🔴 | P3 (D09) |

When picking up new work, prefer closing a 🟡 row before starting a 🔴 one, and check the corresponding `docs/design/` document first — most decisions are already made there.

## Notes that bite

- `go.mod` module is `github.com/opscenter/ai-gateway` — keep it; do not "fix" to other names.
- `cmd/server/wire_gen.go` is hand-maintained; keep it in sync with `wire.go` and the `ProviderSet`s.
- GORM `default:` tags override zero-value fields on `Create` (weight 0, grace_hours 0…) — seed explicitly.
- `backend/internal/console/dist/` holds only a placeholder `index.html` in git; never commit real console assets there.
- Chinese comments/log messages are project convention; key terms: 虚拟 Key = virtual key, 提供方 = provider, 配额 = quota, 审计 = audit, 熔断 = circuit breaker, 结算 = settlement.
- SQL portability across mysql/postgres/sqlite: `CONCAT()` is not valid SQLite syntax (use a dialect check, see `auditSessionExpr` in `biz/gateway.go`); `MIN()`/`MAX()` over a datetime column loses type affinity on SQLite specifically, breaking a `time.Time` `Scan` (known caveat on `ListAuditSessions`, not fixed — SQLite is demo-only).
