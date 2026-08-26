import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";
import { selectOption, selectOptions } from "./helpers/select";

test("maintenance holds viewer profiles at selection and lets a manager profile enter", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  rivune.setMaintenance(true, "Upgrading the media library");

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await expect(page.getByText("Upgrading the media library")).toBeVisible();
  const maintenanceNotice = page.locator(".notice--warning");
  await expect(maintenanceNotice).toBeVisible();
  await expect(maintenanceNotice.locator(".lucide-triangle-alert")).toBeVisible();
  await expect.poll(() => maintenanceNotice.evaluate((element) => getComputedStyle(element).backgroundImage)).toContain("linear-gradient");
  await expect(page.getByRole("button", { name: /Bob/ })).toBeDisabled();
  await expect(page.getByRole("heading", { name: "Alice's Slow Shelf" })).toHaveCount(0);

  await page.getByRole("button", { name: "Alice" }).click();

  await expect(page.getByRole("heading", { name: "Alice's Slow Shelf" })).toBeVisible();
});

test("maintenance profile gate shows its default message when none is configured", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  rivune.setMaintenance(true);

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await expect(page.getByText("Only administrator profiles can be opened until maintenance is complete.")).toBeVisible();
  await expect(page.getByRole("button", { name: /Bob/ })).toBeDisabled();
});

test("administrator can update the global maintenance settings", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();
  await expect(page.getByRole("heading", { name: "Operations" })).toBeVisible();

  await page.getByLabel("Block member access").check();
  await page.getByLabel("Public message").fill("Back after the upgrade");
  await page.getByRole("button", { name: "Save maintenance settings" }).click();

  const request = await rivune.waitForRequest("/api/v1/settings/maintenance", "PUT");
  expect(request.body).toEqual({ enabled: true, message: "Back after the upgrade" });
  await expect(page.getByText("Maintenance settings updated.")).toBeVisible();
});

