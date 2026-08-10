import type { Page } from "@playwright/test";

import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";
import { selectOption } from "./helpers/select";

const profileSections = ["appearance", "playback", "language", "subtitles", "connections"];
const serverSections = ["appearance", "playback", "runtime", "transcoding", "language", "subtitles", "connections", "integrations", "audit"];

const profileSectionCopy = [
  { id: "appearance", title: "Appearance", description: "Theme, motion, and content density." },
  { id: "playback", title: "Playback", description: "Delivery quality, episode flow, and skip actions." },
  { id: "language", title: "Language & metadata", description: "How titles, regions, and episode ordering are resolved." },
  { id: "subtitles", title: "Language & subtitles", description: "Preferred tracks and readable cue styling." },
  { id: "connections", title: "Connections", description: "Mirror this profile's activity while Rivune remains the source of truth." },
] as const;

async function openSettings(page: Page, section?: string) {
  const sectionQuery = section ? `&section=${section}` : "";
  await page.goto(`/#admin?tab=settings${sectionQuery}`);
  await expect(page.locator(".settings-workspace")).toBeVisible();
}

async function sectionIds(page: Page) {
  return page.locator("[data-settings-section]").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("data-settings-section")));
}

test("settings categories keep one panel active and preserve deep links", async ({ page, rivune: _rivune }) => {
  await openSettings(page, "subtitles");

  const navigation = page.locator(".settings-navigation");
  const content = page.locator(".settings-content");
  const subtitles = navigation.locator('[data-settings-section="subtitles"]');
  await expect(subtitles).toHaveAttribute("aria-current", "page");
  await expect(subtitles).toHaveAttribute("aria-controls", "settings-section-subtitles");
  await expect(content.locator("#settings-section-subtitles")).toBeVisible();
  await expect(content.locator('section[id^="settings-section-"]')).toHaveCount(1);
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=subtitles$/);

  const playback = navigation.locator('[data-settings-section="playback"]');
  await playback.click();
  await expect(playback).toHaveAttribute("aria-current", "page");
  await expect(content.locator("#settings-section-playback")).toBeVisible();
  await expect(content.locator("#settings-section-subtitles")).toHaveCount(0);
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=playback$/);

  await page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Operations\b/ }).click();
  await expect(page).toHaveURL(/\/#admin\?tab=operations$/);
  await page.goBack();
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=playback$/);
  await expect(navigation.locator('[data-settings-section="playback"]')).toHaveAttribute("aria-current", "page");
});

test("desktop category rail shows complete wrapped titles and descriptions without horizontal overflow", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openSettings(page);

  const navigation = page.locator(".settings-navigation");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);
  await expect.poll(() => navigation.evaluate((element) => getComputedStyle(element).overflowY)).toBe("auto");
  expect(await navigation.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);

  for (const copy of profileSectionCopy) {
    const item = navigation.locator(`[data-settings-section="${copy.id}"]`);
    const title = item.locator(".settings-navigation__item-title");
    const description = item.locator(".settings-navigation__item-description");
    await expect(item).toHaveAccessibleName(copy.title);
    await expect(item).toHaveAccessibleDescription(copy.description);
    await expect(title).toHaveText(copy.title);
    await expect(description).toHaveText(copy.description);
    await expect(title).toHaveCSS("white-space", "normal");
    await expect(description).toHaveCSS("white-space", "normal");
    await expect(title).toHaveCSS("text-overflow", "clip");
    await expect(description).toHaveCSS("text-overflow", "clip");
    expect(await item.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    expect(await title.evaluate((element) => element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight)).toBe(true);
    expect(await description.evaluate((element) => element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight)).toBe(true);
  }
});

