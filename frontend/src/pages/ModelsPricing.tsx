import { useMemo, useState } from "react";
import {
  api,
  useAsync,
  type ModelItem,
  type PatternTestResp,
  type PriceTable,
  type PriceTableItem,
  type Provider,
} from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, Skeleton, TableSkeleton } from "../components/ui";

const emptyModelForm = {
  id: 0,
  providerId: 0,
  name: "",
  modelType: "llm",
  contextWindow: 0,
  isDefault: false,
  inputPricePerMillion: 0,
  outputPricePerMillion: 0,
  cacheReadPricePerMillion: 0,
  cacheWritePricePerMillion: 0,
};

// Multimodal media adapters, phases 1-2 (docs/superpowers/specs/2026-07-09-
// multimodal-media-adapters-design.md, 2026-07-09-video-generation-phase2-
// design.md): "image"/"tts"/"asr"/"video" are the modality values
// resolveMediaModel filters candidates by; "llm" is the pre-existing
// default. Console-only addition — the backend never validated modelType
// against a whitelist.
const modelTypeOptions = ["llm", "image", "tts", "asr", "video"] as const;
const modelTypeLabelKey: Record<string, "modelTypeLLM" | "modelTypeImage" | "modelTypeTTS" | "modelTypeASR" | "modelTypeVideo"> = {
  llm: "modelTypeLLM",
  image: "modelTypeImage",
  tts: "modelTypeTTS",
  asr: "modelTypeASR",
  video: "modelTypeVideo",
};

const emptyTableForm = { id: 0, name: "", currency: "CNY" };
const emptyItemForm = { id: 0, priceTableId: 0, modelPattern: "", inputPricePerMillion: 0, outputPricePerMillion: 0, cacheReadPerMillion: 0 };

