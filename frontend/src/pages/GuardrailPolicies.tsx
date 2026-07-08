import { useState } from "react";
import { api, useAsync, type CheckerConfig, type PIIPolicy } from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, TableSkeleton } from "../components/ui";

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
    <div className="card" style={{ marginBottom: 8 }}>
      <div className="flex items-center gap-8" style={{ justifyContent: "space-between" }}>
        <span className="pill info">{checker.name}</span>
        <button type="button" className="danger sm" onClick={onRemove}>
          <Icon name="trash" size={13} /> {t("removeChecker", lang)}
        </button>
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
        <label className="field" style={{ marginTop: 8 }}>
          <div className="field-label">{t("blockedTopicsCsv", lang)}</div>
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
        </label>
      )}

      {checker.name === "external" && (
        <div className="form-grid" style={{ marginTop: 8 }}>
          <label className="field">
            <div className="field-label">{t("externalTarget", lang)}</div>
            <input
              value={(settings.target as string) ?? ""}
              onChange={(e) => onChange({ ...checker, settings: { ...settings, target: e.target.value } })}
              placeholder="127.0.0.1:9090"
            />
          </label>
          <label className="field">
            <div className="field-label">{t("externalTimeoutMs", lang)}</div>
            <input
              type="number"
              min="1"
              value={(settings.timeoutMs as number) ?? 1000}
              onChange={(e) => onChange({ ...checker, settings: { ...settings, timeoutMs: Number(e.target.value) || 1000 } })}
            />
          </label>
        </div>
      )}
    </div>
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
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("guardrailPolicies", lang)}</h1>
        </div>
        <div className="actions flex gap-8">
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
          </button>
          <button onClick={() => startEdit()}>
            <Icon name="plus" size={14} /> {t("addGuardrailPolicy", lang)}
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

      {showForm && (
        <form className="card mb-16" onSubmit={submit}>
          <div className="form-grid">
            <label className="field">
              <div className="field-label">{t("name", lang)}</div>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required autoFocus />
            </label>
            <label className="field">
              <div className="field-label">{t("guardrailAction", lang)}</div>
              <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value as PIIPolicy["action"] })}>
                <option value="block">block</option>
                <option value="redact">redact</option>
                <option value="log">log</option>
              </select>
            </label>
            <label className="field">
              <div className="field-label">{t("failMode", lang)}</div>
              <select value={form.failMode} onChange={(e) => setForm({ ...form, failMode: e.target.value as PIIPolicy["failMode"] })}>
                <option value="open">open</option>
                <option value="closed">closed</option>
              </select>
            </label>
            <label className="field span-2">
              <div className="field-label">{t("description", lang)}</div>
              <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </label>
            <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
              <div className="field-label" style={{ margin: 0 }}>{t("enabled", lang)}</div>
            </label>
            <label className="field" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <input type="checkbox" checked={form.isDefault} onChange={(e) => setForm({ ...form, isDefault: e.target.checked })} />
              <div className="field-label" style={{ margin: 0 }}>{t("isDefaultPolicy", lang)}</div>
            </label>

            <div className="field span-3">
              <div className="field-label">{t("checkerChain", lang)}</div>
              <div className="sub mb-8">{t("checkerChainHint", lang)}</div>
              {chain.map((c, i) => (
                <CheckerCard key={i} checker={c} lang={lang} onChange={(nc) => updateChecker(i, nc)} onRemove={() => removeChecker(i)} />
              ))}
              <div className="flex gap-8 items-center">
                <select value={addKind} onChange={(e) => setAddKind(e.target.value as CheckerConfig["name"])}>
                  {CHECKER_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
                </select>
                <button type="button" className="ghost sm" onClick={addChecker}>
                  <Icon name="plus" size={13} /> {t("addChecker", lang)}
                </button>
              </div>
            </div>

            <div className="form-actions">
              <button type="submit"><Icon name="check" size={14} /> {t("save", lang)}</button>
              <button type="button" className="ghost" onClick={() => setShowForm(false)}>{t("cancel", lang)}</button>
            </div>
          </div>
        </form>
      )}

      <div className="table-wrap">
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
                      <button onClick={() => startEdit()}>
                        <Icon name="plus" size={14} /> {t("addGuardrailPolicy", lang)}
                      </button>
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
                    <span className={`pill ${p.enabled ? "on" : "off"}`}>{t(p.enabled ? "enabled" : "disabled", lang)}</span>
                  </td>
                  <td>{p.isDefault ? <span className="pill info">{t("isDefaultPolicy", lang)}</span> : "—"}</td>
                  <td className="num mono">{p.boundKeyCount}</td>
                  <td>
                    <div className="row-actions">
                      <button className="ghost sm" onClick={() => startEdit(p)}>{t("editProvider", lang)}</button>
                      <button className="ghost sm" onClick={() => toggle(p)}>{t(p.enabled ? "disable" : "enable", lang)}</button>
                      <button className="danger sm" onClick={() => remove(p)}>
                        <Icon name="trash" size={13} /> {t("deleteProvider", lang)}
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