test("settings search opens the single matching category", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const navigation = page.locator(".settings-navigation");
  const search = navigation.locator('.settings-navigation__search input[type="search"]');
  const appearance = navigation.locator('[data-settings-section="appearance"]');
  const subtitles = navigation.locator('[data-settings-section="subtitles"]');
  await expect(search).toHaveAccessibleName("Search");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);

  await search.fill("subtitle");
  await expect(subtitles).toBeVisible();
  await expect(appearance).toHaveCount(0);
  await expect.poll(() => sectionIds(page)).toEqual(["subtitles"]);
  await expect(subtitles).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-subtitles")).toBeVisible();
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=subtitles$/);

  await search.clear();
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);
  await expect(subtitles).toHaveAttribute("aria-current", "page");
});

test("scope switching exposes only the categories valid for that target", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const scope = page.getByRole("combobox", { name: "Switch scope" });
  await expect(scope).toHaveAttribute("data-value", "alice");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);

  await page.locator('[data-settings-section="connections"]').click();
  await expect(page.locator("#settings-section-connections")).toBeVisible();
  await selectOption(scope, "server");
  await expect.poll(() => sectionIds(page)).toEqual(serverSections);
  await expect(page.locator('[data-settings-section="connections"]')).toHaveAccessibleName("Jellyfin API");
  await expect(page.locator('[data-settings-section="appearance"]')).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-appearance")).toBeVisible();
  await expect(page.locator(".settings-scope--server")).toContainText("Server defaults");
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=appearance$/);

  await selectOption(scope, "alice");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);
  await expect(page.locator('[data-settings-section="transcoding"]')).toHaveCount(0);
  await expect(page.locator('[data-settings-section="connections"]')).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-connections")).toBeVisible();
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=connections$/);
});

test("dirty preferences can be discarded or saved from the persistent action bar", async ({ page, rivune }) => {
  await openSettings(page);

  const animations = page.getByRole("checkbox", { name: "Interface animations" });
  const saveBar = page.locator(".settings-save-bar");
  const discard = saveBar.getByRole("button", { name: "Discard changes" });
  const save = saveBar.getByRole("button", { name: "Save preferences" });
  await expect(animations).toBeChecked();
  await expect(saveBar).toHaveCount(0);

  await animations.uncheck();
  await expect(saveBar.getByRole("status")).toContainText("Unsaved changes");
  await expect(saveBar).toHaveCount(1);
  await expect(discard).toBeEnabled();
  await expect(save).toBeEnabled();
  await expect(page.getByRole("combobox", { name: "Switch scope" })).toBeDisabled();
  await expect(page.locator(".settings-profile-picker")).toHaveClass(/is-locked/);

  await discard.click();
  await expect(animations).toBeChecked();
  await expect(saveBar).toHaveCount(0);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH")).toHaveLength(0);

  await animations.uncheck();
  await save.click();
  const request = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(request.body).toMatchObject({ animationsEnabled: false });
  await expect(saveBar).toHaveCount(0);
  await expect(page.locator(".app-notification--success").filter({ hasText: "Alice" })).toHaveCount(1);
  await expect(page.locator(".settings-content .notice--success")).toHaveCount(0);
  await expect(page.getByRole("combobox", { name: "Switch scope" })).toBeEnabled();
});

