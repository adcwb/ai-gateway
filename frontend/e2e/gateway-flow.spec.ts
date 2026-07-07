import { expect, test } from "@playwright/test";
import { login, startMockUpstream } from "./helpers";

// P1 exit flow (docs/03-roadmap.md, docs/design/08-web-console.md): login →
// create a provider → create a virtual key → send a proxied request →
// see it in Audit → dashboard counters reflect it.
test("login, create provider + key, proxy a request, see it in audit and dashboard", async ({ page, request }) => {
  const mock = await startMockUpstream();
  try {
    await login(page);

    // ---- Providers: register the mock upstream ------------------------------
    // Scoped to .topbar: the empty-state action button has the same label.
    await page.getByRole("link", { name: /Providers|提供方/ }).click();
    await page.locator(".topbar").getByRole("button", { name: /Add provider|添加提供方/ }).click();
    await page.getByLabel(/Name|名称/).fill("e2e-provider");
    await page.getByPlaceholder("https://api.openai.com/v1").fill(mock.baseURL);
    await page.getByLabel(/API key|API Key/).fill("sk-e2e-test");
    await page.getByPlaceholder("gpt-4o-mini, gpt-4o").fill("mock-model");
    await page.getByRole("button", { name: /^Save$|^保存$/ }).click();
    await expect(page.getByRole("cell", { name: "e2e-provider" })).toBeVisible();

    // ---- Keys: mint a virtual key bound to that provider ---------------------
    await page.getByRole("link", { name: /Virtual Keys|虚拟 Key/ }).click();
    await page.locator(".topbar").getByRole("button", { name: /Create key|创建 Key/ }).click();
    await page.getByLabel(/^Name$|^名称$/).fill("e2e-key");
    await page.getByRole("button", { name: /Submit|提交/ }).click();

    const plainKeyBlock = page.locator("code.code-block");
    await expect(plainKeyBlock).toBeVisible();
    const plainKey = (await plainKeyBlock.textContent())?.trim();
    expect(plainKey).toMatch(/^sk-vk-/);

    // ---- Proxy a real chat completion through the gateway --------------------
    const proxyResp = await request.post("/ai/v1/chat/completions", {
      headers: { Authorization: `Bearer ${plainKey}`, "Content-Type": "application/json" },
      data: { model: "mock-model", messages: [{ role: "user", content: "hello from e2e" }] },
    });
    expect(proxyResp.ok()).toBeTruthy();
    const body = await proxyResp.json();
    expect(body.choices[0].message.content).toBe("pong");

    // ---- Audit: the request shows up ------------------------------------------
    // The audit writer batches asynchronously (auditBatchTimeout=200ms in
    // biz/audit.go) — poll by re-clicking Refresh rather than a single check.
    await page.getByRole("link", { name: /Audit|审计中心/ }).click();
    const modelCell = page.getByRole("cell", { name: "mock-model" }).first();
    await expect(async () => {
      await page.getByRole("button", { name: /Refresh|刷新/ }).click();
      await expect(modelCell).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 10_000 });
    await expect(page.locator("td").filter({ hasText: /^200$/ }).first()).toBeVisible();

    // ---- Dashboard: key count reflects the key just created -------------------
    await page.getByRole("link", { name: /Dashboard|仪表盘/ }).click();
    await expect(page.getByText("1").first()).toBeVisible();
  } finally {
    await mock.close();
  }
});
