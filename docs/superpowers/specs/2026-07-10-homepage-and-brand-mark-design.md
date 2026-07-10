# Public homepage + routing-based brand mark — design

## Context

The gateway has no public-facing page — `main.tsx` mounts the console's `BrowserRouter` at `basename="/console"`, and `internal/server/http.go` only registers `/console/` (+ a `/console` → `/console/` redirect). Visiting the bare domain root 404s today (confirmed: no `mux.Handle("/", ...)` exists). The user wants: visiting the project opens a new public homepage; only a successful login moves into the admin console.

Separately, the sidebar/login brand mark — a small hand-drawn torii-gate SVG (`Icon name="torii"`, `frontend/src/components/ui.tsx`) — read poorly at its ~22px render size ("有点丑" / a bit ugly) and needed replacing.

Both were designed together through several iterations (three discarded directions, kept here only as a record of what was rejected and why — the last iteration is what's built):

1. **Torii, redrawn** — rejected by the user in favor of a wholly new, non-torii concept.
2. **Hexagonal "node" + converging paths** — rejected: "forget the hexagon," too close to generic Kubernetes/network-mesh iconography, and the homepage built around it read as a feature showcase (static hero, feature grid, architecture diagram as decoration) rather than a demonstration.
3. **"Fork" mark (one path splitting into three) + a horizontal animated diagram as the hero** — closer, but the user's final brief (quoting a Stripe/Cloudflare/Vercel-caliber bar) asked for something more specific: the logo must express *input → decision → output* under "movement, convergence, distribution, and control," and the homepage must *demonstrate the product, not describe it* — every section answering one specific question, the hero built from the product's own request-handling shape (not an illustration of it), and a section users can actually click.

What's specified below is the fourth iteration, approved by the user.

## Goal

1. A new **rail-switch mark**: no hexagon, no node/mesh imagery — an input line meeting a decision point that connects to exactly one of several output lines, with the existing amber signal-dot language (already used by the console's pilot-lamp `Live` component) as the one recurring motion signature. Replaces `torii` everywhere it's used (console sidebar, Login pane).
2. A new public **homepage** (`homepage/`, plain HTML/CSS/JS — no React/Vite, no build step) served at `/`, embedded into the Go binary the same way the console is, leaving `/console/`'s login → dashboard flow completely untouched.
3. Backend routing: `mux.Handle("/", homepage.Handler())` in `internal/server/http.go`, additive only.

## Non-goals

- No changes to `/console/`'s routing, auth flow, or any React code beyond the brand-mark swap (`Icon name="torii"` → the new mark).
- No new backend API endpoints — the homepage is static content plus links (`Sign in to Console` → `/console/`, `GitHub` → the repo URL).
- No real analytics/telemetry on the homepage.
- No fabricated metrics. The "Product Proof" section ships only counts verified against this repo (see below) — no invented throughput/scale numbers.
- No bilingual toggle wiring to a persisted preference beyond `localStorage` (mirrors the console's own lightweight lang-preference pattern) — no server-side locale negotiation.

## The mark: a rail switch

```
input ──────┐
            │  (decision point)
            ├──────────────── output A  (faint, idle)
            ●──────────────── output B  (active — ink, full weight)
            │
            └──────────────── output C  (faint, idle)
```

One input line meets a junction; the junction connects to exactly one output line at a time (the others render at reduced opacity — present, but not taken). The amber dot marks the active path. Frozen in a single static frame it still reads as "a switch mid-decision" — no animation or text required to convey "routing." Two renditions:

- **Static** (console sidebar, Login pane, favicon): one frame, the middle track active, no animation — calm, matches the console's existing discipline of spending motion in exactly one place (the pilot lamp).
- **Animated** (homepage only): the active track cycles A→B→C→A on a 3.6s loop (three `@keyframes` blocks gated to non-overlapping percentage windows — `trackA`/`trackB`/`trackC` — each driving both the track's opacity and its dot's `offset-distance` along that segment), respecting `prefers-reduced-motion` (animations disabled, first track shown as the static frame).

Geometry (shared `viewBox="0 0 64 48"`): input `M4,24 L20,24`; three output stubs at `y=8/24/40`, `x=44→60`; three junction-to-output diagonals `M20,24 L44,{8|24|40}`. Legible from 16px (favicon) through 64px (homepage hero) at stroke-width 4–7 depending on render size — verified in the approved preview.

### Implementation

`frontend/src/components/ui.tsx` gains a dedicated `BrandMark` component (not folded into the generic `Icon` — the mark is two-tone (ink stroke + fixed amber dot fill), where every other `Icon` is single-tone `currentColor`, so it doesn't fit that component's contract):

```ts
export function BrandMark({ size = 24, animated = false }: { size?: number; animated?: boolean }): JSX.Element
```

Replaces `<Icon name="torii" .../>` at its three call sites: `App.tsx`'s sidebar `.brand-mark` (`size=22`, static), `Login.tsx`'s `.pane-mark` (`size=26`, static) and `.pane-deco` (`size=320`, static, purely decorative background). `torii` is removed from `IconName`/`PATHS` in the same change — it has no other callers (confirmed via grep).

## The homepage

Plain static HTML/CSS/JS (`homepage/index.html`, `homepage/styles.css`, `homepage/script.js`) — deliberately no React/Vite: the page is mostly static content plus a handful of `setInterval`/click-handler interactions, and pulling in a bundler for that would contradict the console's own "no framework unless it earns it" rule. Fonts load the same way `frontend/index.html` already does (Google Fonts `<link>`, graceful system-font fallback offline) — not embedded as data URIs (that was a preview-artifact-only requirement, since Artifacts block external font requests; the real deployed page has no such restriction). Reuses the console's exact design tokens (`--bg`/`--accent`/`--text`/etc. values, not the variable names, since this is a separate stylesheet) so the two surfaces are visually one product.

Every section answers exactly one question (sections that didn't survive this test were cut during design):

| Section | Question | What's there |
| --- | --- | --- |
| Hero | "What is this?" | No centered logo. A live vertical pipeline — `POST /v1/chat/completions → Policy check → Rate limit → Cache → Routing (carousel: Claude/GPT/Gemini/DeepSeek/Local) → 318ms → 200 Response` — with one signal dot descending through it on a 7.2s CSS loop. The headline/subhead/CTAs sit beside it, not centered on a mark. |
| Before / After | "Why do I need it?" | Two columns, five lines each, no feature names — "A different SDK per provider" → "One OpenAI-compatible endpoint," etc. |
| Outcomes | *(elaborates "why," one level deeper)* | Six cards, each a "so what" sentence ("Automatically routes to the best available provider — and away from a failing one"), not a capability name. |
| Interactive routing playground | "How does it work?" | Four real click-driven strategy buttons (`Cheapest`/`Fastest`/`My priority order`/`Weighted split`, honestly mapped to the actual `least_cost`/`least_latency`/`priority`/`weighted` strategies) that redraw which provider node is highlighted and animate a dot down the picked path. |
| Console mock | "What will I get?" | Live-ticking numbers (requests/latency/cost/throughput via `setInterval`), drifting provider-distribution bars, a scrolling request-row feed — not a static screenshot. |
| Product Proof | "Can I trust it?" | Verified-real counts only (below) — no invented benchmark numbers. |
| Ecosystem pills | *(supports "why different")* | OpenAI SDK / Anthropic Messages API / LangChain / LlamaIndex / MCP — all real per the shipped adapters. |
| Closing CTA | "How do I start?" | The `docker compose up -d` quick-start snippet (real command, `deploy/compose/`) + `Sign in to Console`. |

### Product Proof — verified counts (no fabrication)

The brief's own example numbers (18+ Providers, 150+ Models, 100K+ RPS, <300ms Failover) don't hold up against this codebase and were not used — flagged to the user directly during design, who accepted the substitution. What ships instead, each checked against the repo at design time:

- **5** outbound dialects (`openai_compatible`, `anthropic`, `azure_openai`, `gemini`, `bedrock`), the last covering **5** Bedrock model families (Claude/Titan/Llama/Mistral/Nova) — `backend/CLAUDE.md`'s protocol section.
- **4** inbound API surfaces: OpenAI Chat, Anthropic Messages, OpenAI Responses API, MCP.
- **4** routing strategies: weighted, priority, least-latency, least-cost.
- **3** database backends: MySQL, PostgreSQL, SQLite.
- **MIT** license (`LICENSE`, verified).
- **1** Go binary to deploy.

No GitHub star count or release-version badge — `git tag` returns nothing today, so there is no release to cite; the credibility strip instead offers a plain "Star on GitHub" link, not a fabricated count.

### Differentiation line

One explicit sentence addresses "why different from LiteLLM / OpenRouter / Kong AI Gateway," placed directly under the hero copy rather than a separate section (it's a modifier on "why do I need it," not its own question): *"Self-hosted & open-source — unlike OpenRouter (hosted) or a bare LiteLLM proxy (no billing, no guardrails, no MCP)."*

## Backend: serving the homepage

Mirrors the console's own embed pattern exactly (`backend/internal/console/console.go`):

```go
// backend/internal/homepage/homepage.go
package homepage

//go:embed all:dist
var dist embed.FS

func Handler() http.Handler { /* http.FileServer(http.FS(sub)), no SPA fallback needed — one page */ }
```

`internal/server/http.go` adds one line: `mux.Handle("/", homepage.Handler())`. Go 1.22+ `ServeMux` resolves the more specific `"/console/"` pattern for anything under that prefix; `"/"` (a subtree pattern) only catches what nothing more specific matches — no conflict with the existing `/console/` registration, verified against how the mux already layers `/ai/v1/*` alongside more specific routes.

`backend/internal/homepage/dist/` gets the same placeholder-in-git treatment as `backend/internal/console/dist/` (root `CLAUDE.md`'s existing rule: only a placeholder committed, real assets never committed there). The Makefile's `embed` target gains a copy step (no `web` step needed for the homepage — it's already static source, nothing to build):

```make
embed:
	rm -rf backend/internal/console/dist && cp -r frontend/dist backend/internal/console/dist
	rm -rf backend/internal/homepage/dist && cp -r homepage backend/internal/homepage/dist
```

## Docs touched in the same change

- Root `CLAUDE.md`'s repository-layout tree gains the new top-level `homepage/` entry.
- `frontend/CLAUDE.md` and `docs/design/08-web-console.md` get a short note that the brand mark moved from the torii `Icon` to `BrandMark` (rail-switch), since both documents currently describe/reference the old mark.

## Verification

- `npm run build` (unaffected file set, but touches `ui.tsx`/`App.tsx`/`Login.tsx` — must stay green).
- `cd backend && go build ./...` — new package compiles, `http.go` registers cleanly.
- Manual: open `/` and `/console/` locally, confirm both resolve, confirm the playground buttons and ticking console-mock numbers work without a console error, confirm `prefers-reduced-motion` disables the pipeline/mark/playground animations.
