import { useEffect, useState } from "react";
import { api, useAsync, type AdminKey, type CreateAdminKeyResp, type Tenant, type UserItem } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

const ROLES = ["owner", "admin", "member", "viewer"];
const emptyKeyForm = { name: "", tenantId: 0, role: "viewer" };

export default function Users({ lang }: { lang: Lang }) {
  const [tenantId, setTenantId] = useState(0);
  const [actionError, setActionError] = useState("");

  const tenantsQ = useAsync<Tenant[]>((s) => api.get<Tenant[]>("/ai/gateway/tenants", { signal: s }), []);
  const tenants = tenantsQ.data ?? [];
  useEffect(() => {
    if (tenantId === 0 && tenants[0]) setTenantId(tenants[0].id);
  }, [tenants, tenantId]);

  const usersQ = useAsync<UserItem[]>(
    (s) => api.get<UserItem[]>(`/ai/gateway/users?tenantId=${tenantId}`, { signal: s }),
    [tenantId],
    { skip: tenantId === 0 },
  );
  const users = usersQ.data ?? [];

  const setRole = async (u: UserItem, role: string) => {
    try {
      await api.put("/ai/gateway/users/role", { userId: u.id, tenantId, role });
      usersQ.refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };
  const removeMember = (u: UserItem) => setRole(u, "");
  const toggleEnabled = async (u: UserItem) => {
    try {
      await api.put("/ai/gateway/users/status", { userId: u.id, isEnabled: !u.isEnabled });
      usersQ.refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  // ---- Admin API keys --------------------------------------------------------
  const keysQ = useAsync<AdminKey[]>((s) => api.get<AdminKey[]>("/ai/gateway/admin-keys", { signal: s }), []);
  const keys = keysQ.data ?? [];
  const [showKeyForm, setShowKeyForm] = useState(false);
  const [keyForm, setKeyForm] = useState({ ...emptyKeyForm });
  const [minted, setMinted] = useState<CreateAdminKeyResp | null>(null);

  const createKey = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const resp = await api.post<CreateAdminKeyResp>("/ai/gateway/admin-keys", keyForm);
      setMinted(resp);
      setShowKeyForm(false);
      setKeyForm({ ...emptyKeyForm });
      keysQ.refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };
  const toggleKey = async (k: AdminKey) => {
    try {
      await api.put("/ai/gateway/admin-keys", { id: k.id, isEnabled: !k.isEnabled });
      keysQ.refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };
  const deleteKey = async (k: AdminKey) => {
    if (!window.confirm(t("confirmDeleteAdminKey", lang))) return;
    try {
      await api.del(`/ai/gateway/admin-keys?id=${k.id}`);
      keysQ.refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const showError = actionError || (usersQ.error ? `${t("loadFailed", lang)}: ${usersQ.error}` : "") || (keysQ.error ? `${t("loadFailed", lang)}: ${keysQ.error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("usersAccess", lang)}</h1>
        </div>
        <div className="actions flex gap-8 items-center">
          <select value={tenantId} onChange={(e) => setTenantId(Number(e.target.value))} style={{ width: "auto" }} aria-label={t("selectTenant", lang)}>
            {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
          </select>
          <button className="ghost sm" onClick={() => { usersQ.refresh(); keysQ.refresh(); }}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
        </div>
      </div>

      {showError && <ErrorBanner message={showError} onRetry={() => { setActionError(""); usersQ.refresh(); keysQ.refresh(); }} />}

      <div className="table-wrap mb-16">
        <table>
          <thead>
            <tr>
              <th>{t("name", lang)}</th>
              <th>{t("email", lang)}</th>
              <th>{t("role", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {usersQ.loading && users.length === 0 ? (
              <TableSkeleton cols={5} />
            ) : users.length === 0 ? (
              <tr><td colSpan={5}><EmptyState icon="tenants" title={t("emptyUsers", lang)} sub={t("emptyUsersSub", lang)} /></td></tr>
            ) : (
              users.map((u) => (
                <tr key={u.id}>
                  <td>{u.displayName || "—"} {u.isPlatformAdmin && <span className="pill info">{t("platformAdmin", lang)}</span>}</td>
                  <td className="muted mono">{u.email}</td>
                  <td>
                    <select value={u.role} onChange={(e) => setRole(u, e.target.value)} disabled={u.isPlatformAdmin} style={{ width: "auto" }}>
                      {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
                    </select>
                  </td>
                  <td>{u.isEnabled ? <span className="pill on">{t("enabled", lang)}</span> : <span className="pill off">{t("disabled", lang)}</span>}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => toggleEnabled(u)}>{u.isEnabled ? t("disable", lang) : t("enable", lang)}</button>
                      <button className="danger sm" onClick={() => removeMember(u)}>{t("removeFromTenant", lang)}</button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="topbar">
        <div className="titles"><h2 style={{ fontSize: 15, margin: 0 }}>{t("adminApiKeys", lang)}</h2></div>
        <div className="actions">
          <button onClick={() => setShowKeyForm(true)}><Icon name="plus" size={14} /> {t("addAdminKey", lang)}</button>
        </div>
      </div>

      {minted && (
        <div className="card success mb-16">
          <div className="label">{minted.name} — {t("keyCreatedOnce", lang)}</div>
          <div className="flex gap-8 items-center" style={{ marginTop: 8 }}>
            <code className="code-block" style={{ flex: 1 }}>{minted.plainKey}</code>
            <button className="ghost sm" onClick={() => setMinted(null)}><Icon name="close" size={14} /> {t("close", lang)}</button>
          </div>
        </div>
      )}

      {showKeyForm && (
        <form className="card mb-16" onSubmit={createKey}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={keyForm.name} onChange={(e) => setKeyForm({ ...keyForm, name: e.target.value })} required autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("tenant", lang)}</div>
              <select value={keyForm.tenantId} onChange={(e) => setKeyForm({ ...keyForm, tenantId: Number(e.target.value) })}>
                <option value={0}>{t("platformWide", lang)}</option>
                {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("role", lang)}</div>
              <select value={keyForm.role} onChange={(e) => setKeyForm({ ...keyForm, role: e.target.value })}>
                {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowKeyForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("name", lang)}</th>
              <th>{t("tenant", lang)}</th>
              <th>{t("role", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {keysQ.loading && keys.length === 0 ? (
              <TableSkeleton cols={5} />
            ) : keys.length === 0 ? (
              <tr><td colSpan={5}><EmptyState icon="key" title={t("emptyAdminKeys", lang)} /></td></tr>
            ) : (
              keys.map((k) => (
                <tr key={k.id}>
                  <td className="mono">{k.name} <span className="faint">({k.keyPrefix}…)</span></td>
                  <td className="muted">{k.tenantId === 0 ? t("platformWide", lang) : tenants.find((x) => x.id === k.tenantId)?.name ?? `#${k.tenantId}`}</td>
                  <td className="mono">{k.role}</td>
                  <td>{k.isEnabled ? <span className="pill on">{t("enabled", lang)}</span> : <span className="pill off">{t("disabled", lang)}</span>}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => toggleKey(k)}>{k.isEnabled ? t("disable", lang) : t("enable", lang)}</button>
                      <button className="danger sm" onClick={() => deleteKey(k)}><Icon name="trash" size={13} /> {t("deleteProvider", lang)}</button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
