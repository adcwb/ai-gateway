import { useEffect, useState } from "react";
import { api, useAsync, type CreditsRate, type Provider, type Settings as SettingsResp } from "../api/client";
import { t, type Lang } from "../i18n";
import {
  Button, Card, EmptyState, ErrorBanner, Field, FormGrid, Icon, Pill, TableSkeleton, TableWrap, Topbar,
} from "../components/ui";

const emptyRateForm = { id: 0, currency: "", ratePerCredit: 0.01, description: "" };

export default function Settings({ lang }: { lang: Lang }) {
  const [actionError, setActionError] = useState("");
  const [testResult, setTestResult] = useState("");

  // ---- Alert webhook ---------------------------------------------------------
  const settingsQ = useAsync<SettingsResp>((s) => api.get<SettingsResp>("/ai/gateway/settings", { signal: s }), []);
  const [webhookInput, setWebhookInput] = useState("");
  useEffect(() => {
    if (settingsQ.data) setWebhookInput(settingsQ.data.alertWebhook);
  }, [settingsQ.data]);

  const saveWebhook = async () => {
    try {
      await api.put("/ai/gateway/settings", { alertWebhook: webhookInput });
      setTestResult("");
      settingsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  // ---- Semantic cache embedding config ----------------------------------------
  const providersQ = useAsync<Provider[]>((s) => api.get<Provider[]>("/ai/gateway/providers", { signal: s }), []);
  const providers = providersQ.data ?? [];
  const [embedding, setEmbedding] = useState({ providerId: 0, model: "", dim: 0 });
  useEffect(() => {
    if (settingsQ.data) {
      setEmbedding({
        providerId: settingsQ.data.cacheEmbeddingProviderId || 0,
        model: settingsQ.data.cacheEmbeddingModel || "",
        dim: settingsQ.data.cacheEmbeddingDim || 0,
      });
    }
  }, [settingsQ.data]);

  const saveEmbedding = async () => {
    try {
      await api.put("/ai/gateway/settings", {
        cacheEmbeddingProviderId: embedding.providerId || 0,
        cacheEmbeddingModel: embedding.model,
        cacheEmbeddingDim: embedding.dim || 0,
      });
      settingsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };
  const testWebhook = async () => {
    setTestResult("");
    try {
      await api.post("/ai/gateway/settings/test-webhook");
      setTestResult(t("webhookTestOk", lang));
    } catch (err) {
      setTestResult(`${t("webhookTestFailed", lang)}: ${(err as Error).message}`);
    }
  };

  // ---- Credits rates ----------------------------------------------------------
  const ratesQ = useAsync<CreditsRate[]>((s) => api.get<CreditsRate[]>("/ai/gateway/credits-rates", { signal: s }), []);
  const rates = ratesQ.data ?? [];
  const [showRateForm, setShowRateForm] = useState(false);
  const [rateForm, setRateForm] = useState({ ...emptyRateForm });

  const startEditRate = (r?: CreditsRate) => {
    setRateForm(r ? { id: r.id, currency: r.currency, ratePerCredit: r.ratePerCredit, description: r.description } : { ...emptyRateForm });
    setShowRateForm(true);
  };
  const submitRate = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (rateForm.id) {
        await api.put("/ai/gateway/credits-rates", { id: rateForm.id, ratePerCredit: rateForm.ratePerCredit, description: rateForm.description });
      } else {
        await api.post("/ai/gateway/credits-rates", { currency: rateForm.currency, ratePerCredit: rateForm.ratePerCredit, description: rateForm.description });
      }
      setShowRateForm(false);
      ratesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };
  const toggleRate = async (r: CreditsRate) => {
    try {
      await api.put("/ai/gateway/credits-rates", { id: r.id, isEnabled: !r.isEnabled });
      ratesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };
  const deleteRate = async (r: CreditsRate) => {
    if (!window.confirm(t("confirmDeleteRate", lang))) return;
    try {
      await api.del(`/ai/gateway/credits-rates?id=${r.id}`);
      ratesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const showError = actionError || (settingsQ.error ? `${t("loadFailed", lang)}: ${settingsQ.error}` : "") || (ratesQ.error ? `${t("loadFailed", lang)}: ${ratesQ.error}` : "") || (providersQ.error ? `${t("loadFailed", lang)}: ${providersQ.error}` : "");

  return (
    <div>
      <Topbar eyebrow={t("navManage", lang)} title={t("settings", lang)} />

      {showError && <ErrorBanner message={showError} onRetry={() => { setActionError(""); settingsQ.refresh(); ratesQ.refresh(); providersQ.refresh(); }} />}

      <Card className="mb-16">
        <div className="label mb-16">{t("alertWebhook", lang)}</div>
        <FormGrid>
          <Field
            span={3}
            label={
              <>
                {t("webhookUrl", lang)}
                {settingsQ.data?.alertWebhookIsOverride && (
                  <span style={{ marginLeft: 8 }}><Pill tone="info">{t("consoleOverride", lang)}</Pill></span>
                )}
              </>
            }
          >
            <input value={webhookInput} onChange={(e) => setWebhookInput(e.target.value)} placeholder="https://hooks.example.com/aigw" />
          </Field>
          <div className="form-actions">
            <Button onClick={saveWebhook}><Icon name="check" size={14} /> {t("save", lang)}</Button>
            <Button variant="ghost" onClick={testWebhook} disabled={!webhookInput}>{t("testWebhook", lang)}</Button>
          </div>
        </FormGrid>
        {testResult && <div className="sub mt-8">{testResult}</div>}
      </Card>

      <Card className="mb-16">
        <div className="label mb-16">{t("semanticCacheEmbedding", lang)}</div>
        <div className="sub mb-8">{t("semanticCacheEmbeddingHint", lang)}</div>
        <FormGrid>
          <Field label={t("provider", lang)}>
            <select value={embedding.providerId} onChange={(e) => setEmbedding({ ...embedding, providerId: Number(e.target.value) })}>
              <option value={0}>—</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </Field>
          <Field label={t("embeddingModel", lang)}>
            <input value={embedding.model} onChange={(e) => setEmbedding({ ...embedding, model: e.target.value })} placeholder="text-embedding-3-small" />
          </Field>
          <Field label={t("embeddingDim", lang)}>
            <input type="number" min="0" value={embedding.dim || ""} onChange={(e) => setEmbedding({ ...embedding, dim: Number(e.target.value) || 0 })} />
          </Field>
          <div className="form-actions">
            <Button onClick={saveEmbedding}><Icon name="check" size={14} /> {t("save", lang)}</Button>
          </div>
        </FormGrid>
      </Card>

      <div className="topbar">
        <div className="titles"><h2 style={{ fontSize: 15, margin: 0 }}>{t("creditsRates", lang)}</h2></div>
        <div className="actions">
          <Button onClick={() => startEditRate()}><Icon name="plus" size={14} /> {t("addRate", lang)}</Button>
        </div>
      </div>

      {showRateForm && (
        <Card className="mb-16">
          <form onSubmit={submitRate}>
          <FormGrid>
            <Field label={t("currency", lang)}>
              <input value={rateForm.currency} onChange={(e) => setRateForm({ ...rateForm, currency: e.target.value.toUpperCase() })} required disabled={!!rateForm.id} placeholder="USD" autoFocus />
            </Field>
            <Field label={t("ratePerCredit", lang)}>
              <input type="number" min="0" step="0.0001" value={rateForm.ratePerCredit} onChange={(e) => setRateForm({ ...rateForm, ratePerCredit: Number(e.target.value) || 0 })} required />
            </Field>
            <Field span={3} label={t("remark", lang)}>
              <input value={rateForm.description} onChange={(e) => setRateForm({ ...rateForm, description: e.target.value })} />
            </Field>
            <div className="form-actions">
              <Button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</Button>
              <Button type="button" variant="ghost" onClick={() => setShowRateForm(false)}>{t("cancel", lang)}</Button>
            </div>
          </FormGrid>
          </form>
        </Card>
      )}

      <TableWrap className="mb-16">
        <table>
          <thead>
            <tr>
              <th>{t("currency", lang)}</th>
              <th>{t("ratePerCredit", lang)}</th>
              <th>{t("remark", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {ratesQ.loading && rates.length === 0 ? (
              <TableSkeleton cols={5} />
            ) : rates.length === 0 ? (
              <tr><td colSpan={5}><EmptyState icon="billing" title={t("emptyRates", lang)} sub={t("emptyRatesSub", lang)} /></td></tr>
            ) : (
              rates.map((r) => (
                <tr key={r.id}>
                  <td className="mono">{r.currency}</td>
                  <td className="mono">{r.ratePerCredit}</td>
                  <td className="muted">{r.description || "—"}</td>
                  <td><Pill tone={r.isEnabled ? "on" : "off"}>{t(r.isEnabled ? "enabled" : "disabled", lang)}</Pill></td>
                  <td>
                    <div className="row-actions">
                      <Button variant="ghost" size="sm" onClick={() => startEditRate(r)}>{t("editProvider", lang)}</Button>
                      <Button variant="ghost" size="sm" onClick={() => toggleRate(r)}>{r.isEnabled ? t("disable", lang) : t("enable", lang)}</Button>
                      <Button variant="danger" size="sm" onClick={() => deleteRate(r)}><Icon name="trash" size={13} /> {t("deleteProvider", lang)}</Button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </TableWrap>

      <Card>
        <div className="label mb-16">{t("about", lang)}</div>
        <div className="detail-grid">
          <div><div className="k">{t("aboutProject", lang)}</div><div className="v">ai-gateway</div></div>
          <div><div className="k">{t("aboutRepo", lang)}</div><div className="v">github.com/adcwb/ai-gateway</div></div>
        </div>
      </Card>
    </div>
  );
}
