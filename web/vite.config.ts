import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

// Dev mode proxies API and WebSocket traffic to the Go backend so the Vite
// dev server can be used for live reload against a running blackbird binary.
const backend = process.env.BACKEND ?? "http://127.0.0.1:8222";

export default defineConfig({
  plugins: [solid()],
  server: {
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/ws": { target: backend, ws: true },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
