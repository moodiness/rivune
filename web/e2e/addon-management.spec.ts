import { expect, test } from "./fixtures/rivune";

const addon = {
  id: "30000000-0000-4000-8000-000000000001",
  manifest: {
    id: "metadata-fixture",
    name: "Metadata fixture",
    version: "1.0.0",
    description: "Deterministic addon management fixture",
    types: ["movie", "series"],
  },
  position: 0,
  profileIds: ["alice"],
  installedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

const transportUrl = "https://addon.example/nested/provider/manifest.json?token=fixture-credential&locale=fr";

test("global addon editor loads the protected full transport URL", async ({ page, rivune: _rivune }) => {
  const managementRequests: string[] = [];
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "GET" && url.pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && url.pathname === `/api/v1/addons/${addon.id}/management`) {
      managementRequests.push(url.pathname);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...addon, transportUrl }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await expect(card).toBeVisible();
  const logoBounds = await card.locator(".addon-card__logo").boundingBox();
  const bodyBounds = await card.locator(".addon-card__body").boundingBox();
  expect(logoBounds).not.toBeNull();
  expect(bodyBounds).not.toBeNull();
  expect((bodyBounds?.x ?? 0) - ((logoBounds?.x ?? 0) + (logoBounds?.width ?? Number.POSITIVE_INFINITY))).toBeGreaterThanOrEqual(12);
  await card.getByRole("button", { name: "Edit", exact: true }).click();

  await expect(page.locator("dialog").getByLabel("Transport URL", { exact: true })).toHaveValue(transportUrl);
  expect(managementRequests).toEqual([`/api/v1/addons/${addon.id}/management`]);
});

test("global administrators see joined safe addon diagnostics and declared capabilities", async ({ page, rivune: _rivune }) => {
  const diagnosticsRequests: string[] = [];
  const privateTransport = "https://private-provider.invalid/internal/manifest.json?token=secret";
  const privateProviderDetail = "upstream private-provider.invalid returned credential=secret";
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "GET" && url.pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/addons/diagnostics") {
      diagnosticsRequests.push(url.pathname);
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          observedSince: "2026-01-01T00:00:00Z",
          diagnostics: [{
            addonId: addon.id,
            state: "degraded",
            lastSuccessAt: "2026-01-02T03:04:05Z",
            approximateLatencyMs: 42,
            lastError: { code: "timeout", at: "2026-01-03T04:05:06Z", message: privateProviderDetail },
            capabilities: { resources: ["catalog", "meta"], search: true, pagination: false, searchPagination: true },
            transportUrl: privateTransport,
            providerBody: privateProviderDetail,
          }, {
            addonId: "not-assigned-to-active-profile",
            state: "unavailable",
            lastError: { code: "unavailable", at: "2026-01-03T04:05:06Z" },
            capabilities: { resources: ["stream"], search: false, pagination: false, searchPagination: false },
          }],
        }),
      });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await expect(card).toBeVisible();
  await expect(card.locator(".addon-diagnostics__state")).toHaveText("Degraded");
  await expect(card.getByText("Search", { exact: true })).toBeVisible();
  await expect(card.getByText("Pagination", { exact: true })).toBeVisible();
  await expect(card.getByText("catalog", { exact: true })).toBeVisible();
  await expect(card.getByText("meta", { exact: true })).toBeVisible();
  await expect(card.getByText("Unavailable", { exact: true })).toHaveCount(0);
  await expect(card.getByText("stream", { exact: true })).toHaveCount(0);
  await expect(card.getByText(/^Last success /)).toBeVisible();
  await expect(card.getByText("Approx. 42 ms", { exact: true })).toBeVisible();
  await expect(card.getByText("Last error: Timed out", { exact: true })).toBeVisible();
  await expect(page.getByText(/^Health observations since .*; reset on server restart\.$/)).toHaveCount(1);
  await expect(page.getByText(privateTransport, { exact: false })).toHaveCount(0);
  await expect(page.getByText(privateProviderDetail, { exact: false })).toHaveCount(0);
  expect(diagnosticsRequests).toEqual(["/api/v1/addons/diagnostics"]);
});

test("delegated administration never requests addon diagnostics", async ({ page, rivune }) => {
  const diagnosticsRequests: string[] = [];
  await rivune.configureCategoryScope(page);
  await page.route("**/api/v1/addons/diagnostics", async (route) => {
    diagnosticsRequests.push(new URL(route.request().url()).pathname);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
  });

  await page.goto("/#admin?tab=addons");
  await expect(page.getByRole("heading", { name: "Profiles", exact: true })).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect(diagnosticsRequests).toEqual([]);
  await expect(page.getByText(/^Health observations since /)).toHaveCount(0);
});

test("diagnostics failure leaves addon content intact without invented unknown states", async ({ page, rivune: _rivune }) => {
  const privateFailure = "private-provider.invalid/status?token=secret returned a provider body";
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "GET" && url.pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: privateFailure } }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await expect(card).toBeVisible();
  await expect(page.getByText("Addon diagnostics could not be loaded.", { exact: true })).toBeVisible();
  await expect(card.getByText("Unknown", { exact: true })).toHaveCount(0);
  await expect(page.getByText(privateFailure, { exact: false })).toHaveCount(0);
});
