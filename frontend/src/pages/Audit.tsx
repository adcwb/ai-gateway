import { Fragment, useState } from "react";
import { api, useAsync, type AttemptRecord, type AuditLog, type AuditSessionSummary, type PageResp, type SecurityOverview } from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, CardRow, EmptyState, ErrorBanner, HttpStatus, Icon, Pill, TableSkeleton, TableWrap, Tabs, Topbar } from "../components/ui";

type Tab = "logs" | "sessions" | "security";

function parseAttempts(raw: AttemptRecord[] | string | undefined): AttemptRecord[] {
  if (!raw) return [];
  if (Array.isArray(raw)) return raw;
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export default function Audit({ lang }: { lang: Lang }) {
  const [tab, setTab] = useState<Tab>("logs");
  const [sessionFilter, setSessionFilter] = useState("");
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [showBodies, setShowBodies] = useState<Set<number>>(new Set());

  const logsQ = useAsync<AuditLog[]>(
    (s) =>
      api
        .get<PageResp<AuditLog>>(`/ai/gateway/audit/list?page=1&pageSize=50${sessionFilter ? `&sessionId=${encodeURIComponent(sessionFilter)}` : ""}`, { signal: s })
        .then((r) => r.list ?? r.items ?? []),
    [sessionFilter],
    { skip: tab !== "logs" },
  );
  const logs = logsQ.data ?? [];

  const sessionsQ = useAsync<AuditSessionSummary[]>(
    (s) =>
      api
        .get<PageResp<AuditSessionSummary>>("/ai/gateway/audit/sessions?page=1&pageSize=50", { signal: s })
        .then((r) => r.list ?? r.items ?? []),
    [],
    { skip: tab !== "sessions" },
  );
  const sessions = sessionsQ.data ?? [];

  const securityQ = useAsync<SecurityOverview>(
    (s) => api.get<SecurityOverview>("/ai/gateway/audit/security-overview?topN=5", { signal: s }),
    [],
    { skip: tab !== "security" },
  );
  const security = securityQ.data;

  const toggleBody = (id: number) => {
    setShowBodies((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const viewSession = (sessionId: string) => {
    setSessionFilter(sessionId);
    setTab("logs");
  };

  const cols = 8;
  const activeError = tab === "logs" ? logsQ.error : tab === "sessions" ? sessionsQ.error : securityQ.error;
  const refreshActive = () => {
    if (tab === "logs") logsQ.refresh();
    else if (tab === "sessions") sessionsQ.refresh();
    else securityQ.refresh();
  };

  return (
    <div>
      <Topbar
        eyebrow={t("navObserve", lang)}
        title={t("audit", lang)}
        actions={
          <Button variant="ghost" size="sm" onClick={refreshActive}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </Button>
        }
      />

      <Tabs
        items={[
          { key: "logs", label: t("auditTabLogs", lang) },
          { key: "sessions", label: t("auditTabSessions", lang) },
          { key: "security", label: t("auditTabSecurity", lang) },
        ]}
        active={tab}
        onChange={(k) => setTab(k as Tab)}
      />

      {activeError && <ErrorBanner message={`${t("loadFailed", lang)}: ${activeError}`} onRetry={refreshActive} />}

      {tab === "logs" && (
        <>
          {sessionFilter && (
            <div className="mb-16">
              <Pill tone="info">{t("filteredBySession", lang)}: {sessionFilter}</Pill>{" "}
              <Button variant="ghost" size="sm" onClick={() => setSessionFilter("")}>{t("clearFilter", lang)}</Button>
            </div>
          )}
          <TableWrap>
            <table>
              <thead>
                <tr>
                  <th>{t("time", lang)}</th>
                  <th>{t("model", lang)}</th>
                  <th>{t("tokens", lang)}</th>
                  <th>{t("latency", lang)}</th>
                  <th>{t("httpStatus", lang)}</th>
                  <th>{t("attempts", lang)}</th>
                  <th>{t("clientIp", lang)}</th>
                  <th>{t("error", lang)}</th>
                </tr>
              </thead>
              <tbody>
                {logsQ.loading && logs.length === 0 ? (
                  <TableSkeleton cols={cols} />
                ) : logs.length === 0 ? (
                  <tr><td colSpan={cols}><EmptyState icon="audit" title={t("emptyAudit", lang)} sub={t("emptyAuditSub", lang)} /></td></tr>
                ) : (
                  logs.map((l) => {
                    const attempts = parseAttempts(l.providerAttempts);
                    const expanded = expandedId === l.id;
                    return (
                      <Fragment key={l.id}>
                        <tr className="expandable" onClick={() => setExpandedId(expanded ? null : l.id)}>
                          <td className="muted mono">{l.createdAt ? new Date(l.createdAt).toLocaleString() : "—"}</td>
                          <td className="mono">{l.model ?? "—"}</td>
                          <td className="mono"><span className="muted">{l.promptTokens ?? 0}</span><span className="faint"> / </span>{l.completionTokens ?? 0}</td>
                          <td className="mono">{l.latencyMs != null ? `${l.latencyMs} ms` : "—"}</td>
                          <td><HttpStatus code={l.statusCode ?? undefined} /></td>
                          <td className="mono">{l.attemptsTotal ?? attempts.length ?? "—"}</td>
                          <td className="muted mono">{l.clientIp ?? "—"}</td>
                          <td className="error-text">{l.errorMessage ?? l.errMsg ?? ""}</td>
                        </tr>
                        {expanded && (
                          <tr className="expand-row">
                            <td colSpan={cols}>
                              <div className="detail-grid">
                                <div><div className="k">{t("traceId", lang)}</div><div className="v">{l.traceId || "—"}</div></div>
                                <div><div className="k">{t("sessionId", lang)}</div><div className="v">{l.sessionId || "—"}</div></div>
                                <div><div className="k">{t("requestedModel", lang)}</div><div className="v">{l.requestedModel || "—"}</div></div>
                                <div><div className="k">{t("clientAgent", lang)}</div><div className="v">{l.clientAgent || "—"}</div></div>
                                <div><div className="k">{t("piiAction", lang)}</div><div className="v">{l.piiAction && l.piiAction !== "none" ? `${l.piiAction} (${l.piiTypes || ""})` : "—"}</div></div>
                              </div>

                              {attempts.length > 0 && (
                                <div className="attempt-trail">
                                  <span className="k" style={{ marginRight: 4 }}>{t("attemptsTrail", lang)}:</span>
                                  {attempts.map((a, i) => (
                                    <span key={i}>
                                      <Pill tone={a.status && a.status < 400 ? "on" : "err"}>
                                        #{a.providerId} {a.status || a.err || "—"}
                                      </Pill>
                                      {i < attempts.length - 1 && <span className="arrow"> → </span>}
                                    </span>
                                  ))}
                                </div>
                              )}

                              <Button variant="ghost" size="sm" onClick={(e) => { e.stopPropagation(); toggleBody(l.id); }}>
                                <Icon name="eye" size={13} /> {showBodies.has(l.id) ? t("hideBodies", lang) : t("viewBodies", lang)}
                              </Button>
                              {showBodies.has(l.id) && (
                                <div className="mt-8">
                                  <div className="k" style={{ marginBottom: 4 }}>{t("requestBody", lang)}</div>
                                  <pre className="code-block break-all">{l.requestBody || "—"}</pre>
                                  <div className="k" style={{ margin: "10px 0 4px" }}>{t("responseBody", lang)}</div>
                                  <pre className="code-block break-all">{l.responseBody || "—"}</pre>
                                </div>
                              )}
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })
                )}
              </tbody>
            </table>
          </TableWrap>
        </>
      )}

      {tab === "sessions" && (
        <TableWrap>
          <table>
            <thead>
              <tr>
                <th>{t("sessionId", lang)}</th>
                <th>{t("keys", lang)}</th>
                <th>{t("model", lang)}</th>
                <th>{t("requests", lang)}</th>
                <th>{t("tokens", lang)}</th>
                <th>{t("price", lang)}</th>
                <th>{t("httpStatus", lang)}</th>
                <th>{t("lastActive", lang)}</th>
              </tr>
            </thead>
            <tbody>
              {sessionsQ.loading && sessions.length === 0 ? (
                <TableSkeleton cols={7} />
              ) : sessions.length === 0 ? (
                <tr><td colSpan={7}><EmptyState icon="audit" title={t("emptySessions", lang)} /></td></tr>
              ) : (
                sessions.map((se) => (
                  <tr key={se.sessionId} className="expandable" onClick={() => viewSession(se.sessionId)}>
                    <td className="mono truncate" style={{ maxWidth: 160 }}>{se.sessionId}</td>
                    <td className="muted">{se.keyName || "—"}</td>
                    <td className="mono">{se.model || "—"}</td>
                    <td className="mono">{se.reqCount}</td>
                    <td className="mono">{se.totalTokens}</td>
                    <td className="mono">{(se.priceConsumed / 1_000_000).toFixed(4)}</td>
                    <td><HttpStatus code={se.finalStatusCode} /></td>
                    <td className="muted mono">{se.lastAt ? new Date(se.lastAt).toLocaleString() : "—"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </TableWrap>
      )}

      {tab === "security" && (
        <div>
          <CardRow>
            <Card className="stat"><div className="label">{t("totalRequests", lang)}</div><div className="value">{security?.totalRequests ?? "—"}</div></Card>
            <Card className="stat"><div className="label">{t("blockedRequests", lang)}</div><div className="value">{security?.blockCount ?? "—"}</div></Card>
            <Card className="stat"><div className="label">{t("redactedRequests", lang)}</div><div className="value">{security?.redactCount ?? "—"}</div></Card>
            <Card className="stat"><div className="label">{t("errorRate", lang)}</div><div className="value">{security ? `${(security.errorRate * 100).toFixed(2)}%` : "—"}</div></Card>
          </CardRow>
          <CardRow>
            <Card tone="toplist" style={{ flex: 1 }}>
              <div className="label">{t("topPiiTypes", lang)}</div>
              {(security?.topPiiTypes ?? []).length === 0 ? <div className="empty-note faint">—</div> : (security?.topPiiTypes ?? []).map((r) => (
                <div className="row" key={r.type}><span>{r.type}</span><span className="muted">{r.count}</span></div>
              ))}
            </Card>
            <Card tone="toplist" style={{ flex: 1 }}>
              <div className="label">{t("topErrorModels", lang)}</div>
              {(security?.topErrorModels ?? []).length === 0 ? <div className="empty-note faint">—</div> : (security?.topErrorModels ?? []).map((r) => (
                <div className="row" key={r.model}><span>{r.model}</span><span className="muted">{r.error_count}</span></div>
              ))}
            </Card>
          </CardRow>
        </div>
      )}
    </div>
  );
}
