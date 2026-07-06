import { useEffect, useState } from "react";
import { api, type CreateKeyResp, type PageResp, type Provider, type Tenant, type VirtualKey } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Keys({ lang }: { lang: Lang }) {
  const [keys, setKeys] = useState<VirtualKey[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [error, setError] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [minted, setMinted] = useState<CreateKeyResp | null>(null);
  const [copied, setCopied] = useState(false);
  const [revealed, setRevealed] = useState<Record<number, string>>({});

  const [form, setForm] = useState({ name: "", providerId: 0, tenantId: 0, dailyTokenQuota: 0, routingStrategy: "" });

  const load = async () => {
    setError("");
    try {
      const [ks, ps, ts] = await Promise.all([
        api.get<PageResp<VirtualKey>>("/ai/gateway/key/list?page=1&pageSize=50"),
        api.get<Provider[]>("/ai/gateway/providers"),
        api.get<Tenant[]>("/ai/gateway/tenants"),
      ]);
      setKeys(ks.list ?? ks.items ?? []);
      setProviders(ps ?? []);
      setTenants(ts ?? []);
      setForm((f) => ({
        ...f,
        providerId: f.providerId || ps?.[0]?.id || 0,
        tenantId: f.tenantId || ts?.[0]?.id || 0,
      }));
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.providerId) return;
    try {
      const resp = await api.post<CreateKeyResp>("/ai/gateway/key", {
        name: form.name.trim(),
        providerId: form.providerId,
        tenantId: form.tenantId,
        dailyTokenQuota: form.dailyTokenQuota || 0,
        routingStrategy: form.routingStrategy || undefined,
      });
      setMinted(resp);
      setCopied(false);
      setShowForm(false);
      setForm((f) => ({ ...f, name: "", dailyTokenQuota: 0 }));
      load();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const toggle = async (k: VirtualKey) => {
    try {
      await api.put("/ai/gateway/key/status", { id: k.id, isEnabled: !k.isEnabled });
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const reveal = async (k: VirtualKey) => {
    try {
      const resp = await api.get<{ plainKey?: string; key?: string }>(`/ai/gateway/key/reveal?id=${k.id}`);
      setRevealed((r) => ({ ...r, [k.id]: resp.plainKey ?? resp.key ?? "?" }));
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const revoke = async (k: VirtualKey) => {
    if (!window.confirm(t("confirmRevoke", lang))) return;
    try {
      await api.del(`/ai/gateway/key?id=${k.id}`);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const copyKey = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch {
      /* clipboard unavailable */
    }
  };

  return (
    <div>
      <div className="toolbar">
        <h1>{t("keys", lang)}</h1>
        <div style={{ display: "flex", gap: 8 }}>
          <button className="ghost" onClick={load}>{t("refresh", lang)}</button>
          <button onClick={() => setShowForm((v) => !v)}>{t("createKey", lang)}</button>
        </div>
      </div>
      {error && <p className="error-text">{error}</p>}

      {minted && (
        <div className="card" style={{ marginBottom: 16, borderColor: "var(--ok)" }}>
          <div className="label">{minted.name} — {t("keyCreatedOnce", lang)}</div>
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 8 }}>
            <code style={{ wordBreak: "break-all" }}>{minted.plainKey}</code>
            <button className="ghost" onClick={() => copyKey(minted.plainKey)}>
              {copied ? t("copied", lang) : t("copy", lang)}
            </button>
            <button className="ghost" onClick={() => setMinted(null)}>{t("close", lang)}</button>
          </div>
        </div>
      )}

      {showForm && (
        <form className="card" style={{ marginBottom: 16 }} onSubmit={create}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 10 }}>
            <label>
              <div className="label">{t("name", lang)}</div>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} autoFocus />
            </label>
            <label>
              <div className="label">{t("provider", lang)}</div>
              <select
                value={form.providerId}
                onChange={(e) => setForm({ ...form, providerId: Number(e.target.value) })}
                style={{ width: "100%", background: "#0d0f15", color: "inherit", border: "1px solid var(--border)", borderRadius: 8, padding: "10px 8px" }}
              >
                {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </label>
            <label>
              <div className="label">{t("tenant", lang)}</div>
              <select
                value={form.tenantId}
                onChange={(e) => setForm({ ...form, tenantId: Number(e.target.value) })}
                style={{ width: "100%", background: "#0d0f15", color: "inherit", border: "1px solid var(--border)", borderRadius: 8, padding: "10px 8px" }}
              >
                {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </label>
            <label>
              <div className="label">{t("dailyTokens", lang)}</div>
              <input
                type="number" min="0"
                value={form.dailyTokenQuota || ""}
                onChange={(e) => setForm({ ...form, dailyTokenQuota: Number(e.target.value) || 0 })}
              />
            </label>
            <label>
              <div className="label">{t("routingStrategy", lang)}</div>
              <select
                value={form.routingStrategy}
                onChange={(e) => setForm({ ...form, routingStrategy: e.target.value })}
                style={{ width: "100%", background: "#0d0f15", color: "inherit", border: "1px solid var(--border)", borderRadius: 8, padding: "10px 8px" }}
              >
                <option value="">weighted</option>
                <option value="priority">priority</option>
                <option value="least_latency">least_latency</option>
                <option value="least_cost">least_cost</option>
              </select>
            </label>
            <div style={{ display: "flex", alignItems: "flex-end", gap: 8 }}>
              <button type="submit">{t("submit", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>{t("name", lang)}</th>
            <th>{t("status", lang)}</th>
            <th>{t("expires", lang)}</th>
            <th>{t("actions", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {keys.length === 0 && <tr><td colSpan={5} className="muted">{t("empty", lang)}</td></tr>}
          {keys.map((k) => (
            <tr key={k.id}>
              <td>{k.id}</td>
              <td>
                {k.name}
                {revealed[k.id] && (
                  <div><code style={{ fontSize: 12, wordBreak: "break-all" }}>{revealed[k.id]}</code></div>
                )}
              </td>
              <td>
                <span className={`pill ${k.isEnabled ? "on" : "off"}`}>
                  {t(k.isEnabled ? "enabled" : "disabled", lang)}
                </span>
              </td>
              <td>{k.expiresAt ? new Date(k.expiresAt).toLocaleString() : t("never", lang)}</td>
              <td style={{ whiteSpace: "nowrap" }}>
                <button className="ghost" onClick={() => toggle(k)}>
                  {t(k.isEnabled ? "disable" : "enable", lang)}
                </button>{" "}
                <button className="ghost" onClick={() => reveal(k)}>{t("reveal", lang)}</button>{" "}
                <button className="ghost" style={{ color: "var(--err)" }} onClick={() => revoke(k)}>
                  {t("revoke", lang)}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
