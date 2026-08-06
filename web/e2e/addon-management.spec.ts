import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";

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
  categoryIds: [],
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
      const input = request.postDataJSON() as { enabled?: boolean; profileIds?: string[]; categoryIds?: string[]; transportUrl?: string };
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
  await dialog.getByLabel("Transport URL", { exact: true }).fill("");
  const disableButton = dialog.getByRole("button", { name: "Disable", exact: true });
  await expect(disableButton).toHaveAttribute("aria-pressed", "true");
  await disableButton.click();
  await expect(dialog.getByRole("button", { name: "Enable", exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect(dialog.locator(".addon-availability").getByText("Disabled", { exact: true })).toBeVisible();
  expect(updateInputs).toEqual([]);

  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(updateInputs).toEqual([{ enabled: false }]);
  await expect(card).toHaveClass(/\bis-disabled\b/);
  await expect(card.getByText("Disabled", { exact: true })).toBeVisible();

  await card.getByRole("button", { name: "Edit", exact: true }).click();
  dialog = page.locator("dialog");
  await expect(dialog.getByLabel("Transport URL", { exact: true })).toHaveValue(transportUrl);
  const enableButton = dialog.getByRole("button", { name: "Enable", exact: true });
  await expect(enableButton).toHaveAttribute("aria-pressed", "false");
  await enableButton.focus();
  await page.keyboard.press("Space");
  await expect(dialog.getByRole("button", { name: "Disable", exact: true })).toHaveAttribute("aria-pressed", "true");
  expect(updateInputs).toHaveLength(1);
  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();

  await expect(dialog).toHaveCount(0);
  expect(updateInputs).toEqual([
    { enabled: false },
    { enabled: true },
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
  expect(updateInputs).toEqual([{ enabled: false }]);
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
    profileIds: ["alice"],
    categoryIds: [],
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
      const input = request.postDataJSON() as { profileIds: string[]; categoryIds: string[] };
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...previewResponse, profileIds: input.profileIds, categoryIds: input.categoryIds }) });
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
  const profilesDisclosure = tool.locator(".assignment-picker__profiles");
  await profilesDisclosure.getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  const bobAssignment = profilesDisclosure.locator("label").filter({ hasText: "Bob" });
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
    { transportUrl, profileIds: ["alice"], categoryIds: [] },
    { transportUrl: changedTransportUrl, profileIds: ["alice", "bob"], categoryIds: [] },
    { transportUrl: changedTransportUrl, profileIds: ["alice"], categoryIds: [] },
  ]);
  expect(installInput).toEqual({ transportUrl: changedTransportUrl, profileIds: ["alice"], categoryIds: [] });
});

