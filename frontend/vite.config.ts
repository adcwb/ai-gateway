import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies API calls to a locally running gateway (backend/).
export default defineConfig({
  plugins: [react()],
  base: "/console/",
  server: {
    port: 5173,
    proxy: {
      "/ai": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
