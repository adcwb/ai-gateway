import { useState } from "react";
import { api, useAsync, type UsagePoint } from "../api/client";
import { t, type Lang } from "../i18n";
import { AreaChart, ErrorBanner, Icon } from "../components/ui";

const RANGES = [7, 14, 30, 90] as const;

export default function Usage({ lang }: { lang: Lang }) {
  const [days, setDays] = useState<(typeof RANGES)[number]>(14);

  const { data, loading, error, refresh } = useAsync<UsagePoint[]>(
    (s) => api.get<UsagePoint[]>(`/ai/gateway/stats/timeseries?days=${days}`, { signal: s }).then((ts) => ts ?? []),
    [days],
  );
  const series = data ?? [];
  const firstLoad = loading && series.length === 0;

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navObserve", lang)}</div>
          <h1>{t("usage", lang)}</h1>
        </div>
        <div className="actions flex gap-8 items-center">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
        </div>
      </div>

      <div className="tabs">
        {RANGES.map((d) => (
          <button
            key={d}
            type="button"
            className={`tab ${d === days ? "active" : ""}`}
            onClick={() => setDays(d)}
          >
            {t("daysN", lang).replace("{n}", String(d))}
          </button>
        ))}
      </div>

      {error && <ErrorBanner message={`${t("loadFailed", lang)}: ${error}`} onRetry={refresh} />}

      <div className="cards">
        <AreaChart
          title={t("requests", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: p.requests }))}
          loading={firstLoad}
          fmt={(v) => String(v)}
        />
        <AreaChart
          title={t("promptTokens", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: p.promptTokens }))}
          loading={firstLoad}
          fmt={(v) => String(v)}
        />
      </div>
      <div className="cards">
        <AreaChart
          title={t("completionTokens", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: p.completionTokens }))}
          loading={firstLoad}
          fmt={(v) => String(v)}
        />
        <AreaChart
          title={t("billedTrend", lang)}
          points={series.map((p) => ({ label: p.day.slice(5), value: Math.round(p.priceCredits * 100) / 100 }))}
          loading={firstLoad}
          fmt={(v) => v.toFixed(2)}
        />
      </div>
    </div>
  );
}
