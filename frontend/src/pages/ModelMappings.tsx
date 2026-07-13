import { useEffect, useState } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  api,
  useAsync,
  type FallbackChainEntry,
  type ModelItem,
  type ModelMapping,
  type PageResp,
  type Provider,
  type VirtualKey,
} from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, EmptyState, ErrorBanner, Field, FormGrid, Icon, Pill, TableSkeleton, TableWrap, Topbar } from "../components/ui";

const emptyForm = {
  id: 0,
  virtualModel: "",
  realModelId: 0,
  description: "",
  isEnabled: true,
};

// Modality filter for the "真实模型" picker (phase 3, docs/superpowers/specs/
// 2026-07-09-model-mapping-modality-validation-phase3-design.md). Client-side
// only — never sent to the backend, never stored on the mapping itself;
// modality is always derived from the selected real model's own modelType.
// Duplicated from ModelsPricing.tsx's identical 5-entry array rather than
// shared across pages — it's a constant, not logic.
const modelTypeOptions = ["llm", "image", "tts", "asr", "video"] as const;
const modelTypeLabelKey: Record<string, "modelTypeLLM" | "modelTypeImage" | "modelTypeTTS" | "modelTypeASR" | "modelTypeVideo"> = {
  llm: "modelTypeLLM",
  image: "modelTypeImage",
  tts: "modelTypeTTS",
  asr: "modelTypeASR",
  video: "modelTypeVideo",
};

function FallbackRow({
  entry,
  index,
  providers,
  lang,
  onChange,
  onRemove,
}: {
  entry: FallbackChainEntry;
  index: number;
  providers: Provider[];
  lang: Lang;
  onChange: (i: number, entry: FallbackChainEntry) => void;
  onRemove: (i: number) => void;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: index });
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };
  const fallbackModelPlaceholder = t("fallbackModelName", lang);
  return (
    <div ref={setNodeRef} style={style} className="flex gap-8 items-center">
      <Button type="button" variant="ghost" size="sm" {...attributes} {...listeners} style={{ cursor: "grab" }}>
        <Icon name="drag" size={14} />
      </Button>
      <select
        value={entry.providerId || 0}
        onChange={(e) => onChange(index, { ...entry, providerId: Number(e.target.value) })}
        style={{ flex: 1 }}
      >
        <option value={0}>—</option>
        {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
      <input
        value={entry.model}
        onChange={(e) => onChange(index, { ...entry, model: e.target.value })}
        placeholder={fallbackModelPlaceholder}
        style={{ flex: 1 }}
      />
      <Button type="button" variant="danger" size="sm" onClick={() => onRemove(index)}>
        <Icon name="trash" size={13} />
      </Button>
    </div>
  );
}

