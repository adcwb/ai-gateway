# Console warm-theme restyle + design-system component extraction — design

## Context

The web console (`frontend/`) has a hand-rolled design system ("Signal Terminal": ink-black canvas, signal-teal accent, monospace data) described in `frontend/CLAUDE.md` and implemented as CSS custom properties + selectors in `frontend/src/styles.css`, with a thin layer of exported React helpers in `frontend/src/components/ui.tsx` (`Icon`, `Skeleton`, `Spinner`, `Live`, `EmptyState`, `ErrorBanner`, `Modal`, `Gauge`, `StatValue`, `StatCard`, `AreaChart`, `TableSkeleton`, `HttpStatus` — 11 components).

Most of the actual visual language is **not** componentized: buttons (`.ghost`/`.danger`/`.sm` modifiers), cards (`.card`), tables (`.table-wrap` + hand-written `<table>`), pills (`.pill` + tone modifier), forms (`.field`/`.field-label`/`.form-grid`), the topbar (`.topbar`/`.eyebrow`/`.actions`), and tabs (`.tabs`/`.tab`) are all applied as raw CSS classes directly in the 14 page files under `frontend/src/pages/`. A grep across those pages found: `.topbar` in all 14 files, button-class modifiers 71×, `.field`/`.form-grid` 156× (the single largest pattern), `.pill` 19×, plus `.card` used generically (not just via `StatCard`) and `.tabs` in 2 files (`Usage.tsx`, `Audit.tsx`).

Two motivations converged on one project:

1. **`/design-sync` prep** — syncing this repo's design system to Claude Design needs a real, richly-exported component library. Eleven small helpers wasn't enough; the CSS-class-only patterns above needed to become real components first.
2. **A full visual rebrand**, requested directly: replace the dark teal theme with a warm cream/amber/black light theme — large border radius, ultra-thin input borders, and a tab strip with a light neumorphic/embossed feel — plus a richer button/status hierarchy (not "black buttons dominate everything"), higher data density for this data-dense ops tool, deliberate micro-interactions, and a documented visualization color spec.

