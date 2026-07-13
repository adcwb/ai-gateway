import { useState } from "react";
import { api, useAsync, type CheckerConfig, type PIIPolicy } from "../api/client";
import { t, type Lang } from "../i18n";
import { Button, Card, EmptyState, ErrorBanner, Field, FormGrid, Icon, Pill, TableSkeleton, TableWrap, Topbar } from "../components/ui";

const DETECTORS = ["cn_id_card", "cn_mobile", "bank_card", "email", "ipv4", "api_secret"] as const;
const CHECKER_KINDS: CheckerConfig["name"][] = ["pii_rules", "prompt_injection", "topic_fence", "external"];

const emptyForm = {
  id: 0,
  name: "",
  enabled: true,
  action: "block" as PIIPolicy["action"],
  isDefault: false,
  description: "",
  failMode: "open" as PIIPolicy["failMode"],
};

function newChecker(name: CheckerConfig["name"]): CheckerConfig {
  switch (name) {
    case "pii_rules":
      return { name, settings: { detectors: Object.fromEntries(DETECTORS.map((d) => [d, true])), promptInjection: false } };
    case "topic_fence":
      return { name, settings: { blockedTopics: [] } };
    case "external":
      return { name, settings: { target: "", timeoutMs: 1000 } };
    default:
      return { name };
  }
}

function CheckerCard({
  checker,
  lang,
  onChange,
  onRemove,
}: {
  checker: CheckerConfig;
  lang: Lang;
  onChange: (c: CheckerConfig) => void;
  onRemove: () => void;
}) {
  const settings = (checker.settings ?? {}) as Record<string, unknown>;

  return (
    <Card style={{ marginBottom: 8 }}>
      <div className="flex items-center gap-8" style={{ justifyContent: "space-between" }}>
        <Pill tone="info">{checker.name}</Pill>
        <Button type="button" variant="danger" size="sm" onClick={onRemove}>
          <Icon name="trash" size={13} /> {t("removeChecker", lang)}
        </Button>
      </div>

      {checker.name === "pii_rules" && (
        <div style={{ marginTop: 8 }}>
          <div className="field-label">{t("detectors", lang)}</div>
          <div className="flex gap-8" style={{ flexWrap: "wrap", marginTop: 4 }}>
            {DETECTORS.map((d) => {
              const detectors = (settings.detectors as Record<string, boolean>) ?? {};
              const on = detectors[d] !== false;
              return (
                <label key={d} className="flex items-center gap-8" style={{ fontSize: 12 }}>
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={(e) =>
                      onChange({ ...checker, settings: { ...settings, detectors: { ...detectors, [d]: e.target.checked } } })
                    }
                  />
                  <span className="mono">{d}</span>
                </label>
              );
            })}
          </div>
          <label className="flex items-center gap-8" style={{ marginTop: 8, fontSize: 12 }}>
            <input
              type="checkbox"
              checked={!!settings.promptInjection}
              onChange={(e) => onChange({ ...checker, settings: { ...settings, promptInjection: e.target.checked } })}
            />
            {t("piiRulesInjectionFlag", lang)}
          </label>
        </div>
      )}

      {checker.name === "prompt_injection" && <div className="sub" style={{ marginTop: 8 }}>{t("promptInjectionHint", lang)}</div>}

      {checker.name === "topic_fence" && (
        <Field label={t("blockedTopicsCsv", lang)} style={{ marginTop: 8 }}>
          <input
            value={((settings.blockedTopics as string[]) ?? []).join(", ")}
            onChange={(e) =>
              onChange({
                ...checker,
                settings: { ...settings, blockedTopics: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) },
              })
            }
            placeholder={t("blockedTopicsHint", lang)}
          />
        </Field>
      )}

      {checker.name === "external" && (
        <FormGrid style={{ marginTop: 8 }}>
          <Field label={t("externalTarget", lang)}>
            <input
              value={(settings.target as string) ?? ""}
              onChange={(e) => onChange({ ...checker, settings: { ...settings, target: e.target.value } })}
              placeholder="127.0.0.1:9090"
            />
          </Field>
          <Field label={t("externalTimeoutMs", lang)}>
            <input
              type="number"
              min="1"
              value={(settings.timeoutMs as number) ?? 1000}
              onChange={(e) => onChange({ ...checker, settings: { ...settings, timeoutMs: Number(e.target.value) || 1000 } })}
            />
          </Field>
        </FormGrid>
      )}
    </Card>
  );
}