export default function ModelMappings({ lang }: { lang: Lang }) {
  const [selectedKeyId, setSelectedKeyId] = useState(0);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...emptyForm });
  const [chain, setChain] = useState<FallbackChainEntry[]>([]);
  const [modalityFilter, setModalityFilter] = useState<string>("llm");
  const [batchProviderId, setBatchProviderId] = useState(0);
  const [actionError, setActionError] = useState("");

  const keysQ = useAsync<VirtualKey[]>(
    (s) =>
      api
        .get<PageResp<VirtualKey>>("/ai/gateway/key/list?page=1&pageSize=200", { signal: s })
        .then((r) => r.list ?? r.items ?? []),
    [],
  );
  const keys = keysQ.data ?? [];

  const providersQ = useAsync<Provider[]>((s) => api.get<Provider[]>("/ai/gateway/providers", { signal: s }), []);
  const providers = providersQ.data ?? [];

  const modelsQ = useAsync<ModelItem[]>((s) => api.get<ModelItem[]>("/ai/gateway/model-items", { signal: s }), []);
  const models = modelsQ.data ?? [];
  const providerNameById = new Map(providers.map((p) => [p.id, p.name]));

  useEffect(() => {
    if (!selectedKeyId && keys.length > 0) setSelectedKeyId(keys[0].id);
  }, [keys, selectedKeyId]);

  const mappingsQ = useAsync<ModelMapping[]>(
    (s) =>
      selectedKeyId
        ? api.get<ModelMapping[]>(`/ai/gateway/model-mappings?virtualKeyId=${selectedKeyId}`, { signal: s })
        : Promise.resolve([]),
    [selectedKeyId],
    { skip: !selectedKeyId },
  );
  const mappings = mappingsQ.data ?? [];

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const startEdit = (m?: ModelMapping) => {
    if (m) {
      setForm({
        id: m.id,
        virtualModel: m.virtualModel,
        realModelId: m.realModelId,
        description: m.description ?? "",
        isEnabled: m.isEnabled,
      });
      setChain(m.fallbackChain ?? []);
      const currentType = m.realModel?.modelType ?? models.find((x) => x.id === m.realModelId)?.modelType;
      setModalityFilter(currentType || "llm");
    } else {
      setModalityFilter("llm");
      setForm({ ...emptyForm, realModelId: models.find((x) => (x.modelType || "llm") === "llm")?.id || 0 });
      setChain([]);
    }
    setShowForm(true);
  };

  const changeModalityFilter = (next: string) => {
    setModalityFilter(next);
    const stillMatches = models.some((x) => x.id === form.realModelId && (x.modelType || "llm") === next);
    if (!stillMatches) setForm((f) => ({ ...f, realModelId: 0 }));
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedKeyId || !form.virtualModel.trim() || !form.realModelId) return;
    const body = {
      virtualModel: form.virtualModel.trim(),
      realModelId: form.realModelId,
      description: form.description,
      isEnabled: form.isEnabled,
      fallbackChain: chain.filter((c) => c.providerId && c.model.trim()),
    };
    try {
      if (form.id) {
        await api.put("/ai/gateway/model-mappings", { id: form.id, ...body });
      } else {
        await api.post("/ai/gateway/model-mappings", { virtualKeyId: selectedKeyId, ...body });
      }
      setShowForm(false);
      mappingsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const remove = async (m: ModelMapping) => {
    if (!window.confirm(t("confirmDeleteModelMapping", lang))) return;
    try {
      await api.del(`/ai/gateway/model-mappings?id=${m.id}`);
      mappingsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const toggle = async (m: ModelMapping) => {
    try {
      await api.put("/ai/gateway/model-mappings", { id: m.id, isEnabled: !m.isEnabled });
      mappingsQ.refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const addChainRow = () => setChain((c) => [...c, { providerId: providers[0]?.id || 0, model: "" }]);
  const updateChainRow = (i: number, entry: FallbackChainEntry) =>
    setChain((c) => c.map((x, idx) => (idx === i ? entry : x)));
  const removeChainRow = (i: number) => setChain((c) => c.filter((_, idx) => idx !== i));

  const addAllModelsFromProvider = () => {
    if (!batchProviderId) return;
    const existing = new Set(chain.map((c) => `${c.providerId}:${c.model}`));
    const toAdd = models
      .filter((m) => m.providerId === batchProviderId && (m.modelType || "llm") === modalityFilter)
      .filter((m) => !existing.has(`${batchProviderId}:${m.name}`))
      .map((m) => ({ providerId: batchProviderId, model: m.name }));
    if (toAdd.length === 0) return;
    setChain((c) => [...c, ...toAdd]);
    setBatchProviderId(0);
  };

  const onDragEnd = (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    setChain((c) => arrayMove(c, Number(active.id), Number(over.id)));
  };

  const cols = 4;
  const showError =
    actionError ||
    (keysQ.error ? `${t("loadFailed", lang)}: ${keysQ.error}` : "") ||
    (mappingsQ.error ? `${t("loadFailed", lang)}: ${mappingsQ.error}` : "");

  return (
    <div>
      <Topbar
        eyebrow={t("navManage", lang)}
        title={t("modelMappings", lang)}
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={() => mappingsQ.refresh()}>
              <Icon name="refresh" size={14} /> {t("refresh", lang)}
            </Button>
            <Button onClick={() => startEdit()} disabled={!selectedKeyId}>
              <Icon name="plus" size={14} /> {t("addModelMapping", lang)}
            </Button>
          </>
        }
      />

      {showError && (
        <ErrorBanner
          message={showError}
          onRetry={() => {
            setActionError("");
            mappingsQ.refresh();
          }}
        />
      )}

      <Card className="mb-16">
        <Field label={t("selectVirtualKey", lang)}>
          <select value={selectedKeyId} onChange={(e) => setSelectedKeyId(Number(e.target.value))}>
            {keys.length === 0 && <option value={0}>—</option>}
            {keys.map((k) => <option key={k.id} value={k.id}>{k.name}</option>)}
          </select>
        </Field>
      </Card>

      {showForm && (
        <Card className="mb-16">
          <form onSubmit={submit}>
          <FormGrid>
            <Field label={t("virtualModelName", lang)}>
              <input
                value={form.virtualModel}
                onChange={(e) => setForm({ ...form, virtualModel: e.target.value })}
                required
                autoFocus
                placeholder="gpt-4"
              />
            </Field>
            <Field label={t("modelType", lang)}>
              <select value={modalityFilter} onChange={(e) => changeModalityFilter(e.target.value)}>
                {modelTypeOptions.map((mt) => <option key={mt} value={mt}>{t(modelTypeLabelKey[mt], lang)}</option>)}
              </select>
            </Field>
            <Field label={t("realModel", lang)}>
              <select
                value={form.realModelId}
                onChange={(e) => setForm({ ...form, realModelId: Number(e.target.value) })}
                required
              >
                <option value={0}>—</option>
                {models
                  .filter((m) => (m.modelType || "llm") === modalityFilter)
                  .map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name} ({providerNameById.get(m.providerId) ?? `#${m.providerId}`})
                    </option>
                  ))}
              </select>
            </Field>
            <Field span={2} label={t("description", lang)}>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </Field>
            <Field row label={t("enabled", lang)}>
              <input type="checkbox" checked={form.isEnabled} onChange={(e) => setForm({ ...form, isEnabled: e.target.checked })} />
            </Field>

            <div className="field span-3">
              <div className="field-label">{t("fallbackChain", lang)}</div>
              <div className="sub mb-8">{t("fallbackChainHint", lang)}</div>
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
                <SortableContext items={chain.map((_, i) => i)} strategy={verticalListSortingStrategy}>
                  <div className="flex" style={{ flexDirection: "column", gap: 6 }}>
                    {chain.map((entry, i) => (
                      <FallbackRow
                        key={i}
                        entry={entry}
                        index={i}
                        providers={providers}
                        lang={lang}
                        onChange={updateChainRow}
                        onRemove={removeChainRow}
                      />
                    ))}
                  </div>
                </SortableContext>
              </DndContext>
              <div className="flex gap-8 items-center" style={{ marginTop: 8 }}>
                <Button type="button" variant="ghost" size="sm" onClick={addChainRow}>
                  <Icon name="plus" size={13} /> {t("addFallbackStep", lang)}
                </Button>
                <select
                  value={batchProviderId}
                  onChange={(e) => setBatchProviderId(Number(e.target.value))}
                  style={{ flex: 1 }}
                >
                  <option value={0}>—</option>
                  {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
                <Button type="button" variant="ghost" size="sm" disabled={!batchProviderId} onClick={addAllModelsFromProvider}>
                  <Icon name="plus" size={13} /> {t("addAllModelsFromProvider", lang)}
                </Button>
              </div>
              <div className="sub">{t("addAllModelsFromProviderHint", lang)}</div>
            </div>

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
              <th>{t("virtualModelName", lang)}</th>
              <th>{t("realModel", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {mappingsQ.loading && mappings.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : mappings.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState
                    icon="providers"
                    title={t("emptyModelMappings", lang)}
                    sub={t("emptyModelMappingsSub", lang)}
                    action={
                      selectedKeyId ? (
                        <Button onClick={() => startEdit()}>
                          <Icon name="plus" size={14} /> {t("addModelMapping", lang)}
                        </Button>
                      ) : undefined
                    }
                  />
                </td>
              </tr>
            ) : (
              mappings.map((m) => (
                <tr key={m.id}>
                  <td className="mono">{m.virtualModel}</td>
                  <td className="muted mono">{m.realModel?.name ?? m.realModelId}</td>
                  <td>
                    <Pill tone={m.isEnabled ? "on" : "off"}>
                      {t(m.isEnabled ? "enabled" : "disabled", lang)}
                    </Pill>
                  </td>
                  <td>
                    <div className="row-actions">
                      <Button variant="ghost" size="sm" onClick={() => startEdit(m)}>{t("editProvider", lang)}</Button>
                      <Button variant="ghost" size="sm" onClick={() => toggle(m)}>
                        {t(m.isEnabled ? "disable" : "enable", lang)}
                      </Button>
                      <Button variant="danger" size="sm" onClick={() => remove(m)}>
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