test("interface language inherits server defaults and supports profile overrides", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  const scope = page.locator(".settings-profile-picker").getByRole("combobox");
  const savePreferences = page.locator(".settings-save-bar").getByRole("button").last();
  await selectOption(scope, "server");
  await page.locator('[data-settings-section="language"]').click();

  const language = page.locator('[role="combobox"][name="interfaceLanguage"]');
  await expect.poll(async () => (await selectOptions(language)).map((option) => option.value)).toEqual([
    "", "en", "fr", "de", "es", "it", "pt-BR",
  ]);
  await selectOption(language, "fr");
  await savePreferences.click();

  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toMatchObject({ interfaceLanguage: "fr" });
  expect(serverRequest.body).not.toHaveProperty("notificationsEnabled");
  expect(serverRequest.body).not.toHaveProperty("notificationDurationSeconds");
  expect(serverRequest.body).not.toHaveProperty("notificationPollIntervalSeconds");
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await expect(page.getByRole("navigation", { name: "Navigation principale" }).getByRole("button", { name: "Accueil" })).toBeVisible();
  await expect(language).toBeVisible();

  await selectOption(scope, "alice");
  await page.locator('[data-settings-section="language"]').click();
  await expect(language).toHaveAttribute("data-value", "");
  const effectiveRequestCount = rivune.matching("/api/v1/profiles/alice/settings/effective", "GET").length;
  rivune.delayNextEffectiveSettings(250);
  const delayedEffectiveSettings = page.waitForResponse((response) => {
    const request = response.request();
    return new URL(response.url()).pathname === "/api/v1/profiles/alice/settings/effective" && request.method() === "GET";
  });
  await selectOption(language, "de");
  await savePreferences.click();

  const firstProfileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(firstProfileRequest.body).toMatchObject({ interfaceLanguage: "de" });
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings/effective", "GET").length).toBe(effectiveRequestCount + 1);

  await selectOption(language, "es");
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(2);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toMatchObject({ interfaceLanguage: "es" });
  await expect(page.locator("html")).toHaveAttribute("lang", "es");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await delayedEffectiveSettings;
  await expect(page.locator("html")).toHaveAttribute("lang", "es");

  await expect(language).toBeVisible();
  await selectOption(language, "");
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(3);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toMatchObject({ interfaceLanguage: null });
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await page.getByRole("button", { name: /Changer de profil/ }).first().click();
  await expect(page.getByRole("heading", { name: "Qui regarde ?" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
});

test("server transcoding disable confirms active sessions and the global veto wins over profile policy", async ({ page, rivune }) => {
  const activity = {
    summary: { activeSessions: 2, activeJobs: 1, processingSlots: 1, processingLimit: 3, storageBytes: 0, storageLimitBytes: 1_073_741_824 },
    diagnostics: { ffmpegVersion: "7.1", ffprobeVersion: "7.1", hardwareAcceleration: "software", videoEncoder: "h264", preferredVideoCodec: "auto", encodeCodecs: ["h264"], decodeCodecs: ["h264"], qualityPreset: "balanced", hardwareToneMap: false, toneMapBackend: "software", transcodeThreads: 4, maximumReadRate: 1.5, totals: { started: 1, succeeded: 0, failed: 0, softwareFallbacks: 0 }, pools: { process: { active: 1, limit: 3 }, probe: { active: 0, limit: 3 }, subtitle: { active: 0, limit: 3 }, trickplay: { active: 0, limit: 1 } } },
    sessions: [
      { id: "transcoded", title: "Converted feature", mediaType: "movie", mode: "transcode_audio", username: "fixture-owner", profileId: "alice", profile: "Alice", device: "Web", platform: "Browser", processing: true, positionSeconds: 60, durationSeconds: 600, createdAt: "2026-07-31T12:00:00Z", lastSeenAt: "2026-07-31T12:00:00Z", expiresAt: "2026-07-31T13:00:00Z" },
      { id: "direct", title: "Direct feature", mediaType: "movie", mode: "direct", username: "fixture-owner", profileId: "alice", profile: "Alice", device: "TV", platform: "Browser", processing: false, positionSeconds: 30, durationSeconds: 600, createdAt: "2026-07-31T12:00:00Z", lastSeenAt: "2026-07-31T12:00:00Z", expiresAt: "2026-07-31T13:00:00Z" },
    ],
    jobs: [],
  };
  await page.route("**/api/v1/playback/activity", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify(activity) }));
  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  await page.locator('[data-settings-section="playback"]').click();
  const scope = page.locator(".settings-profile-picker").getByRole("combobox");
  const inheritedPolicy = page.getByRole("combobox", { name: "Transcoding" });
  await expect(inheritedPolicy).toHaveAttribute("data-value", "inherit");
  await expect(page.getByText("Transcoding is available as a last resort for this profile.")).toBeVisible();
  await selectOption(scope, "server");
  await page.locator('[data-settings-section="transcoding"]').click();

  const allowTranscoding = page.getByRole("checkbox", { name: /Allow transcoding/ });
  await expect(allowTranscoding).toBeChecked();
  await allowTranscoding.uncheck();
  const savePreferences = page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" });
  await savePreferences.click();

  const confirm = page.getByRole("dialog");
  await expect(confirm.getByRole("heading", { name: "Disable server transcoding?" })).toBeVisible();
  await expect(confirm.getByText("1 transcoded session is active. It will continue, but no new transcoding will start.")).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/settings", "PATCH").length).toBe(0);
  await page.keyboard.press("Escape");
  await expect(confirm).toHaveCount(0);

  await savePreferences.click();
  await page.getByRole("button", { name: "Disable transcoding" }).click();
  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toMatchObject({ allowTranscoding: false });
  await expect(page.getByText("1 transcoded session is still active. Stop it from Playback activity if needed.")).toBeVisible();
  expect(rivune.requests.filter((request) => request.method === "DELETE" && request.pathname.startsWith("/api/v1/playback/sessions/"))).toHaveLength(0);

  await selectOption(scope, "alice");
  await page.locator('[data-settings-section="playback"]').click();
  const profilePolicy = page.getByRole("combobox", { name: "Transcoding" });
  await expect(profilePolicy).toHaveAttribute("data-value", "inherit");
  await expect.poll(async () => (await selectOptions(profilePolicy)).map((option) => option.label)).toEqual(["Inherit server setting", "Enabled", "Disabled"]);
  await selectOption(profilePolicy, "enabled");
  await expect(page.getByText("The server setting takes priority, so this profile cannot enable transcoding.")).toBeVisible();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();
  const profileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(profileRequest.body).toMatchObject({ transcoding: "enabled" });
});
test("cast member limits persist in server and profile scopes", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  await page.locator('[data-settings-section="playback"]').click();
  const scope = page.locator(".settings-profile-picker").getByRole("combobox");
  const savePreferences = page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" });
  const mode = page.getByRole("combobox", { name: "Cast member limit mode" });
  const limit = page.getByRole("spinbutton", { name: "Maximum cast members" });

  await expect(mode).toHaveAttribute("data-value", "inherit");
  await expect.poll(async () => (await selectOptions(mode)).map((option) => option.label)).toEqual(["Inherit server setting", "Custom value"]);
  await expect(limit).toBeDisabled();
  await expect(limit).toHaveValue("20");
  await expect(limit).toHaveAttribute("max", "20");

  await selectOption(scope, "server");
  await page.locator('[data-settings-section="playback"]').click();
  await expect(mode).toHaveCount(0);
  await expect(limit).toBeEnabled();
  await expect(limit).toHaveValue("20");
  await expect(limit).toHaveAttribute("min", "1");
  await expect(limit).toHaveAttribute("max", "100");
  await limit.fill("12");
  await savePreferences.click();
  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toMatchObject({ maximumCastMembers: 12 });

  await selectOption(scope, "alice");
  await page.locator('[data-settings-section="playback"]').click();
  await expect(mode).toHaveAttribute("data-value", "inherit");
  await expect(limit).toBeDisabled();
  await expect(limit).toHaveValue("12");
  await expect(limit).toHaveAttribute("max", "12");
  await selectOption(mode, "custom");
  await expect(limit).toBeEnabled();
  await limit.fill("13");
  await expect(limit).toHaveValue("12");
  await limit.fill("8");
  await savePreferences.click();
  const customRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(customRequest.body).toMatchObject({ maximumCastMembers: 8 });

  await selectOption(mode, "inherit");
  await expect(limit).toBeDisabled();
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(2);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toMatchObject({ maximumCastMembers: null });
});

