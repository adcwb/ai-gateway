import { useEffect, useState } from "react";
import { api, type KeyStats, type ProviderHealth } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Dashboard({ lang }: { lang: Lang }) {
  const [stats, setStats] = useState<KeyStats | null>(null);
  const [health, setHealth] = useState<ProviderHealth[]>([]);
  const [error, setError] = useState("");

  const load = async () => {
    setError("");
    try {
      const [s, h] = await Promise.all([
        api.get<KeyStats>("/ai/gateway/key/stats"),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health"),
      ]);
      setStats(s);
      setHealth(h ?? []);
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
