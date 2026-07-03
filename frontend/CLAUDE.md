# CLAUDE.md — frontend/

Guidance for working on the web console. Repo-wide context lives in the root `CLAUDE.md`; backend conventions in `backend/CLAUDE.md`.

## Stack & structure

Vite + React 18 + TypeScript (strict) + react-router-dom. **Deliberately no UI framework yet** — plain CSS in `src/styles.css` (dark theme, CSS variables); discuss before adding any dependency. Design reference: `docs/design/08-web-console.md` (the full design targets shadcn/ui + TanStack Query; migrate when the console grows past the current page set).

```text
src/
├── api/client.ts    # fetch wrapper + ALL shared API types; envelope + admin-token auth
├── i18n.ts          # bilingual dictionary (en/zh) — dependency-free t(key, lang)
├── App.tsx          # auth guard, sidebar shell, routes
├── styles.css       # the entire design system
└── pages/           # Dashboard, Keys, Providers, Audit, Tenants, Billing, Login
```

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
6. Follow the shared list-page pattern: toolbar (title + refresh) → error line → `.cards` stats → `<table>`; reuse `.pill`, `.dot.{closed,half_open,open}`, `.muted`, `.error-text` classes.

## Current pages vs designed scope

Implemented: Dashboard (key stats, 7-day usage, provider health), Keys (list only), Providers (list + live breaker), Audit (log list), Tenants (list/create tenant+project), Billing (balance, enable/disable, recharge, ledger).

Missing vs `docs/design/08-web-console.md`: key **create/edit/reveal/quota UI** (biggest gap — creation is API-only today), provider create/edit forms, model & price-table management page, audit body viewer / sessions / security tabs, settings page, charts (usage timeseries endpoint exists but unplotted), login via user accounts + RBAC-aware navigation, Playwright E2E.
