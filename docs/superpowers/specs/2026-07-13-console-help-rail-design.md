# Console help rail — design

## Problem

On wide viewports the console's `.main` content column is capped at
`max-width: 1340px` (`frontend/src/styles.css`), but `.layout` is a plain flex
row with no centering — `.sidebar` (236px) and `.main` sit flush left, and any
viewport width beyond `236 + 1340 = 1576px` is unclaimed background. On a
1920px+ display this is a visibly empty strip to the right of every page.

## Goals

- Fill that space with genuinely useful, page-specific contextual help — not
  decoration — on the console's 8 CRUD list pages.
- Zero impact below the point where the space doesn't exist: narrow/laptop
  viewports keep today's layout exactly as-is.
- Reuse the existing hand-rolled design system (`ui.tsx` / `styles.css`); no
  new dependency.

## Non-goals

- Dashboard, Audit, Usage, Billing, Settings: chart/tab-driven layouts, not
  the shared list-page pattern. No rail content for these; the `<aside>`
  simply doesn't render on their routes.
- Per-tip dismissal, analytics, or remote-configurable content. This is
  static, hand-written copy shipped with the build.

## Layout mechanics

`App.tsx` renders `<div className="layout">` containing `<nav
className="sidebar">` and `<main className="main">`. A third sibling,
`<aside className="help-rail">`, is added after `<main>`:

```css
.help-rail { margin-left: auto; flex-shrink: 0; width: 280px;
  position: sticky; top: 0; max-height: 100vh; overflow-y: auto; }
.help-rail.collapsed { width: 44px; }
@media (max-width: 1679px) { .help-rail { display: none; } }
```

`margin-left: auto` pins it to the right edge of `.layout`, reproducing
exactly the dead zone the user pointed at, now filled. The `1680px` viewport
breakpoint (sidebar 236 + main max 1340 + rail 280 + gap ≈ 1856, floored to a
round number with margin) keeps it off at common 1440–1600px laptop widths,
where there isn't room for it without squeezing `.main` into horizontal
scroll. It is always mounted in the DOM (so the collapsed/expanded toggle
state doesn't reset across the breakpoint) — only `display: none` hides it.

## Component: `HelpRail` (new, in `components/ui.tsx`)

```tsx
function HelpRail({
  icon, tips, collapsed, onToggle, title, collapseLabel, expandLabel,
}: {
  icon: IconName;
  tips: { title: string; body: string }[];
  collapsed: boolean;
  onToggle: () => void;
  title: string;
  collapseLabel: string;
  expandLabel: string;
})
```

- Expanded: header (icon + `title`, e.g. "Tips" / "提示") with a collapse
  button, then each tip as a title (bold, ~13px) + body (muted, ~12.5px)
  block separated by hairline dividers — visually consistent with
  `EmptyState`/`Card` typography already in the system.
- Collapsed: a 44px strip with just the expand button (icon rotated 180°
  via a `chevron` icon added to the `IconName` union in `ui.tsx`, following
  the existing inline-SVG pattern — no new dependency).
- Collapse state: single global boolean in `localStorage`
  (`aigw_help_rail_collapsed`), lifted into `App.tsx` since that's where the
  `<aside>` is rendered; not per-page (one preference, not eight).

## Content wiring

New file `frontend/src/helpContent.ts`:

```ts
export const HELP_CONTENT: Record<string, { icon: IconName; tips: { titleKey: string; bodyKey: string }[] }> = {
  "/keys": { icon: "key", tips: [...] },
  "/providers": { icon: "providers", tips: [...] },
  "/models-pricing": { icon: "pricetag", tips: [...] },
  "/model-mappings": { icon: "sync", tips: [...] },
  "/guardrail-policies": { icon: "alert", tips: [...] },
  "/mcp-servers": { icon: "providers", tips: [...] },
  "/tenants": { icon: "tenants", tips: [...] },
  "/users": { icon: "users", tips: [...] },
};
```

`App.tsx` looks up `HELP_CONTENT[useLocation().pathname]`; renders
`<HelpRail>` only when there's a match, so the five chart/tab pages simply
get no `<aside>` (no layout-shift concern — `.main` doesn't depend on the
rail's presence, and it's `margin-left: auto` regardless of whether it
exists).

Every `titleKey`/`bodyKey` is a new entry in `i18n.ts`'s `dict`, following
the file's existing flat key-per-string convention (both `en` and `zh`
required per the frontend hard rules). Plus three chrome strings:
`helpRailTitle`, `helpRailCollapse`, `helpRailExpand`.

