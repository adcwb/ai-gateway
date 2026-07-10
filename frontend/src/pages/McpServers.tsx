import { useState } from "react";
import { api, useAsync, type McpServer } from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, EmptyState, ErrorBanner, Field, FormGrid, Icon, Pill, TableSkeleton, TableWrap, Topbar } from "../components/ui";

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
      <Topbar
        eyebrow={t("navManage", lang)}
        title={t("mcpServers", lang)}
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={refresh}>
              <Icon name="refresh" size={14} /> {t("refresh", lang)}
            </Button>
            <Button onClick={() => startEdit()}>
              <Icon name="plus" size={14} /> {t("addMcpServer", lang)}
            </Button>
          </>
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

      {showForm && (
        <Card className="mb-16">
          <form onSubmit={submit}>
          <FormGrid>
            <Field label={t("name", lang)}>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required autoFocus />
            </Field>
            <Field label={t("mcpBaseUrl", lang)}>
              <input
                value={form.baseUrl}
                onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
                required
                placeholder="https://example.com/mcp"
              />
            </Field>
            <Field label={t("apiKeyWriteOnly", lang)}>
              <input
                type="password"
                value={form.apiKey}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                placeholder={form.id ? "••••••  (leave blank to keep)" : t("mcpApiKeyOptional", lang)}
              />
            </Field>
            <Field span={2} label={t("description", lang)}>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </Field>
            <Field row label={t("enabled", lang)}>
              <input type="checkbox" checked={form.isEnabled} onChange={(e) => setForm({ ...form, isEnabled: e.target.checked })} />
            </Field>
            <div className="form-actions">
              <Button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</Button>
              <Button type="button" variant="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</Button>
            </div>
          </FormGrid>
          </form>
        </Card>
      )}

      <TableWrap>
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
                      <Button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addMcpServer", lang)}
                      </Button>
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
                    <Pill tone={s.isEnabled ? "on" : "off"}>
                      {t(s.isEnabled ? "enabled" : "disabled", lang)}
                    </Pill>
                  </td>
                  <td>
                    <div className="row-actions">
                      <Button variant="ghost" size="sm" onClick={() => startEdit(s)}>{t("editProvider", lang)}</Button>
                      <Button variant="danger" size="sm" onClick={() => remove(s)}>
                        <Icon name="trash" size={13} /> {t("deleteProvider", lang)}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableWrap>
    </div>
  );
}
