import { useEffect, useState } from "react";
import { api, type Provider, type ProviderHealth } from "../api/client";
import { t, type Lang } from "../i18n";

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
  const [providers, setProviders] = useState<Provider[]>([]);
  const [health, setHealth] = useState<Map<number, ProviderHealth>>(new Map());
  const [error, setError] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...emptyForm });

  const load = async () => {
    setError("");
    try {
      const [list, h] = await Promise.all([
        api.get<Provider[]>("/ai/gateway/providers"),
        api.get<ProviderHealth[]>("/ai/gateway/providers/health"),
      ]);
      setProviders(list ?? []);
      setHealth(new Map((h ?? []).map((x) => [x.providerId, x])));
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    try {
      if (form.id) {
        await api.put("/ai/gateway/providers", {
          id: form.id,
          name: form.name,
          baseUrl: form.baseUrl,
          providerType: form.providerType,
          apiKey: form.apiKey || "",
          models,
          weight: form.weight,
          priority: form.priority,
        });
      } else {
        await api.post("/ai/gateway/providers", {
          name: form.name,
          baseUrl: form.baseUrl,
          providerType: form.providerType,
          apiKey: form.apiKey,
          models,
          weight: form.weight,
          priority: form.priority,
        });
      }
      setShowForm(false);
      load();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const syncModels = async (p: Provider) => {
    try {
      await api.post(`/ai/gateway/providers/sync-models?id=${p.id}`);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const remove = async (p: Provider) => {
    if (!window.confirm(t("confirmDeleteProvider", lang))) return;
    try {
      await api.del(`/ai/gateway/providers?id=${p.id}`);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const selStyle: React.CSSProperties = {
    width: "100%", background: "#0d0f15", color: "inherit",
    border: "1px solid var(--border)", borderRadius: 8, padding: "10px 8px",
  };

  return (
    <div>
      <div className="toolbar">
        <h1>{t("providers", lang)}</h1>
        <div style={{ display: "flex", gap: 8 }}>
          <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
          <button onClick={() => startEdit()}>{t("addProvider", lang)}</button>
        </div>
      </div>
      {error && <p className="error-text">{error}</p>}

      {showForm && (
        <form className="card" style={{ marginBottom: 16 }} onSubmit={submit}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 10 }}>
            <label>
              <div className="label">{t("name", lang)}</div>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
            </label>
            <label>
              <div className="label">{t("baseUrl", lang)}</div>
              <input value={form.baseUrl} onChange={(e) => setForm({ ...form, baseUrl: e.target.value })} required placeholder="https://api.openai.com/v1" />
            </label>
            <label>
              <div className="label">{t("providerType", lang)}</div>
              <select value={form.providerType} onChange={(e) => setForm({ ...form, providerType: e.target.value })} style={selStyle}>
                <option value="openai_compatible">openai_compatible</option>
                <option value="anthropic">anthropic</option>
                <option value="azure_openai">azure_openai</option>
                <option value="gemini">gemini</option>
              </select>
            </label>
            <label>
              <div className="label">{t("apiKeyWriteOnly", lang)}</div>
              <input type="password" value={form.apiKey} onChange={(e) => setForm({ ...form, apiKey: e.target.value })} required={!form.id} />
            </label>
            <label>
              <div className="label">{t("weight", lang)}</div>
              <input type="number" min="0" value={form.weight} onChange={(e) => setForm({ ...form, weight: Number(e.target.value) || 0 })} />
            </label>
            <label>
              <div className="label">{t("priority", lang)}</div>
              <input type="number" min="0" value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) || 0 })} />
            </label>
            <label style={{ gridColumn: "1 / span 3" }}>
              <div className="label">{t("modelsCsv", lang)}</div>
              <input value={form.modelsCsv} onChange={(e) => setForm({ ...form, modelsCsv: e.target.value })} placeholder="gpt-4o-mini, gpt-4o" />
            </label>
            <div style={{ display: "flex", gap: 8 }}>
              <button type="submit">{t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

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
          {providers.length === 0 && <tr><td colSpan={7} className="muted">{t("empty", lang)}</td></tr>}
          {providers.map((p) => {
            const h = health.get(p.id);
            return (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td className="muted">{p.baseUrl}</td>
                <td className="muted">{p.providerType}</td>
                <td>
                  {h ? (<><span className={`dot ${h.state}`} />{t(`breaker_${h.state}`, lang)}</>) : "—"}
                </td>
                <td>{p.weight} / P{p.priority}</td>
                <td className="muted" style={{ maxWidth: 240, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {(p.models ?? []).map((m) => m.name).join(", ")}
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  <button className="ghost" onClick={() => startEdit(p)}>{t("editProvider", lang)}</button>{" "}
                  <button className="ghost" onClick={() => syncModels(p)}>{t("syncModels", lang)}</button>{" "}
                  <button className="ghost" style={{ color: "var(--err)" }} onClick={() => remove(p)}>{t("deleteProvider", lang)}</button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
