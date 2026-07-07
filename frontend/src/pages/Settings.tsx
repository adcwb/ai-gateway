import { useEffect, useState } from "react";
import { api, useAsync, type CreditsRate, type Settings as SettingsResp } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

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

  const showError = actionError || (settingsQ.error ? `${t("loadFailed", lang)}: ${settingsQ.error}` : "") || (ratesQ.error ? `${t("loadFailed", lang)}: ${ratesQ.error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("settings", lang)}</h1>
        </div>
      </div>

      {showError && <ErrorBanner message={showError} onRetry={() => { setActionError(""); settingsQ.refresh(); ratesQ.refresh(); }} />}

      <div className="card mb-16">
        <div className="label mb-16">{t("alertWebhook", lang)}</div>
        <div className="form-grid">
          <label className="field span-3">
            <div className="field-label">
              {t("webhookUrl", lang)}
              {settingsQ.data?.alertWebhookIsOverride && <span className="pill info" style={{ marginLeft: 8 }}>{t("consoleOverride", lang)}</span>}
            </div>
            <input value={webhookInput} onChange={(e) => setWebhookInput(e.target.value)} placeholder="https://hooks.example.com/aigw" />
          </label>
          <div className="form-actions">
            <button onClick={saveWebhook}><Icon name="check" size={14} /> {t("save", lang)}</button>
            <button className="ghost" onClick={testWebhook} disabled={!webhookInput}>{t("testWebhook", lang)}</button>
          </div>
        </div>
        {testResult && <div className="sub mt-8">{testResult}</div>}
      </div>

      <div className="topbar">
        <div className="titles"><h2 style={{ fontSize: 15, margin: 0 }}>{t("creditsRates", lang)}</h2></div>
        <div className="actions">
          <button onClick={() => startEditRate()}><Icon name="plus" size={14} /> {t("addRate", lang)}</button>
        </div>
      </div>

      {showRateForm && (
        <form className="card mb-16" onSubmit={submitRate}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("currency", lang)}</div>
              <input value={rateForm.currency} onChange={(e) => setRateForm({ ...rateForm, currency: e.target.value.toUpperCase() })} required disabled={!!rateForm.id} placeholder="USD" autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("ratePerCredit", lang)}</div>
              <input type="number" min="0" step="0.0001" value={rateForm.ratePerCredit} onChange={(e) => setRateForm({ ...rateForm, ratePerCredit: Number(e.target.value) || 0 })} required />
            </label>
            <label className="field span-3">
              <div className="field-label">{t("remark", lang)}</div>
              <input value={rateForm.description} onChange={(e) => setRateForm({ ...rateForm, description: e.target.value })} />
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowRateForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap mb-16">
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
                  <td>{r.isEnabled ? <span className="pill on">{t("enabled", lang)}</span> : <span className="pill off">{t("disabled", lang)}</span>}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => startEditRate(r)}>{t("editProvider", lang)}</button>
                      <button className="ghost sm" onClick={() => toggleRate(r)}>{r.isEnabled ? t("disable", lang) : t("enable", lang)}</button>
                      <button className="danger sm" onClick={() => deleteRate(r)}><Icon name="trash" size={13} /> {t("deleteProvider", lang)}</button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="label mb-16">{t("about", lang)}</div>
        <div className="detail-grid">
          <div><div className="k">{t("aboutProject", lang)}</div><div className="v">ai-gateway</div></div>
          <div><div className="k">{t("aboutRepo", lang)}</div><div className="v">github.com/opscenter/ai-gateway</div></div>
        </div>
      </div>
    </div>
  );
}
