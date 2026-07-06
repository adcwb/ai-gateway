import { api, useAsync, type AuditLog, type PageResp } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, HttpStatus, Icon, TableSkeleton } from "../components/ui";

export default function Audit({ lang }: { lang: Lang }) {
  const { data, loading, error, refresh } = useAsync<AuditLog[]>(
    (s) =>
      api
        .get<PageResp<AuditLog>>("/ai/gateway/audit/list?page=1&pageSize=50", { signal: s })
        .then((r) => r.list ?? r.items ?? []),
    [],
  );
  const logs = data ?? [];
  const cols = 7;

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navObserve", lang)}</div>
          <h1>{t("audit", lang)}</h1>
        </div>
        <div className="actions">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
        </div>
      </div>

      {error && <ErrorBanner message={`${t("loadFailed", lang)}: ${error}`} onRetry={refresh} />}

      <div className="table-wrap">
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
            {loading && logs.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : logs.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState icon="audit" title={t("emptyAudit", lang)} sub={t("emptyAuditSub", lang)} />
                </td>
              </tr>
            ) : (
              logs.map((l) => (
                <tr key={l.id}>
                  <td className="muted mono">{l.createdAt ? new Date(l.createdAt).toLocaleString() : "—"}</td>
                  <td className="mono">{l.model ?? "—"}</td>
                  <td className="mono">
                    <span className="muted">{l.promptTokens ?? 0}</span>
                    <span className="faint"> / </span>
                    {l.completionTokens ?? 0}
                  </td>
                  <td className="mono">{l.latencyMs != null ? `${l.latencyMs} ms` : "—"}</td>
                  <td><HttpStatus code={l.statusCode ?? undefined} /></td>
                  <td className="muted mono">{l.clientIp ?? "—"}</td>
                  <td className="error-text">{l.errMsg ?? ""}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