test("Jellyfin app credential is profile-scoped, copyable once, rotatable, and revocable", async ({ page, rivune }) => {
  rivune.setJellyfinEnabled(true);
  const jellyfinPassword = (generation: number) => `rivune_jfa_${String(generation).padStart(43, "A")}`;
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  let rejectCredentialRead = true;
  await page.route(/\/api\/v1\/profiles\/alice\/jellyfin-credential$/, async (route) => {
    if (route.request().method() === "GET" && rejectCredentialRead) {
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "temporarily_unavailable", message: "Credential status is temporarily unavailable." } }) });
      return;
    }
    await route.fallback();
  });
  await openSettings(page, "connections");

  const scope = page.getByRole("combobox", { name: "Switch scope" });
  const panel = page.locator('[data-jellyfin-profile="alice"]');
  await expect(panel).toBeVisible();
  rejectCredentialRead = false;
  await panel.getByRole("button", { name: "Try again" }).click();
  await expect(panel).toContainText("This app password grants access only to this profile.");
  await expect(panel.getByRole("textbox", { name: "Username" })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Add" })).toBeVisible();

  await selectOption(scope, "server");
  await page.locator('[data-settings-section="connections"]').click();
  await expect(page.getByRole("checkbox", { name: "Jellyfin" })).toBeChecked();
  await expect(page.locator(".settings-group .jellyfin-access")).toHaveCount(0);

  await selectOption(scope, "alice");
  await page.locator('[data-settings-section="connections"]').click();
  let releaseCreate!: () => void;
  const createGate = new Promise<void>((resolve) => { releaseCreate = resolve; });
  await page.route(/\/api\/v1\/profiles\/alice\/jellyfin-credential$/, async (route) => {
    if (route.request().method() === "POST") await createGate;
    await route.fallback();
  });
  const guardedURL = page.url();
  await page.evaluate((currentURL) => {
    const state = window.history.state;
    window.history.replaceState(state, "", "/#admin?tab=settings&section=appearance");
    window.history.pushState(state, "", currentURL);
  }, guardedURL);
  await panel.getByRole("button", { name: "Add" }).click();
  const secretDialog = page.getByRole("dialog");
  await expect(secretDialog.locator(".settings-skeleton")).toBeVisible();
  await expect(secretDialog.getByRole("button", { name: "Close" })).toHaveCount(0);
  await expect(page).toHaveURL(guardedURL);
  await page.goBack();
  await expect(page).toHaveURL(guardedURL);
  await expect(secretDialog.locator(".settings-skeleton")).toBeVisible();
  releaseCreate();
  await expect(secretDialog).toContainText("This password is shown only once");
  const username = secretDialog.getByRole("textbox", { name: "Username" });
  const password = secretDialog.getByRole("textbox", { name: "Password" });
  const stableUsername = await username.inputValue();
  expect(stableUsername).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$/);
  await expect(password).toHaveValue(jellyfinPassword(1));

  await secretDialog.getByRole("button", { name: "Copy: Username" }).click();
  expect(await page.evaluate(async () => navigator.clipboard.readText())).toBe(stableUsername);
  await secretDialog.getByRole("button", { name: "Copy: Password" }).click();
  expect(await page.evaluate(async () => navigator.clipboard.readText())).toBe(jellyfinPassword(1));
  await secretDialog.locator(".modal-actions").getByRole("button", { name: "Close" }).click();

  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(panel.getByRole("textbox", { name: "Username" })).toHaveValue(stableUsername);
  await expect(panel.getByRole("textbox", { name: "Password" })).toHaveCount(0);
  const persistedBrowserState = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(persistedBrowserState).not.toContain("rivune_jfa_");
  await expect(panel).toContainText("Enabled");

  await panel.getByRole("button", { name: "Refresh" }).click();
  const rotateDialog = page.getByRole("dialog");
  await expect(rotateDialog).toContainText("immediately invalidates the current password and all Jellyfin sessions");
  await rotateDialog.getByRole("button", { name: "Refresh" }).click();
  await expect(secretDialog.getByRole("textbox", { name: "Username" })).toHaveValue(stableUsername);
  await expect(secretDialog.getByRole("textbox", { name: "Password" })).toHaveValue(jellyfinPassword(2));
  await secretDialog.locator(".modal-actions").getByRole("button", { name: "Close" }).click();

  await panel.getByRole("button", { name: "Remove" }).click();
  const revokeDialog = page.getByRole("dialog");
  await expect(revokeDialog).toContainText("immediately invalidates the password and all Jellyfin sessions");
  await revokeDialog.getByRole("button", { name: "Remove" }).click();
  await expect(panel).toContainText("Disabled");
  await expect(panel.getByRole("textbox", { name: "Username" })).toHaveValue(stableUsername);
  await expect(panel.getByRole("button", { name: "Add" })).toBeVisible();

  await panel.getByRole("button", { name: "Add" }).click();
  await expect(secretDialog.getByRole("textbox", { name: "Username" })).toHaveValue(stableUsername);
  await expect(secretDialog.getByRole("textbox", { name: "Password" })).toHaveValue(jellyfinPassword(3));
  expect(rivune.matching("/api/v1/profiles/alice/jellyfin-credential", "POST")).toHaveLength(2);
  expect(rivune.matching("/api/v1/profiles/alice/jellyfin-credential/rotate", "POST")).toHaveLength(1);
  expect(rivune.matching("/api/v1/profiles/alice/jellyfin-credential", "DELETE")).toHaveLength(1);
});

