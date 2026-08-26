import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.RIVUNE_RELEASE_BASE_URL;
if (!baseURL) throw new Error("RIVUNE_RELEASE_BASE_URL is required");

export default defineConfig({
  testDir: "./e2e",
  testMatch: "release-journey.spec.ts",
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: "line",
  outputDir: "test-results/release-journey",
  use: {
    baseURL,
    locale: "en-US",
    timezoneId: "UTC",
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
    trace: "off",
    screenshot: "off",
    video: "off",
  },
  projects: [{ name: "chromium-release", use: { ...devices["Desktop Chrome"] } }],
});
