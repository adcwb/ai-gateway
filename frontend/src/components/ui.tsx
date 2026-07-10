// Shared UI primitives for the console: inline-SVG icons (no emoji, no deps),
// loading/empty/error building blocks, and the layout/form/status components
// (Topbar, Button, Card, CardRow, Field, FormGrid, Pill, TableWrap, Tabs)
// used across every page instead of raw CSS classes.
import { useEffect, useId } from "react";
import { createPortal } from "react-dom";
import type React from "react";

export type IconName =
  | "dashboard" | "key" | "providers" | "audit" | "tenants" | "billing"
  | "refresh" | "plus" | "copy" | "check" | "eye" | "trash" | "logout" | "globe"
  | "alert" | "inbox" | "close" | "sync" | "search" | "settings" | "pricetag" | "users" | "drag";

const PATHS: Record<IconName, React.ReactNode> = {
  dashboard: <><rect x="3" y="3" width="7" height="9" rx="1" /><rect x="14" y="3" width="7" height="5" rx="1" /><rect x="14" y="12" width="7" height="9" rx="1" /><rect x="3" y="16" width="7" height="5" rx="1" /></>,
  key: <><circle cx="8" cy="16" r="4" /><path d="M10.8 13.2 20 4" /><path d="M16 4h4v4" /></>,
  providers: <><rect x="3" y="4" width="18" height="7" rx="1.5" /><rect x="3" y="13" width="18" height="7" rx="1.5" /><path d="M7 7.5h.01" /><path d="M7 16.5h.01" /></>,
  audit: <><path d="M8 6h13" /><path d="M8 12h13" /><path d="M8 18h13" /><path d="M3.5 6h.01" /><path d="M3.5 12h.01" /><path d="M3.5 18h.01" /></>,
  tenants: <><rect x="4" y="3" width="16" height="18" rx="1" /><path d="M9 21v-4h6v4" /><path d="M9 7h.01M15 7h.01M9 11h.01M15 11h.01" /></>,
  billing: <><rect x="2" y="5" width="20" height="14" rx="2" /><path d="M2 10h20" /></>,
  refresh: <><polyline points="23 4 23 10 17 10" /><path d="M20.5 15a9 9 0 1 1-2.1-9.4L23 10" /></>,
  sync: <><polyline points="23 4 23 10 17 10" /><path d="M20.5 15a9 9 0 1 1-2.1-9.4L23 10" /></>,
  plus: <><path d="M12 5v14" /><path d="M5 12h14" /></>,
  copy: <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
  check: <path d="M20 6 9 17l-5-5" />,
  eye: <><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" /><circle cx="12" cy="12" r="3" /></>,
  trash: <><path d="M3 6h18" /><path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" /><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /><path d="M10 11v6M14 11v6" /></>,
  logout: <><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><path d="M16 17l5-5-5-5" /><path d="M21 12H9" /></>,
  globe: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18" /><path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18z" /></>,
  alert: <><path d="M12 9v4" /><path d="M12 17h.01" /><path d="M10.3 3.9l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3.1l-8-14a2 2 0 0 0-3.4 0z" /></>,
  inbox: <><path d="M22 12h-6l-2 3h-4l-2-3H2" /><path d="M5.5 5.5 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.5A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.5z" /></>,
  close: <><path d="M18 6 6 18" /><path d="M6 6l12 12" /></>,
  search: <><circle cx="11" cy="11" r="7" /><path d="M21 21l-4.3-4.3" /></>,
  settings: <><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></>,
  pricetag: <><path d="M12.5 2H4a2 2 0 0 0-2 2v8.5a2 2 0 0 0 .59 1.41l9 9a2 2 0 0 0 2.82 0l7.5-7.5a2 2 0 0 0 0-2.82l-9-9A2 2 0 0 0 12.5 2z" /><path d="M7.5 7.5h.01" /></>,
  users: <><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M23 21v-2a4 4 0 0 0-3-3.87" /><path d="M16 3.13a4 4 0 0 1 0 7.75" /></>,
  drag: <><circle cx="9" cy="6" r="1.2" /><circle cx="15" cy="6" r="1.2" /><circle cx="9" cy="12" r="1.2" /><circle cx="15" cy="12" r="1.2" /><circle cx="9" cy="18" r="1.2" /><circle cx="15" cy="18" r="1.2" /></>,
};