test("ambiguous Jellyfin credential creation reconciles status and offers rotation", async ({ page, rivune }) => {
  rivune.setJellyfinEnabled(true);
  rivune.failNextJellyfinCredentialCreateAfterCommit();
  await openSettings(page, "connections");

  const panel = page.locator('[data-jellyfin-profile="alice"]');
  await panel.getByRole("button", { name: "Add" }).click();
  await expect(panel).toContainText("password was not received");
  await expect(panel).toContainText("Enabled");
  await expect(panel.getByRole("button", { name: "Refresh" })).toBeEnabled();
  await expect(panel.getByRole("button", { name: "Add" })).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("credential issuance actions reflect direct profile access", async ({ page, rivune }) => {
  rivune.setJellyfinEnabled(true);
  await page.route(/\/api\/v1\/profiles\/alice\/jellyfin-credential$/, async (route) => {
    if (route.request().method() !== "GET") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active: true,
        canIssue: false,
        generation: 1,
        username: "20000000-0000-4000-8000-000000000001",
        createdAt: "2026-08-07T12:00:00Z",
      }),
    });
  });
  await openSettings(page, "connections");

  const panel = page.locator('[data-jellyfin-profile="alice"]');
  await expect(panel).toContainText("requires direct access to this profile");
  await expect(panel.getByRole("button", { name: "Refresh" })).toBeDisabled();
  await expect(panel.getByRole("button", { name: "Remove" })).toBeEnabled();
});

test("active profile can manage its Jellyfin credential without manager permission", async ({ page, rivune }) => {
  rivune.setJellyfinEnabled(true);
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  await rivune.configureGlobalAdmin(page, "bob");
  await openSettings(page, "connections");

  await expect(page.getByRole("combobox", { name: "Switch scope" })).toHaveCount(0);
  const panel = page.locator('[data-jellyfin-profile="bob"]');
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("button", { name: "Add" })).toBeEnabled();
});

test("global administrator assigns transcoding per profile", async ({ page, rivune }) => {
  await openSettings(page, "playback");

  const scope = page.getByRole("combobox", { name: "Switch scope" });
  const transcoding = page.getByLabel("Transcoding");
  await expect(transcoding).toBeVisible();
  await selectOption(scope, "bob");
  await expect(page.getByRole("heading", { name: "Bob preferences" })).toBeVisible();
  await selectOption(transcoding, "disabled");
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/bob/settings", "PATCH");
  expect(request.body).toMatchObject({ transcoding: "disabled" });
});

test("viewer cannot change transcoding and unrelated saves omit its policy", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  await rivune.configureGlobalAdmin(page, "bob");
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true }).click();
  await page.locator('[data-settings-section="playback"]').click();

  await expect(page.getByLabel("Transcoding")).toHaveCount(0);
  await expect(page.locator(".settings-transcoding-state")).toBeVisible();

  await page.locator('[data-settings-section="appearance"]').click();
  await page.getByRole("checkbox", { name: "Interface animations" }).uncheck();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/bob/settings", "PATCH");
  expect(request.body).toMatchObject({ animationsEnabled: false });
  expect(request.body).not.toHaveProperty("transcoding");
});

