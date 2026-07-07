#!/usr/bin/env node
// Builds and runs the ai-gateway server against a fresh, disposable SQLite DB
// for Playwright E2E (docs/design/08-web-console.md testing section). Started
// via playwright.config.ts's `webServer`, which polls E2E_BACKEND_URL/healthz
// until ready and tears this process down after the run.
//
// Self-contained on purpose: no Redis/MySQL required, so `npm run test:e2e`
// works on a bare checkout exactly like `go test ./...` does for the backend.
import { spawnSync, spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const backendDir = path.resolve(import.meta.dirname, "..", "..", "backend");
const workDir = mkdtempSync(path.join(tmpdir(), "aigw-e2e-"));
const isWin = process.platform === "win32";
const binPath = path.join(workDir, isWin ? "e2e-server.exe" : "e2e-server");
const dbPath = path.join(workDir, "e2e.db").replace(/\\/g, "/");
const port = process.env.E2E_BACKEND_PORT || "8080";

const configYAML = `
server:
  http:
    addr: ":${port}"
  metrics:
    addr: ""
database:
  driver: "sqlite"
  dsn: "${dbPath}"
redis:
  addr: "127.0.0.1:${process.env.E2E_REDIS_PORT || "63799"}"
ai:
  proxy_timeout_sec: 30
  agent_timeout_sec: 60
system:
  encryption_key: "e2e0123456789e2e0123456789e2e01"
  admin_token: "${process.env.E2E_ADMIN_TOKEN || "e2e-admin-token"}"
observability:
  otlp_endpoint: ""
`;
const configPath = path.join(workDir, "config.yaml");
writeFileSync(configPath, configYAML);

console.log(`[e2e] building server -> ${binPath}`);
const build = spawnSync("go", ["build", "-o", binPath, "./cmd/server"], {
  cwd: backendDir,
  stdio: "inherit",
});
if (build.status !== 0) {
  console.error("[e2e] backend build failed");
  process.exit(build.status ?? 1);
}

console.log(`[e2e] starting server on :${port} (db=${dbPath})`);
const child = spawn(binPath, ["-conf", configPath], { stdio: "inherit" });

// child.kill(signal) is unreliable for terminating a Windows process tree —
// it does not reliably stop the grandchild HTTP listener, leaving the port
// bound after this script exits (observed while developing this harness:
// repeated runs failed with EADDRINUSE from an orphaned e2e-server.exe).
// taskkill /T /F is the robust way to kill the whole tree on win32.
function stopChild() {
  if (child.killed || child.exitCode !== null) return;
  if (isWin) {
    spawnSync("taskkill", ["/pid", String(child.pid), "/t", "/f"]);
  } else {
    child.kill("SIGTERM");
  }
}
process.on("SIGINT", stopChild);
process.on("SIGTERM", stopChild);
process.on("exit", stopChild);
child.on("exit", (code) => process.exit(code ?? 0));
