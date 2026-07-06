import { useState } from "react";
import { api, useAsync, type Provider, type ProviderHealth } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

const emptyForm = {
  id: 0,
  name: "",
  baseUrl: "",
  providerType: "openai_compatible",
  apiKey: "",
  modelsCsv: "",
  weight: 100,
  priority: 0,
};

export default function Providers({ lang }: { lang: Lang }) {
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...emptyForm });
  const [actionError, setActionError] = useState("");

  const { data, loading, error, refresh } = useAsync<[Provider[], ProviderHealth[]]>(
    (s) =>
      Promise.all([
        api.get<Provider[]>("/ai/gateway/providers", { signal: s }),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health", { signal: s }),
      ]),
    [],
  );
  const providers = data?.[0] ?? [];
  const healthMap = new Map((data?.[1] ?? []).map((h) => [h.providerId, h]));

  const startEdit = (p?: Provider) => {
    if (p) {
      setForm({
        id: p.id,
        name: p.name,
        baseUrl: p.baseUrl,
        providerType: p.providerType || "openai_compatible",
        apiKey: "",
        modelsCsv: (p.models ?? []).map((m) => m.name).join(", "),
        weight: p.weight,
        priority: p.priority,
      });
    } else {
      setForm({ ...emptyForm });
    }
    setShowForm(true);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const models = form.modelsCsv
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean)
      .map((name, i) => ({ name, is_default: i === 0 }));
    const body = {
      name: form.name,
      baseUrl: form.baseUrl,
      providerType: form.providerType,
      apiKey: form.apiKey || "",
      models,
      weight: form.weight,
      priority: form.priority,
    };
    try {
      if (form.id) {
        await api.put("/ai/gateway/providers", { id: form.id, ...body });
      } else {
        await api.post("/ai/gateway/providers", body);
      }
      setShowForm(false);
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const syncModels = async (p: Provider) => {
    try {
      await api.post(`/ai/gateway/providers/sync-models?id=${p.id}`);
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const remove = async (p: Provider) => {
    if (!window.confirm(t("confirmDeleteProvider", lang))) return;
    try {
      await api.del(`/ai/gateway/providers?id=${p.id}`);
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const cols = 7;
  const showError = actionError || (error ? `${t("loadFailed", lang)}: ${error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navOperate", lang)}</div>
          <h1>{t("providers", lang)}</h1>
        </div>
        <div className="actions flex gap-8">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
          <button onClick={() => startEdit()}>
            <Icon name="plus" size={14} /> {t("addProvider", lang)}
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
              <div className="field-label">{t("baseUrl", lang)}</div>
              <input
                value={form.baseUrl}
                onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
                required
                placeholder="https://api.openai.com/v1"
              />
            </label>
            <label className="field">
              <div className="field-label">{t("providerType", lang)}</div>
              <select value={form.providerType} onChange={(e) => setForm({ ...form, providerType: e.target.value })}>
                <option value="openai_compatible">openai_compatible</option>
                <option value="anthropic">anthropic</option>
                <option value="azure_openai">azure_openai</option>
                <option value="gemini">gemini</option>
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("apiKeyWriteOnly", lang)}</div>
              <input
                type="password"
                value={form.apiKey}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                required={!form.id}
                placeholder={form.id ? "••••••  (leave blank to keep)" : ""}
              />
            </label>
            <label className="field">
              <div className="field-label">{t("weight", lang)}</div>
              <input type="number" min="0" value={form.weight} onChange={(e) => setForm({ ...form, weight: Number(e.target.value) || 0 })} />
            </label>
            <label className="field">
              <div className="field-label">{t("priority", lang)}</div>
              <input type="number" min="0" value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) || 0 })} />
            </label>
            <label className="field span-3">
              <div className="field-label">{t("modelsCsv", lang)}</div>
              <input value={form.modelsCsv} onChange={(e) => setForm({ ...form, modelsCsv: e.target.value })} placeholder="gpt-4o-mini, gpt-4o" />
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
              <th>{t("baseUrl", lang)}</th>
              <th>{t("providerType", lang)}</th>
              <th>{t("state", lang)}</th>
              <th>{t("weight", lang)}</th>
              <th>{t("models", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {loading && providers.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : providers.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState
                    icon="providers"
                    title={t("emptyProviders", lang)}
                    sub={t("emptyProvidersSub", lang)}
                    action={
                      <button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addProvider", lang)}
                      </button>
                    }
                  />
                </td>
              </tr>
            ) : (
              providers.map((p) => {
                const h = healthMap.get(p.id);
                return (
                  <tr key={p.id}>
                    <td>{p.name}</td>
                    <td className="muted mono"><span className="truncate">{p.baseUrl}</span></td>
                    <td className="muted mono">{p.providerType}</td>
                    <td>
                      {h ? (
                        <><span className={`dot ${h.state}`} />{t(`breaker_${h.state}`, lang)}</>
                      ) : <span className="faint">—</span>}
                    </td>
                    <td className="mono">{p.weight} <span className="faint">/ P{p.priority}</span></td>
                    <td className="muted">
                      <span className="truncate">{(p.models ?? []).map((m) => m.name).join(", ") || "—"}</span>
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="ghost sm" onClick={() => startEdit(p)}>{t("editProvider", lang)}</button>
                        <button className="ghost sm" onClick={() => syncModels(p)}>
                          <Icon name="sync" size={13} /> {t("syncModels", lang)}
                        </button>
                        <button className="danger sm" onClick={() => remove(p)}>
                          <Icon name="trash" size={13} /> {t("deleteProvider", lang)}
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
