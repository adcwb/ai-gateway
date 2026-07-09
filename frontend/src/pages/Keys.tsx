import { useEffect, useState } from "react";
import {
  api,
  useAsync,
  type CacheConfig,
  type CreateKeyResp,
  type KeyQuotaUsage,
  type PageResp,
  type PIIPolicy,
  type Provider,
  type QuotaConfig,
  type QuotaConfigItem,
  type Tenant,
  type VirtualKey,
} from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Gauge, Icon, Modal, Skeleton, TableSkeleton } from "../components/ui";

export default function Keys({ lang }: { lang: Lang }) {
  const [showForm, setShowForm] = useState(false);
  const [minted, setMinted] = useState<CreateKeyResp | null>(null);
  const [copied, setCopied] = useState(false);
  const [revealed, setRevealed] = useState<Record<number, string>>({});
  const [actionError, setActionError] = useState("");
  const [quotaKeyId, setQuotaKeyId] = useState<number | null>(null);
  const [form, setForm] = useState({
    name: "",
    providerId: 0,
    tenantId: 0,
    dailyTokenQuota: 0,
    routingStrategy: "",
    toolWhitelistCsv: "",
    hourlyToolCallQuota: 0,
    piiPolicyId: 0,
    exactEnabled: false,
    semanticEnabled: false,
    ttlSec: 3600,
    semanticThreshold: 0.92,
    semanticTtlSec: 3600,
    billingPolicy: "free" as NonNullable<CacheConfig["billingPolicy"]>,
    discountPercent: 50,
  });

  const { data, loading, error, refresh } = useAsync<[VirtualKey[], Provider[], Tenant[], PIIPolicy[]]>(
    (s) =>
      Promise.all([
        api
          .get<PageResp<VirtualKey>>("/ai/gateway/key/list?page=1&pageSize=50", { signal: s })
          .then((r) => r.list ?? r.items ?? []),
        api.get<Provider[]>("/ai/gateway/providers", { signal: s }),
        api.get<Tenant[]>("/ai/gateway/tenants", { signal: s }),
        api.get<PIIPolicy[]>("/ai/gateway/pii-policies", { signal: s }),
      ]),
    [],
  );
  const keys = data?.[0] ?? [];
  const providers = data?.[1] ?? [];
  const tenants = data?.[2] ?? [];
  const piiPolicies = data?.[3] ?? [];

  // Seed the create-form's provider/tenant defaults once the lists arrive.
  // Keyed on `data` itself (stable while null/unchanged), not the derived
  // `providers`/`tenants` fallbacks — `data?.[n] ?? []` allocates a new array
  // every render while data is null, which would re-trigger this effect
  // (and its setForm) in an infinite loop for as long as the fetch fails.
  useEffect(() => {
    setForm((f) => ({
      ...f,
      providerId: f.providerId || providers[0]?.id || 0,
      tenantId: f.tenantId || tenants[0]?.id || 0,
    }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.providerId) return;
    const toolWhitelist = form.toolWhitelistCsv
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const cacheConfig: CacheConfig = {
      exactEnabled: form.exactEnabled,
      semanticEnabled: form.semanticEnabled,
      ttlSec: form.ttlSec,
      semanticThreshold: form.semanticThreshold,
      semanticTtlSec: form.semanticTtlSec,
      billingPolicy: form.billingPolicy,
      discountPercent: form.billingPolicy === "discount" ? form.discountPercent : undefined,
    };
    try {
      const resp = await api.post<CreateKeyResp>("/ai/gateway/key", {
        name: form.name.trim(),
        providerId: form.providerId,
        tenantId: form.tenantId,
        dailyTokenQuota: form.dailyTokenQuota || 0,
        routingStrategy: form.routingStrategy || undefined,
        toolWhitelist: toolWhitelist.length ? toolWhitelist : undefined,
        hourlyToolCallQuota: form.hourlyToolCallQuota || 0,
        piiPolicyId: form.piiPolicyId || undefined,
        cacheConfig,
      });
      setMinted(resp);
      setCopied(false);
      setShowForm(false);
      setForm((f) => ({ ...f, name: "", dailyTokenQuota: 0, toolWhitelistCsv: "", hourlyToolCallQuota: 0 }));
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
            <label className="field">
              <div className="field-label">{t("hourlyToolCallQuota", lang)}</div>
              <input
                type="number"
                min="0"
                value={form.hourlyToolCallQuota || ""}
                onChange={(e) => setForm({ ...form, hourlyToolCallQuota: Number(e.target.value) || 0 })}
              />
            </label>
            <label className="field span-2">
              <div className="field-label">{t("toolWhitelistCsv", lang)}</div>
              <input
                value={form.toolWhitelistCsv}
                onChange={(e) => setForm({ ...form, toolWhitelistCsv: e.target.value })}
                placeholder={t("toolWhitelistHint", lang)}
              />
            </label>
            <label className="field">
              <div className="field-label">{t("guardrailPolicy", lang)}</div>
              <select value={form.piiPolicyId} onChange={(e) => setForm({ ...form, piiPolicyId: Number(e.target.value) })}>
                <option value={0}>{t("useDefaultPolicy", lang)}</option>
                {piiPolicies.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </label>

            <div className="field span-3">
              <div className="field-label">{t("cacheConfig", lang)}</div>
              <div className="form-grid" style={{ marginTop: 4 }}>
                <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <input type="checkbox" checked={form.exactEnabled} onChange={(e) => setForm({ ...form, exactEnabled: e.target.checked })} />
                  <div className="field-label" style={{ margin: 0 }}>{t("exactCacheEnabled", lang)}</div>
                </label>
                <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <input type="checkbox" checked={form.semanticEnabled} onChange={(e) => setForm({ ...form, semanticEnabled: e.target.checked })} />
                  <div className="field-label" style={{ margin: 0 }}>{t("semanticCacheEnabled", lang)}</div>
                </label>
                <label className="field">
                  <div className="field-label">{t("cacheTtlSec", lang)}</div>
                  <input type="number" min="0" value={form.ttlSec} onChange={(e) => setForm({ ...form, ttlSec: Number(e.target.value) || 0 })} />
                </label>
                <label className="field">
                  <div className="field-label">{t("semanticThreshold", lang)}</div>
                  <input
                    type="number"
                    min="0"
                    max="1"
                    step="0.01"
                    value={form.semanticThreshold}
                    onChange={(e) => setForm({ ...form, semanticThreshold: Number(e.target.value) || 0 })}
                  />
                </label>
                <label className="field">
                  <div className="field-label">{t("semanticCacheTtlSec", lang)}</div>
                  <input type="number" min="0" value={form.semanticTtlSec} onChange={(e) => setForm({ ...form, semanticTtlSec: Number(e.target.value) || 0 })} />
                </label>
                <label className="field">
                  <div className="field-label">{t("cacheBillingPolicy", lang)}</div>
                  <select
                    value={form.billingPolicy}
                    onChange={(e) => setForm({ ...form, billingPolicy: e.target.value as typeof form.billingPolicy })}
                  >
                    <option value="free">free</option>
                    <option value="discount">discount</option>
                    <option value="full">full</option>
                  </select>
                </label>
                {form.billingPolicy === "discount" && (
                  <label className="field">
                    <div className="field-label">{t("cacheDiscountPercent", lang)}</div>
                    <input
                      type="number"
                      min="0"
                      max="100"
                      value={form.discountPercent}
                      onChange={(e) => setForm({ ...form, discountPercent: Number(e.target.value) || 0 })}
                    />
                  </label>
                )}
              </div>
            </div>

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
                      <button className="ghost sm" onClick={() => setQuotaKeyId(k.id)}>
                        <Icon name="dashboard" size={13} /> {t("quotas", lang)}
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

      {quotaKeyId != null && (
        <QuotaModal keyId={quotaKeyId} lang={lang} onClose={() => setQuotaKeyId(null)} />
      )}
    </div>
  );
}

const emptyModelQuota: QuotaConfigItem = {
  modelName: "",
  dailyTokenQuota: 0,
  hourlyTokenQuota: 0,
  hourlyReqQuota: 0,
  dailyPointQuota: 0,
  hourlyPointQuota: 0,
  dailyTokenUsed: 0,
  hourlyTokenUsed: 0,
  hourlyReqUsed: 0,
  dailyPointUsed: 0,
  hourlyPointUsed: 0,
};

type GlobalQuotaField =
  | "dailyTokenQuota" | "hourlyTokenQuota" | "hourlyReqQuota"
  | "maxConcurrency" | "dailyPointQuota" | "hourlyPointQuota";
type ModelQuotaField =
  | "dailyTokenQuota" | "hourlyTokenQuota" | "hourlyReqQuota"
  | "dailyPointQuota" | "hourlyPointQuota";

function QuotaModal({ keyId, lang, onClose }: { keyId: number; lang: Lang; onClose: () => void }) {
  const [config, setConfig] = useState<QuotaConfig | null>(null);
  const [usage, setUsage] = useState<KeyQuotaUsage | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);

  const load = () => {
    setLoading(true);
    setLoadError("");
    Promise.all([
      api.get<QuotaConfig>(`/ai/gateway/key/quota-config?keyId=${keyId}`),
      api.get<KeyQuotaUsage>(`/ai/gateway/key/quota-usage?keyId=${keyId}`),
    ])
      .then(([c, u]) => {
        setConfig(c);
        setUsage(u);
      })
      .catch((e) => setLoadError((e as Error).message))
      .finally(() => setLoading(false));
  };

  useEffect(load, [keyId]);

  const updateField = (key: GlobalQuotaField, value: number) => {
    setConfig((c) => (c ? { ...c, [key]: value } : c));
  };

  const updateModelQuota = (idx: number, patch: Partial<QuotaConfigItem>) => {
    setConfig((c) => {
      if (!c) return c;
      const modelQuotas = c.modelQuotas.slice();
      modelQuotas[idx] = { ...modelQuotas[idx], ...patch };
      return { ...c, modelQuotas };
    });
  };

  const addModelQuota = () => {
    setConfig((c) => (c ? { ...c, modelQuotas: [...c.modelQuotas, { ...emptyModelQuota }] } : c));
  };

  const removeModelQuota = (idx: number) => {
    setConfig((c) => (c ? { ...c, modelQuotas: c.modelQuotas.filter((_, i) => i !== idx) } : c));
  };

  const save = async () => {
    if (!config) return;
    setSaving(true);
    setSaveError("");
    try {
      await api.put("/ai/gateway/key/quota-config", {
        keyId: config.keyId,
        dailyTokenQuota: config.dailyTokenQuota,
        hourlyTokenQuota: config.hourlyTokenQuota,
        hourlyReqQuota: config.hourlyReqQuota,
        maxConcurrency: config.maxConcurrency,
        dailyPointQuota: config.dailyPointQuota,
        hourlyPointQuota: config.hourlyPointQuota,
        modelQuotas: config.modelQuotas.filter((m) => m.modelName.trim()),
      });
      onClose();
    } catch (e) {
      setSaveError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const numField = (labelKey: string, key: GlobalQuotaField, value: number) => (
    <label className="field" key={key}>
      <div className="field-label">{t(labelKey, lang)}</div>
      <input
        type="number"
        min="0"
        value={value || ""}
        onChange={(e) => updateField(key, Number(e.target.value) || 0)}
      />
    </label>
  );

  const modelNumField = (labelKey: string, key: ModelQuotaField, idx: number, value: number) => (
    <label className="field" key={key}>
      <div className="field-label">{t(labelKey, lang)}</div>
      <input
        type="number"
        min="0"
        value={value || ""}
        onChange={(e) => updateModelQuota(idx, { [key]: Number(e.target.value) || 0 })}
      />
    </label>
  );

  return (
    <Modal title={t("quotas", lang)} onClose={onClose} closeLabel={t("close", lang)} width={760}>
      {loading ? (
        <div className="flex" style={{ flexDirection: "column", gap: 10 }}>
          {Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} w="100%" h={14} />)}
        </div>
      ) : loadError ? (
        <ErrorBanner message={`${t("loadQuotasFailed", lang)}: ${loadError}`} onRetry={load} />
      ) : config && usage ? (
        <>
          <h1 className="section-title" style={{ marginTop: 0 }}>{t("quotaUsage", lang)}</h1>
          <Gauge label={t("dailyTokenQuota", lang)} used={usage.dailyTokenUsed} quota={usage.dailyTokenQuota} unlimitedLabel={t("unlimited", lang)} />
          <Gauge label={t("hourlyTokenQuota", lang)} used={usage.hourlyTokenUsed} quota={usage.hourlyTokenQuota} unlimitedLabel={t("unlimited", lang)} />
          <Gauge label={t("hourlyReqQuota", lang)} used={usage.hourlyReqUsed} quota={usage.hourlyReqQuota} unlimitedLabel={t("unlimited", lang)} />
          <Gauge label={t("concurrency", lang)} used={usage.currentConcurrency} quota={usage.maxConcurrency} unlimitedLabel={t("unlimited", lang)} />
          <Gauge label={t("dailyPointQuota", lang)} used={usage.dailyPointUsed} quota={usage.dailyPointQuota} unlimitedLabel={t("unlimited", lang)} />
          <Gauge label={t("hourlyPointQuota", lang)} used={usage.hourlyPointUsed} quota={usage.hourlyPointQuota} unlimitedLabel={t("unlimited", lang)} />

          <h1 className="section-title">{t("globalQuotas", lang)}</h1>
          <div className="form-grid">
            {numField("dailyTokenQuota", "dailyTokenQuota", config.dailyTokenQuota)}
            {numField("hourlyTokenQuota", "hourlyTokenQuota", config.hourlyTokenQuota)}
            {numField("hourlyReqQuota", "hourlyReqQuota", config.hourlyReqQuota)}
            {numField("maxConcurrency", "maxConcurrency", config.maxConcurrency)}
            {numField("dailyPointQuota", "dailyPointQuota", config.dailyPointQuota)}
            {numField("hourlyPointQuota", "hourlyPointQuota", config.hourlyPointQuota)}
          </div>

          <h1 className="section-title">{t("perModelQuotas", lang)}</h1>
          <div className="muted" style={{ fontSize: 12.5, marginBottom: 10 }}>{t("perModelQuotasHint", lang)}</div>
          {config.modelQuotas.map((m, idx) => (
            <div
              className="form-grid"
              key={idx}
              style={{ marginBottom: 10, paddingBottom: 10, borderBottom: "1px solid var(--border)" }}
            >
              <label className="field">
                <div className="field-label">{t("modelName", lang)}</div>
                <input value={m.modelName} onChange={(e) => updateModelQuota(idx, { modelName: e.target.value })} />
              </label>
              {modelNumField("dailyTokenQuota", "dailyTokenQuota", idx, m.dailyTokenQuota)}
              {modelNumField("hourlyTokenQuota", "hourlyTokenQuota", idx, m.hourlyTokenQuota)}
              {modelNumField("hourlyReqQuota", "hourlyReqQuota", idx, m.hourlyReqQuota)}
              {modelNumField("dailyPointQuota", "dailyPointQuota", idx, m.dailyPointQuota)}
              {modelNumField("hourlyPointQuota", "hourlyPointQuota", idx, m.hourlyPointQuota)}
              <div className="form-actions">
                <button type="button" className="danger sm" onClick={() => removeModelQuota(idx)}>
                  <Icon name="trash" size={13} /> {t("removeRow", lang)}
                </button>
              </div>
            </div>
          ))}
          <button type="button" className="ghost sm" onClick={addModelQuota} style={{ marginBottom: 18 }}>
            <Icon name="plus" size={13} /> {t("addModelQuota", lang)}
          </button>

          {saveError && <ErrorBanner message={saveError} />}
          <div className="form-actions">
            <button type="button" onClick={save} disabled={saving}>
              <Icon name="check" size={14} /> {t("saveQuotas", lang)}
            </button>
          </div>
        </>
      ) : null}
    </Modal>
  );
}
