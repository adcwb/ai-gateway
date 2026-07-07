import { useState } from "react";
import { api, useAsync, type McpServer } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

const emptyForm = {
  id: 0,
  name: "",
  baseUrl: "",
  apiKey: "",
  description: "",
  isEnabled: true,
};

export default function McpServers({ lang }: { lang: Lang }) {
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...emptyForm });
  const [actionError, setActionError] = useState("");

  const { data, loading, error, refresh } = useAsync<McpServer[]>(
    (s) => api.get<McpServer[]>("/ai/gateway/mcp-servers", { signal: s }),
    [],
  );
  const servers = data ?? [];

  const startEdit = (s?: McpServer) => {
    if (s) {
      setForm({
        id: s.id,
        name: s.name,
        baseUrl: s.baseUrl,
        apiKey: "",
        description: s.description ?? "",
        isEnabled: s.isEnabled,
      });
    } else {
      setForm({ ...emptyForm });
    }
    setShowForm(true);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const body = {
      name: form.name,
      baseUrl: form.baseUrl,
      apiKey: form.apiKey || "",
      description: form.description,
      isEnabled: form.isEnabled,
    };
    try {
      if (form.id) {
        await api.put("/ai/gateway/mcp-servers", { id: form.id, ...body });
      } else {
        await api.post("/ai/gateway/mcp-servers", body);
      }
      setShowForm(false);
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const remove = async (s: McpServer) => {
    if (!window.confirm(t("confirmDeleteMcpServer", lang))) return;
    try {
      await api.del(`/ai/gateway/mcp-servers?id=${s.id}`);
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const cols = 5;
  const showError = actionError || (error ? `${t("loadFailed", lang)}: ${error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("mcpServers", lang)}</h1>
        </div>
        <div className="actions flex gap-8">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
          <button onClick={() => startEdit()}>
            <Icon name="plus" size={14} /> {t("addMcpServer", lang)}
          </button>
        </div>
      </div>

      {showError && (
        <ErrorBanner
          message={showError}
          onRetry={() => {
            setActionError("");
            refresh();
          }}
        />
      )}

      {showForm && (
        <form className="card mb-16" onSubmit={submit}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("mcpBaseUrl", lang)}</div>
              <input
                value={form.baseUrl}
                onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
                required
                placeholder="https://example.com/mcp"
              />
            </label>
            <label className="field">
              <div className="field-label">{t("apiKeyWriteOnly", lang)}</div>
              <input
                type="password"
                value={form.apiKey}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                placeholder={form.id ? "••••••  (leave blank to keep)" : t("mcpApiKeyOptional", lang)}
              />
            </label>
            <label className="field span-2">
              <div className="field-label">{t("description", lang)}</div>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </label>
            <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <input type="checkbox" checked={form.isEnabled} onChange={(e) => setForm({ ...form, isEnabled: e.target.checked })} />
              <div className="field-label" style={{ margin: 0 }}>{t("enabled", lang)}</div>
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("name", lang)}</th>
              <th>{t("mcpBaseUrl", lang)}</th>
              <th>{t("description", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {loading && servers.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : servers.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState
                    icon="providers"
                    title={t("emptyMcpServers", lang)}
                    sub={t("emptyMcpServersSub", lang)}
                    action={
                      <button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addMcpServer", lang)}
                      </button>
                    }
                  />
                </td>
              </tr>
            ) : (
              servers.map((s) => (
                <tr key={s.id}>
                  <td>{s.name}</td>
                  <td className="muted mono"><span className="truncate">{s.baseUrl}</span></td>
                  <td className="muted"><span className="truncate">{s.description || "—"}</span></td>
                  <td>
                    <span className={`pill ${s.isEnabled ? "on" : "off"}`}>
                      {t(s.isEnabled ? "enabled" : "disabled", lang)}
                    </span>
                  </td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => startEdit(s)}>{t("editProvider", lang)}</button>
                      <button className="danger sm" onClick={() => remove(s)}>
                        <Icon name="trash" size={13} /> {t("deleteProvider", lang)}
                      </button>
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
