import { useEffect, useState } from "react";
import { api, type Provider, type ProviderHealth } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Providers({ lang }: { lang: Lang }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [health, setHealth] = useState<Map<number, ProviderHealth>>(new Map());
  const [error, setError] = useState("");

  const load = async () => {
    setError("");
    try {
      const [list, h] = await Promise.all([
        api.get<Provider[]>("/ai/gateway/providers"),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health"),
      ]);
      setProviders(list ?? []);
      setHealth(new Map((h ?? []).map((x) => [x.providerId, x])));
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div>
      <div className="toolbar">
        <h1>{t("providers", lang)}</h1>
        <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
      </div>
      {error && <p className="error-text">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>{t("name", lang)}</th>
            <th>{t("baseUrl", lang)}</th>
            <th>{t("state", lang)}</th>
            <th>{t("weight", lang)}</th>
            <th>{t("priority", lang)}</th>
            <th>{t("models", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {providers.length === 0 && (
            <tr><td colSpan={6} className="muted">{t("empty", lang)}</td></tr>
          )}
          {providers.map((p) => {
            const h = health.get(p.id);
            return (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td className="muted">{p.baseUrl}</td>
                <td>
                  {h ? (
                    <>
                      <span className={`dot ${h.state}`} />
                      {t(`breaker_${h.state}`, lang)}
                    </>
                  ) : (
                    "—"
                  )}
                </td>
                <td>{p.weight}</td>
                <td>{p.priority}</td>
                <td className="muted">{(p.models ?? []).map((m) => m.name).join(", ")}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
