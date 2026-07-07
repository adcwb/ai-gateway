import type { Page } from "@playwright/test";
import http from "node:http";
import type { AddressInfo } from "node:net";

export const ADMIN_TOKEN = process.env.E2E_ADMIN_TOKEN || "e2e-admin-token";

/** Logs into the console with the E2E admin token. */
export async function login(page: Page) {
  await page.goto("/");
  await page.getByRole("textbox").first().fill(ADMIN_TOKEN);
  await page.getByRole("button", { name: /登录|Sign in/ }).click();
  await page.waitForURL(/\/console\/?$/);
}

/**
 * Starts a minimal OpenAI-compatible mock upstream so tests exercise a real
 * 200 response (audit tokens, dashboard counters) without depending on the
 * network or a real provider key. Returns the base URL and a close() to tear
 * it down; the caller registers close() in an `after` hook.
 */
export function startMockUpstream(): Promise<{ baseURL: string; close: () => Promise<void> }> {
  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          id: "chatcmpl-e2e",
          object: "chat.completion",
          model: "mock-model",
          choices: [{ index: 0, message: { role: "assistant", content: "pong" }, finish_reason: "stop" }],
          usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 },
        }),
      );
    });
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address() as AddressInfo;
      resolve({
        baseURL: `http://127.0.0.1:${addr.port}`,
        close: () => new Promise((r) => server.close(() => r())),
      });
    });
  });
}
