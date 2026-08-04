import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

function manualChunk(id: string): string | undefined {
  const modulePath = id.split("\\").join("/");
  if (modulePath.includes("/node_modules/hls.js/")) return "hls";
  if (
    modulePath.includes("/node_modules/react/") ||
    modulePath.includes("/node_modules/react-dom/") ||
    modulePath.includes("/node_modules/scheduler/")
  ) return "react-vendor";
  if (modulePath.includes("/node_modules/lucide-react/")) return "icons-vendor";
  if (modulePath.includes("/node_modules/qrcode.react/")) return "qrcode-vendor";
  return undefined;
}

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: resolve(__dirname, "../server/internal/webui/dist"),
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        onlyExplicitManualChunks: true,
        manualChunks: manualChunk,
      },
    },
  },
  server: {
    host: "0.0.0.0",
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/.well-known": "http://127.0.0.1:8080",
      "/health": "http://127.0.0.1:8080",
    },
  },
});
