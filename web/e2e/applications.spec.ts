import { expect, test, type Page } from "@playwright/test";

const releaseEndpoint = "https://api.github.com/repos/moodiness/rivune/releases/latest";
const tag = "v2.0.0";
const assets = [
  "Rivune-Android.apk",
  "Rivune-iOS-unsigned.ipa",
  "Rivune-tvOS-unsigned.ipa",
  "Rivune-visionOS-unsigned.ipa",
  "Rivune-macOS.dmg",
  "Rivune-x64.exe",
  "Rivune-arm64.exe",
].map((name, index) => ({
  name,
  state: "uploaded",
  size: 1024 * 1024 * (index + 1),
  digest: `sha256:${String(index + 1).repeat(64)}`,
  browser_download_url: `https://github.com/moodiness/rivune/releases/download/${tag}/${name}`,
}));

const release = {
  tag_name: tag,
  name: tag,
  html_url: `https://github.com/moodiness/rivune/releases/tag/${tag}`,
  published_at: "2026-08-21T12:00:00Z",
  draft: false,
  prerelease: false,
  assets: [
    ...assets,
    {
      name: "rivune-update.json",
      state: "uploaded",
      size: 2048,
      digest: `sha256:${"f".repeat(64)}`,
      browser_download_url: `https://github.com/moodiness/rivune/releases/download/${tag}/rivune-update.json`,
    },
  ],
};

async function serveRelease(page: Page) {
  await page.route(releaseEndpoint, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(release),
  }));
}

test("lists exact stable assets, fingerprints, warnings, and QR links without booting auth", async ({ page }) => {
  let backendRequests = 0;
  page.on("request", (request) => {
    if (request.url().includes("/.well-known/rivune")) backendRequests += 1;
  });
  await serveRelease(page);

  await page.goto("/apps");

  await expect(page).toHaveTitle("Rivune applications");
  await expect(page.getByRole("heading", { name: "Rivune on every screen." })).toBeVisible();
  await expect(page.getByText("Latest stable release")).toBeVisible();
  await expect(page.getByText(tag, { exact: true })).toBeVisible();
  await expect(page.locator(".applications-card")).toHaveCount(7);
  await expect(page.locator(`[data-asset="Rivune-Android.apk"] code`)).toHaveText("1".repeat(64));
  await expect(page.locator(`[data-asset="Rivune-iOS-unsigned.ipa"]`)).toContainText("cannot be installed as downloaded");
  await expect(page.locator(`[data-asset="Rivune-x64.exe"]`)).toContainText("SmartScreen");
  await expect(page.locator(`[data-asset="Rivune-iOS-unsigned.ipa"]`).getByRole("link", { name: "Sign and install locally" })).toHaveAttribute("href", "https://github.com/moodiness/rivune#direct-application-downloads");
  await expect(page.locator(`[data-asset="Rivune-tvOS-unsigned.ipa"]`).getByRole("link", { name: "Sign and install locally" })).toBeVisible();
  await expect(page.locator(`[data-asset="Rivune-visionOS-unsigned.ipa"]`).getByRole("link", { name: "Sign and install locally" })).toBeVisible();

  for (const asset of assets) {
    await expect(page.locator(`[data-asset="${asset.name}"] a`, { hasText: "Download" })).toHaveAttribute("href", asset.browser_download_url);
  }

  const android = page.locator(`[data-asset="Rivune-Android.apk"]`);
  await android.getByRole("button", { name: "Show QR" }).click();
  await expect(android.getByRole("img", { name: "Download Rivune-Android.apk" })).toBeVisible();
  expect(backendRequests).toBe(0);
});

test("recommends the detected Windows ARM64 executable", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { configurable: true, get: () => "Win32" });
    Object.defineProperty(navigator, "userAgent", { configurable: true, get: () => "Mozilla/5.0 (Windows NT 10.0; ARM64) AppleWebKit/537.36 Chrome/140 Safari/537.36" });
  });
  await serveRelease(page);

  await page.goto("/apps");

  await expect(page.getByText("Detected:").locator("..")).toContainText("Windows on ARM64");
  await expect(page.locator(`[data-asset="Rivune-arm64.exe"]`)).toHaveClass(/is-recommended/);
  await expect(page.locator(`[data-asset="Rivune-arm64.exe"]`)).toContainText("Recommended");
});

test("rejects release metadata containing an unexpected asset", async ({ page }) => {
  const unexpectedRelease = {
    ...release,
    assets: [
      ...release.assets,
      {
        name: "unexpected.bin",
        state: "uploaded",
        size: 1,
        digest: `sha256:${"a".repeat(64)}`,
        browser_download_url: `https://github.com/moodiness/rivune/releases/download/${tag}/unexpected.bin`,
      },
    ],
  };
  await page.route(releaseEndpoint, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(unexpectedRelease),
  }));

  await page.goto("/apps");

  await expect(page.getByRole("alert")).toContainText("Release metadata failed validation");
  await expect(page.locator(".applications-card")).toHaveCount(0);
});

test("rejects failed metadata and retries a validated response", async ({ page }) => {
  let requests = 0;
  let unavailable = true;
  await page.route(releaseEndpoint, async (route) => {
    requests += 1;
    if (unavailable) await route.fulfill({ status: 503, body: "unavailable" });
    else await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(release) });
  });

  await page.goto("/apps");
  await expect(page.getByRole("alert")).toContainText("Downloads are temporarily unavailable");
  unavailable = false;
  await page.getByRole("button", { name: "Try again" }).click();

  await expect(page.locator(".applications-card")).toHaveCount(7);
  expect(requests).toBeGreaterThanOrEqual(2);
});
