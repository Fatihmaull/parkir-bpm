import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Proxy /api → edge-api (localhost:8080), termasuk WebSocket /api/v1/stream.
// Menghindari CORS: frontend & backend tampak satu origin di dev.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.EDGE_API_URL || "http://localhost:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
});
