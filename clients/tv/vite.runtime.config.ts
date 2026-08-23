import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const packageMetadata = JSON.parse(readFileSync(fileURLToPath(new URL("./package.json", import.meta.url)), "utf8")) as { version: string };

export default defineConfig({
  root: fileURLToPath(new URL("./common", import.meta.url)),
  publicDir: false,
  plugins: [react()],
  define: {
    __RIVUNE_VERSION__: JSON.stringify(packageMetadata.version),
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  build: {
    outDir: fileURLToPath(new URL("./dist/runtime-parts", import.meta.url)),
    emptyOutDir: true,
    target: "chrome53",
    sourcemap: false,
    cssCodeSplit: false,
    assetsInlineLimit: Number.MAX_SAFE_INTEGER,
    reportCompressedSize: false,
    lib: {
      entry: fileURLToPath(new URL("./common/main.tsx", import.meta.url)),
      name: "RivuneTvRuntime",
      formats: ["iife"],
      fileName: "application",
      cssFileName: "application",
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
      },
    },
  },
});