test("appearance choices expose effective inheritance and persist explicit overrides", async ({ page, rivune }) => {
  await openSettings(page, "appearance");

  const theme = page.getByRole("radiogroup", { name: "Theme" });
  const density = page.getByRole("radiogroup", { name: "Card density" });
  await expect(theme.getByRole("radio", { name: "Follow this device" })).toBeVisible();
  await expect(density.getByRole("radio", { name: "Comfortable" })).toBeVisible();
  await expect(theme.locator(".is-effective.is-inherited")).toHaveCount(1);
  await expect(density.locator(".is-effective.is-inherited")).toHaveCount(1);

  await theme.getByRole("radio", { name: "Dark" }).check();
  await density.getByRole("radio", { name: "Compact" }).check();
  await expect(theme.getByRole("radio", { name: "Dark" })).toBeChecked();
  await expect(density.getByRole("radio", { name: "Compact" })).toBeChecked();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(request.body).toMatchObject({ theme: "dark", cardDensity: "compact" });
});

test("subtitle controls update the preview before preferences are saved", async ({ page, rivune: _rivune }) => {
  await openSettings(page, "subtitles");

  const preview = page.locator(".subtitle-preview");
  await expect(preview).toBeVisible();
  await expect(preview.locator(".subtitle-preview__caption")).not.toBeEmpty();

  await page.getByRole("slider", { name: "Subtitle size" }).fill("150");
  await page.getByLabel("Subtitle text color").fill("#00ff00");
  await page.getByRole("slider", { name: "Background opacity" }).fill("75");

  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-scale").trim())).toBe("150");
  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-color").trim())).toBe("#00FF00");
  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-opacity").trim())).toBe("75");
  await expect(page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" })).toBeEnabled();
});

test("runtime settings render requested and active values and save through the dirty action flow", async ({ page, rivune }) => {
  rivune.setHardwareRestartPending("nvenc", "software");
  await openSettings(page);
  await selectOption(page.getByRole("combobox", { name: "Switch scope" }), "server");
  await page.locator('[data-settings-section="runtime"]').click();

  const runtime = page.locator("#settings-section-runtime");
  await expect(runtime.getByLabel("Timezone")).toHaveValue("America/Toronto");
  await expect(runtime.getByLabel("Jellyfin debug logging")).toBeChecked();
  await expect(runtime.getByRole("combobox", { name: "Hardware acceleration" })).toHaveAttribute("data-value", "nvenc");
  await expect(runtime.getByLabel("Maximum transcode bitrate")).toHaveValue("18000");
  await expect(runtime.getByLabel("Temporary media quota")).toHaveValue("24576");
  await expect(runtime.getByLabel("Artwork quota")).toHaveValue("8192");

  const pendingRows = runtime.locator(".runtime-setting").filter({ has: page.locator(".is-pending") });
  await expect(pendingRows).toHaveCount(1);
  await expect(pendingRows).toContainText("Hardware acceleration");
  await expect(pendingRows.locator(".runtime-setting-state")).toContainText("Requested · NVIDIA NVENC");
  await expect(pendingRows.locator(".runtime-setting-state")).toContainText("Active · Software");
  await expect(pendingRows.locator(".is-pending")).toHaveText("Restart required");
  await expect(runtime.locator(".runtime-setting:not(:has(.is-pending)) .runtime-setting-state")).toHaveCount(5);

  await runtime.getByLabel("Maximum transcode bitrate").fill("20000");
  const saveBar = page.locator(".settings-save-bar");
  await expect(saveBar.getByRole("status")).toContainText("Unsaved changes");
  await saveBar.getByRole("button", { name: "Save preferences" }).click();
  const request = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(request.body).toEqual({ transcodeMaxBitrateKbps: 20000 });
  await expect(runtime.getByLabel("Maximum transcode bitrate")).toHaveValue("20000");
  await expect(runtime.locator(".is-pending")).toHaveCount(1);
  await expect(pendingRows.locator(".is-pending")).toHaveText("Restart required");
});

test("integration credentials stay write-only and preserve omission versus null", async ({ page, rivune }) => {
  await openSettings(page);
  await selectOption(page.getByRole("combobox", { name: "Switch scope" }), "server");
  const loadResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/settings/integrations" && response.request().method() === "GET");
  await page.locator('[data-settings-section="integrations"]').click();
  const loadedIntegrations = await loadResponse;
  expect(loadedIntegrations.headers()["cache-control"]).toBe("no-store");
  const loadedBody = await loadedIntegrations.json() as { credentials: Record<string, Record<string, unknown>> };
  for (const status of Object.values(loadedBody.credentials)) expect(Object.keys(status).sort()).toEqual(["configured", "updatedAt"]);

  const integrations = page.locator("#settings-section-integrations");
  await expect(integrations.getByRole("heading", { name: "Integrations" })).toBeVisible();
  const credentialNames = ["tmdbAccessToken", "fanartApiKey", "mdblistApiKey", "tvdbApiKey", "tvdbPin", "traktClientId", "traktClientSecret", "simklClientId"];
  for (const name of credentialNames) {
    await expect(integrations.locator(`#integration-${name}`)).toHaveValue("");
    await expect(integrations.locator(`#integration-${name}`)).toHaveAttribute("type", "password");
    await expect(integrations.locator(`#integration-${name}`)).toHaveAttribute("autocomplete", "new-password");
  }
  const tmdbRow = integrations.locator(".configuration-credential").filter({ has: page.locator("#integration-tmdbAccessToken") });
  await expect(tmdbRow).toContainText("Configured");

  const submittedSecret = "fixture-submitted-secret-never-returned";
  await integrations.locator("#integration-tmdbAccessToken").fill(submittedSecret);
  const saveResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/settings/integrations" && response.request().method() === "PATCH");
  await integrations.getByRole("button", { name: "Save integration changes" }).click();
  const firstRequest = await rivune.waitForRequest("/api/v1/settings/integrations", "PATCH");
  expect(firstRequest.body).toEqual({ tmdbAccessToken: submittedSecret });
  const firstResponse = await saveResponse;
  expect(firstResponse.headers()["cache-control"]).toBe("no-store");
  expect(await firstResponse.text()).not.toContain(submittedSecret);
  for (const name of credentialNames) await expect(integrations.locator(`#integration-${name}`)).toHaveValue("");
  expect(await page.content()).not.toContain(submittedSecret);
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain(submittedSecret);

  await tmdbRow.getByRole("button", { name: "Remove TMDB access token" }).click();
  await expect(tmdbRow.getByRole("status")).toContainText("will be removed");
  await expect(tmdbRow.getByRole("button", { name: "Undo removal of TMDB access token" })).toBeVisible();
  const removeResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/settings/integrations" && response.request().method() === "PATCH");
  await integrations.getByRole("button", { name: "Save integration changes" }).click();
  const clearedResponse = await removeResponse;
  expect(clearedResponse.headers()["cache-control"]).toBe("no-store");
  expect(await clearedResponse.text()).not.toContain(submittedSecret);
  await expect.poll(() => rivune.matching("/api/v1/settings/integrations", "PATCH").length).toBe(2);
  expect(rivune.matching("/api/v1/settings/integrations", "PATCH").at(-1)?.body).toEqual({ tmdbAccessToken: null });
  await expect(tmdbRow).toContainText("Not configured");
  await expect(integrations.locator("#integration-tmdbAccessToken")).toHaveValue("");
  expect(await page.content()).not.toContain(submittedSecret);
});

test("configuration history renders change metadata without snapshot values and fixtures paginate by key", async ({ page, rivune: _rivune }) => {
  await openSettings(page);
  await selectOption(page.getByRole("combobox", { name: "Switch scope" }), "server");
  await page.locator('[data-settings-section="audit"]').click();

  const audit = page.locator("#settings-section-audit");
  await expect(audit.getByRole("heading", { name: "Configuration history" })).toBeVisible();
  const events = audit.locator(".configuration-audit__events > li");
  await expect(events).toHaveCount(2);
  await expect(events.nth(0)).toContainText("Server settings updated");
  await expect(events.nth(0)).toContainText("Changed fields: hardwareAcceleration");
  await expect(events.nth(0)).toContainText("Changed by Administrator · Revision 12");
  await expect(events.nth(1)).toContainText("Integrations updated");
  const integrationChanges = events.nth(1).locator(".configuration-audit__changes > li");
  await expect(integrationChanges).toHaveCount(3);
  await expect(integrationChanges.nth(0)).toContainText("TMDB access token");
  await expect(integrationChanges.nth(0)).toContainText("Configured");
  await expect(integrationChanges.nth(1)).toContainText("TVDB API key");
  await expect(integrationChanges.nth(1)).toContainText("Configured");
  await expect(integrationChanges.nth(2)).toContainText("TVDB PIN");
  await expect(integrationChanges.nth(2)).toContainText("Configured");
  const renderedAudit = await audit.innerText();
  expect(renderedAudit).not.toContain("software");
  expect(renderedAudit).not.toContain("true");
  expect(renderedAudit).not.toContain("false");

  const pageOne = await page.evaluate(async () => {
    const profileContext = sessionStorage.getItem("rivune.profile.context") ?? "";
    const response = await fetch("/api/v1/settings/audit?limit=1", { headers: { Authorization: "Bearer fixture-access", "X-Rivune-Profile-Context": profileContext } });
    return { status: response.status, cacheControl: response.headers.get("cache-control"), body: await response.json() as { events: Array<{ id: number }>; nextCursor: number | null } };
  });
  expect(pageOne.status).toBe(200);
  expect(pageOne.cacheControl).toBe("no-store");
  expect(pageOne.body.events.map((event) => event.id)).toEqual([120]);
  expect(pageOne.body.nextCursor).toBe(120);

  const pageTwo = await page.evaluate(async (cursor) => {
    const profileContext = sessionStorage.getItem("rivune.profile.context") ?? "";
    const response = await fetch(`/api/v1/settings/audit?limit=1&cursor=${cursor}`, { headers: { Authorization: "Bearer fixture-access", "X-Rivune-Profile-Context": profileContext } });
    return await response.json() as { events: Array<{ id: number }>; nextCursor: number | null };
  }, pageOne.body.nextCursor);
  expect(pageTwo.events.map((event) => event.id)).toEqual([110]);
  expect(pageTwo.nextCursor).toBeNull();
});

test("category-scoped administrators cannot see or call integration and audit management", async ({ page, rivune }) => {
  await rivune.configureCategoryScope(page, CATEGORY_IDS.household);
  await openSettings(page);

  await expect(page.locator('[data-settings-section="integrations"]')).toHaveCount(0);
  await expect(page.locator('[data-settings-section="audit"]')).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Integrations" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Configuration history" })).toHaveCount(0);
  expect(rivune.matching("/api/v1/settings/integrations", "GET")).toHaveLength(0);
  expect(rivune.matching("/api/v1/settings/audit", "GET")).toHaveLength(0);

  const statuses = await page.evaluate(async () => {
    const profileContext = sessionStorage.getItem("rivune.profile.context") ?? "";
    const headers = { Authorization: "Bearer fixture-access", "X-Rivune-Profile-Context": profileContext };
    return Promise.all([fetch("/api/v1/settings/integrations", { headers }), fetch("/api/v1/settings/audit", { headers })]).then((responses) => responses.map((response) => response.status));
  });
  expect(statuses).toEqual([403, 403]);
});

