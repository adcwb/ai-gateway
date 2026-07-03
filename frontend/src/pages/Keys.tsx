import { useEffect, useState } from "react";
import { api, type PageResp, type VirtualKey } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Keys({ lang }: { lang: Lang }) {
  const [keys, setKeys] = useState<VirtualKey[]>([]);
  const [error, setError] = useState("");

  const load = async () => {
    setError("");
    try {
      const resp = await api.get<PageResp<VirtualKey>>("/ai/gateway/key/list?page=1&pageSize=50");
      setKeys(resp.list ?? resp.items ?? []);
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
        <h1>{t("keys", lang)}</h1>
        <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
      </div>
      {error && <p className="error-text">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>{t("name", lang)}</th>
            <th>{t("status", lang)}</th>
            <th>{t("expires", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {keys.length === 0 && (
            <tr><td colSpan={4} className="muted">{t("empty", lang)}</td></tr>
          )}
          {keys.map((k) => (
            <tr key={k.id}>
              <td>{k.id}</td>
              <td>{k.name}</td>
              <td>
                <span className={`pill ${k.isEnabled ? "on" : "off"}`}>
                  {t(k.isEnabled ? "enabled" : "disabled", lang)}
                </span>
              </td>
              <td>{k.expiresAt ? new Date(k.expiresAt).toLocaleString() : t("never", lang)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