/** Icon size scale: sm(14) buttons/row-actions, md(16) nav/stat chips,
 *  lg(26) empty-state/brand marks — every call site should use one of
 *  these three rather than an arbitrary number. */
export const ICON_SIZE = { sm: 14, md: 16, lg: 26 } as const;

export function Icon({ name, size = ICON_SIZE.md, className }: { name: IconName; size?: number; className?: string }) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={size >= ICON_SIZE.lg ? 1.6 : 1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {PATHS[name]}
    </svg>
  );
}

/** The brand mark — a rail switch: one input line meets a decision point
 *  that connects to exactly one of three output lines (the other two sit
 *  faint, idle). Two-tone (ink stroke + a fixed amber dot), so unlike
 *  every other single-tone `Icon` it isn't tinted via `currentColor` and
 *  doesn't belong in that component. Static — the console already spends
 *  its one animated signature on `Live`'s pilot lamp; the animated cycling
 *  rendition of this same mark lives only on the public homepage
 *  (`homepage/`, plain HTML/CSS, no React), not here. */
export function BrandMark({ size = 24, strokeWidth = 5.5, className }: { size?: number; strokeWidth?: number; className?: string }) {
  // strokeWidth is in the 64-unit viewBox, so it scales WITH size — a large
  // decorative rendition (e.g. a login-pane background watermark) needs an
  // explicitly thin value passed in, or the proportionally-identical stroke
  // becomes tens of pixels thick and the geometry reads as a blob, not a mark.
  return (
    <svg width={size} height={(size * 48) / 64} viewBox="0 0 64 48" className={className} aria-hidden="true" focusable="false">
      <line x1="4" y1="24" x2="20" y2="24" stroke="var(--text)" strokeWidth={strokeWidth} strokeLinecap="round" />
      <line x1="44" y1="8" x2="60" y2="8" stroke="var(--text)" strokeWidth={strokeWidth} strokeLinecap="round" opacity={0.25} />
      <line x1="44" y1="40" x2="60" y2="40" stroke="var(--text)" strokeWidth={strokeWidth} strokeLinecap="round" opacity={0.25} />
      <path d="M20 24 L44 24" stroke="var(--text)" strokeWidth={strokeWidth} strokeLinecap="round" fill="none" />
      <line x1="44" y1="24" x2="60" y2="24" stroke="var(--text)" strokeWidth={strokeWidth} strokeLinecap="round" />
      <circle cx="32" cy="24" r={strokeWidth * 0.7} fill="var(--accent)" />
    </svg>
  );
}

/** Reserved-space loader so content never jumps on arrival. */
export function Skeleton({ w, h = 16, r }: { w?: number | string; h?: number | string; r?: number | string }) {
  return <span className="skeleton" style={{ display: "inline-block", width: w, height: h, borderRadius: r ?? 6, verticalAlign: "middle" }} />;
}

export function Spinner({ size = 16 }: { size?: number }) {
  return <Icon name="refresh" size={size} className="spin" />;
}

/** Pulsing real-time indicator — an embossed bezel housing a glowing amber
 *  bulb, like a pilot lamp on physical routing hardware. Respects
 *  prefers-reduced-motion via CSS. */
export function Live({ label }: { label: string }) {
  return (
    <span className="live" title={label}>
      <span className="live-bezel"><span className="live-bulb" /></span>
      {label}
    </span>
  );
}

/** The eyebrow+title+actions page header — identical structure on every page. */
export function Topbar({
  eyebrow,
  title,
  actions,
}: {
  eyebrow: string;
  title: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="topbar">
      <div className="titles">
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
      </div>
      {actions && <div className="actions">{actions}</div>}
    </div>
  );
}

export type ButtonVariant = "primary" | "secondary" | "ghost" | "subtle" | "danger";

/** Five-tier button so a solid ink fill isn't the only visual weight on a
 *  page: primary (the one committing action), secondary (tonal amber),
 *  ghost (hairline, tertiary), subtle (flat, repeated row actions), danger. */
