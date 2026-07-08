# Key quota editor UI — design

## Context

`docs/design/08-web-console.md` (D08) module 2 describes a Virtual Keys detail drawer with an `Quotas` tab backed by `GET key/quota-config`, `PUT key/quota-config`, `GET key/quota-usage`. The backend has shipped these three endpoints (`backend/internal/service/gateway.go:191-222`, routes at `backend/internal/server/http.go:86-88`) since early on, but the console never calls them — `Keys.tsx` only sets `dailyTokenQuota` once, at create time, and there is no way to view or edit quotas on an existing key. `frontend/CLAUDE.md`'s own "Missing vs docs/design/08-web-console.md" note calls this out explicitly.

This is a backend-capability-exists / frontend-doesn't-expose-it gap, not new product surface — the fix is UI-only.

## Goal

Let an operator view live quota usage and edit both global and per-model quota overrides for an existing virtual key, from the Keys page, using only the three existing endpoints.

## Non-goals

- No changes to the backend (DTOs/endpoints are already correct and sufficient).
- No general detail-drawer with Overview/Models/Security/Usage tabs — D08's full mockup was already deliberately simplified in the shipped console (see D08's ADR addendum); this spec only closes the Quotas gap.
- No Dashboard KPI changes (error %/p95 latency) — `ai_usage_daily` doesn't aggregate those fields; out of scope for a frontend-only change.
- No notification-channel work — also backend-gated, separate scope.

## UI shape