export default function GuardrailPolicies({ lang }: { lang: Lang }) {
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...emptyForm });
  const [chain, setChain] = useState<CheckerConfig[]>([]);
  const [addKind, setAddKind] = useState<CheckerConfig["name"]>("pii_rules");
  const [actionError, setActionError] = useState("");

  const { data, loading, error, refresh } = useAsync<PIIPolicy[]>(
    (s) => api.get<PIIPolicy[]>("/ai/gateway/pii-policies", { signal: s }),
    [],
  );
  const policies = data ?? [];

  const startEdit = (p?: PIIPolicy) => {
    if (p) {
      setForm({
        id: p.id,
        name: p.name,
        enabled: p.enabled,
        action: p.action,
        isDefault: p.isDefault,
        description: p.description ?? "",
        failMode: p.failMode || "open",
      });
      setChain(p.checkerChain ?? []);
    } else {
      setForm({ ...emptyForm });
      setChain([]);
    }
    setShowForm(true);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) return;
    const body = {
      name: form.name.trim(),
      enabled: form.enabled,
      action: form.action,
      isDefault: form.isDefault,
      description: form.description,
      failMode: form.failMode,
      checkerChain: chain,
    };
    try {
      if (form.id) {
        await api.put("/ai/gateway/pii-policies", { id: form.id, ...body });
      } else {
        await api.post("/ai/gateway/pii-policies", body);
      }
      setShowForm(false);
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const remove = async (p: PIIPolicy) => {
    if (!window.confirm(t("confirmDeleteGuardrailPolicy", lang))) return;
    try {
      await api.del(`/ai/gateway/pii-policies?id=${p.id}`);
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const toggle = async (p: PIIPolicy) => {
    try {
      await api.put("/ai/gateway/pii-policies", { id: p.id, enabled: !p.enabled });
      refresh();
    } catch (err) {
      setActionError((err as Error).message);
    }
  };

  const addChecker = () => setChain((c) => [...c, newChecker(addKind)]);
  const updateChecker = (i: number, c: CheckerConfig) => setChain((chain) => chain.map((x, idx) => (idx === i ? c : x)));
  const removeChecker = (i: number) => setChain((c) => c.filter((_, idx) => idx !== i));

  const cols = 6;
  const showError = actionError || (error ? `${t("loadFailed", lang)}: ${error}` : "");

  return (
    <div>
      <Topbar
        eyebrow={t("navManage", lang)}
        title={t("guardrailPolicies", lang)}
        actions={
          <>
            <Button variant="ghost" size="sm" onClick={refresh}>
              <Icon name="refresh" size={14} /> {t("refresh", lang)}
            </Button>
            <Button onClick={() => startEdit()}>
              <Icon name="plus" size={14} /> {t("addGuardrailPolicy", lang)}
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
            <Field label={t("guardrailAction", lang)}>
              <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value as PIIPolicy["action"] })}>
                <option value="block">block</option>
                <option value="redact">redact</option>
                <option value="log">log</option>
              </select>
            </Field>
            <Field label={t("failMode", lang)}>
              <select value={form.failMode} onChange={(e) => setForm({ ...form, failMode: e.target.value as PIIPolicy["failMode"] })}>
                <option value="open">open</option>
                <option value="closed">closed</option>
              </select>
            </Field>
            <Field span={2} label={t("description", lang)}>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </Field>
            <Field row label={t("enabled", lang)}>
              <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
            </Field>
            <Field row label={t("isDefaultPolicy", lang)}>
              <input type="checkbox" checked={form.isDefault} onChange={(e) => setForm({ ...form, isDefault: e.target.checked })} />
            </Field>

            <div className="field span-3">
              <div className="field-label">{t("checkerChain", lang)}</div>
              <div className="sub mb-8">{t("checkerChainHint", lang)}</div>
              <div className="sub mb-8">{t("streamingGuardrailNotice", lang)}</div>
              {chain.map((c, i) => (
                <CheckerCard key={i} checker={c} lang={lang} onChange={(nc) => updateChecker(i, nc)} onRemove={() => removeChecker(i)} />
              ))}
              <div className="flex gap-8 items-center">
                <select value={addKind} onChange={(e) => setAddKind(e.target.value as CheckerConfig["name"])}>
                  {CHECKER_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
                </select>
                <Button type="button" variant="ghost" size="sm" onClick={addChecker}>
                  <Icon name="plus" size={13} /> {t("addChecker", lang)}
                </Button>
              </div>
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
              <th>{t("name", lang)}</th>
              <th>{t("guardrailAction", lang)}</th>
              <th>{t("status", lang)}</th>
              <th>{t("isDefaultPolicy", lang)}</th>
              <th>{t("boundKeyCount", lang)}</th>
              <th>{t("actions", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {loading && policies.length === 0 ? (
              <TableSkeleton cols={cols} />
            ) : policies.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState
                    icon="alert"
                    title={t("emptyGuardrailPolicies", lang)}
                    sub={t("emptyGuardrailPoliciesSub", lang)}
                    action={
                      <Button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addGuardrailPolicy", lang)}
                      </Button>
                    }
                  />
                </td>
              </tr>
            ) : (
              policies.map((p) => (
                <tr key={p.id}>
                  <td>{p.name}</td>
                  <td className="mono">{p.action}</td>
                  <td>
                    <Pill tone={p.enabled ? "on" : "off"}>{t(p.enabled ? "enabled" : "disabled", lang)}</Pill>
                  </td>
                  <td>{p.isDefault ? <Pill tone="info">{t("isDefaultPolicy", lang)}</Pill> : "—"}</td>
                  <td className="num mono">{p.boundKeyCount}</td>
                  <td>
                    <div className="row-actions">
                      <Button variant="ghost" size="sm" onClick={() => startEdit(p)}>{t("editProvider", lang)}</Button>
                      <Button variant="ghost" size="sm" onClick={() => toggle(p)}>{t(p.enabled ? "disable" : "enable", lang)}</Button>
                      <Button variant="danger" size="sm" onClick={() => remove(p)}>
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
