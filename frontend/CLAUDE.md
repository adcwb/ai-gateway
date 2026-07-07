# CLAUDE.md — frontend/

Guidance for working on the web console. Repo-wide context lives in the root `CLAUDE.md`; backend conventions in `backend/CLAUDE.md`.

## Stack & structure

Vite + React 18 + TypeScript (strict) + react-router-dom. **Deliberately no UI framework** — the design system is hand-rolled ("Signal Terminal": ink canvas, signal-teal accent, monospace data) in `src/styles.css` + `src/components/ui.tsx`; discuss before adding any dependency. Design reference: `docs/design/08-web-console.md`.

```text
src/
├── api/client.ts        # fetch wrapper + ALL shared API types; envelope + admin-token auth;
│                        #   useAsync<T>() — race-safe fetching with optional polling
├── i18n.ts              # bilingual dictionary (en/zh) — dependency-free t(key, lang)
├── App.tsx              # auth guard, grouped sidebar shell (nav eyebrows), routes
├── styles.css           # the entire design system (CSS variables, cards, tables, forms)
├── components/
│   ├── ui.tsx           # Icon, Skeleton/TableSkeleton, Spinner, Live, EmptyState,
│   │                    #   ErrorBanner, StatCard/StatValue, AreaChart, HttpStatus
│   └── ErrorBoundary.tsx
└── pages/               # Dashboard, Keys, Providers, Audit, Tenants, Billing, Login
```

Note: `index.html` preloads Inter/JetBrains Mono from Google Fonts — inside the embedded single-binary console this fails gracefully to the system font stack (offline deployments lose the custom fonts, nothing breaks).

## Build & dev

```bash
npm run dev      # :5173, proxies /ai → http://127.0.0.1:8080 (vite.config.ts)
npm run build    # tsc -b && vite build → dist/
```

`make embed` at the repo root copies `dist/` into `backend/internal/console/dist` for the single-binary build. Only the placeholder `index.html` is committed there — never commit real build assets. Deployed base path is `/console/` (`base` in vite.config.ts, `basename` in main.tsx — keep them in sync).

## Hard rules

1. **Pure client of the documented management API** — zero private endpoints. If a page needs data the API doesn't expose, add the endpoint to the backend first (envelope + naming conventions per `backend/CLAUDE.md`).
2. **Every user-facing string** goes through `src/i18n.ts` with both `en` and `zh` values. No hardcoded literals in JSX.
3. **All API types live in `api/client.ts`** — mirror backend DTO JSON shapes (camelCase). Money is integer micro-credits: render with the `credits()` helper (÷ 1_000_000), never raw.
4. Envelope handling is centralized in `request<T>()`: success `{code: 0, data, msg}`, error `{code: REASON, msg}` with the HTTP status from kerrors — components only `try/catch` and show `(e as Error).message`.
5. Auth = admin token in localStorage sent as `Authorization: Bearer`; `Login.tsx` validates it against `GET /ai/gateway/key/stats`. There is no user system yet (see gaps).
6. Follow the shared list-page pattern: `.topbar` (eyebrow + title + actions) → `ErrorBanner` → `.cards` stats → `.table-wrap > table` with `TableSkeleton` while loading and `EmptyState` when empty; fetch through `useAsync` (pass its `signal` to `api.*`), never ad-hoc `useEffect` fetching.

## Current pages vs designed scope

Implemented: Dashboard (key stats, 7-day usage, daily SVG charts, provider health), Keys (create form with show-once plaintext, enable/disable, reveal, revoke), Providers (create/edit forms for all four dialects, sync-models, delete, live breaker, active-probe toggle), Models & Pricing (model cost catalog CRUD, price-table + item CRUD, pattern tester), Audit (Logs/Sessions/Security tabs; row-expand shows trace/session id, failover trail, PII action, lazy request/response body viewer), Tenants (list/create tenant+project), Billing (balance, enable/disable, recharge, ledger), Settings (credits-rate CRUD, alert-webhook override + test-send, about panel), Users & Access (per-tenant member role editor, admin API key CRUD with show-once plaintext). Login shows a "Sign in with SSO" button when `GET /ai/gateway/auth/config` reports OIDC configured; `App.tsx` probes `GET /ai/gateway/auth/me` on mount so a session cookie (not just the localStorage admin token) keeps a refreshed page authenticated.

Playwright E2E (`frontend/e2e/`, `npm run test:e2e`): `gateway-flow.spec.ts` (login → create provider → create key → proxy a request against a spun-up mock upstream → see it in Audit → Dashboard reflects it) and `reseller-flow.spec.ts` (recharge → consume past zero → 402 suspension → recharge → resume) per the P1 exit criteria in `docs/03-roadmap.md`. `playwright.config.ts`'s `webServer` builds and runs a disposable SQLite-backed backend itself (`e2e/start-backend.mjs`) plus the Vite dev server — no Docker/compose needed, mirroring the backend's offline `go test ./...`. `E2E_BACKEND_PORT`/`E2E_FRONTEND_PORT` env vars override the default 8080/5173 (also honored by `vite.config.ts`'s proxy) if those are already in use locally.

Missing vs `docs/design/08-web-console.md`: quota-editing UI on existing keys, fallback-chain drag editor, guardrail-chain builder, notification channels beyond the single webhook.