export function Button({
  variant = "primary",
  size = "md",
  className,
  ...rest
}: {
  variant?: ButtonVariant;
  size?: "md" | "sm";
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const cls = [variant !== "primary" && variant, size === "sm" && "sm", className].filter(Boolean).join(" ");
  return <button className={cls || undefined} {...rest} />;
}

export type CardTone = "default" | "success" | "toplist";

/** Generic card container — the `.card` shell already used ad hoc for plain
 *  panels (create-forms, stat groupings) alongside the richer `StatCard`. */
export function Card({
  tone = "default",
  className,
  ...rest
}: { tone?: CardTone } & React.HTMLAttributes<HTMLDivElement>) {
  const cls = ["card", tone !== "default" && tone, className].filter(Boolean).join(" ");
  return <div className={cls} {...rest} />;
}

/** The flex-wrap row that groups Cards/StatCards. */
export function CardRow({ children, style }: { children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <div className="cards" style={style}>
      {children}
    </div>
  );
}

/** A labeled form field — `<label className="field">` wrapping a
 *  field-label + input; `row` gives the inline checkbox layout, `span`
 *  the form-grid column span. */
export function Field({
  label,
  children,
  span,
  row,
  className,
  style,
}: {
  label: React.ReactNode;
  children: React.ReactNode;
  span?: 2 | 3;
  row?: boolean;
  className?: string;
  style?: React.CSSProperties;
}) {
  const cls = ["field", span === 2 && "span-2", span === 3 && "span-3", className].filter(Boolean).join(" ");
  const labelStyle = row ? { margin: 0 } : undefined;
  const fieldStyle = row ? { flexDirection: "row" as const, alignItems: "center", gap: 8, ...style } : style;
  const labelEl = <div className="field-label" style={labelStyle}>{label}</div>;
  return (
    <label className={cls} style={fieldStyle}>
      {/* row mode is a checkbox field: input-then-label, matching a native checkbox layout */}
      {row ? (<>{children}{labelEl}</>) : (<>{labelEl}{children}</>)}
    </label>
  );
}

/** The 3-column form grid; nests fine for sub-grids. */
export function FormGrid({ children, style }: { children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <div className="form-grid" style={style}>
      {children}
    </div>
  );
}

export type PillTone = "on" | "off" | "warn" | "info" | "err";

/** Status badge. `variant="outline"` is the lower-emphasis form for
 *  metadata tags that shouldn't compete visually with real status. */
export function Pill({
  tone = "off",
  variant = "soft",
  children,
}: {
  tone?: PillTone;
  variant?: "soft" | "outline";
  children: React.ReactNode;
}) {
  const cls = ["pill", variant === "outline" ? "outline" : tone].filter(Boolean).join(" ");
  return <span className={cls}>{children}</span>;
}

/** Thin wrapper around a hand-written `<table>`; scrolls its own overflow
 *  horizontally so the page body never scrolls sideways. */
export function TableWrap({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={["table-wrap", className].filter(Boolean).join(" ")}>{children}</div>;
}

/** Recessed/embossed tab strip — used today only on Usage/Audit. */
export function Tabs({
  items,
  active,
  onChange,
}: {
  items: { key: string; label: string }[];
  active: string;
  onChange: (key: string) => void;
}) {
  return (
    <div className="tabs">
      {items.map((it) => (
        <button
          key={it.key}
          type="button"
          className={`tab ${active === it.key ? "active" : ""}`}
          onClick={() => onChange(it.key)}
        >
          {it.label}
        </button>
      ))}
    </div>
  );
}

export function EmptyState({
  icon = "inbox",
  title,
  sub,
  action,
}: {
  icon?: IconName;
  title: string;
  sub?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <div className="ico"><Icon name={icon} size={26} /></div>
      <div className="ttl">{title}</div>
      {sub && <div className="sub">{sub}</div>}
      {action && <div className="act">{action}</div>}
    </div>
  );
}

export function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="error-banner" role="alert">
      <span className="ico"><Icon name="alert" size={16} /></span>
      <span style={{ flex: 1 }}>{message}</span>
      {onRetry && (
        <button className="ghost sm retry" onClick={onRetry}>
          <Icon name="refresh" size={13} /> Retry
        </button>
      )}
    </div>
  );
}

