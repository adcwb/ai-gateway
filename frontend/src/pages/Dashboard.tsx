import { useEffect, useState } from "react";
import { api, type KeyStats, type ProviderHealth, type UsageOverview, type UsagePoint } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Dashboard({ lang }: { lang: Lang }) {
  const [stats, setStats] = useState<KeyStats | null>(null);
  const [health, setHealth] = useState<ProviderHealth[]>([]);
  const [usage, setUsage] = useState<UsageOverview | null>(null);
  const [series, setSeries] = useState<UsagePoint[]>([]);
  const [error, setError] = useState("");

  const load = async () => {
    setError("");
    try {
      const [s, h, u, ts] = await Promise.all([
        api.get<KeyStats>("/ai/gateway/key/stats"),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health"),
        api.get<UsageOverview>("/ai/gateway/stats/overview?days=7"),
        api.get<UsagePoint[]>("/ai/gateway/stats/timeseries?days=14"),
      ]);
      setStats(s);
      setHealth(h ?? []);
      setUsage(u);
      setSeries(ts ?? []);
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 15_000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const num = (v: unknown) => (typeof v === "number" ? v : "—");

  return (
    <div>
      <div className="toolbar">
        <h1>{t("dashboard", lang)}</h1>
        <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
      </div>
      {error && <p className="error-text">{error}</p>}
      <div className="cards">
        <div className="card">
          <div className="label">{t("totalKeys", lang)}</div>
          <div className="value">{num(stats?.total)}</div>
        </div>
        <div className="card">
          <div className="label">{t("enabledKeys", lang)}</div>
          <div className="value">{num(stats?.enabled)}</div>
        </div>
        <div className="card">
          <div className="label">{t("disabledKeys", lang)}</div>
          <div className="value">{num(stats?.disabled)}</div>
        </div>
      </div>

      <h1 style={{ fontSize: 16 }}>{t("usage7d", lang)}</h1>
      <div className="cards">
        <div className="card">
          <div className="label">{t("requests", lang)}</div>
          <div className="value">{usage?.requests ?? "—"}</div>
        </div>
        <div className="card">
          <div className="label">{t("promptTokens", lang)}</div>
          <div className="value">{usage?.promptTokens ?? "—"}</div>
        </div>
        <div className="card">
          <div className="label">{t("completionTokens", lang)}</div>
          <div className="value">{usage?.completionTokens ?? "—"}</div>
        </div>
        <div className="card">
          <div className="label">{t("price", lang)}</div>
          <div className="value">{usage ? usage.priceCredits.toFixed(2) : "—"}</div>
        </div>
        <div className="card">
          <div className="label">{t("cacheHits", lang)}</div>
          <div className="value">{usage?.cacheHits ?? "—"}</div>
        </div>
        <div className="card" style={{ minWidth: 220 }}>
          <div className="label">{t("topModels", lang)}</div>
          <div style={{ marginTop: 4, fontSize: 13 }}>
            {(usage?.topModels ?? []).slice(0, 5).map((m) => (
              <div key={m.model} style={{ display: "flex", justifyContent: "space-between" }}>
                <span>{m.model}</span>
                <span className="muted">{m.requests}</span>
              </div>
            ))}
            {(!usage || usage.topModels.length === 0) && <span className="muted">—</span>}
          </div>
        </div>
      </div>

      <div className="cards">
        <MiniBars
          title={t("usageTrend", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: p.requests }))}
        />
        <MiniBars
          title={t("billedTrend", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: Math.round(p.priceCredits * 100) / 100 }))}
        />
      </div>

      <h1 style={{ fontSize: 16 }}>{t("providerHealth", lang)}</h1>
      <table>
        <thead>
          <tr>
            <th>{t("name", lang)}</th>
            <th>{t("state", lang)}</th>
            <th>{t("status", lang)}</th>
            <th>{t("weight", lang)}</th>
            <th>{t("priority", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {health.length === 0 && (
            <tr><td colSpan={5} className="muted">{t("empty", lang)}</td></tr>
          )}
          {health.map((p) => (
            <tr key={p.providerId}>
              <td>{p.name}</td>
              <td>
                <span className={`dot ${p.state}`} />
                {t(`breaker_${p.state}`, lang)}
              </td>
              <td>
                <span className={`pill ${p.isEnabled ? "on" : "off"}`}>
                  {t(p.isEnabled ? "enabled" : "disabled", lang)}
                </span>
              </td>
              <td>{p.weight}</td>
              <td>{p.priority}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// MiniBars: dependency-free SVG bar chart for the daily usage rollup.
function MiniBars({ title, points }: { title: string; points: { label: string; value: number }[] }) {
  const W = 420;
  const H = 120;
  const pad = 4;
  const max = Math.max(1, ...points.map((p) => p.value));
  const bw = points.length > 0 ? (W - pad * 2) / points.length : W;
  return (
    <div className="card" style={{ flex: 1, minWidth: 320 }}>
      <div className="label">{title}</div>
      {points.length === 0 ? (
        <div className="muted" style={{ marginTop: 8 }}>—</div>
      ) : (
        <svg width="100%" viewBox={`0 0 ${W} ${H + 18}`} style={{ marginTop: 6 }}>
          {points.map((p, i) => {
            const h = Math.max(2, (p.value / max) * H);
            return (
              <g key={p.label + i}>
                <rect
                  x={pad + i * bw + 1}
                  y={H - h}
                  width={Math.max(2, bw - 3)}
                  height={h}
                  rx={2}
                  fill="var(--accent)"
                  opacity={0.85}
                >
                  <title>{`${p.label}: ${p.value}`}</title>
                </rect>
                {points.length <= 16 && (
                  <text x={pad + i * bw + bw / 2} y={H + 13} fontSize="8" fill="var(--muted)" textAnchor="middle">
                    {p.label}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
      )}
    </div>
  );
}
