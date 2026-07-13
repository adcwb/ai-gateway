import fs from "node:fs";
import path from "node:path";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies API calls to a locally running gateway (backend/).
// E2E_BACKEND_PORT lets Playwright (playwright.config.ts) point this at a
// disposable test backend instead of the default :8080 dev instance.
const backendPort = process.env.E2E_BACKEND_PORT || "8080";

// Dev-only: serves the sibling ../homepage static site at the dev server
// root, mirroring how the Go backend serves it at "/" in production
// (backend/internal/homepage) alongside this app at "/console/" (this
// config's own `base`). Vite's `public/` dir can't do this cleanly — it's
// scoped under `base` and would get bundled into the console's own dist,
// wrongly landing homepage assets under /console/ instead of /. This
// middleware only runs in `vite dev`; `vite build` is untouched.
function homepagePreview(): Plugin {
  const homepageDir = path.resolve(import.meta.dirname, "..", "homepage");
  const mimeTypes: Record<string, string> = {
    ".html": "text/html; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
  };
  return {
    name: "homepage-preview",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (!req.url || req.url.startsWith("/console") || req.url.startsWith("/@") || req.url.startsWith("/src")) {
          next();
          return;
        }
        const urlPath = req.url.split("?")[0];
        const filePath = path.join(homepageDir, urlPath === "/" ? "index.html" : urlPath);
        // path.relative + ".." check (not a bare filePath.startsWith(homepageDir)
        // prefix match, which a sibling dir like "homepage-internal" would also
        // pass) so a crafted request can't escape homepageDir.
        const rel = path.relative(homepageDir, filePath);
        if (rel.startsWith("..") || path.isAbsolute(rel)) {
          next();
          return;
        }
        fs.readFile(filePath, (err, data) => {
          if (err) {
            next();
            return;
          }
          res.setHeader("Content-Type", mimeTypes[path.extname(filePath)] || "application/octet-stream");
          res.end(data);
        });
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), homepagePreview()],
  base: "/console/",
  server: {
    host: "0.0.0.0", // reachable from other devices on the LAN, not just localhost
    port: 5173,
    proxy: {
      "/ai": {
        target: `http://127.0.0.1:${backendPort}`,
        changeOrigin: true,
      },
    },
  },
  preview: {
    host: "0.0.0.0",
    port: 5173,
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
