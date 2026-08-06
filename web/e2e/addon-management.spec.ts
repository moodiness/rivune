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
  enabled: true,
  profileIds: ["alice"],
  installedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

const transportUrl = "https://addon.example/nested/provider/manifest.json?token=fixture-credential&locale=fr";

test("global addon editor hydrates protected transport and availability from management", async ({ page, rivune: _rivune }) => {
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
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...addon, enabled: false, transportUrl }) });
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
  const availability = page.locator("dialog").locator(".addon-availability");
  await expect(availability.getByText("Disabled", { exact: true })).toBeVisible();
  await expect(availability.getByRole("button", { name: "Enable", exact: true })).toHaveAttribute("aria-pressed", "false");
  expect(managementRequests).toEqual([`/api/v1/addons/${addon.id}/management`]);
});

test("addon availability changes stay local until Save and can be re-enabled", async ({ page, rivune: _rivune }) => {
  let managed = { ...addon, transportUrl };
  const updateInputs: unknown[] = [];
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET" && pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [managed] }) });
      return;
    }
    if (request.method() === "GET" && pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
      return;
    }
    if (request.method() === "GET" && pathname === `/api/v1/addons/${addon.id}/management`) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(managed) });
      return;
    }
    if (request.method() === "PUT" && pathname === `/api/v1/addons/${addon.id}`) {
      const input = request.postDataJSON() as { enabled: boolean; profileIds: string[]; transportUrl: string };
      updateInputs.push(input);
      managed = { ...managed, ...input };
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(managed) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  let dialog = page.locator("dialog");
  const disableButton = dialog.getByRole("button", { name: "Disable", exact: true });
  await expect(disableButton).toHaveAttribute("aria-pressed", "true");
  await disableButton.click();
  await expect(dialog.getByRole("button", { name: "Enable", exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect(dialog.locator(".addon-availability").getByText("Disabled", { exact: true })).toBeVisible();
  expect(updateInputs).toEqual([]);

  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(updateInputs).toEqual([{ profileIds: ["alice"], enabled: false, transportUrl }]);
  await expect(card).toHaveClass(/\bis-disabled\b/);
  await expect(card.getByText("Disabled", { exact: true })).toBeVisible();

  await card.getByRole("button", { name: "Edit", exact: true }).click();
  dialog = page.locator("dialog");
  const enableButton = dialog.getByRole("button", { name: "Enable", exact: true });
  await expect(enableButton).toHaveAttribute("aria-pressed", "false");
  await enableButton.focus();
  await page.keyboard.press("Space");
  await expect(dialog.getByRole("button", { name: "Disable", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(updateInputs).toHaveLength(1);
  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();

  await expect(dialog).toHaveCount(0);
  expect(updateInputs).toEqual([
    { profileIds: ["alice"], enabled: false, transportUrl },
    { profileIds: ["alice"], enabled: true, transportUrl },
  ]);
  await expect(card).not.toHaveClass(/\bis-disabled\b/);
  await expect(card.getByText("Disabled", { exact: true })).toHaveCount(0);
});

test("failed availability save preserves the draft and installed card state", async ({ page, rivune: _rivune }) => {
  const updateInputs: unknown[] = [];
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET" && pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
      return;
    }
    if (request.method() === "GET" && pathname === `/api/v1/addons/${addon.id}/management`) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...addon, transportUrl }) });
      return;
    }
    if (request.method() === "PUT" && pathname === `/api/v1/addons/${addon.id}`) {
      updateInputs.push(request.postDataJSON());
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "The addon could not be saved." } }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  const dialog = page.locator("dialog");
  await dialog.getByRole("button", { name: "Disable", exact: true }).click();
  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();

  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("The addon could not be saved.", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Enable", exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect(dialog.locator(".addon-availability").getByText("Disabled", { exact: true })).toBeVisible();
  await expect(card).not.toHaveClass(/\bis-disabled\b/);
  await expect(card.getByText("Disabled", { exact: true })).toHaveCount(0);
  expect(updateInputs).toEqual([{ profileIds: ["alice"], enabled: false, transportUrl }]);
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

test("delegated administration never requests addon diagnostics or preview", async ({ page, rivune }) => {
  const diagnosticsRequests: string[] = [];
  const previewRequests: string[] = [];
  await rivune.configureCategoryScope(page);
  await page.route("**/api/v1/addons/diagnostics", async (route) => {
    diagnosticsRequests.push(new URL(route.request().url()).pathname);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
  });
  await page.route("**/api/v1/addons/preview", async (route) => {
    previewRequests.push(new URL(route.request().url()).pathname);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({}) });
  });

  await page.goto("/#admin?tab=addons");
  await expect(page.getByRole("heading", { name: "Profiles", exact: true })).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect(diagnosticsRequests).toEqual([]);
  expect(previewRequests).toEqual([]);
  await expect(page.getByRole("button", { name: "Review add-on", exact: true })).toHaveCount(0);
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

test("addon installation previews declarations before sending the exact confirmed input", async ({ page, rivune: _rivune }) => {
  const changedTransportUrl = `${transportUrl}&variant=next`;
  const previewResponse = {
    manifest: {
      id: "preview-fixture",
      name: "Preview fixture",
      version: "2.4.0",
      description: "Declared preview description",
      types: ["movie", "series"],
      behaviorHints: { p2p: true, adult: true, configurationRequired: true },
    },
    capabilities: { resources: ["catalog", "meta", "stream"], search: false, pagination: false, searchPagination: true },
  };
  const requestOrder: string[] = [];
  const previewInputs: unknown[] = [];
  let installInput: unknown;
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET" && pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons/preview") {
      requestOrder.push("preview");
      previewInputs.push(request.postDataJSON());
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(previewResponse) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons") {
      requestOrder.push("install");
      installInput = request.postDataJSON();
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...addon, manifest: previewResponse.manifest, profileIds: ["alice"] }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const tool = page.locator(".admin-tool-card").filter({ has: page.getByRole("heading", { name: "Install from a manifest", exact: true }) });
  const manifestUrl = tool.getByLabel("Manifest URL", { exact: true });
  const bobAssignment = tool.locator(".profile-assignment label").filter({ hasText: "Bob" });
  const bobCheckbox = bobAssignment.getByRole("checkbox");
  await manifestUrl.fill(transportUrl);
  await expect(tool.getByRole("button", { name: "Install addon", exact: true })).toHaveCount(0);

  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();
  await expect.poll(() => requestOrder).toEqual(["preview"]);
  const preview = tool.locator(".addon-preview");
  await expect(preview.getByRole("heading", { name: "Review before installing", exact: true })).toBeVisible();
  await expect(preview.getByText("Preview fixture", { exact: true })).toBeVisible();
  await expect(preview.getByText("v2.4.0", { exact: true })).toBeVisible();
  await expect(preview.getByText("Declared preview description", { exact: true })).toBeVisible();
  for (const declaration of ["movie", "series", "catalog", "meta", "stream", "Search", "Pagination", "P2P", "Adult content", "Configuration required"]) {
    await expect(preview.getByText(declaration, { exact: true })).toBeVisible();
  }

  await manifestUrl.fill(changedTransportUrl);
  await expect(preview).toHaveCount(0);
  await bobAssignment.getByText("Bob", { exact: true }).click();
  await expect(bobCheckbox).toBeChecked();
  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();
  await expect.poll(() => requestOrder).toEqual(["preview", "preview"]);
  await expect(preview).toBeVisible();

  await bobAssignment.getByText("Bob", { exact: true }).click();
  await expect(bobCheckbox).not.toBeChecked();
  await expect(preview).toHaveCount(0);
  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();
  await expect.poll(() => requestOrder).toEqual(["preview", "preview", "preview"]);
  await preview.getByRole("button", { name: "Install addon", exact: true }).click();
  await expect.poll(() => requestOrder).toEqual(["preview", "preview", "preview", "install"]);

  expect(previewInputs).toEqual([
    { transportUrl, profileIds: ["alice"] },
    { transportUrl: changedTransportUrl, profileIds: ["alice", "bob"] },
    { transportUrl: changedTransportUrl, profileIds: ["alice"] },
  ]);
  expect(installInput).toEqual({ transportUrl: changedTransportUrl, profileIds: ["alice"] });
});

test("preview failure is isolated and never renders provider details", async ({ page, rivune: _rivune }) => {
  const privateFailure = "provider.invalid/private?token=secret failed during preview";
  const requests: string[] = [];
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET" && pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons/preview") {
      requests.push("preview");
      await route.fulfill({ status: 502, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: privateFailure } }) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons") requests.push("install");
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const tool = page.locator(".admin-tool-card").filter({ has: page.getByRole("heading", { name: "Install from a manifest", exact: true }) });
  await tool.getByLabel("Manifest URL", { exact: true }).fill(transportUrl);
  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();

  await expect(tool.getByText("The add-on could not be reviewed.", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: addon.manifest.name, exact: true })).toBeVisible();
  await expect(page.getByText(privateFailure, { exact: false })).toHaveCount(0);
  await expect(tool.locator(".addon-preview")).toHaveCount(0);
  await expect(tool.getByRole("button", { name: "Install addon", exact: true })).toHaveCount(0);
  expect(requests).toEqual(["preview"]);
});
