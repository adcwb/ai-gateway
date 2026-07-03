import { useEffect, useState } from "react";
import { api, type AuditLog, type PageResp } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Audit({ lang }: { lang: Lang }) {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [error, setError] = useState("");

  const load = async () => {
    setError("");
    try {
      const resp = await api.get<PageResp<AuditLog>>("/ai/gateway/audit/list?page=1&pageSize=50");
      setLogs(resp.list ?? resp.items ?? []);
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
        <h1>{t("audit", lang)}</h1>
        <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
      </div>
      {error && <p className="error-text">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>{t("time", lang)}</th>
            <th>{t("model", lang)}</th>
            <th>{t("tokens", lang)}</th>
            <th>{t("latency", lang)}</th>
            <th>{t("httpStatus", lang)}</th>
            <th>{t("clientIp", lang)}</th>
            <th>{t("error", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {logs.length === 0 && (
            <tr><td colSpan={7} className="muted">{t("empty", lang)}</td></tr>
          )}
          {logs.map((l) => (
            <tr key={l.id}>
              <td className="muted">{l.createdAt ? new Date(l.createdAt).toLocaleString() : "—"}</td>
              <td>{l.model ?? "—"}</td>
              <td>{l.promptTokens ?? 0} / {l.completionTokens ?? 0}</td>
              <td>{l.latencyMs != null ? `${l.latencyMs} ms` : "—"}</td>
              <td>{l.statusCode ?? "—"}</td>
              <td className="muted">{l.clientIp ?? "—"}</td>
              <td className="error-text">{l.errMsg ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