A **modal dialog**, not an inline row-expand (Audit.tsx's pattern) or a drawer. The user picked modal explicitly over the row-expand alternative during design review. No modal primitive exists in this codebase yet, so this spec adds a small generic one.

### `Modal` (new, `components/ui.tsx`)

```
<Modal title={string} lang={Lang} onClose={() => void} width?={number}>
  {children}
</Modal>
```

- Rendered via `createPortal(..., document.body)` so it isn't clipped by table/card overflow.
- Backdrop click and `Escape` both call `onClose`.
- Header: title + a close (`Icon name="close"`) button, reusing existing icon set.
- New CSS in `styles.css`: `.modal-overlay` (fixed, full-viewport, dim backdrop, centers content, `z-index` above the sidebar's `20`), `.modal` (uses `--surface`, `--border`, `--radius-lg`, `--shadow-pop` — the same tokens `.card` uses, so it reads as part of the same design system), `.modal-header`, `.modal-body` (scrollable if content overflows viewport height).
- Generic — no quota-specific logic. Reusable by future pages.

### `QuotaModal` (new, sibling component inside `pages/Keys.tsx`, same file — mirrors `ModelMappings.tsx`'s `FallbackRow` convention of colocating a page's sub-components in one file)

Props: `{ keyId: number; lang: Lang; onClose: () => void }`.

On mount: one-shot parallel fetch (not polling — this is a modal, not a live dashboard) of:
- `GET /ai/gateway/key/quota-config?keyId=<id>` → `QuotaConfigResp`
- `GET /ai/gateway/key/quota-usage?keyId=<id>` → `KeyQuotaUsageResp`

Renders three sections top to bottom:

1. **Usage** (read-only, from `quota-usage`): one `.gauge` bar per dimension — daily token, hourly token, hourly request, concurrency, daily point, hourly point. Each shows `used / quota` and a filled-bar percentage; a quota of `0` renders as "unlimited" (no bar), matching the backend's existing `0 = unlimited` convention (see `hourlyToolCallQuota`'s doc comment in `dto/gateway.go` for the established meaning of 0 across this DTO family).
2. **Global quotas** (editable form, prefilled from `quota-config`): number inputs for `dailyTokenQuota`, `hourlyTokenQuota`, `hourlyReqQuota`, `maxConcurrency`, `dailyPointQuota`, `hourlyPointQuota`. Same `.form-grid`/`.field` markup Keys.tsx's create form already uses.
3. **Per-model overrides** (editable table, from `quota-config.modelQuotas`): rows of `{modelName, dailyTokenQuota, hourlyTokenQuota, hourlyReqQuota, dailyPointQuota, hourlyPointQuota}` with a remove button per row and an "add row" control (model name input + the five number fields, defaulting to 0/unlimited).

Footer: Save button → `PUT /ai/gateway/key/quota-config` with the full `UpdateQuotaConfigReq` (global fields + `modelQuotas` array). On success: close the modal (the Keys list itself doesn't show quota state, so no row refresh is needed). On error: inline error text in the modal body (reusing the existing "catch → show `(e as Error).message`" convention), modal stays open so the user's edits aren't lost.

### Keys.tsx row action

Add a "Quotas" button (`Icon name="dashboard"`) to `.row-actions`, alongside enable/reveal/revoke. Clicking sets a `quotaKeyId: number | null` state; `quotaKeyId !== null` renders `<QuotaModal keyId={quotaKeyId} ... onClose={() => setQuotaKeyId(null)} />`.

## Types (`api/client.ts`)

New exported interfaces mirroring the backend DTOs exactly (camelCase, per the existing "mirror backend DTO JSON shapes" rule):

```ts
export interface QuotaConfigItem {
  modelName: string;
  dailyTokenQuota: number; hourlyTokenQuota: number; hourlyReqQuota: number;
  dailyPointQuota: number; hourlyPointQuota: number;
  dailyTokenUsed: number; hourlyTokenUsed: number; hourlyReqUsed: number;
  dailyPointUsed: number; hourlyPointUsed: number;
}
export interface QuotaConfig {
  keyId: number; name: string; keyPrefix: string; providerId: number;
  allowedModels: unknown;
  dailyTokenQuota: number; hourlyTokenQuota: number; hourlyReqQuota: number; maxConcurrency: number;
  dailyPointQuota: number; hourlyPointQuota: number;
  modelQuotas: QuotaConfigItem[];
}
export interface KeyQuotaUsage {
  keyId: number;
  dailyTokenQuota: number; dailyTokenUsed: number;
  hourlyTokenQuota: number; hourlyTokenUsed: number;
  hourlyReqQuota: number; hourlyReqUsed: number;
  maxConcurrency: number; currentConcurrency: number;
  dailyPointQuota: number; dailyPointUsed: number;
  hourlyPointQuota: number; hourlyPointUsed: number;
}
```

## i18n

New keys in both `en`/`zh` (`i18n.ts`): `quotas`, `quotaUsage`, `globalQuotas`, `perModelQuotas`, `modelName`, `concurrency`, `unlimited`, `addModelQuota`, `removeRow`, `saveQuotas`, `quotaSaveFailed`. Reuse existing keys where field meaning matches (e.g. any existing point/token quota labels), add new ones only where the create form doesn't already have an equivalent (its `dailyTokens` label text is create-form-specific — "0 = unlimited" — and can be reused for `dailyTokenQuota` here rather than duplicated).

## Error handling

- Fetch failure (either endpoint): show an `ErrorBanner`-style message inside the modal body with a retry action; no partial form render.
- Save failure: inline error message near the Save button; form state preserved.
- Both follow the existing envelope/try-catch convention in `api/client.ts` — no new error-handling machinery.

## Testing / verification

- `cd frontend && npm run build` (tsc strict + vite) must pass.
- Manual verification: start the Go backend (`go run ./cmd/server` or the built binary) + `npm run dev`, create a key, open its Quotas modal, confirm usage gauges render, edit and save global + per-model quotas, confirm a second open reflects the saved values, and confirm an induced error (e.g. invalid input) shows inline without closing the modal.
- No new Playwright E2E spec is required by this scope (existing `gateway-flow.spec.ts`/`reseller-flow.spec.ts` are unaffected); could be added later if the team wants CI coverage, but that's not part of this design.

## Files touched

| File | Change |
| --- | --- |
| `frontend/src/components/ui.tsx` | new `Modal` component |
| `frontend/src/styles.css` | new `.modal-overlay`/`.modal`/`.modal-header`/`.modal-body`/`.gauge` CSS |
| `frontend/src/api/client.ts` | new `QuotaConfig`, `QuotaConfigItem`, `KeyQuotaUsage` types |
| `frontend/src/i18n.ts` | new en/zh string pairs |
| `frontend/src/pages/Keys.tsx` | new "Quotas" row action, new `QuotaModal` sibling component |
