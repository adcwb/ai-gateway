import { expect, test } from "@playwright/test";
import { ADMIN_TOKEN, login, startMockUpstream } from "./helpers";

// P1 reseller exit flow (docs/03-roadmap.md): recharge balance → issue key →
// consume → hit zero → requests rejected with a billing error → recharge →
// traffic resumes.
//
// Setup (grace_hours=0, a credits rate, and a deliberately huge per-token
// cost so one request reliably blows past the recharged balance) is done via
// the management API directly rather than the UI — the UI doesn't expose
// grace_hours or model cost pricing granular enough to make depletion
// deterministic in a single request, and this is a standard "arrange via
// API, act + assert via UI" E2E pattern.
test("recharge, consume to suspension, recharge, resume", async ({ page, request }) => {
  const mock = await startMockUpstream();
  const authHeaders = { Authorization: `Bearer ${ADMIN_TOKEN}`, "Content-Type": "application/json" };

  try {
    // ---- Arrange via API -------------------------------------------------------
    await request.post("/ai/gateway/credits-rates", {
      headers: authHeaders,
      data: { currency: "CNY", ratePerCredit: 1 },
    });

    const providerResp = await request.post("/ai/gateway/providers", {
      headers: authHeaders,
      data: {
        name: "e2e-reseller-provider",
        baseUrl: mock.baseURL,
        providerType: "openai_compatible",
        apiKey: "sk-e2e-test",
        models: [{ name: "mock-model", is_default: true }],
      },
    });
    const provider = await providerResp.json();

    // Huge per-token cost: 1000 CNY/M tokens means the 5+2 mock-usage tokens
    // alone cost ~7 CNY — comfortably more than the 1-credit recharge below.
    await request.post("/ai/gateway/model-items", {
      headers: authHeaders,
      data: { providerId: provider.data.id, name: "mock-model", inputPricePerMillion: 1_000_000, outputPricePerMillion: 1_000_000 },
    });

    const tenantResp = await request.post("/ai/gateway/tenants", {
      headers: authHeaders,
      data: { name: "e2e-reseller-tenant", displayName: "E2E Reseller" },
    });
    const tenant = await tenantResp.json();

    await request.put("/ai/gateway/billing/account", {
      headers: authHeaders,
      data: { tenantId: tenant.data.id, isEnabled: true, graceHours: 0 },
    });
    await request.post("/ai/gateway/billing/recharge", {
      headers: authHeaders,
      data: { tenantId: tenant.data.id, credits: 1, remark: "e2e seed" },
    });

    const keyResp = await request.post("/ai/gateway/key", {
      headers: authHeaders,
      data: { name: "e2e-reseller-key", providerId: provider.data.id, tenantId: tenant.data.id },
    });
    const plainKey = (await keyResp.json()).data.plainKey as string;

    const proxyHeaders = { Authorization: `Bearer ${plainKey}`, "Content-Type": "application/json" };
    const proxyBody = { model: "mock-model", messages: [{ role: "user", content: "hi" }] };

    // ---- Act + assert via UI/API -----------------------------------------------
    // Request 1: balance is positive (1 credit) — succeeds, and its cost blows
    // straight past zero (recharged 1 credit vs. ~7 credits of cost).
    const first = await request.post("/ai/v1/chat/completions", { headers: proxyHeaders, data: proxyBody });
    expect(first.ok()).toBeTruthy();

    // Request 2: balance is now negative and grace_hours=0, so the account is
    // already suspended — the gate rejects before any upstream call.
    const second = await request.post("/ai/v1/chat/completions", { headers: proxyHeaders, data: proxyBody });
    expect(second.status()).toBe(402);

    await login(page);
    await page.getByRole("link", { name: /Billing|计费中心/ }).click();
    await page.getByLabel(/Tenant|租户/).selectOption({ label: "e2e-reseller-tenant" });
    await expect(page.getByText(/suspended|已停用/)).toBeVisible();

    // Recharge back above zero via the UI — status returns to active immediately.
    // Status renders inline as "CNY · active" (Billing.tsx), so match the
    // substring rather than anchoring to the whole text node.
    await page.getByPlaceholder(/Amount|金额/).fill("100");
    await page.getByRole("button", { name: /Submit|提交/ }).click();
    await expect(page.getByText(/active|正常/).first()).toBeVisible();
    await expect(page.getByText(/suspended|已停用/)).toHaveCount(0);

    // Request 3: resumes successfully.
    const third = await request.post("/ai/v1/chat/completions", { headers: proxyHeaders, data: proxyBody });
    expect(third.ok()).toBeTruthy();
  } finally {
    await mock.close();
  }
});