test("direct title limits persist in server and profile appearance preferences", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  await page.locator('[data-settings-section="appearance"]').click();
  const scope = page.locator(".settings-profile-picker").getByRole("combobox");
  const savePreferences = page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" });
  const mode = page.getByRole("combobox", { name: "Direct title limit mode" });
  const limit = page.locator('input[name="maximumDirectTitles"]');

  await expect(mode).toHaveAttribute("data-value", "inherit");
  await expect(limit).toBeDisabled();
  await expect(limit).toHaveValue("20");
  await expect(limit).toHaveAttribute("max", "20");
  await selectOption(mode, "custom");
  await savePreferences.click();
  const initialProfileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(initialProfileRequest.body).toEqual({ maximumDirectTitles: 20 });

  await selectOption(scope, "server");
  await page.locator('[data-settings-section="appearance"]').click();
  await expect(mode).toHaveCount(0);
  await expect(limit).toBeEnabled();
  await expect(limit).toHaveAttribute("min", "1");
  await expect(limit).toHaveAttribute("max", "100");
  await limit.fill("6");
  await savePreferences.click();
  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toEqual({ maximumDirectTitles: 6 });

  await selectOption(scope, "alice");
  await page.locator('[data-settings-section="appearance"]').click();
  await expect(mode).toHaveAttribute("data-value", "custom");
  await expect(limit).toBeEnabled();
  await expect(limit).toHaveValue("6");
  await expect(limit).toHaveAttribute("max", "6");
  await page.getByRole("checkbox", { name: "Interface animations" }).click();
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(2);
  const unrelatedRequest = rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1);
  expect(unrelatedRequest?.body).toEqual({ animationsEnabled: false });

  await limit.fill("7");
  await expect(limit).toHaveValue("6");
  await limit.fill("3");
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(3);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toEqual({ maximumDirectTitles: 3 });

  await selectOption(mode, "inherit");
  await expect(limit).toBeDisabled();
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(4);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toEqual({ maximumDirectTitles: null });
});


test("the selected interface language localizes Home copy", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  await selectOption(page.locator(".settings-profile-picker").getByRole("combobox"), "server");
  await page.locator('[data-settings-section="language"]').click();

  await selectOption(page.locator('[role="combobox"][name="interfaceLanguage"]'), "fr");
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();
  await rivune.waitForRequest("/api/v1/settings", "PATCH");
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");

  await page.getByRole("navigation", { name: "Navigation principale" }).getByRole("button", { name: "Accueil", exact: true }).click();
  await expect(page.getByText("Continuer à regarder", { exact: true })).toBeVisible();
  await expect(page.getByText("Continue Watching", { exact: true })).toHaveCount(0);
});

