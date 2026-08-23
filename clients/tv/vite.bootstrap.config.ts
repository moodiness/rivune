import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

export default defineConfig({
  publicDir: false,
  build: {
    outDir: fileURLToPath(new URL("./dist/bootstrap", import.meta.url)),
    emptyOutDir: true,
    target: "chrome53",
    sourcemap: false,
    reportCompressedSize: false,
    lib: {
      entry: fileURLToPath(new URL("./updater/main.ts", import.meta.url)),
      name: "RivuneTvBootstrap",
      formats: ["iife"],
      fileName: () => "updater.js",
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
      },
    },
  },
});