A style reference the user provided (`api.openstarry.com`) turned out not to match the brief on inspection (it's a dark navy/cyan theme) — confirmed with the user, who chose to proceed from the written brief alone rather than that reference.

The direction was validated in three passes before this doc:
- **`frontend-design` skill** — translated the brief into a concrete token system, deliberately avoiding the "cream + serif + terracotta" look that skill flags as an AI-design cliché (picked **Space Grotesk**, a technical/geometric display face, over a warm editorial serif).
- **`dataviz` skill** — derived a categorical chart palette and status-color text variants, validated by *running* `scripts/validate_palette.js` and a WCAG contrast check rather than eyeballing hex values (results below).
- **A static preview artifact** (`console-warm-theme-preview.html`, published during brainstorming) — a mocked Dashboard screen built from the actual token values, approved by the user before this doc was written.

## Goal

1. Replace every dark-theme token value in `frontend/src/styles.css` with the new warm-theme values (same custom-property names — this is a value swap, not a rename, to keep the diff reviewable), and formalize the spacing, typography, and icon-size scales that don't exist as systems today (just ad hoc per-selector values).
2. Add ~9 new exported components to `frontend/src/components/ui.tsx` (`Topbar`, `Button`, `Card`, `CardRow`, `Field`, `FormGrid`, `Pill`, `TableWrap`, `Tabs`) plus a `StatCard` enhancement (trend delta + sparkline).
3. Migrate all 14 pages in `frontend/src/pages/` to use the new components instead of raw CSS classes, preserving every page's existing structure, data, and i18n strings exactly — this is a visual/structural refactor, not a content or behavior change.
4. Increase table/list data density; add a mid-size responsive tier and fix `.table-wrap`'s overflow behavior; add the specified micro-interactions (button press, tab slide, table-row hover accent).
5. Document the visualization palette/spec so the existing `AreaChart`/`Gauge`/`StatCard` and any future chart follow one rule set.

## Non-goals

- No new functional capability — no new API calls, routes, or pages.
- No dark/light theme *toggle*. This is a hard cutover (the console has no user-facing theme preference today, and none is being added); `html { color-scheme: dark }` becomes `light`, full stop.
- No Storybook or automated visual-regression tooling — that belongs to `/design-sync`'s own conversion process, not this task, and isn't otherwise justified by this repo's size.
- No rewrite of `AreaChart`/`Gauge`/`Modal`'s existing prop contracts beyond retinting + `StatCard`'s new optional props — callers are unaffected.
- No copy changes. Every string stays behind its existing `t()` key (`frontend/CLAUDE.md` hard rule #2); the preview artifact's illustrative copy ("Requests today", "acme-prod", etc.) does not imply renaming anything in `src/i18n.ts`.
- No change to `.dot` (circuit-breaker state indicators in `Providers.tsx`) beyond retinting — the embossed "pilot lamp" treatment is scoped to the `Live` component only (see Signature element below); applying it everywhere would dilute it.

## Token system (`frontend/src/styles.css` `:root`)

All existing custom-property **names** are kept; only values change, plus four new tokens (`--ok-text`/`--warn-text`/`--err-text`/`--info-text` and `--shadow-btn`) and one new font role (`--font-display`). This keeps every non-`:root` selector that already reads `var(--accent)`, `var(--border)`, etc. working unmodified — the "large-radius, thin-border, amber-on-cream" look falls out of the token swap almost everywhere; only the shadows, tabs, buttons, and pills need selector-level rewrites (below), because their old rules were tuned for a dark canvas.

```css
:root {
  /* canvas + surfaces — lighter = more elevated, same convention as before;
     --surface-3 is the one exception: it's the *recessed*-groove tone (tab
     track, gauge track), so it goes warmer/darker than --bg on purpose */
  --bg:            #F6F1E7;
  --bg-elev:       #FAF6EC;
  --surface:       #FCF9F2;
  --surface-2:     #FFFFFF;
  --surface-3:     #EDE5D2;
  --border:        rgba(34, 28, 19, 0.10);
  --border-strong: rgba(34, 28, 19, 0.18);

  /* text */
  --text:  #221C13;
  --muted: #726552;   /* 5.0:1 on --bg */
  --faint: #8f8371;

  /* amber — the one brand accent (warn status shares this family) */
  --accent:        #C8811F;
  --accent-strong: #B36F16;
  --accent-soft:   rgba(200, 129, 31, 0.14);
  --accent-softer: rgba(200, 129, 31, 0.07);
  --accent-line:   rgba(200, 129, 31, 0.38);

  /* status — soft tones for chip/badge backgrounds; -text tones are new,
     for when a status color carries a label directly on --bg (see below) */
  --ok:        #4B8B63;  --ok-soft:   rgba(75, 139, 99, 0.14);   --ok-text:   #2E6B47;
  --warn:      #C8811F;  --warn-soft: rgba(200, 129, 31, 0.14);  --warn-text: #8A5A12;
  --err:       #B5493A;  --err-soft:  rgba(181, 73, 58, 0.14);   --err-text:  #A43F30;
  --info:      #4C7A8C;  --info-soft: rgba(76, 122, 140, 0.14);  --info-text: #3D6472;

  /* type */
  --font-display: "Space Grotesk", var(--font-sans);
  --font-sans: "Inter", -apple-system, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
  --font-mono: "JetBrains Mono", "Fira Code", ui-monospace, "SF Mono", Menlo, Consolas, monospace;

  /* shape + motion */
  --radius:    18px;  /* was 10px */
  --radius-sm: 10px;  /* was 6px  */
  --radius-lg: 26px;  /* was 14px */
  --ease: cubic-bezier(0.4, 0, 0.2, 1);
  --dur:  160ms;
  --shadow-card: 0 1px 2px rgba(34, 28, 19, 0.06), 0 10px 24px -16px rgba(34, 28, 19, 0.20);
  --shadow-pop:  0 1px 0 rgba(255, 255, 255, 0.6) inset, 0 0 0 1px var(--border-strong), 0 18px 36px -18px rgba(34, 28, 19, 0.28);
  --shadow:      0 1px 0 rgba(34, 28, 19, 0.05), 0 16px 40px -20px rgba(34, 28, 19, 0.22);
  --shadow-btn:  0 1px 0 rgba(255, 255, 255, 0.30) inset, 0 2px 6px -2px rgba(34, 28, 19, 0.35);
  --ring:        0 0 0 3px var(--accent-soft);
}

html { color-scheme: light; }  /* was dark */
```

`index.html` gains a Google Fonts preload for Space Grotesk (weights 500/700) alongside the existing Inter/JetBrains Mono preloads, same graceful-offline-fallback comment already documented (`frontend/CLAUDE.md`: "inside the embedded single-binary console this fails gracefully to the system font stack").

### Validated, not eyeballed

- **Status text contrast** (WCAG, against `--bg` #F6F1E7): the *soft*-tone base hues fail small-text AA on their own (`--accent` 2.82:1, `--ok` 3.61:1) — hence the new `-text` variants, all ≥ 5.2:1 (`--ok-text` 5.64, `--warn-text` 5.25, `--err-text` 5.60, `--info-text` 5.71). Soft tones stay valid for chip *backgrounds* and icons-on-tinted-chips, where the contrast that matters is icon-vs-chip, not hue-vs-cream.
- **Categorical chart palette** (`dataviz` skill's `validate_palette.js`, `--mode light --surface "#F6F1E7" --pairs all`): `#2a78d6` (blue) → `#1baf7a` (aqua) → `#4a3aa7` (violet) → `#e87ba4` (magenta), fixed order. Passes lightness band, chroma floor, and CVD separation (worst pair ΔE 12.9); aqua and magenta land under 3:1 contrast, which is a **non-dismissable WARN** — any chart using them must ship direct labels, never color alone (see Visualization spec below). These four hues are deliberately **excluded from being reachable from `--accent`/`--ok`/`--warn`/`--err`** territory so a data series can never be mistaken for a status indicator.

## Spacing scale

Today's spacing is ad hoc — a grep of `styles.css` turns up gap/padding/margin values at 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 16, 18, 20, 22, 24, 28, 32, 44, 48, 56, 64px, tuned by eye per rule. A design system needs a closed set. New 4px-base scale, added to `:root` alongside the color/radius tokens:

```css
--space-1: 4px;
--space-2: 8px;
--space-3: 12px;
--space-4: 16px;
--space-5: 20px;
--space-6: 24px;
--space-7: 32px;
--space-8: 40px;
--space-9: 48px;
```

Existing rules snap to the nearest step during migration (a few examples — the full mapping happens rule-by-rule as each selector is touched anyway for the retint): card padding `16px 18px` → `var(--space-4) var(--space-4)`; cards-gap `14px` → `var(--space-4)` (rounds up rather than down — the warm/large-radius direction wants slightly more breathing room than the old dense dark theme, not less); topbar padding `18px 0 14px` → `var(--space-5) 0 var(--space-4)`; form-grid gap `12px` → `var(--space-3)` (unchanged, already on-grid). The data-density section's table cell padding is corrected to `var(--space-2) var(--space-4)` (`8px 16px`, both already on-grid) rather than the earlier `8px 14px`, which was off-grid.

Off-grid values that are deliberate exceptions, not oversights, stay as-is and get a one-line comment saying so: the login split-panel's `56px 64px` (a full-bleed hero panel, not a data-density-driven layout) and the modal's `24px` overlay padding.

## Typography scale

Today's font sizes span ~15 distinct values across `styles.css` (10, 10.5, 11, 11.5, 12, 12.5, 13, 13.5, 14, 15, 16, 17, 20, 22, 24px) with no shared naming — each selector picked its own number. Consolidated into 9 named roles (all values kept close to their current usage, so no selector's rendered size jumps more than ~1px):

| Role | Size | Weight | Family | Used for |
| --- | --- | --- | --- | --- |
| H1 (page title) | 21px | 650 | `--font-display` | `.topbar h1` |
| H2 (panel/dialog title) | 15px | 650 | `--font-display` | `.modal-header h2` |
| Section label | 12px, uppercase, tracked | 600 | `--font-sans` | `.section-title`, panel headers |
| Body | 13.5px | 400 | `--font-sans` | table `td`, inputs, paragraph text |
| Label | 11px | 600 | `--font-sans` | `.field-label`, stat-card `.label`, chart title |
| Metric | 22px | 600 | `--font-mono` | stat-card big value (was 24px — see Data density: the whole card is getting denser, the headline number steps down 2px with it) |
| Data (inline mono) | 12.5px | 400 | `--font-mono` | table numeric/id columns, `.code-block` |
| Caption | 10.5px | 500 | `--font-sans` or `--font-mono` | timestamps, chart axis labels, faint meta |
| Brand | 15px | 700 | `--font-display` | sidebar `.brand-name` |

`font-variant-numeric: tabular-nums` stays wherever digits line up in columns (the existing `.mono, code, kbd, .num, .value, td.id, .col-data` selector list) — unaffected by the retint, still correct under the new mono stack.

## Icon system

Single source stays the hand-rolled inline-SVG set in `Icon` (`frontend/src/components/ui.tsx`) — 24×24 viewBox, stroke-only (no fills except the status dots), stroke-width 1.8, round linecap/linejoin. No external icon library is introduced (consistent with `frontend/CLAUDE.md`'s "deliberately no UI framework" rule).

A grep of every `<Icon size={...}>` call site found real usage clusters at **13, 14, 15, 16, 26, 28px** — several of these are accidental near-duplicates of each other (e.g. `Keys.tsx` mixes `size={13}` and `size={14}` for what are visually the same "icon inside a small button" role). Formalized into 3 named sizes, and every call site snaps to the nearest one during migration:

| Size | Value | Used for |
| --- | --- | --- |
| `sm` | 14px | buttons (all tiers), row actions, form-adjacent icons — the overwhelming majority of call sites |
| `md` | 16px | sidebar nav items, stat-card icon chips |
| `lg` | 26px | empty-state illustration, login-pane brand mark |

Stroke-width stays 1.8 at `sm`/`md`; `lg` drops to 1.6 so the larger mark doesn't read as bolder than the rest of the system. Icon color always follows a text/status token (`--muted` inactive nav, `--accent` active/brand chip, `--ok-text`/`--warn-text`/`--err-text`/`--info-text` for status icons) — never a hardcoded hex, so retinting the whole icon set is a token-only change.

## New components (`frontend/src/components/ui.tsx`)

Added to the existing single file (currently 330 lines; this adds roughly 250–300) rather than split into a directory — keeps `frontend/CLAUDE.md`'s documented structure intact. The file's header doc-comment is updated to list all exports.

```ts
// Topbar — the eyebrow+title+actions header, identical across all 14 pages
function Topbar(props: { eyebrow: string; title: string; actions?: React.ReactNode }): JSX.Element

// Button — 5 tiers instead of the current 3 (default/ghost/danger), so a
// solid black fill isn't the only visual weight on a page
type ButtonVariant = "primary" | "secondary" | "ghost" | "subtle" | "danger";
function Button(
  props: { variant?: ButtonVariant; size?: "md" | "sm" } & React.ButtonHTMLAttributes<HTMLButtonElement>
): JSX.Element
// primary   → solid var(--text) fill, cream text            (one per view — the committing action)
// secondary → solid tonal var(--accent) fill, ink text       (important, non-committing)
// ghost     → transparent, hairline border                   (today's overused default — Refresh/Cancel)
// subtle    → flat var(--surface) fill, no border             (repeated low-emphasis row actions)
// danger    → outline terracotta; solid only on a confirm step

// Card — the generic container `.card` already used ad hoc (Tenants.tsx's
// create-forms, Dashboard/Audit's `.toplist`, Keys/Users' `.success`)
function Card(
  props: { tone?: "default" | "success" | "toplist" } & React.HTMLAttributes<HTMLDivElement>
): JSX.Element  // style/className pass through unchanged (e.g. <Card style={{flex:1,minWidth:280}}>)

// CardRow — the `.cards` flex-wrap row that groups Cards/StatCards
function CardRow(props: { children: React.ReactNode; style?: React.CSSProperties }): JSX.Element

// Field — <label className="field ..."> wrapping a field-label + input;
// `row` gives the inline checkbox layout seen in Keys.tsx/ModelsPricing.tsx
function Field(props: {
  label: string; children: React.ReactNode; span?: 2 | 3; row?: boolean;
  className?: string; style?: React.CSSProperties;
}): JSX.Element

// FormGrid — the 3-col `.form-grid`; nests fine (Keys.tsx's cache-config sub-grid)
function FormGrid(props: { children: React.ReactNode; style?: React.CSSProperties }): JSX.Element

// Pill — status badge; `outline` is new, for lower-emphasis metadata tags
// that shouldn't compete visually with real status
type PillTone = "on" | "off" | "warn" | "info" | "err";
function Pill(props: { tone?: PillTone; variant?: "soft" | "outline"; children: React.ReactNode }): JSX.Element

// TableWrap — thin wrapper around a hand-written <table>
function TableWrap(props: { children: React.ReactNode }): JSX.Element

// Tabs — used today only in Usage.tsx/Audit.tsx; gets the recessed/embossed
// track treatment (see Signature element)
function Tabs(props: {
  items: { key: string; label: string }[]; active: string; onChange: (key: string) => void;
}): JSX.Element
```

`StatCard` gains two new optional props, kept backward-compatible (existing callers with neither prop render identically):

```ts
function StatCard(props: {
  icon: IconName; label: string; value: React.ReactNode; sub?: React.ReactNode;
  loading?: boolean; tone?: "accent" | "ok" | "warn" | "info" | "err";
  delta?: { pct: number; goodDirection: "up" | "down" };  // renders "▲ 8.2%" colored by
                                                            // whether *this* metric's direction is good —
                                                            // e.g. rising requests is good (up=good),
                                                            // falling latency is also good (down=good)
  sparkline?: number[];  // optional inline single-hue SVG trend, no axes
}): JSX.Element
```

A pre-existing quirk found during the CSS audit: `Keys.tsx` uses `className="field span-2"`, but `.span-2` has no rule today (only `.span-3` exists) — a harmless no-op. Since `styles.css` is being rewritten wholesale for the retint anyway, this gets fixed as an incidental correctness fix (`Field`'s `span={2}` prop will emit a real `.span-2 { grid-column: span 2; }` rule) rather than carried forward as a second dead class in freshly-written CSS.

## Button & status hierarchy

| Button tier | Look | Used for |
| --- | --- | --- |
| Primary | solid `--text` fill, cream text | one per view — the committing action |
| Secondary | solid tonal `--accent` fill, ink text | important, non-committing |
| Ghost | transparent, hairline border | tertiary (today's overused default) |
| Subtle | flat `--surface` fill, no border | repeated row actions in dense tables |
| Danger | outline terracotta, solid only on confirm | destructive actions |

| Pill | Soft variant | Outline variant |
| --- | --- | --- |
| on/ok | `--ok-soft` bg, `--ok-text` text | hairline border, `--muted` text |
| warn | `--warn-soft` bg, `--warn-text` text | " |
| err | `--err-soft` bg, `--err-text` text | " |
| info | `--info-soft` bg, `--info-text` text | " |

## Data density

- Table `td`/`th` padding: `11px 16px` → `var(--space-2) var(--space-4)` (`8px 16px` — see Spacing scale).
- Table font stays at the Typography scale's `Body` role (`13.5px`) — density comes from the padding cut, not from also shrinking type, so table text doesn't end up smaller than form/paragraph text elsewhere on the same page.
- Row hover: keep the existing background shift, add a 2px `--accent` left-edge accent tick (`::before` on `tbody tr:hover`) — echoes the sidebar's existing active-nav indicator, gives density without losing scannability.

## Responsive layout

Today there is exactly one breakpoint (`max-width: 860px`): the sidebar collapses from a fixed 236px column to a horizontal top strip, and `.form-grid` drops from 3 columns to 1. That collapse is unchanged. Two real gaps found while auditing for this spec, both fixed here rather than left as a follow-up, since they're small and this is already a full-stylesheet pass:

- **`.table-wrap` clips instead of scrolling.** It's `overflow: hidden` today — a table wider than its container (e.g. `Audit.tsx`'s 7-column detail rows) gets silently clipped, not a horizontal scrollbar. Becomes `TableWrap`'s default: `overflow-x: auto`, own scroll container, so the page body never scrolls sideways (matches how wide tables are already handled in this project's other surfaces, e.g. the Artifact skill's own rule for wide content).
- **No mid-size tier.** Between the collapse point (860px) and a full desktop window, `.form-grid`'s 3 columns get uncomfortably narrow before the collapse kicks in. New tier:

```css
@media (max-width: 1279px) and (min-width: 861px) {
  .form-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .form-grid .span-3 { grid-column: 1 / span 2; }
}
```

`CardRow`'s stat-card row already wraps via flexbox at any width (`flex-wrap: wrap`, `min-width` per card) — no new breakpoint needed there. `Tabs` and `Topbar` are single-row flex layouts that already degrade acceptably down to the existing 860px collapse; unchanged.

## Micro-interactions

- **Buttons**: `:active` → `transform: scale(0.98)`, shadow flattens to none — a physical press.
- **Tabs**: the active pill's background/shadow transition (`background var(--dur) var(--ease)`) so switching reads as a slide/press, not a snap.
- **Table rows**: the density section's hover accent tick, transitioned in with the existing row background transition.
- Carried over unchanged (retinted only): card hover-lift, skeleton shimmer, the `Live` pulse (see Signature element).

## Signature element: the pilot lamp

The one place skeuomorphism is spent (per the brainstorm: "spend boldness in one place"). `Live` (`frontend/src/components/ui.tsx`) changes from a flat pulsing dot to a small embossed bezel housing a glowing amber bulb — an indicator-lamp reference apt for a product literally called a gateway:

```css
.live-bezel {
  width: 16px; height: 16px; border-radius: 50%;
  background: var(--bg);
  box-shadow: inset 0 1px 2px rgba(34,28,19,.28), inset 0 -1px 0 rgba(255,255,255,.6), 0 0 0 1px var(--border);
}
.live-bulb {
  width: 7px; height: 7px; border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 4px 1px var(--accent), 0 0 0 1px var(--accent-line);
  animation: live-glow 1.8s ease-in-out infinite;
}
@keyframes live-glow { 0%, 100% { opacity: .55; } 50% { opacity: 1; } }
```

The tab strip gets the matching recessed treatment (inset-shadowed track, ink-filled "pressed" active tab) — the CSS is in the preview artifact and carries over verbatim. `.dot` (circuit-breaker state, `Providers.tsx`) is retinted only, not embossed — kept flat and quiet on purpose (non-goals).

## Visualization spec

- **Single-series charts** (today's only case — `AreaChart` on the Usage page, `Gauge` on quota bars): sequential, one hue — `--accent` (amber). No change to this rule.
- **Categorical (multi-series) charts**, if/when one is built (e.g. a future per-provider breakdown): the validated 4-hue palette above, fixed order, never cycled. A 5th/6th series is a soft cap (legend or small multiples); never generate a 5th hue.
- **Status colors are reserved** — never reused as a categorical series hue, so a data series can never visually read as a warning/error.
- **Relief rule is mandatory, not optional**: any chart whose palette includes a sub-3:1-contrast hue (aqua, magenta, per the validator run above) ships direct labels — the existing native `<title>` tooltip pattern satisfies "not color-alone" for the single-series case; a future multi-series chart needs visible end-of-line labels or a legend, not just tooltips.
- **StatCard delta**: direction-aware coloring, not hue-fixed to "up" — `goodDirection` makes a falling latency read `--ok-text` green despite the down arrow, exactly like a rising request count does. Neither is inherently "the accent color's job"; both are status, not identity.
- **Sparkline**: single-hue (`--accent`), no axes/gridlines, dependency-free inline SVG — same hand-rolled approach as the existing `AreaChart`, not a new charting dependency.

## Migration (all 14 pages)

Mechanical transform, applied per page in `frontend/src/pages/`: `Audit.tsx`, `Billing.tsx`, `Dashboard.tsx`, `GuardrailPolicies.tsx`, `Keys.tsx`, `Login.tsx`, `McpServers.tsx`, `ModelMappings.tsx`, `ModelsPricing.tsx`, `Providers.tsx`, `Settings.tsx`, `Tenants.tsx`, `Usage.tsx`, `Users.tsx`.

| Old JSX | New |
| --- | --- |
| `<div className="topbar">...` | `<Topbar eyebrow=... title=... actions={...} />` |
| `<button>`/`.ghost`/`.danger`/`.sm` | `<Button variant=... size=...>` |
| `<div className="card ...">` | `<Card tone=...>` |
| `<div className="cards">` | `<CardRow>` |
| `<label className="field ...">` | `<Field label=... span=... row=...>` |
| `<div className="form-grid">` | `<FormGrid>` |
| `<span className={\`pill ${tone}\`}>` | `<Pill tone=...>` |
| `<div className="table-wrap">` | `<TableWrap>` |
| `<div className="tabs">`/`.tab` (Usage.tsx, Audit.tsx only) | `<Tabs items=... active=... onChange=... />` |

Redundant duplicate utility classes found alongside these patterns (e.g. `Usage.tsx`'s `className="actions flex gap-8 items-center"`, where `.topbar .actions` already sets that exact flex/gap/align-items) are dropped during migration — no visual change, just dead weight removed.

Everything else in each page — state, data fetching, `useAsync` usage, conditionals, i18n keys, table column definitions — is untouched.

## Verification

- `npm run build` (`tsc -b && vite build`) after each page's migration — the project's existing strict-TS gate.
- Full Playwright E2E suite (`npm run test:e2e`: `gateway-flow.spec.ts`, `reseller-flow.spec.ts`) at the end — catches structural/interaction breakage (a missing button, a broken form submit), not styling.
- No automated visual-regression tool exists in this repo (non-goal to add one); manual spot-check via Playwright screenshots of a handful of migrated pages against the approved preview artifact is the visual-confidence step.

## Docs updated in the same PR

- `frontend/CLAUDE.md` — "Stack & structure" section: theme name/description and the `ui.tsx` component list.
- `docs/design/08-web-console.md` — an appended ADR-style entry noting the theme change and component extraction (existing decisions are not rewritten, per root `CLAUDE.md` rule 4).
- `frontend/index.html` — Space Grotesk font preload.