test("viewer settings use the full desktop workspace", async ({ page, rivune }) => {
  await rivune.configureCategoryScope(page, CATEGORY_IDS.kids);
  await page.setViewportSize({ width: 1568, height: 899 });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true }).click();

  const workspace = page.locator(".settings-workspace");
  const navigation = workspace.locator(".settings-navigation");
  const content = workspace.locator(".settings-content");
  await expect(page.locator(".admin-layout--preferences .admin-tabs")).toHaveCount(0);
  await expect(workspace).toBeVisible();
  await expect(navigation).toBeVisible();
  await expect(content).toBeVisible();
  await expect.poll(async () => (await workspace.boundingBox())?.width ?? 0).toBeGreaterThan(1100);
  await expect.poll(() => navigation.locator("[data-settings-section]").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("data-settings-section")))).toEqual(["appearance", "playback", "language", "subtitles", "connections"]);
  await expect(content.getByRole("heading", { name: "Device notifications" })).toHaveCount(0);
});

test("a global administrator exposes server administration through the shared Settings destination only for its manager profile", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  await page.goto("/");

  const mainNavigation = page.getByRole("navigation", { name: "Main navigation" });
  await expect(mainNavigation.getByRole("button", { name: "Settings", exact: true })).toHaveCount(1);
  await expect(mainNavigation.getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
  await mainNavigation.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Categories\b/ })).toBeVisible();

  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await page.getByRole("button", { name: "Bob Profile" }).click();
  await expect(page.getByRole("heading", { name: "Bob's Fresh Picks" })).toBeVisible();
  await expect(mainNavigation.getByRole("button", { name: "Settings", exact: true })).toHaveCount(1);
  await expect(mainNavigation.getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
  await mainNavigation.getByRole("button", { name: "Settings", exact: true }).click();

  await expect(page.locator(".admin-layout--preferences .admin-tabs")).toHaveCount(0);
  await expect(page.getByText("Profile access", { exact: true })).toBeVisible();
  await expect(page.locator(".settings-profile-picker")).toHaveCount(0);
  await expect(page.getByText("Server defaults", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Bob preferences" })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileNavigation = page.getByRole("navigation", { name: "Mobile navigation" });
  await expect(mobileNavigation.getByRole("button", { name: "Settings", exact: true })).toHaveCount(1);
  await expect(mobileNavigation.getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
});

test("tracking authorization survives provider slow-down and completes polling", async ({ page, rivune: _rivune }) => {
  let connected = false;
  let tokenAttempts = 0;
  let releaseSuccess = () => {};
  const successGate = new Promise<void>((resolve) => { releaseSuccess = resolve; });
  const status = () => ({
    provider: "trakt",
    configured: true,
    connected,
    syncWatched: true,
    syncProgress: true,
    syncLibrary: true,
    pendingItems: 0,
  });

  await page.route("**/api/v1/profiles/alice/tracking**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (pathname === "/api/v1/profiles/alice/tracking" && request.method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ providers: [status()] }) });
      return;
    }
    if (pathname === "/api/v1/profiles/alice/tracking/trakt/device-code" && request.method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "tracking-authorization-1",
          provider: "trakt",
          userCode: "ABCD-EFGH",
          verificationUrl: "https://fixtures.rivune.test/trakt",
          expiresAt: "2099-01-01T00:00:00Z",
          intervalSeconds: 0.05,
        }),
      });
      return;
    }
    if (pathname === "/api/v1/profiles/alice/tracking/trakt/device-code/tracking-authorization-1/token" && request.method() === "POST") {
      tokenAttempts += 1;
      if (tokenAttempts === 1) {
        await route.fulfill({
          status: 429,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "tracking_authorization_slow_down", message: "Try again shortly." } }),
        });
        return;
      }
      await successGate;
      connected = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(status()) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin");
  await page.locator('[data-admin-tab="settings"]').click();
  await page.locator('[data-settings-section="connections"]').click();
  await page.getByRole("button", { name: "Connect account" }).click();

  const authorizationCode = page.locator(".tracking-settings code");
  await expect(authorizationCode).toHaveText("ABCD-EFGH");
  await expect.poll(() => tokenAttempts).toBe(2);
  await expect(authorizationCode).toBeVisible();
  await expect(page.getByRole("button", { name: "Connect account" })).toBeDisabled();

  releaseSuccess();

  await expect(authorizationCode).toHaveCount(0);
  await expect(page.getByText("Connected to this profile")).toBeVisible();
  await expect(page.getByText("Trakt is now connected.")).toBeVisible();
});
