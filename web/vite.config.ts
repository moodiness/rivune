import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

export default defineConfig({
  base: process.env.RIVUNE_PUBLIC_APPS_BUILD === "1" ? "/rivune/" : "/",
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("../server/internal/webui/dist", import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: "hls", test: /node_modules[\\/]hls\.js[\\/]/ },
            { name: "react-vendor", test: /node_modules[\\/](?:react|react-dom|scheduler)[\\/]/ },
            { name: "icons-vendor", test: /node_modules[\\/]lucide-react[\\/]/ },
            { name: "qrcode-vendor", test: /node_modules[\\/]qrcode\.react[\\/]/ },
          ],
        },
      },
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/.well-known": "http://127.0.0.1:8080",
      "/health": "http://127.0.0.1:8080",
    },
  },
  preview: {
    host: "127.0.0.1",
  },
});