test("category-first addon assignment keeps durable categories independent through preview confirmation", async ({ page, rivune }) => {
  rivune.seedProfiles(1, CATEGORY_IDS.kids);
  let assignedAddon = { ...addon, profileIds: ["bob"], categoryIds: [CATEGORY_IDS.kids], transportUrl };
  const previewInputs: unknown[] = [];
  const updateInputs: unknown[] = [];
  let installInput: unknown;
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (request.method() === "GET" && pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [assignedAddon] }) });
      return;
    }
    if (request.method() === "GET" && pathname === "/api/v1/addons/diagnostics") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ observedSince: "2026-01-01T00:00:00Z", diagnostics: [] }) });
      return;
    }
    if (request.method() === "GET" && pathname === `/api/v1/addons/${addon.id}/management`) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(assignedAddon) });
      return;
    }
    if (request.method() === "PUT" && pathname === `/api/v1/addons/${addon.id}`) {
      const input = request.postDataJSON();
      updateInputs.push(input);
      assignedAddon = { ...assignedAddon, ...input };
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(assignedAddon) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons/preview") {
      const input = request.postDataJSON() as { profileIds: string[]; categoryIds: string[] };
      previewInputs.push(input);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ manifest: addon.manifest, capabilities: { resources: ["catalog"], search: false, pagination: false, searchPagination: false }, profileIds: input.profileIds, categoryIds: input.categoryIds }) });
      return;
    }
    if (request.method() === "POST" && pathname === "/api/v1/addons") {
      installInput = request.postDataJSON();
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(assignedAddon) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ hasText: addon.manifest.name });
  await expect(card.getByText("3 Profiles reached", { exact: true })).toBeVisible();
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  let dialog = page.getByRole("dialog");
  const editCategories = dialog.locator(".assignment-picker__categories");
  await expect(editCategories.getByText("Household", { exact: true })).toBeVisible();
  await expect(editCategories.getByText("Kids", { exact: true })).toBeVisible();
  await expect(editCategories.getByText("Guest", { exact: true })).toBeVisible();
  await expect(editCategories.locator("label").filter({ hasText: "Kids" }).getByRole("checkbox")).toBeChecked();
  const selectedCheckboxStyle = await editCategories.locator("label").filter({ hasText: "Kids" }).getByRole("checkbox").evaluate((checkbox) => {
    const style = getComputedStyle(checkbox);
    return { appearance: style.appearance, borderRadius: style.borderRadius, width: style.width, backgroundColor: style.backgroundColor };
  });
  expect(selectedCheckboxStyle).toEqual({ appearance: "none", borderRadius: "7px", width: "22px", backgroundColor: "rgb(242, 154, 120)" });
  const editProfiles = dialog.locator(".assignment-picker__profiles");
  await expect(editProfiles).not.toHaveAttribute("open", "");
  await expect(editProfiles.getByText("Individual profiles", { exact: true })).not.toBeVisible();
  await editProfiles.getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  await expect(editProfiles.locator("label").filter({ hasText: "Bob" }).getByRole("checkbox")).toBeChecked();
  await dialog.getByLabel("Transport URL", { exact: true }).fill("");
  await editCategories.locator("label").filter({ hasText: "Kids" }).getByRole("checkbox").uncheck();
  await editCategories.locator("label").filter({ hasText: "Household" }).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  dialog = page.getByRole("dialog");
  const secondProfiles = dialog.locator(".assignment-picker__profiles");
  await expect(dialog.getByLabel("Transport URL", { exact: true })).toHaveValue(transportUrl);
  await secondProfiles.getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  await secondProfiles.locator("label").filter({ hasText: "Bob" }).getByRole("checkbox").uncheck();
  await dialog.getByRole("button", { name: "Save addon", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(updateInputs).toEqual([{ categoryIds: [CATEGORY_IDS.household] }, { profileIds: [] }]);

  const tool = page.locator(".admin-tool-card").filter({ hasText: "Install from a manifest" });
  await tool.getByLabel("Manifest URL", { exact: true }).fill(transportUrl);
  const kidsCheckbox = tool.locator(".assignment-picker__categories label").filter({ hasText: "Kids" }).getByRole("checkbox");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  await expect(kidsCheckbox).toBeFocused();
  expect(await kidsCheckbox.evaluate((element) => getComputedStyle(element).outlineStyle)).toBe("solid");
  await page.keyboard.press("Space");
  await tool.locator(".assignment-picker__profiles").getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  await tool.locator(".assignment-picker__profiles label").filter({ hasText: "Bob" }).getByRole("checkbox").check();
  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();
  await expect(tool.locator(".addon-preview")).toBeVisible();
  await kidsCheckbox.uncheck();
  await expect(tool.locator(".addon-preview")).toHaveCount(0);
  await kidsCheckbox.check();
  await tool.getByRole("button", { name: "Review add-on", exact: true }).click();
  await tool.locator(".addon-preview").getByRole("button", { name: "Install addon", exact: true }).click();

  const snapshot = { transportUrl, profileIds: ["alice", "bob"], categoryIds: [CATEGORY_IDS.kids] };
  expect(previewInputs).toEqual([snapshot, snapshot]);
  expect(installInput).toEqual(snapshot);
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
