import { useEffect, useState } from "react";
import {
  api,
  credits,
  useAsync,
  type BillingAccount,
  type LedgerEntry,
  type PageResp,
  type Tenant,
} from "../api/client";
import { t, type Lang } from "../i18n";
import { EmptyState, ErrorBanner, Icon, Skeleton, TableSkeleton } from "../components/ui";

export default function Billing({ lang }: { lang: Lang }) {
  const [tenantId, setTenantId] = useState(0);
  const [amount, setAmount] = useState("");
  const [actionError, setActionError] = useState("");

  // Tenants drive the picker + balance card and load independently of the
  // ledger. A ledger 404 (e.g. BILLING_ACCOUNT_NOT_FOUND for a tenant created
  // outside the console) must never blank the selector or the balance.
  const tenantsQ = useAsync<Tenant[]>(
    (s) => api.get<Tenant[]>("/ai/gateway/tenants", { signal: s }),
    [],
  );
  const tenants = tenantsQ.data ?? [];

  // Seed the picker with the first tenant once the list arrives.
  useEffect(() => {
    if (tenantId === 0 && tenants[0]) setTenantId(tenants[0].id);
  }, [tenants, tenantId]);

  // Ledger is keyed on the chosen tenant and skipped until one is picked, so
  // we never fire a request with tenantId=0.
  const ledgerQ = useAsync<LedgerEntry[]>(
    (s) =>
      api
        .get<PageResp<LedgerEntry>>(
          `/ai/gateway/billing/ledger?tenantId=${tenantId}&pageSize=50`,
          { signal: s },
        )
        .then((r) => r.list ?? []),
    [tenantId],
    { skip: tenantId === 0 },
  );
  const ledger = ledgerQ.data ?? [];

  const refresh = () => {
    tenantsQ.refresh();
    ledgerQ.refresh();
  };

  const current = tenants.find((x) => x.id === tenantId);
  const acct: BillingAccount | null | undefined = current?.account;

  const toggleBilling = async () => {
    if (!acct) return;
    try {
      await api.put("/ai/gateway/billing/account", { tenantId, isEnabled: !acct.isEnabled });
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const recharge = async () => {
    const v = parseFloat(amount);
    if (!v || v <= 0) return;
    try {
      await api.post("/ai/gateway/billing/recharge", { tenantId, credits: v, remark: "console recharge" });
      setAmount("");
      refresh();
    } catch (e) {
      setActionError((e as Error).message);
    }
  };

  const cols = 5;
  // Top-level error banner is only for tenants (page-wide) or action errors.
  // Ledger failures are localized to the ledger table below.
  const showError = actionError || (tenantsQ.error ? `${t("loadFailed", lang)}: ${tenantsQ.error}` : "");

  // Ledger table states. While no tenant is picked, or the first ledger fetch
  // is in flight, show skeletons. Once we have a result (data or error) we
  // branch into the localized error / no-account / empty / rows paths.
  const ledgerPending = tenantId === 0 || (ledgerQ.loading && ledgerQ.data == null && !ledgerQ.error);
  const noAccount = ledgerQ.errorCode === "BILLING_ACCOUNT_NOT_FOUND";

  return (
    <div>
      <div className="topbar">
        <div className="titles">
          <div className="eyebrow">{t("navManage", lang)}</div>
          <h1>{t("billing", lang)}</h1>
        </div>
        <div className="actions flex gap-8 items-center">
          <select
            value={tenantId}
            onChange={(e) => setTenantId(Number(e.target.value))}
            style={{ width: "auto" }}
            aria-label={t("selectTenant", lang)}
          >
            {tenants.map((x) => (
              <option key={x.id} value={x.id}>{x.name}</option>
            ))}
          </select>
          <button className="ghost sm" onClick={refresh}>
            <Icon name="refresh" size={14} /> {t("refresh", lang)}
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

      <div className="cards">
        <div className="card stat">
          <div className="label">{t("balance", lang)}</div>
          {tenantsQ.loading && !acct ? <Skeleton w={90} h={26} /> : <div className="value">{acct ? credits(acct.balanceMicro) : "—"}</div>}
          <div className="sub">{acct ? `${acct.currency} · ${t(`status_${acct.status}`, lang)}` : ""}</div>
        </div>
        <div className="card stat">
          <div className="label">{t("billingMode", lang)}</div>
          <div className="value" style={{ fontSize: 18 }}>{acct?.mode ?? "—"}</div>
          <button className="ghost sm" style={{ marginTop: 10 }} onClick={toggleBilling}>
            {acct?.isEnabled ? t("disableBilling", lang) : t("enableBilling", lang)}
          </button>
        </div>
        <div className="card" style={{ minWidth: 280 }}>
          <div className="label">{t("recharge", lang)}</div>
          <div className="flex gap-8" style={{ marginTop: 6 }}>
            <input
              type="number"
              min="0"
              step="0.01"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder={t("rechargeAmount", lang)}
              onKeyDown={(e) => e.key === "Enter" && recharge()}
            />
            <button onClick={recharge}>
              <Icon name="plus" size={14} /> {t("submit", lang)}
            </button>
          </div>
        </div>
      </div>

      <h1 className="section-title">{t("ledger", lang)}</h1>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("time", lang)}</th>
              <th>{t("entryType", lang)}</th>
              <th className="num">{t("amount", lang)}</th>
              <th className="num">{t("balanceAfter", lang)}</th>
              <th>{t("remark", lang)}</th>
            </tr>
          </thead>
          <tbody>
            {ledgerPending ? (
              <TableSkeleton cols={cols} />
            ) : ledgerQ.error ? (
              noAccount ? (
                <tr>
                  <td colSpan={cols}>
                    <EmptyState
                      icon="billing"
                      title={t("noBillingAccount", lang)}
                      sub={t("noBillingAccountSub", lang)}
                      action={
                        <button className="ghost sm" onClick={ledgerQ.refresh}>
                          <Icon name="refresh" size={13} /> {t("retry", lang)}
                        </button>
                      }
                    />
                  </td>
                </tr>
              ) : (
                <tr>
                  <td colSpan={cols}>
                    <div className="table-error">
                      <Icon name="alert" size={16} />
                      <span>{t("loadFailed", lang)}: {ledgerQ.error}</span>
                      <button className="ghost sm" onClick={ledgerQ.refresh}>
                        <Icon name="refresh" size={13} /> {t("retry", lang)}
                      </button>
                    </div>
                  </td>
                </tr>
              )
            ) : ledger.length === 0 ? (
              <tr>
                <td colSpan={cols}>
                  <EmptyState icon="billing" title={t("emptyLedger", lang)} sub={t("emptyLedgerSub", lang)} />
                </td>
              </tr>
            ) : (
              ledger.map((e) => (
                <tr key={e.id}>
                  <td className="muted mono">{new Date(e.createdAt).toLocaleString()}</td>
                  <td className="mono">{e.entryType}</td>
                  <td className="num mono" style={{ color: e.amountMicro >= 0 ? "var(--ok)" : "var(--err)" }}>
                    {e.amountMicro >= 0 ? "+" : ""}{credits(e.amountMicro)}
                  </td>
                  <td className="num mono">{credits(e.balanceAfterMicro)}</td>
                  <td className="muted">{e.remark || e.refId}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
