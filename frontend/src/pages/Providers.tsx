import { useState } from "react";
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
import { api, useAsync, type Provider, type ProviderHealth } from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, EmptyState, ErrorBanner, Field, FormGrid, Icon, Modal, TableSkeleton, TableWrap, Topbar } from "../components/ui";

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
  const [showSort, setShowSort] = useState(false);
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
      <Topbar
        eyebrow={t("navOperate", lang)}
        title={t("providers", lang)}
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={refresh}>
              <Icon name="refresh" size={14} /> {t("refresh", lang)}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setShowSort(true)} disabled={providers.length < 2}>
              <Icon name="drag" size={14} /> {t("reorderPriority", lang)}
            </Button>
            <Button onClick={() => startEdit()}>
              <Icon name="plus" size={14} /> {t("addProvider", lang)}
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
            <Field label={t("baseUrl", lang)}>
              <input
                value={form.baseUrl}
                onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
                required
                placeholder="https://api.openai.com/v1"
              />
            </Field>
            <Field label={t("providerType", lang)}>
              <select value={form.providerType} onChange={(e) => setForm({ ...form, providerType: e.target.value })}>
                <option value="openai_compatible">openai_compatible</option>
                <option value="anthropic">anthropic</option>
                <option value="azure_openai">azure_openai</option>
                <option value="gemini">gemini</option>
              </select>
            </Field>
            <Field label={t("apiKeyWriteOnly", lang)}>
              <input
                type="password"
                value={form.apiKey}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                required={!form.id}
                placeholder={form.id ? "••••••  (leave blank to keep)" : ""}
              />
            </Field>
            <Field label={t("weight", lang)}>
              <input type="number" min="0" value={form.weight} onChange={(e) => setForm({ ...form, weight: Number(e.target.value) || 0 })} />
            </Field>
            <Field label={t("priority", lang)}>
              <input type="number" min="0" value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) || 0 })} />
            </Field>
            <Field span={3} label={t("modelsCsv", lang)}>
              <input value={form.modelsCsv} onChange={(e) => setForm({ ...form, modelsCsv: e.target.value })} placeholder="gpt-4o-mini, gpt-4o" />
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
                      <Button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addProvider", lang)}
                      </Button>
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
                        <Button variant="ghost" size="sm" onClick={() => startEdit(p)}>{t("editProvider", lang)}</Button>
                        <Button variant="ghost" size="sm" onClick={() => syncModels(p)}>
                          <Icon name="sync" size={13} /> {t("syncModels", lang)}
                        </Button>
                        <Button variant="danger" size="sm" onClick={() => remove(p)}>
                          <Icon name="trash" size={13} /> {t("deleteProvider", lang)}
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </TableWrap>

      {showSort && (
        <ReorderPriorityModal
          providers={providers}
          lang={lang}
          onClose={() => setShowSort(false)}
          onSaved={() => {
            setShowSort(false);
            refresh();
          }}
        />
      )}
    </div>
  );
}

function PriorityRow({ provider, rank }: { provider: Provider; rank: number }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: provider.id });
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };
  return (
    <div ref={setNodeRef} style={style} className="flex gap-8 items-center">
      <Button type="button" variant="ghost" size="sm" {...attributes} {...listeners} style={{ cursor: "grab" }}>
        <Icon name="drag" size={14} />
      </Button>
      <span className="mono faint" style={{ width: 24 }}>#{rank + 1}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div>{provider.name}</div>
        <div className="muted mono truncate">{provider.baseUrl}</div>
      </div>
      <span className="faint mono">W{provider.weight}</span>
    </div>
  );
}

function ReorderPriorityModal({
  providers,
  lang,
  onClose,
  onSaved,
}: {
  providers: Provider[];
  lang: Lang;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [order, setOrder] = useState<Provider[]>(() =>
    [...providers].sort((a, b) => a.priority - b.priority || a.name.localeCompare(b.name)),
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const onDragEnd = (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    setOrder((list) => {
      const from = list.findIndex((p) => p.id === active.id);
      const to = list.findIndex((p) => p.id === over.id);
      return from < 0 || to < 0 ? list : arrayMove(list, from, to);
    });
  };

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      // Gap-based ranks (0, 10, 20, ...) leave room for later manual fine-tuning;
      // only providers whose priority actually changed are written.
      const updates = order
        .map((p, i) => ({ id: p.id, priority: i * 10, changed: p.priority !== i * 10 }))
        .filter((u) => u.changed);
      await Promise.all(updates.map((u) => api.put("/ai/gateway/providers", { id: u.id, priority: u.priority })));
      onSaved();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title={t("reorderPriority", lang)} onClose={onClose} closeLabel={t("close", lang)} width={520}>
      <p className="sub mb-8">{t("reorderPriorityHint", lang)}</p>
      {error && <ErrorBanner message={error} onRetry={() => setError("")} />}
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
        <SortableContext items={order.map((p) => p.id)} strategy={verticalListSortingStrategy}>
          <div className="flex" style={{ flexDirection: "column", gap: 6 }}>
            {order.map((p, i) => (
              <PriorityRow key={p.id} provider={p} rank={i} />
            ))}
          </div>
        </SortableContext>
      </DndContext>
      <div className="form-actions" style={{ marginTop: 16 }}>
        <Button onClick={save} disabled={saving}>
          <Icon name="check" size={14} /> {t("save", lang)}
        </Button>
        <Button variant="ghost" onClick={onClose}>{t("cancel", lang)}</Button>
      </div>
    </Modal>
  );
}
