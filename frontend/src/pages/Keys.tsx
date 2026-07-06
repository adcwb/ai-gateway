import { useEffect, useState } from "react";
import {
  api,
  useAsync,
  type CreateKeyResp,
  type PageResp,
  type Provider,
  type Tenant,
  type VirtualKey,
} from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

export default function Keys({ lang }: { lang: Lang }) {
  const [showForm, setShowForm] = useState(false);
  const [minted, setMinted] = useState<CreateKeyResp | null>(null);
  const [copied, setCopied] = useState(false);
  const [revealed, setRevealed] = useState<Record<number, string>>({});
  const [actionError, setActionError] = useState("");
  const [form, setForm] = useState({
    name: "",
    providerId: 0,
    tenantId: 0,
    dailyTokenQuota: 0,
    routingStrategy: "",
  });

  const { data, loading, error, refresh } = useAsync<[VirtualKey[], Provider[], Tenant[]]>(
    (s) =>
      Promise.all([
        api
          .get<PageResp<VirtualKey>>("/ai/gateway/key/list?page=1&pageSize=50", { signal: s })
          .then((r) => r.list ?? r.items ?? []),
        api.get<Provider[]>("/ai/gateway/providers", { signal: s }),
        api.get<Tenant[]>("/ai/gateway/tenants", { signal: s }),
      ]),
    [],
  );
  const keys = data?.[0] ?? [];
  const providers = data?.[1] ?? [];
  const tenants = data?.[2] ?? [];

  // Seed the create-form's provider/tenant defaults once the lists arrive.
  useEffect(() => {
    setForm((f) => ({
      ...f,
      providerId: f.providerId || providers[0]?.id || 0,
      tenantId: f.tenantId || tenants[0]?.id || 0,
    }));
  }, [providers, tenants]);

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
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const toggle = async (k: VirtualKey) => {
    try {
      await api.put("/ai/gateway/key/status", { id: k.id, isEnabled: !k.isEnabled });
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const reveal = async (k: VirtualKey) => {
    try {
      const resp = await api.get<{ plainKey?: string; key?: string }>(`/ai/gateway/key/reveal?id=${k.id}`);
      setRevealed((r) => ({ ...r, [k.id]: resp.plainKey ?? resp.key ?? "?" }));
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const revoke = async (k: VirtualKey) => {
    if (!window.confirm(t("confirmRevoke", lang))) return;
    try {
      await api.del(`/ai/gateway/key?id=${k.id}`);
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
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

  const cols = 5;
  const showError = actionError || (error ? `${t("loadFailed", lang)}: ${error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navOperate", lang)}</div>
          <h1>{t("keys", lang)}</h1>
        </div>
        <div className="actions flex gap-8">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
          <button onClick={() => setShowForm((v) => !v)}>
            <Icon name="plus" size={14} /> {t("createKey", lang)}
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

      {minted && (
        <div className="card success mb-16">
          <div className="label">{minted.name} — {t("keyCreatedOnce", lang)}</div>
          <div className="flex gap-8 items-center" style={{ marginTop: 8 }}>
            <code className="code-block" style={{ flex: 1 }}>{minted.plainKey}</code>
            <button className="ghost sm" onClick={() => copyKey(minted.plainKey)}>
              <Icon name={copied ? "check" : "copy"} size={14} /> {copied ? t("copied", lang) : t("copy", lang)}
            </button>
            <button className="ghost sm" onClick={() => setMinted(null)}>
              <Icon name="close" size={14} /> {t("close", lang)}
            </button>
          </div>
        </div>
      )}

      {showForm && (
        <form className="card mb-16" onSubmit={create}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("provider", lang)}</div>
              <select value={form.providerId} onChange={(e) => setForm({ ...form, providerId: Number(e.target.value) })}>
                {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("tenant", lang)}</div>
              <select value={form.tenantId} onChange={(e) => setForm({ ...form, tenantId: Number(e.target.value) })}>
                {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("dailyTokens", lang)}</div>
              <input
                type="number"
                min="0"
                value={form.dailyTokenQuota || ""}
                onChange={(e) => setForm({ ...form, dailyTokenQuota: Number(e.target.value) || 0 })}
              />
            </label>
            <label className="field">
              <div className="field-label">{t("routingStrategy", lang)}</div>
              <select value={form.routingStrategy} onChange={(e) => setForm({ ...form, routingStrategy: e.target.value })}>
                <option value="">weighted</option>
                <option value="priority">priority</option>
                <option value="least_latency">least_latency</option>
                <option value="least_cost">least_cost</option>
              </select>
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="plus" size={14} /> {t("submit", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap">
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
            {loading && keys.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : keys.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState
                    icon="key"
                    title={t("emptyKeys", lang)}
                    sub={t("emptyKeysSub", lang)}
                    action={
                      <button onClick={() => setShowForm(true)}>
                        <Icon name="plus" size={14} /> {t("createKey", lang)}
                      </button>
                    }
                  />
                </td>
              </tr>
            ) : (
              keys.map((k) => (
                <tr key={k.id}>
                  <td className="id">{k.id}</td>
                  <td>
                    {k.name}
                    {revealed[k.id] && (
                      <div className="mono break-all" style={{ fontSize: 12, marginTop: 2, color: "var(--accent)" }}>
                        {revealed[k.id]}
                      </div>
                    )}
                  </td>
                  <td>
                    <span className={`pill ${k.isEnabled ? "on" : "off"}`}>
                      {t(k.isEnabled ? "enabled" : "disabled", lang)}
                    </span>
                  </td>
                  <td className="muted mono">{k.expiresAt ? new Date(k.expiresAt).toLocaleString() : t("never", lang)}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => toggle(k)}>
                        {t(k.isEnabled ? "disable" : "enable", lang)}
                      </button>
                      <button className="ghost sm" onClick={() => reveal(k)}>
                        <Icon name="eye" size={13} /> {t("reveal", lang)}
                      </button>
                      <button className="danger sm" onClick={() => revoke(k)}>
                        <Icon name="trash" size={13} /> {t("revoke", lang)}
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
