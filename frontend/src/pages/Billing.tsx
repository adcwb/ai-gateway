import { useEffect, useState } from "react";
import { api, credits, type BillingAccount, type LedgerEntry, type PageResp, type Tenant } from "../api/client";
import { t, type Lang } from "../i18n";

export default function Billing({ lang }: { lang: Lang }) {
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [tenantId, setTenantId] = useState(0);
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [amount, setAmount] = useState("");
  const [error, setError] = useState("");

  const current = tenants.find((x) => x.id === tenantId);
  const acct: BillingAccount | null | undefined = current?.account;

  const load = async (tid?: number) => {
    setError("");
    try {
      const ts = await api.get<Tenant[]>("/ai/gateway/tenants");
      setTenants(ts ?? []);
      const effective = tid || tenantId || ts?.[0]?.id || 0;
      if (effective && effective !== tenantId) setTenantId(effective);
      if (effective) {
        const lg = await api.get<PageResp<LedgerEntry>>(`/ai/gateway/billing/ledger?tenantId=${effective}&pageSize=50`);
        setLedger(lg.list ?? []);
      }
    } catch (e) {
      setError(`${t("loadFailed", lang)}: ${(e as Error).message}`);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (tenantId) load(tenantId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tenantId]);

  const toggleBilling = async () => {
    if (!acct) return;
    try {
      await api.put("/ai/gateway/billing/account", { tenantId, isEnabled: !acct.isEnabled });
      load(tenantId);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const recharge = async () => {
    const v = parseFloat(amount);
    if (!v || v <= 0) return;
    try {
      await api.post("/ai/gateway/billing/recharge", { tenantId, credits: v, remark: "console recharge" });
      setAmount("");
      load(tenantId);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <div>
      <div className="toolbar">
        <h1>{t("billing", lang)}</h1>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <span className="muted">{t("selectTenant", lang)}</span>
          <select
            value={tenantId}
            onChange={(e) => setTenantId(Number(e.target.value))}
            style={{ background: "#0d0f15", color: "inherit", border: "1px solid var(--border)", borderRadius: 8, padding: "6px 8px" }}
          >
            {tenants.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
          </select>
          <button className="ghost" onClick={() => load(tenantId)}>{t("refresh", lang)}</button>
        </div>
      </div>
      {error && <p className="error-text">{error}</p>}

      <div className="cards">
        <div className="card">
          <div className="label">{t("balance", lang)}</div>
          <div className="value">{acct ? credits(acct.balanceMicro) : "—"}</div>
          <div className="muted">{acct?.currency ?? ""} · {acct ? t(`status_${acct.status}`, lang) : ""}</div>
        </div>
        <div className="card">
          <div className="label">{t("billingMode", lang)}</div>
          <div className="value" style={{ fontSize: 18 }}>{acct?.mode ?? "—"}</div>
          <button className="ghost" style={{ marginTop: 8 }} onClick={toggleBilling}>
            {acct?.isEnabled ? t("disableBilling", lang) : t("enableBilling", lang)}
          </button>
        </div>
        <div className="card" style={{ minWidth: 260 }}>
          <div className="label">{t("recharge", lang)}</div>
          <div style={{ display: "flex", gap: 8, marginTop: 6 }}>
            <input
              type="number"
              min="0"
              step="0.01"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder={t("rechargeAmount", lang)}
            />
            <button onClick={recharge}>{t("submit", lang)}</button>
          </div>
        </div>
      </div>

      <h1 style={{ fontSize: 16 }}>{t("ledger", lang)}</h1>
      <table>
        <thead>
          <tr>
            <th>{t("time", lang)}</th>
            <th>{t("entryType", lang)}</th>
            <th>{t("amount", lang)}</th>
            <th>{t("balanceAfter", lang)}</th>
            <th>{t("remark", lang)}</th>
          </tr>
        </thead>
        <tbody>
          {ledger.length === 0 && <tr><td colSpan={5} className="muted">{t("empty", lang)}</td></tr>}
          {ledger.map((e) => (
            <tr key={e.id}>
              <td className="muted">{new Date(e.createdAt).toLocaleString()}</td>
              <td>{e.entryType}</td>
              <td style={{ color: e.amountMicro >= 0 ? "var(--ok)" : "var(--err)" }}>
                {e.amountMicro >= 0 ? "+" : ""}{credits(e.amountMicro)}
              </td>
              <td>{credits(e.balanceAfterMicro)}</td>
              <td className="muted">{e.remark || e.refId}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
