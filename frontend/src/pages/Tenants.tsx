import { useEffect, useState } from "react";
import { api, credits, type Project, type Tenant } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Tenants({ lang }: { lang: Lang }) {
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [error, setError] = useState("");
  const [newTenant, setNewTenant] = useState("");
  const [newProject, setNewProject] = useState("");
  const [projectTenant, setProjectTenant] = useState(0);

  const load = async () => {
    setError("");
    try {
      const [ts, ps] = await Promise.all([
        api.get<Tenant[]>("/ai/gateway/tenants"),
        api.get<Project[]>("/ai/gateway/projects"),
      ]);
      setTenants(ts ?? []);
      setProjects(ps ?? []);
      if (ts?.length && projectTenant === 0) setProjectTenant(ts[0].id);
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const createTenant = async () => {
    if (!newTenant.trim()) return;
    try {
      await api.post("/ai/gateway/tenants", { name: newTenant.trim(), displayName: newTenant.trim() });
      setNewTenant("");
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const createProject = async () => {
    if (!newProject.trim() || !projectTenant) return;
    try {
      await api.post("/ai/gateway/projects", { tenantId: projectTenant, name: newProject.trim() });
      setNewProject("");
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <div>
      <div className="toolbar">
        <h1>{t("tenants", lang)}</h1>
        <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
      </div>
      {error && <p className="error-text">{error}</p>}

      <div className="cards">
        <div className="card" style={{ flex: 1 }}>
          <div className="label">{t("createTenant", lang)}</div>
          <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
            <input value={newTenant} onChange={(e) => setNewTenant(e.target.value)} placeholder={t("name", lang)} />
            <button onClick={createTenant}>{t("submit", lang)}</button>
          </div>
        </div>
        <div className="card" style={{ flex: 1 }}>
          <div className="label">{t("createProject", lang)}</div>
          <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
            <select
              value={projectTenant}
              onChange={(e) => setProjectTenant(Number(e.target.value))}
              style={{ background: "#0d0f15", color: "inherit", border: "1px solid var(--border)", borderRadius: 8, padding: "0 8px" }}
            >
              {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
            </select>
            <input value={newProject} onChange={(e) => setNewProject(e.target.value)} placeholder={t("name", lang)} />
            <button onClick={createProject}>{t("submit", lang)}</button>
          </div>
        </div>
      </div>

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
          {tenants.length === 0 && <tr><td colSpan={7} className="muted">{t("empty", lang)}</td></tr>}
          {tenants.map((x) => (
            <tr key={x.id}>
              <td>{x.id}</td>
              <td>{x.displayName || x.name}</td>
              <td>{x.keyCount}</td>
              <td>
                <span className={`pill ${x.account?.isEnabled ? "on" : "off"}`}>
                  {x.account?.isEnabled ? "on" : "off"}
                </span>
              </td>
              <td>{x.account ? `${credits(x.account.balanceMicro)} ${x.account.currency}` : "—"}</td>
              <td>{x.account ? t(`status_${x.account.status}`, lang) : "—"}</td>
              <td className="muted">
                {projects.filter((p) => p.tenantId === x.id).map((p) => p.name).join(", ") || "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