## Content (3 tips per page, grounded in each page's actual fields/behavior)

**Keys** — quotas & per-model overrides (0 = unlimited); response cache
(exact + semantic, TTL, threshold, cache-hit discount); guardrail binding
falls back to the tenant default when unset.

**Providers** — weight (same-priority weighted round-robin) vs priority
(fallback order, lowest first); breaker lifecycle
closed→open→half-open; sync-models pulls the live list instead of manual
CSV entry.

**Models & Pricing** — model catalog (upstream cost) vs price tables
(independent sell-side pricing); pattern tester previews wildcard/regex
matches before saving; price tables are multi-currency via Settings →
Credits rates.

**Model Mappings** — virtual model name (what clients send) vs real model
(what's forwarded upstream); fallback chain tried in order after a
retryable failure, drag to reorder; "add all models from…" bulk-appends a
provider's matching-modality models.

**Guardrail Policies** — checker chain runs in the order checkers are
added; exactly one policy can be the tenant default, used when a key has no
explicit binding; the `external` checker calls a custom gRPC endpoint.

**MCP Servers** — proxy path `/ai/mcp/{serverName}`, gated by the same
`sk-vk-*` virtual keys as model traffic; empty tool whitelist = every tool
is callable; tool calls consume a separate hourly quota dimension from
tokens/requests.

**Tenants** — tenant → project → keys hierarchy, billing/quota templates
live at tenant/project level; a project's quota template pre-fills new
keys' defaults; the gateway boots with one default tenant, more are for
billing/quota isolation.

**Users & Access** — four-role matrix (owner > admin > member > viewer)
applied to RBAC-covered actions; admin API keys automate the management
API itself, not `/ai/v1` traffic; members appear automatically after their
first SSO login, then get a role assigned.

Full bilingual copy (transcribe verbatim into `i18n.ts` — do not
re-derive):

### Keys

1. **en** Quotas & rate limits / **zh** 配额与限流 — en: "Global quotas can be
   overridden per model; leave a field at 0 for unlimited." / zh:
   "全局配额（日/时 Token、请求数、并发、积分）支持按模型覆盖，字段留 0 表示不限。"
2. **en** Response cache / **zh** 响应缓存 — en: "Exact-match and semantic
   caching toggle independently, with TTL, similarity threshold, and a
   cache-hit billing discount." / zh:
   "精确匹配缓存与语义缓存可分别开关，含 TTL、相似度阈值与命中折扣。"
3. **en** Guardrail binding / **zh** 防护策略绑定 — en: "A key with no
   explicit policy binding falls back to the tenant's default guardrail
   policy." / zh: "未显式绑定策略的 Key 会使用租户默认策略。"

### Providers

1. **en** Weight vs priority / **zh** 权重与优先级 — en: "Same priority =
   weighted round-robin; different priority = fallback order, lowest tried
   first." / zh: "同优先级按权重加权轮询；不同优先级则是故障转移顺序，数字小的先尝试。"
2. **en** Breaker states / **zh** 熔断状态 — en: "closed (healthy) → open
   (tripped, fails fast) → half-open (probing recovery)." / zh:
   "关闭（健康）→打开（熔断，快速失败）→半开（探测恢复）。"
3. **en** Sync models / **zh** 同步模型 — en: "Pulls the live model list
   from upstream instead of typing a comma-separated list by hand." / zh:
   "直接从上游拉取真实模型列表，无需手动输入逗号分隔的模型名。"

### Models & Pricing

1. **en** Catalog vs price tables / **zh** 目录 vs 价格表 — en: "The model
   catalog tracks upstream cost; price tables are independent sell-side
   pricing you assign to tenants." / zh:
   "模型目录记录上游真实成本；价格表是可独立分配给租户的对外售价。"
2. **en** Pattern tester / **zh** 匹配测试器 — en: "Preview which known
   models a wildcard/regex price row matches before saving." / zh:
   "保存前预览通配符/正则会命中哪些已知模型。"
3. **en** Multi-currency / **zh** 多币种 — en: "Price tables are priced per
   currency; rates are configured under Settings → Credits rates." / zh:
   "价格表按币种计价，汇率在\"系统设置→积分汇率\"里配置。"

### Model Mappings

1. **en** Virtual vs real model / **zh** 虚拟名 vs 真实模型 — en: "The
   virtual name is what clients put in the `model` field; the real model is
   what's actually sent upstream." / zh:
   "虚拟模型名是客户端请求里写的 model 字段，真实模型才是转发给提供方的名字。"
2. **en** Fallback chain / **zh** 故障转移链 — en: "Tried in order after the
   primary mapping hits a retryable failure; drag to reorder." / zh:
   "主映射失败且可重试时按顺序尝试，可拖拽调整顺序。"
3. **en** Bulk add / **zh** 批量添加 — en: "\"Add all models from…\"
   appends every matching-modality model of a provider that isn't already
   in the chain." / zh:
   "\"从渠道批量添加\"会把某提供方下模态匹配、尚未在链中的模型一次性加入。"

### Guardrail Policies

1. **en** Ordered checker chain / **zh** 检测链按序执行 — en: "Checkers run
   in the order you add them to the chain." / zh:
   "pii_rules / prompt_injection / topic_fence / external 按加入顺序依次执行。"
2. **en** One default per tenant / **zh** 唯一默认策略 — en: "Keys with no
   explicit binding use whichever policy is marked default." / zh:
   "未绑定策略的 Key 会使用被标记为默认的那一个。"
3. **en** External checker / **zh** 外部检测器 — en: "The \"external\"
   checker calls your own gRPC endpoint for custom logic beyond the
   built-ins." / zh: "\"external\" 通过 gRPC 调用你自己的服务实现内置检测器之外的自定义逻辑。"

### MCP Servers

1. **en** Proxy path / **zh** 代理路径 — en: "Registered servers are
   reachable at `/ai/mcp/{serverName}`, gated by the same `sk-vk-*`
   virtual keys as model traffic." / zh:
   "已注册的服务器可通过 /ai/mcp/{serverName} 访问，鉴权方式与模型调用相同（sk-vk-*）。"
2. **en** Tool whitelist / **zh** 工具白名单 — en: "An empty whitelist means
   every tool the server exposes is callable." / zh:
   "留空表示该服务器暴露的全部工具都可调用。"
3. **en** Separate quota dimension / **zh** 独立配额维度 — en: "Tool calls
   consume their own hourly quota, separate from token/request quotas." /
   zh: "工具调用消耗独立的\"每小时工具调用配额\"，与 Token/请求配额分开计算。"

### Tenants

1. **en** Hierarchy / **zh** 层级关系 — en: "Tenant → project → keys;
   billing and quota templates live at the tenant/project level." / zh:
   "租户→项目→Key；计费与配额模板都挂在租户/项目层。"
2. **en** Quota template inheritance / **zh** 配额模板继承 — en: "A
   project's quota template pre-fills defaults for keys created under
   it." / zh: "项目的配额模板会预填给该项目下新建的 Key。"
3. **en** Default tenant / **zh** 默认租户 — en: "The gateway boots with a
   default tenant so you can issue keys immediately; add more to isolate
   billing and quotas." / zh:
   "网关启动时自带一个默认租户，方便立即签发 Key；多租户用于隔离计费与配额。"

### Users & Access

1. **en** Four-role matrix / **zh** 四级角色矩阵 — en: "owner > admin >
   member > viewer, applied to RBAC-covered actions like key reveal,
   provider/pricing/settings management, billing, and member management." /
   zh: "owner > admin > member > viewer，作用于取回 Key、提供方/价格表/设置管理、计费、成员管理等受控操作。"
2. **en** Admin API key purpose / **zh** 管理员 API Key 用途 — en: "For
   automating the management API itself — not for `/ai/v1` model traffic,
   which is what virtual keys are for." / zh:
   "用于自动化管理接口本身，不用于 /ai/v1 模型调用（那是虚拟 Key 的职责）。"
3. **en** First SSO login / **zh** SSO 首次登录 — en: "Members appear here
   automatically after their first SSO login; you then assign a role." /
   zh: "成员首次通过 SSO 登录后会自动出现在列表里，再手动分配角色。"

## Edge cases

- **Language toggle**: rail content re-renders through the same `t(key,
  lang)` mechanism as everything else — no special handling needed.
- **Route without content** (Dashboard/Audit/Usage/Billing/Settings): no
  `<aside>` rendered; `.main` unaffected either way since it doesn't share a
  flex-basis negotiation with the rail (both are independently sized).
  focus/tab order: the rail comes after `.main` in DOM order, so tab
  order visits page content before rail tips, which is correct.
- **Narrow-then-wide resize**: purely CSS-driven visibility (`display:
  none` under 1680px), so no JS resize listener or remount needed; collapse
  state persists underneath regardless of visibility.

## Testing

No new Playwright coverage — this is presentational, non-interactive-flow
content. Manual check: `npm run dev`, verify the rail appears only above
1680px viewport width, collapses/expands and persists across a reload, and
both `en`/`zh` render correctly on all 8 target pages.
