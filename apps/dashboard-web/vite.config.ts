import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// /api    → edge-api (:8080) — data operasional lokal + WebSocket.
// /cloud  → cloud-api (:9090) — auth & agregat multi-tenant (prefix di-strip).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.EDGE_API_URL || "http://localhost:8080",
        changeOrigin: true,
        ws: true,
      },
      "/cloud": {
        target: process.env.CLOUD_API_URL || "http://localhost:9090",
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/cloud/, ""),
      },
    },
  },
});
