import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies API calls to a locally running gateway (backend/).
// E2E_BACKEND_PORT lets Playwright (playwright.config.ts) point this at a
// disposable test backend instead of the default :8080 dev instance.
const backendPort = process.env.E2E_BACKEND_PORT || "8080";

export default defineConfig({
  plugins: [react()],
  base: "/console/",
  server: {
    port: 5173,
    proxy: {
      "/ai": {
        target: `http://127.0.0.1:${backendPort}`,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