/** Centered overlay dialog, portaled to document.body so table/card overflow
 *  never clips it. Backdrop click and Escape both close it. */
export function Modal({
  title,
  onClose,
  children,
  width = 640,
  closeLabel = "Close",
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  width?: number;
  closeLabel?: string;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return createPortal(
    <div
      className="modal-overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" style={{ maxWidth: width }} role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-header">
          <h2>{title}</h2>
          <button className="ghost sm" onClick={onClose} aria-label={closeLabel} title={closeLabel}>
            <Icon name="close" size={14} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>,
    document.body,
  );
}

/** Usage-vs-quota bar. A quota of 0 means unlimited (per the backend's
 *  existing convention, e.g. hourlyToolCallQuota) — rendered as a bare
 *  value with no fill rather than a misleading 0% bar. */
export function Gauge({
  label,
  used,
  quota,
  unlimitedLabel = "unlimited",
}: {
  label: string;
  used: number;
  quota: number;
  unlimitedLabel?: string;
}) {
  const unlimited = quota <= 0;
  const pct = unlimited ? 0 : Math.min(100, (used / quota) * 100);
  const tone = pct >= 100 ? "err" : pct >= 80 ? "warn" : "";
  return (
    <div className="gauge">
      <div className="gauge-head">
        <span className="gauge-label">{label}</span>
        <span className="gauge-value">{unlimited ? `${used} / ${unlimitedLabel}` : `${used} / ${quota}`}</span>
      </div>
      {!unlimited && (
        <div className="gauge-track">
          <div className={`gauge-fill ${tone}`} style={{ width: `${pct}%` }} />
        </div>
      )}
    </div>
  );
}

/** Convenience: render a value or its loading skeleton. */
export function StatValue({ value, loading, suffix }: { value: React.ReactNode; loading?: boolean; suffix?: string }) {
  if (loading) return <Skeleton w={64} h={24} />;
  return <>{value}{suffix && <span className="faint" style={{ fontSize: 13, marginLeft: 4 }}>{suffix}</span>}</>;
}

/** Stat card: an accent-tinted icon chip + label + big monospace value.
 *  The chip gives each metric a visual anchor so a row of cards reads as a
 *  set rather than a row of plain boxes. */
export function StatCard({
  icon,
  label,
  value,
  sub,
  loading,
  tone = "accent",
  delta,
  sparkline,
}: {
  icon: IconName;
  label: string;
  value: React.ReactNode;
  sub?: React.ReactNode;
  loading?: boolean;
  tone?: "accent" | "ok" | "warn" | "info" | "err";
  /** Trend arrow + %, colored by whether *this* metric's direction is good —
   *  e.g. rising requests is good (up), falling latency is also good (down). */
  delta?: { pct: number; goodDirection: "up" | "down" };
  /** Optional inline single-hue trend line, no axes — same dependency-free
   *  SVG approach as AreaChart. */
  sparkline?: number[];
}) {
  const rising = delta ? delta.pct >= 0 : false;
  const good = delta ? (rising ? delta.goodDirection === "up" : delta.goodDirection === "down") : false;
  const body = (
    <>
      <div className="value">{loading ? <Skeleton w={64} h={24} /> : value}</div>
      {delta && !loading && (
        <div className={`delta ${good ? "good" : "bad"}`}>
          {rising ? "▲" : "▼"} {Math.abs(delta.pct).toFixed(1)}%
        </div>
      )}
      {sub != null && sub !== "" && <div className="sub">{sub}</div>}
    </>
  );
  const showSparkline = sparkline && sparkline.length > 1 && !loading;
  return (
    <div className={`card stat tone-${tone}`}>
      <div className="stat-head">
        <span className="chip"><Icon name={icon} size={16} /></span>
        <span className="label">{label}</span>
      </div>
      {/* .row's flex/padding is only appropriate once there's a sparkline to
       *  sit beside — every other caller keeps the original flat layout. */}
      {showSparkline ? (
        <div className="row">
          <div>{body}</div>
          <Sparkline points={sparkline!} />
        </div>
      ) : (
        body
      )}
    </div>
  );
}

/** Single-hue inline trend line, no axes/gridlines — the sparkline half of
 *  the stat-tile figure contract (value + delta + optional sparkline). */
export function Sparkline({ points, width = 72, height = 28 }: { points: number[]; width?: number; height?: number }) {
  const max = Math.max(...points);
  const min = Math.min(...points);
  const range = max - min || 1;
  const step = width / (points.length - 1);
  const path = points
    .map((p, i) => `${i === 0 ? "M" : "L"}${(i * step).toFixed(1)},${(height - ((p - min) / range) * height).toFixed(1)}`)
    .join(" ");
  return (
    <svg className="spark" width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      <path d={path} fill="none" stroke="var(--accent)" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** Dependency-free SVG area chart for daily rollups. Gradient fill + crisp
 *  line + grid + axis; native <title> tooltips on each point keep it
 *  accessible without JS hover plumbing. */
export function AreaChart({
  title,
  points,
  loading,
  fmt,
}: {
  title: string;
  points: { label: string; value: number }[];
  loading?: boolean;
  fmt?: (v: number) => string;
}) {
  const raw = useId();
  const gid = "ag" + raw.replace(/[^a-zA-Z0-9]/g, "");
  const W = 480, H = 168, padT = 12, padB = 24, padL = 8, padR = 8;
  const innerW = W - padL - padR;
  const innerH = H - padT - padB;
  const n = points.length;
  const max = Math.max(1, ...points.map((p) => p.value));
  const x = (i: number) => padL + (n <= 1 ? innerW / 2 : (i / (n - 1)) * innerW);
  const y = (v: number) => padT + innerH - (v / max) * innerH;
  const line = points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)} ${y(p.value).toFixed(1)}`).join(" ");
  const area = n > 0
    ? `${line} L${x(n - 1).toFixed(1)} ${(padT + innerH).toFixed(1)} L${x(0).toFixed(1)} ${(padT + innerH).toFixed(1)} Z`
    : "";
  const grid = [0, 0.25, 0.5, 0.75, 1];
  const fmtVal = fmt ?? ((v: number) => String(v));

  return (
    <div className="card chart area" style={{ flex: 1, minWidth: 320 }}>
      <div className="chart-title">{title}</div>
      {loading && n === 0 ? (
        <Skeleton w="100%" h={H} />
      ) : n === 0 ? (
        <div className="empty-note">—</div>
      ) : (
        <svg className="chart" width="100%" viewBox={`0 0 ${W} ${H}`} style={{ marginTop: 6, display: "block" }}>
          <defs>
            <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.34" />
              <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <g className="grid">
            {grid.map((g) => {
              const gy = padT + innerH - g * innerH;
              return <line key={g} x1={padL} x2={W - padR} y1={gy} y2={gy} />;
            })}
          </g>
          {area && <path className="area" d={area} fill={`url(#${gid})`} />}
          {line && <path className="line" d={line} />}
          {points.map((p, i) => (
            <g key={p.label + i} className="pt">
              <circle cx={x(i)} cy={y(p.value)} r={2.6}>
                <title>{`${p.label}: ${fmtVal(p.value)}`}</title>
              </circle>
              {n <= 16 && (
                <text className="axis" x={x(i)} y={H - 7} textAnchor="middle">{p.label}</text>
              )}
            </g>
          ))}
        </svg>
      )}
    </div>
  );
}

/** Skeleton rows for a table while data loads — reserves space, no jump. */
export function TableSkeleton({ cols, rows = 6 }: { cols: number; rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <tr key={r}>
          {Array.from({ length: cols }).map((_, c) => (
            <td key={c}><Skeleton w={c === 0 ? 130 : 80} h={12} /></td>
          ))}
        </tr>
      ))}
    </>
  );
}

/** HTTP status code rendered as a tonal pill (2xx ok / 4xx warn / 5xx err). */
export function HttpStatus({ code }: { code?: number }) {
  if (code == null) return <span className="faint">—</span>;
  const tone = code < 300 ? "on" : code < 500 ? "warn" : "err";
  return <span className={`pill ${tone}`}>{code}</span>;
}

