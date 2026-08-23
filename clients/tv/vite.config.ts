import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import legacy from "@vitejs/plugin-legacy";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const packageMetadata = JSON.parse(readFileSync(fileURLToPath(new URL("./package.json", import.meta.url)), "utf8")) as { version: string };

const platformBootstrap = {
  name: "rivune-platform-bootstrap",
  enforce: "post" as const,
  transformIndexHtml: {
    order: "post" as const,
    handler(html: string) {
      const script = '<script src="./platform.js"></script>';
      const withoutBootstrap = html.replace(script, "");
      return withoutBootstrap.replace("<head>", `<head>\n    ${script}`);
    },
  },
};

export default defineConfig({
  root: fileURLToPath(new URL("./common", import.meta.url)),
  base: "./",
  publicDir: "public",
  plugins: [
    react(),
    legacy({
      targets: ["Chrome >= 53"],
      modernPolyfills: true,
      renderLegacyChunks: true,
    }),
    platformBootstrap,
  ],
  define: {
    __RIVUNE_VERSION__: JSON.stringify(packageMetadata.version),
  },
  build: {
    outDir: fileURLToPath(new URL("./dist/common", import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
    cssCodeSplit: false,
    assetsInlineLimit: 0,
  },
  server: {
    host: "127.0.0.1",
    port: 5180,
  },
  preview: {
    host: "127.0.0.1",
    port: 4180,
  },
});
