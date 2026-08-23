import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    include: ["common/**/*.test.ts", "common/**/*.test.tsx", "updater/**/*.test.ts"],
    restoreMocks: true,
    clearMocks: true,
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./common", import.meta.url)),
    },
  },
});