export default function ModelsPricing({ lang }: { lang: Lang }) {
  const [actionError, setActionError] = useState("");

  // ---- Model catalog -------------------------------------------------------
  const providersQ = useAsync<Provider[]>((s) => api.get<Provider[]>("/ai/gateway/providers", { signal: s }), []);
  const providers = providersQ.data ?? [];
  const modelsQ = useAsync<ModelItem[]>((s) => api.get<ModelItem[]>("/ai/gateway/model-items", { signal: s }), []);
  const models = modelsQ.data ?? [];
  const providerName = (id: number) => providers.find((p) => p.id === id)?.name ?? `#${id}`;

  const [showModelForm, setShowModelForm] = useState(false);
  const [modelForm, setModelForm] = useState({ ...emptyModelForm });

  const startEditModel = (m?: ModelItem) => {
    if (m) {
      setModelForm({
        id: m.id, providerId: m.providerId, name: m.name, modelType: m.modelType, contextWindow: m.contextWindow,
        isDefault: m.isDefault, inputPricePerMillion: m.inputPricePerMillion, outputPricePerMillion: m.outputPricePerMillion,
        cacheReadPricePerMillion: m.cacheReadPricePerMillion, cacheWritePricePerMillion: m.cacheWritePricePerMillion,
      });
    } else {
      setModelForm({ ...emptyModelForm, providerId: providers[0]?.id ?? 0 });
    }
    setShowModelForm(true);
  };

  const submitModel = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (modelForm.id) {
        await api.put("/ai/gateway/model-items", modelForm);
      } else {
        await api.post("/ai/gateway/model-items", modelForm);
      }
      setShowModelForm(false);
      modelsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const deleteModel = async (m: ModelItem) => {
    if (!window.confirm(t("confirmDeleteModelItem", lang))) return;
    try {
      await api.del(`/ai/gateway/model-items?id=${m.id}`);
      modelsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  // ---- Price tables ---------------------------------------------------------
  const tablesQ = useAsync<PriceTable[]>((s) => api.get<PriceTable[]>("/ai/gateway/price-tables", { signal: s }), []);
  const tables = tablesQ.data ?? [];
  const [activeTableId, setActiveTableId] = useState<number | null>(null);
  const activeTable = tables.find((tbl) => tbl.id === activeTableId) ?? tables[0] ?? null;

  const [showTableForm, setShowTableForm] = useState(false);
  const [tableForm, setTableForm] = useState({ ...emptyTableForm });
  const submitTable = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (tableForm.id) {
        await api.put("/ai/gateway/price-tables", tableForm);
      } else {
        await api.post("/ai/gateway/price-tables", tableForm);
      }
      setShowTableForm(false);
      tablesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };
  const deleteTable = async (tbl: PriceTable) => {
    if (!window.confirm(t("confirmDeletePriceTable", lang))) return;
    try {
      await api.del(`/ai/gateway/price-tables?id=${tbl.id}`);
      if (activeTableId === tbl.id) setActiveTableId(null);
      tablesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const [showItemForm, setShowItemForm] = useState(false);
  const [itemForm, setItemForm] = useState({ ...emptyItemForm });
  const startEditItem = (it?: PriceTableItem) => {
    if (it) {
      setItemForm({ ...it });
    } else {
      setItemForm({ ...emptyItemForm, priceTableId: activeTable?.id ?? 0 });
    }
    setShowItemForm(true);
  };
  const submitItem = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      if (itemForm.id) {
        await api.put("/ai/gateway/price-tables/items", itemForm);
      } else {
        await api.post("/ai/gateway/price-tables/items", itemForm);
      }
      setShowItemForm(false);
      tablesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };
  const deleteItem = async (it: PriceTableItem) => {
    if (!window.confirm(t("confirmDeletePriceItem", lang))) return;
    try {
      await api.del(`/ai/gateway/price-tables/items?id=${it.id}`);
      tablesQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  // ---- Pattern tester --------------------------------------------------------
  const knownModels = useMemo(() => {
    const set = new Set<string>();
    for (const m of models) set.add(m.name);
    for (const p of providers) for (const pm of p.models ?? []) set.add(pm.name);
    return Array.from(set);
  }, [models, providers]);
  const [testerPattern, setTesterPattern] = useState("");
  const [testerResult, setTesterResult] = useState<PatternTestResp | null>(null);
  const runTester = async (pattern: string) => {
    setTesterPattern(pattern);
    if (!pattern) {
      setTesterResult(null);
      return;
    }
    try {
      const resp = await api.post<PatternTestResp>("/ai/gateway/price-tables/test-pattern", { pattern, models: knownModels });
      setTesterResult(resp);
    } catch {
      setTesterResult(null);
    }
  };

  const showError = actionError || (modelsQ.error ? `${t("loadFailed", lang)}: ${modelsQ.error}` : "") || (tablesQ.error ? `${t("loadFailed", lang)}: ${tablesQ.error}` : "");

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("modelsPricing", lang)}</h1>
        </div>
        <div className="actions flex gap-8">
          <button className="ghost sm" onClick={() => { modelsQ.refresh(); tablesQ.refresh(); }}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
        </div>
      </div>

      {showError && <ErrorBanner message={showError} onRetry={() => { setActionError(""); modelsQ.refresh(); tablesQ.refresh(); }} />}

      {/* ---------------- Model catalog ---------------- */}
      <div className="topbar" style={{ marginTop: 4 }}>
        <div className="titles"><h2 style={{ fontSize: 15, margin: 0 }}>{t("modelCatalog", lang)}</h2></div>
        <div className="actions">
          <button onClick={() => startEditModel()}><Icon name="plus" size={14} /> {t("addModelItem", lang)}</button>
        </div>
      </div>

      {showModelForm && (
        <form className="card mb-16" onSubmit={submitModel}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("provider", lang)}</div>
              <select value={modelForm.providerId} onChange={(e) => setModelForm({ ...modelForm, providerId: Number(e.target.value) })} disabled={!!modelForm.id}>
                {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={modelForm.name} onChange={(e) => setModelForm({ ...modelForm, name: e.target.value })} required disabled={!!modelForm.id} autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("modelType", lang)}</div>
              <select value={modelForm.modelType} onChange={(e) => setModelForm({ ...modelForm, modelType: e.target.value })}>
                {modelTypeOptions.map((mt) => <option key={mt} value={mt}>{t(modelTypeLabelKey[mt], lang)}</option>)}
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("contextWindow", lang)}</div>
              <input type="number" min="0" value={modelForm.contextWindow} onChange={(e) => setModelForm({ ...modelForm, contextWindow: Number(e.target.value) || 0 })} />
            </label>
            <label className="field">
              <div className="field-label">{t("inputPrice", lang)}</div>
              <input type="number" min="0" step="0.01" value={modelForm.inputPricePerMillion} onChange={(e) => setModelForm({ ...modelForm, inputPricePerMillion: Number(e.target.value) || 0 })} />
            </label>
            <label className="field">
              <div className="field-label">{t("outputPrice", lang)}</div>
              <input type="number" min="0" step="0.01" value={modelForm.outputPricePerMillion} onChange={(e) => setModelForm({ ...modelForm, outputPricePerMillion: Number(e.target.value) || 0 })} />
            </label>
            <label className="field">
              <div className="field-label">{t("cacheReadPrice", lang)}</div>
              <input type="number" min="0" step="0.01" value={modelForm.cacheReadPricePerMillion} onChange={(e) => setModelForm({ ...modelForm, cacheReadPricePerMillion: Number(e.target.value) || 0 })} />
            </label>
            <label className="field">
              <div className="field-label">{t("cacheWritePrice", lang)}</div>
              <input type="number" min="0" step="0.01" value={modelForm.cacheWritePricePerMillion} onChange={(e) => setModelForm({ ...modelForm, cacheWritePricePerMillion: Number(e.target.value) || 0 })} />
            </label>
            <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <input type="checkbox" checked={modelForm.isDefault} onChange={(e) => setModelForm({ ...modelForm, isDefault: e.target.checked })} />
              <div className="field-label" style={{ margin: 0 }}>{t("isDefaultModel", lang)}</div>
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowModelForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap mb-16">
        <table>
          <thead>
            <tr>
              <th>{t("provider", lang)}</th>
              <th>{t("name", lang)}</th>
              <th>{t("contextWindow", lang)}</th>
              <th>{t("inputPrice", lang)}</th>
              <th>{t("outputPrice", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {modelsQ.loading && models.length === 0 ? (
              <TableSkeleton cols={7} />
            ) : models.length === 0 ? (
              <tr><td colSpan={7}><EmptyState icon="providers" title={t("emptyModelItems", lang)} sub={t("emptyModelItemsSub", lang)} /></td></tr>
            ) : (
              models.map((m) => (
                <tr key={m.id}>
                  <td className="muted mono">{providerName(m.providerId)}</td>
                  <td className="mono">
                    {m.name}{" "}
                    {m.modelType && m.modelType !== "llm" && (
                      <span className="pill info">{t(modelTypeLabelKey[m.modelType] ?? "modelTypeLLM", lang)}</span>
                    )}{" "}
                    {m.isDefault && <span className="pill on">{t("isDefaultModel", lang)}</span>}
                  </td>
                  <td className="mono">{m.contextWindow || "—"}</td>
                  <td className="mono">{m.inputPricePerMillion}</td>
                  <td className="mono">{m.outputPricePerMillion}</td>
                  <td>{m.isEnabled ? <span className="pill on">{t("enabled", lang)}</span> : <span className="pill off">{t("disabled", lang)}</span>}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => startEditModel(m)}>{t("editProvider", lang)}</button>
                      <button className="danger sm" onClick={() => deleteModel(m)}><Icon name="trash" size={13} /> {t("deleteProvider", lang)}</button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* ---------------- Price tables ---------------- */}
      <div className="topbar">
        <div className="titles"><h2 style={{ fontSize: 15, margin: 0 }}>{t("priceTables", lang)}</h2></div>
        <div className="actions">
          <button onClick={() => { setTableForm({ ...emptyTableForm }); setShowTableForm(true); }}>
            <Icon name="plus" size={14} /> {t("addPriceTable", lang)}
          </button>
        </div>
      </div>

      {showTableForm && (
        <form className="card mb-16" onSubmit={submitTable}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={tableForm.name} onChange={(e) => setTableForm({ ...tableForm, name: e.target.value })} required autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("currency", lang)}</div>
              <input value={tableForm.currency} onChange={(e) => setTableForm({ ...tableForm, currency: e.target.value.toUpperCase() })} placeholder="CNY" />
            </label>
            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowTableForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      {tablesQ.loading && tables.length === 0 ? (
        <div className="card"><Skeleton w="100%" h={60} /></div>
      ) : tables.length === 0 ? (
        <div className="card"><EmptyState icon="billing" title={t("emptyPriceTables", lang)} sub={t("emptyPriceTablesSub", lang)} /></div>
      ) : (
        <>
          <div className="row-actions mb-16" style={{ flexWrap: "wrap" }}>
            {tables.map((tbl) => (
              <button
                key={tbl.id}
                className={activeTable?.id === tbl.id ? "sm" : "ghost sm"}
                onClick={() => setActiveTableId(tbl.id)}
              >
                {tbl.name} <span className="faint">({tbl.currency})</span>
              </button>
            ))}
          </div>

          {activeTable && (
            <div className="card mb-16">
              <div className="topbar" style={{ marginBottom: 10 }}>
                <div className="titles"><div className="eyebrow">{activeTable.currency}</div><h3 style={{ margin: 0, fontSize: 14 }}>{activeTable.name}</h3></div>
                <div className="actions flex gap-8">
                  <button className="ghost sm" onClick={() => startEditItem()}><Icon name="plus" size={13} /> {t("addPriceItem", lang)}</button>
                  <button className="danger sm" onClick={() => deleteTable(activeTable)}><Icon name="trash" size={13} /> {t("deletePriceTable", lang)}</button>
                </div>
              </div>

              <label className="field mb-16">
                <div className="field-label">{t("patternTester", lang)}</div>
                <input value={testerPattern} onChange={(e) => runTester(e.target.value)} placeholder="gpt-4o.*" />
                {testerResult && (
                  <div className="sub" style={{ marginTop: 6 }}>
                    {testerResult.isRegex ? t("patternIsRegex", lang) : t("patternIsExact", lang)}:{" "}
                    {testerResult.matched.length > 0 ? testerResult.matched.join(", ") : t("patternNoMatch", lang)}
                  </div>
                )}
              </label>

              {showItemForm && (
                <form className="card mb-16" onSubmit={submitItem}>
                  <div className="form-grid">
                    <label className="field">
                      <div className="field-label">{t("modelPattern", lang)}</div>
                      <input value={itemForm.modelPattern} onChange={(e) => setItemForm({ ...itemForm, modelPattern: e.target.value })} required autoFocus placeholder="gpt-4o.*" />
                    </label>
                    <label className="field">
                      <div className="field-label">{t("inputPrice", lang)}</div>
                      <input type="number" min="0" step="0.01" value={itemForm.inputPricePerMillion} onChange={(e) => setItemForm({ ...itemForm, inputPricePerMillion: Number(e.target.value) || 0 })} />
                    </label>
                    <label className="field">
                      <div className="field-label">{t("outputPrice", lang)}</div>
                      <input type="number" min="0" step="0.01" value={itemForm.outputPricePerMillion} onChange={(e) => setItemForm({ ...itemForm, outputPricePerMillion: Number(e.target.value) || 0 })} />
                    </label>
                    <label className="field">
                      <div className="field-label">{t("cacheReadPrice", lang)}</div>
                      <input type="number" min="0" step="0.01" value={itemForm.cacheReadPerMillion} onChange={(e) => setItemForm({ ...itemForm, cacheReadPerMillion: Number(e.target.value) || 0 })} />
                    </label>
                    <div className="form-actions">
                      <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
                      <button type="button" className="ghost" onClick={() => setShowItemForm(false)}>{t("cancel", lang)}</button>
                    </div>
                  </div>
                </form>
              )}

              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>{t("modelPattern", lang)}</th>
                      <th>{t("inputPrice", lang)}</th>
                      <th>{t("outputPrice", lang)}</th>
                      <th>{t("cacheReadPrice", lang)}</th>
                      <th>{t("actions", lang)}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(activeTable.items ?? []).length === 0 ? (
                      <tr><td colSpan={5}><EmptyState icon="billing" title={t("emptyPriceItems", lang)} /></td></tr>
                    ) : (
                      (activeTable.items ?? []).map((it) => (
                        <tr key={it.id}>
                          <td className="mono">{it.modelPattern}</td>
                          <td className="mono">{it.inputPricePerMillion}</td>
                          <td className="mono">{it.outputPricePerMillion}</td>
                          <td className="mono">{it.cacheReadPerMillion}</td>
                          <td>
                            <div className="row-actions">
                              <button className="ghost sm" onClick={() => startEditItem(it)}>{t("editProvider", lang)}</button>
                              <button className="danger sm" onClick={() => deleteItem(it)}><Icon name="trash" size={13} /> {t("deleteProvider", lang)}</button>
                            </div>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
