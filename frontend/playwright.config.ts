import { defineConfig, devices } from "@playwright/test";

// E2E against the compose-equivalent local stack (docs/design/08-web-console.md
// "Testing & verification"): both the frontend dev server and a disposable
// SQLite-backed backend instance are launched by Playwright itself, so
// `npm run test:e2e` is a single self-contained command — no Docker, no
// external services, matching the backend's offline `go test ./...` ethos.
const FRONTEND_PORT = process.env.E2E_FRONTEND_PORT || "5173";
const BACKEND_PORT = process.env.E2E_BACKEND_PORT || "8080";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  timeout: 30_000,
  use: {
    baseURL: `http://localhost:${FRONTEND_PORT}/console/`,
    trace: "retain-on-failure",
  },
  webServer: [
    {
      command: "node e2e/start-backend.mjs",
      url: `http://127.0.0.1:${BACKEND_PORT}/healthz`,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command: `npm run dev -- --port ${FRONTEND_PORT} --strictPort`,
      url: `http://localhost:${FRONTEND_PORT}/console/`,
      reuseExistingServer: !process.env.CI,
      timeout: 30_000,
    },
  ],
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
