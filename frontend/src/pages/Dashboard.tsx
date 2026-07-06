import { api, useAsync, type KeyStats, type ProviderHealth, type UsageOverview, type UsagePoint } from "../api/client";
import { t, type Lang } from "../i18n";
import { AreaChart, EmptyState, ErrorBanner, Icon, Live, Skeleton, StatCard, TableSkeleton } from "../components/ui";

export default function Dashboard({ lang }: { lang: Lang }) {
  const { data, loading, error, refresh } = useAsync<
    [KeyStats, ProviderHealth[], UsageOverview, UsagePoint[]]
  >(
    (s) =>
      Promise.all([
        api.get<KeyStats>("/ai/gateway/key/stats", { signal: s }),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health", { signal: s }).then((h) => h ?? []),
        api.get<UsageOverview>("/ai/gateway/stats/overview?days=7", { signal: s }),
        api.get<UsagePoint[]>("/ai/gateway/stats/timeseries?days=14", { signal: s }).then((ts) => ts ?? []),
      ]),
    [],
    { intervalMs: 15_000 },
  );
  const stats = data?.[0] ?? null;
  const health = data?.[1] ?? [];
  const usage = data?.[2] ?? null;
  const series = data?.[3] ?? [];

  // Only show skeletons on the first load — polling shouldn't flash them.
  const firstKey = loading && !stats;
  const firstUsage = loading && !usage;
  const firstSeries = loading && series.length === 0;
  const firstHealth = loading && health.length === 0;

  const num = (v: unknown) => (typeof v === "number" ? v : "—");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navOperate", lang)}</div>
          <h1>{t("dashboard", lang)}</h1>
        </div>
        <div className="actions flex gap-8 items-center">
          <Live label={`${t("live", lang)} 15s`} />
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
        </div>
      </div>

      {error && <ErrorBanner message={`${t("loadFailed", lang)}: ${error}`} onRetry={refresh} />}

      {/* Key stats */}
      <div className="cards">
        <StatCard icon="key" tone="accent" label={t("totalKeys", lang)} loading={firstKey} value={num(stats?.total)} />
        <StatCard icon="check" tone="ok" label={t("enabledKeys", lang)} loading={firstKey} value={num(stats?.enabled)} />
        <StatCard icon="key" tone="warn" label={t("disabledKeys", lang)} loading={firstKey} value={num(stats?.disabled)} />
      </div>

      {/* Usage overview */}
      <h1 className="section-title">{t("usage7d", lang)}</h1>
      <div className="cards">
        <StatCard icon="audit" tone="info" label={t("requests", lang)} loading={firstUsage} value={usage?.requests ?? "—"} />
        <StatCard icon="dashboard" tone="accent" label={t("promptTokens", lang)} loading={firstUsage} value={usage?.promptTokens ?? "—"} />
        <StatCard icon="dashboard" tone="accent" label={t("completionTokens", lang)} loading={firstUsage} value={usage?.completionTokens ?? "—"} />
        <StatCard icon="billing" tone="ok" label={t("price", lang)} loading={firstUsage} value={usage ? usage.priceCredits.toFixed(2) : "—"} />
        <StatCard icon="check" tone="ok" label={t("cacheHits", lang)} loading={firstUsage} value={usage?.cacheHits ?? "—"} />
        <div className="card toplist">
          <div className="label">{t("topModels", lang)}</div>
          <div style={{ marginTop: 4 }}>
            {firstUsage ? (
              <div className="flex" style={{ flexDirection: "column", gap: 6 }}>
                {Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} w="100%" h={12} />)}
              </div>
            ) : (usage?.topModels ?? []).slice(0, 5).length === 0 ? (
              <span className="muted">—</span>
            ) : (
              (usage?.topModels ?? []).slice(0, 5).map((m) => (
                <div className="row" key={m.model}>
                  <span className="mono truncate" style={{ maxWidth: 160 }}>{m.model}</span>
                  <span className="muted">{m.requests}</span>
                </div>
              ))
            )}
          </div>
        </div>
      </div>

      {/* Trends */}
      <div className="cards">
        <AreaChart
          title={t("usageTrend", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: p.requests }))}
          loading={firstSeries}
          fmt={(v) => String(v)}
        />
        <AreaChart
          title={t("billedTrend", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: Math.round(p.priceCredits * 100) / 100 }))}
          loading={firstSeries}
          fmt={(v) => v.toFixed(2)}
        />
      </div>

      {/* Provider health */}
      <h1 className="section-title">{t("providerHealth", lang)}</h1>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("name", lang)}</th>
              <th>{t("state", lang)}</th>
              <th>{t("status", lang)}</th>
              <th className="num">{t("weight", lang)}</th>
              <th className="num">{t("priority", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {firstHealth ? (
              <TableSkeleton cols={5} rows={4} />
            ) : health.length === 0 ? (
              <tr>
                <td colSpan={5}>
                  <EmptyState icon="providers" title={t("empty", lang)} />
                </td>
              </tr>
            ) : (
              health.map((p) => (
                <tr key={p.providerId}>
                  <td>{p.name}</td>
                  <td>
                    <span className={`dot ${p.state}`} />{t(`breaker_${p.state}`, lang)}
                  </td>
                  <td>
                    <span className={`pill ${p.isEnabled ? "on" : "off"}`}>
                      {t(p.isEnabled ? "enabled" : "disabled", lang)}
                    </span>
                  </td>
                  <td className="num mono">{p.weight}</td>
                  <td className="num mono">{p.priority}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