test("mobile configuration panels stay within the viewport with keyboard-accessible labels", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSettings(page);
  await selectOption(page.getByRole("combobox", { name: "Switch scope" }), "server");
  const navigation = page.locator(".settings-navigation");

  await navigation.locator('[data-settings-section="runtime"]').click();
  const runtime = page.locator("#settings-section-runtime");
  await expect(runtime.getByLabel("Timezone")).toBeVisible();
  await expect(runtime.getByRole("combobox", { name: "Hardware acceleration" })).toHaveAccessibleName("Hardware acceleration");
  expect(await runtime.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await navigation.locator('[data-settings-section="integrations"]').click();
  const integrations = page.locator("#settings-section-integrations");
  const tmdbInput = integrations.locator("#integration-tmdbAccessToken");
  const tmdbRemove = integrations.getByRole("button", { name: "Remove TMDB access token" });
  await expect(tmdbInput).toHaveAccessibleName("Replace TMDB access token");
  await tmdbInput.focus();
  await tmdbInput.press("Tab");
  await expect(tmdbRemove).toBeFocused();
  expect(await integrations.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await navigation.locator('[data-settings-section="audit"]').click();
  const audit = page.locator("#settings-section-audit");
  await expect(audit.getByRole("heading", { name: "Configuration history" })).toBeVisible();
  expect(await audit.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("mobile settings contain horizontal category overflow without widening the page", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSettings(page);

  const workspace = page.locator(".settings-workspace");
  const navigation = workspace.locator(".settings-navigation");
  const content = workspace.locator(".settings-content");
  const navigationList = navigation.getByRole("navigation", { name: "Settings" });
  const navigationBounds = await navigation.boundingBox();
  const contentBounds = await content.boundingBox();
  const workspaceBounds = await workspace.boundingBox();
  expect(navigationBounds).not.toBeNull();
  expect(contentBounds).not.toBeNull();
  expect(workspaceBounds).not.toBeNull();
  expect(contentBounds!.y).toBeGreaterThanOrEqual(navigationBounds!.y + navigationBounds!.height);
  expect(workspaceBounds!.x + workspaceBounds!.width).toBeLessThanOrEqual(390);
  expect(await navigationList.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  const appearance = navigation.locator('[data-settings-section="appearance"]');
  const playback = navigation.locator('[data-settings-section="playback"]');
  await appearance.focus();
  await appearance.press("ArrowRight");
  await expect(playback).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const mobileCategoryMetrics = await navigation.locator("[data-settings-section]").evaluateAll((items) => items.map((item) => {
    const title = item.querySelector<HTMLElement>(".settings-navigation__item-title");
    return {
      height: item.getBoundingClientRect().height,
      titleFits: Boolean(title && title.scrollWidth <= title.clientWidth && title.scrollHeight <= title.clientHeight),
    };
  }));
  expect(mobileCategoryMetrics.every(({ height, titleFits }) => height <= 64 && titleFits)).toBe(true);
  await expect(navigation.locator('[data-settings-section="language"]')).toHaveAccessibleName("Language & metadata");
  await expect(navigation.locator('[data-settings-section="subtitles"]')).toHaveAccessibleName("Language & subtitles");
  await expect(navigation.locator('[data-settings-section="connections"]')).toHaveAccessibleName("Connections");

  await page.getByRole("checkbox", { name: "Interface animations" }).uncheck();
  const saveBarBounds = await page.locator(".settings-save-bar").boundingBox();
  expect(saveBarBounds).not.toBeNull();
  expect(saveBarBounds!.x + saveBarBounds!.width).toBeLessThanOrEqual(390);
  expect(saveBarBounds!.y + saveBarBounds!.height).toBeLessThanOrEqual(844);
});

test("category rail supports roving keyboard navigation", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const navigation = page.locator(".settings-navigation");
  const appearance = navigation.locator('[data-settings-section="appearance"]');
  const playback = navigation.locator('[data-settings-section="playback"]');
  const connections = navigation.locator('[data-settings-section="connections"]');
  await appearance.focus();
  await appearance.press("ArrowDown");
  await expect(playback).toBeFocused();
  await playback.press("End");
  await expect(connections).toBeFocused();
  await connections.press("Home");
  await expect(appearance).toBeFocused();

  await appearance.press("ArrowDown");
  await playback.press("Enter");
  await expect(playback).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-playback")).toBeVisible();
});
