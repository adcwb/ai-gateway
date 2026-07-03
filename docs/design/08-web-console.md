# D08 · Web Console

> [中文版](../zh-CN/design/08-web-console.md) · Part of the [ai-gateway documentation suite](../README.md)

| | |
| --- | --- |
| **Phase** | P1 (MVP: modules 1–3, 5 + login) · P2 (modules 4, 6, 7, 8) |
| **Depends on** | [D04 Auth/RBAC](04-multi-tenancy-and-auth.md) (login, roles), [D03 Billing](03-billing-and-monetization.md), [D05 Observability](05-observability.md), [D01 Routing](01-routing-and-lb.md) |
| **Depended on by** | — (pure client of the public API, by design principle 4: *headless first*) |

## Context

The gateway is API-only. Evaluators judge an infra project by its console before reading API docs; operators need to see breaker states and balances, not query Redis. The console's contract: **every action it performs must be possible via the documented management API** — zero private endpoints (P1 exit criterion). It is a reference client, which also keeps the API honest.

## Tech stack (ADR)

- **Context:** must ship inside the single binary, be maintainable by a Go-centric community, and support zh/en from day one.
- **Decision:** React 18 + TypeScript + Vite, shadcn/ui (+ Tailwind), TanStack Query + Router, Recharts for charts, react-i18next (en/zh resource files, per roadmap invariant 5). Source in `web/`; `make build` runs the Vite build and embeds `web/dist` via Go `embed.FS`, served at `/console/` by the Kratos HTTP server with SPA fallback. `go build` alone (no Node) still works: an empty-dist placeholder build tag keeps the binary pure-Go for API-only users.
- **Options rejected:** Vue (fine, but shadcn/React has the larger component ecosystem the team standardizes on); HTMX/server-rendered (poor fit for chart/dashboard-heavy UI); separate deployment (violates single-binary).
- **Consequences:** Node is a *build-time* dependency only; CI builds the console once and caches it. API base URL is same-origin (`/ai/gateway/...`), so no CORS surface by default.

Auth: session cookie from `/ai/gateway/auth/login` ([D04](04-multi-tenancy-and-auth.md)); every page respects the role matrix — the UI hides what RBAC forbids, and the API remains the real enforcement.

## Information architecture

```mermaid
flowchart LR
    L[Login] --> S["Shell: sidebar + tenant switcher + topbar"]
    S --> M1[1 Dashboard]
    S --> M2[2 Virtual Keys]
    S --> M3[3 Providers]
    S --> M4[4 Models & Pricing]
    S --> M5[5 Audit Center]
    S --> M6[6 Billing Center]
    S --> M7[7 Tenants & Members]
    S --> M8[8 Settings]
```

Global shell: left sidebar (the 8 modules, filtered by role), top bar with tenant switcher (platform admins see all tenants; others see their memberships), time-range picker shared by analytic views, language toggle, user menu. All list views share one pattern: server-side pagination, column filters mapping 1:1 onto API query params, CSV export of the current filter.

---

### 1 · Dashboard

