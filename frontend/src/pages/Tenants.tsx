import { useEffect, useState } from "react";
import { api, credits, useAsync, type Project, type Tenant } from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, CardRow, EmptyState, ErrorBanner, Icon, Pill, TableSkeleton, TableWrap, Topbar } from "../components/ui";

export default function Tenants({ lang }: { lang: Lang }) {
  const [newTenant, setNewTenant] = useState("");
  const [newProject, setNewProject] = useState("");
  const [projectTenant, setProjectTenant] = useState(0);
  const [actionError, setActionError] = useState("");

  const { data, loading, error, refresh } = useAsync<[Tenant[], Project[]]>(
    (s) =>
      Promise.all([
        api.get<Tenant[]>("/ai/gateway/tenants", { signal: s }),
        api.get<Project[]>("/ai/gateway/projects", { signal: s }),
      ]),
    [],
  );
  const tenants = data?.[0] ?? [];
  const projects = data?.[1] ?? [];

  // Default the project-creator's tenant picker to the first tenant, once.
  useEffect(() => {
    if (projectTenant === 0 && tenants[0]) setProjectTenant(tenants[0].id);
  }, [tenants, projectTenant]);

  const createTenant = async () => {
    if (!newTenant.trim()) return;
    try {
      await api.post("/ai/gateway/tenants", { name: newTenant.trim(), displayName: newTenant.trim() });
      setNewTenant("");
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const createProject = async () => {
    if (!newProject.trim() || !projectTenant) return;
    try {
      await api.post("/ai/gateway/projects", { tenantId: projectTenant, name: newProject.trim() });
      setNewProject("");
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const cols = 7;
  const showError = actionError || (error ? `${t("loadFailed", lang)}: ${error}` : "");

  return (
    <div>
      <Topbar
        eyebrow={t("navManage", lang)}
        title={t("tenants", lang)}
        actions={
          <Button variant="ghost" size="sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </Button>
        }
      />

      {showError && (
        <ErrorBanner
          message={showError}
          onRetry={() => {
            setActionError("");
            refresh();
          }}
        />
      )}

      <CardRow>
        <Card style={{ flex: 1, minWidth: 280 }}>
          <div className="label">{t("createTenant", lang)}</div>
          <div className="flex gap-8" style={{ marginTop: 6 }}>
            <input
              value={newTenant}
              onChange={(e) => setNewTenant(e.target.value)}
              placeholder={t("name", lang)}
              onKeyDown={(e) => e.key === "Enter" && createTenant()}
            />
            <Button onClick={createTenant}>
              <Icon name="plus" size={14} /> {t("submit", lang)}
            </Button>
          </div>
        </Card>
        <Card style={{ flex: 1, minWidth: 320 }}>
          <div className="label">{t("createProject", lang)}</div>
          <div className="flex gap-8" style={{ marginTop: 6 }}>
            <select value={projectTenant} onChange={(e) => setProjectTenant(Number(e.target.value))}>
              {tenants.map((x) => (
                <option key={x.id} value={x.id}>{x.name}</option>
              ))}
            </select>
            <input
              value={newProject}
              onChange={(e) => setNewProject(e.target.value)}
              placeholder={t("name", lang)}
              onKeyDown={(e) => e.key === "Enter" && createProject()}
            />
            <Button onClick={createProject}>
              <Icon name="plus" size={14} /> {t("submit", lang)}
            </Button>
          </div>
        </Card>
      </CardRow>

      <TableWrap>
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>{t("name", lang)}</th>
              <th>{t("keyCount", lang)}</th>
              <th>{t("billingEnabled", lang)}</th>
              <th>{t("balance", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("project", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {loading && tenants.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : tenants.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState icon="tenants" title={t("emptyTenants", lang)} sub={t("emptyTenantsSub", lang)} />
                </td>
              </tr>
            ) : (
              tenants.map((x) => {
                const st = x.account?.status;
                const tone = st === "active" ? "on" : st === "grace" ? "warn" : "err";
                return (
                  <tr key={x.id}>
                    <td className="id">{x.id}</td>
                    <td>{x.displayName || x.name}</td>
                    <td className="mono">{x.keyCount}</td>
                    <td>
                      <Pill tone={x.account?.isEnabled ? "on" : "off"}>{x.account?.isEnabled ? "on" : "off"}</Pill>
                    </td>
                    <td className="mono">
                      {x.account ? `${credits(x.account.balanceMicro)} ${x.account.currency}` : "—"}
                    </td>
                    <td>{x.account ? <Pill tone={tone}>{t(`status_${st}`, lang)}</Pill> : "—"}</td>
                    <td className="muted">
                      {projects.filter((p) => p.tenantId === x.id).map((p) => p.name).join(", ") || "—"}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableWrap>
    </div>
  );
}