```text
┌─────────────────────────────────────────────────────────────────┐
│ [Time range ▾]                                    [Auto-refresh]│
│ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐         │
│ │Requests│ │Error % │ │p95 ms  │ │Tokens  │ │Cost    │  KPI    │
│ └────────┘ └────────┘ └────────┘ └────────┘ └────────┘         │
│ ┌───────────────────────────┐ ┌───────────────────────────┐    │
│ │ Requests & errors (line)  │ │ Cost & tokens (stacked bar)│    │
│ └───────────────────────────┘ └───────────────────────────┘    │
│ ┌─────────────┐ ┌─────────────┐ ┌──────────────────────────┐   │
│ │ Top models  │ │ Top keys    │ │ Provider health strip:   │   │
│ │ (bar)       │ │ (bar)       │ │ ● openai ● azure ◐ dash  │   │
│ └─────────────┘ └─────────────┘ └──────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

Interactions: every chart segment click-throughs to the audit list pre-filtered; health dots open the provider detail with the breaker timeline. Data: **new** `GET /ai/gateway/stats/overview` + `GET /ai/gateway/stats/timeseries` (server aggregates from `ai_usage_daily` + audit; the console never scrapes Prometheus — operators use Grafana for infra views, the console shows *business* views).

### 2 · Virtual Keys

```text
┌ Keys ────────────────────────────────────────── [+ Create Key] ┐
│ filter: project ▾ status ▾ search…                             │
│ NAME      PROJECT  STATUS  QUOTA USE        EXPIRES   ⋯        │
│ team-a    core     ●on     ▓▓▓▓▓░░ 68% /d   2026-12   [⏻][✎]  │
│ ci-bot    infra    ○off    ░░░░░░░  0%      never     [⏻][✎]  │
└────────────────────────────────────────────────────────────────┘
Detail drawer: Overview | Quotas | Models | Security | Usage
```

- **Create wizard** (3 steps): ① basics (name, project, expiry) → ② quotas (per-dimension inputs prefilled from the project template; per-model override table) → ③ access (model whitelist picker, IP whitelist with CIDR validation, cache/guardrail policy selectors). On success the plaintext `sk-vk-*` shows **once** in a copy-modal (reveal later requires the Owner/Admin `reveal` action, which is operator-audit-logged).
- Detail drawer tabs map to: `GET key/quota-config`, `PUT key/quota-config`, `GET key/quota-usage` (live gauges of the Redis windows), usage charts filtered to the key.
- APIs: existing CRUD (`POST/PUT/DELETE /ai/gateway/key`, `.../list`, `.../stats`, `.../reveal`, `.../status`, quota endpoints). **New:** none for MVP — this module is why the management API already exists.

### 3 · Providers

```text
┌ Providers ─────────────────────────────────── [+ Add Provider] ┐
│ NAME     TYPE       HEALTH      WEIGHT  P95    MODELS  ⋯       │
│ openai   openai_c   ● closed    ▓▓▓ 60  820ms  14      [✎]    │
│ azure    azure_oai  ◐ half-open ▓░░ 30  1.2s   9       [✎]    │
│ dash     openai_c   ○ open      ▓░░ 10  —      22      [✎]    │
│ ── Fallback chains ──────────────────────────────────────────  │
│ gpt-4o:  openai → azure → dash:qwen-max          [edit chain]  │
└────────────────────────────────────────────────────────────────┘
```

- Health column = live breaker state (**new** `GET /ai/gateway/providers/health`, reading `RouterManager`); clicking opens a breaker-event timeline (from `ai_gateway_router_events`).
- Weight editing inline (slider + number); fallback-chain editor is an ordered drag list of provider+model pairs writing `fallback_chain` on the mapping ([D01](01-routing-and-lb.md)).
- "Sync models" button per provider → **new** `POST /ai/gateway/providers/{id}/sync-models` (fetches upstream `/models`, diffs against `AIModelItem`).
- Provider form includes type-specific `adapter_config` fields ([D02](02-protocol-adapters.md)) rendered per `ProviderType`; API key input is write-only (never echoed).
- APIs: **new** provider CRUD (`/ai/gateway/providers`…) — today providers are DB-managed only; this module forces the missing endpoints into the public API.

### 4 · Models & Pricing (P2)

Model catalog (per provider: name, cost prices, enabled) · price tables editor (sell-side, per [D03](03-billing-and-monetization.md): table list → item grid with regex pattern column and a **pattern tester** input that shows which known models match live — same matcher semantics as mappings) · model-mapping manager with the same regex tester. APIs: **new** `/ai/gateway/model-items`, `/ai/gateway/price-tables`, `/ai/gateway/model-mappings` CRUD.

### 5 · Audit Center

```text
┌ Audit ── [Logs] [Sessions] [Security] ─────────────────────────┐
│ filter: key ▾ provider ▾ model ▾ status ▾ time ▾ search        │
│ TIME     KEY     MODEL   PROV   TOK(in/out)  ms   ST  CACHE PII│
│ 10:32:01 team-a  gpt-4o  openai 1.2k/310     840  200 —    —  │
│ 10:31:58 ci-bot  gpt-4o  azure  0.9k/120     620  200 hitX —  │
│ ▸ row expand: attempts trail (openai ✗429 → azure ✓), trace id,│
│   [View bodies] (role-gated, lazy-loads audit_log_bodies)      │
└────────────────────────────────────────────────────────────────┘
```

- **Logs** tab: existing `GET /ai/gateway/audit/list`; body viewer lazy-loads and renders chat messages as a conversation with redaction markers highlighted.
- **Sessions** tab: existing `GET /ai/gateway/audit/sessions` — session groups with aggregates, expandable to member requests.
- **Security** tab: existing `GET /ai/gateway/audit/security-overview` extended with guardrail-finding breakdowns ([D06](06-security-and-guardrails.md)): findings by type/action over time, top offending keys, click-through to logs.

### 6 · Billing Center (P2)

Per-tenant: balance card (balance, frozen, mode, status incl. grace countdown) + `[Recharge]` (amount → gateway choice → payment order → QR/redirect → poll order status) · ledger table (entry type, amount, balance-after, provenance link — a `deduct` row links to its audit rows) · plans & subscription card · invoices list (generate for period → line items from `ai_usage_daily`) · budget alert config (low watermark + channels). APIs: the [D03](03-billing-and-monetization.md) surface (`/ai/gateway/billing/accounts|ledger|plans|subscriptions|orders|invoices`).

### 7 · Tenants & Members (P2)

Platform admins: tenant list/create, per-tenant status & price-table binding. Tenant owners: project tree (create/edit projects, quota templates), member list (invite by email, role dropdown per the [D04](04-multi-tenancy-and-auth.md) matrix), admin API keys management (create → show-once, scope + role). Also surfaces the operator activity log (`ai_admin_audit_logs`).

### 8 · Settings (P2)

Global routing defaults (strategy, retry budget) · guardrail policy editor (checker chain builder with per-checker config forms) · cache global config · notification channels (webhook URLs, SMTP) with test-send · credits rates editor (existing `ai_credits_rates`) · about/version/license.

---

## New API endpoints the console forces into existence

The console is the demand driver for these public additions (all follow the existing envelope + naming conventions):

| Endpoint group | Backing design |
| --- | --- |
| `GET /ai/gateway/stats/overview`, `/stats/timeseries` | [D03](03-billing-and-monetization.md) `ai_usage_daily` |
| `/ai/gateway/providers` CRUD + `/health` + `/sync-models` | [D01](01-routing-and-lb.md) |
| `/ai/gateway/model-items`, `/price-tables`, `/model-mappings` CRUD | [D03](03-billing-and-monetization.md) |
| `/ai/gateway/tenants`, `/projects`, `/users`, `/auth/*`, `/admin-keys` | [D04](04-multi-tenancy-and-auth.md) |
| `/ai/gateway/billing/*` | [D03](03-billing-and-monetization.md) |
| `/ai/gateway/guardrail-policies`, `/cache` (flush), `/settings` | [D06](06-security-and-guardrails.md), [D07](07-caching-strategies.md) |

## Touched code

| Location | Change |
| --- | --- |
| `web/` (new) | the SPA |
| `internal/server/http.go` | `/console/` embed.FS handler + SPA fallback; new API routes |
| `internal/service/*.go` | handlers for the new endpoint groups (thin, per layer rules) |
| `Makefile` / CI | `web-build` target; embed placeholder build tag |

## Testing & verification

- Playwright E2E in CI against the compose stack: login → create key → send a proxied request (scripted) → see it in audit → check dashboard counters. The P1 reseller exit flow ([Roadmap](../03-roadmap.md)) is a second scripted E2E.
- Contract check: the console's generated API client is built from the OpenAPI spec ([D10](10-deployment-and-ops.md)); CI fails if the console calls an endpoint absent from the spec — mechanically enforcing "no private endpoints."
- RBAC snapshot tests: each role renders the correct navigation and action set.
