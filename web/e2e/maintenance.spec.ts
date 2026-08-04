import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";

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

test("interface language inherits server defaults and supports profile RTL overrides", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  const scope = page.locator(".settings-profile-picker select");
  const savePreferences = page.locator(".settings-save-bar").getByRole("button").last();
  await scope.selectOption("server");
  await page.locator('[data-settings-section="language"]').click();

  const language = page.locator('select[name="interfaceLanguage"]');
  await expect(language.locator("option")).toHaveCount(46);
  await expect.poll(() => language.locator("option").evaluateAll((options) => options.map((option) => (option as HTMLOptionElement).value))).toEqual([
    "", "en", "fr", "fr-CA", "es", "es-MX", "es-AR", "es-CL", "es-CO", "es-PE", "it", "de", "ru", "pt-PT", "pt-BR", "ar", "ja", "ko", "zh-CN", "zh-TW", "pl", "hy", "nl", "sv", "da", "fi", "nb", "tr", "uk", "cs", "sk", "ro", "el", "he", "hi", "id", "vi", "th", "hu", "bg", "hr", "sr", "ms", "ca", "fa", "fil",
  ]);
  await language.selectOption("fr");
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

  await scope.selectOption("alice");
  await expect(language).toHaveValue("");
  const effectiveRequestCount = rivune.matching("/api/v1/profiles/alice/settings/effective", "GET").length;
  rivune.delayNextEffectiveSettings(250);
  const delayedEffectiveSettings = page.waitForResponse((response) => {
    const request = response.request();
    return new URL(response.url()).pathname === "/api/v1/profiles/alice/settings/effective" && request.method() === "GET";
  });
  await language.selectOption("he");
  await savePreferences.click();

  const firstProfileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(firstProfileRequest.body).toMatchObject({ interfaceLanguage: "he" });
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings/effective", "GET").length).toBe(effectiveRequestCount + 1);

  await language.selectOption("ar");
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(2);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toMatchObject({ interfaceLanguage: "ar" });
  await expect(page.locator("html")).toHaveAttribute("lang", "ar");
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByRole("button", { name: "الرئيسية", exact: true })).toBeVisible();
  await delayedEffectiveSettings;
  await expect(page.locator("html")).toHaveAttribute("lang", "ar");

  await expect(language).toBeVisible();
  await language.selectOption("");
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
    diagnostics: { videoEncoder: "h264", hardwareToneMap: false },
    sessions: [
      { id: "transcoded", title: "Converted feature", mediaType: "movie", mode: "transcode_audio", username: "fixture-owner", profileId: "alice", profile: "Alice", device: "Web", platform: "Browser", processing: true, positionSeconds: 60, durationSeconds: 600, createdAt: "2026-07-31T12:00:00Z", lastSeenAt: "2026-07-31T12:00:00Z", expiresAt: "2026-07-31T13:00:00Z" },
      { id: "direct", title: "Direct feature", mediaType: "movie", mode: "direct", username: "fixture-owner", profileId: "alice", profile: "Alice", device: "TV", platform: "Browser", processing: false, positionSeconds: 30, durationSeconds: 600, createdAt: "2026-07-31T12:00:00Z", lastSeenAt: "2026-07-31T12:00:00Z", expiresAt: "2026-07-31T13:00:00Z" },
    ],
    jobs: [],
  };
  await page.route("**/api/v1/playback/activity", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify(activity) }));
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  await page.locator('[data-settings-section="playback"]').click();
  const scope = page.locator(".settings-profile-picker select");
  const inheritedPolicy = page.locator(".setting-control--transcoding select");
  await expect(inheritedPolicy).toHaveValue("inherit");
  await expect(page.getByText("Transcoding is available as a last resort for this profile.")).toBeVisible();
  await scope.selectOption("server");
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

  await scope.selectOption("alice");
  await page.locator('[data-settings-section="playback"]').click();
  const profilePolicy = page.locator(".setting-control--transcoding select");
  await expect(profilePolicy).toHaveValue("inherit");
  await expect(profilePolicy.locator("option")).toHaveText(["Inherit server setting", "Enabled", "Disabled"]);
  await profilePolicy.selectOption("enabled");
  await expect(page.getByText("The server setting takes priority, so this profile cannot enable transcoding.")).toBeVisible();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();
  const profileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(profileRequest.body).toMatchObject({ transcoding: "enabled" });
});
test("cast member limits persist in server and profile scopes", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  await page.locator('[data-settings-section="playback"]').click();
  const scope = page.locator(".settings-profile-picker select");
  const savePreferences = page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" });
  const mode = page.getByRole("combobox", { name: "Cast member limit mode" });
  const limit = page.getByRole("spinbutton", { name: "Maximum cast members" });

  await expect(mode).toHaveValue("inherit");
  await expect(mode.locator("option")).toHaveText(["Inherit server setting", "Custom value"]);
  await expect(limit).toBeDisabled();
  await expect(limit).toHaveValue("20");
  await expect(limit).toHaveAttribute("max", "20");

  await scope.selectOption("server");
  await expect(mode).toHaveCount(0);
  await expect(limit).toBeEnabled();
  await expect(limit).toHaveValue("20");
  await expect(limit).toHaveAttribute("min", "1");
  await expect(limit).toHaveAttribute("max", "100");
  await limit.fill("12");
  await savePreferences.click();
  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toMatchObject({ maximumCastMembers: 12 });

  await scope.selectOption("alice");
  await expect(mode).toHaveValue("inherit");
  await expect(limit).toBeDisabled();
  await expect(limit).toHaveValue("12");
  await expect(limit).toHaveAttribute("max", "12");
  await mode.selectOption("custom");
  await expect(limit).toBeEnabled();
  await limit.fill("13");
  await expect(limit).toHaveValue("12");
  await limit.fill("8");
  await savePreferences.click();
  const customRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(customRequest.body).toMatchObject({ maximumCastMembers: 8 });

  await mode.selectOption("inherit");
  await expect(limit).toBeDisabled();
  await savePreferences.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/settings", "PATCH").length).toBe(2);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH").at(-1)?.body).toMatchObject({ maximumCastMembers: null });
});


test("the selected interface language localizes Home copy", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  await page.locator(".settings-profile-picker select").selectOption("server");
  await page.locator('[data-settings-section="language"]').click();

  await page.locator('select[name="interfaceLanguage"]').selectOption("fr");
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();
  await rivune.waitForRequest("/api/v1/settings", "PATCH");
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");

  await page.getByRole("navigation", { name: "Navigation principale" }).getByRole("button", { name: "Accueil", exact: true }).click();
  await expect(page.getByText("Continuer à regarder", { exact: true })).toBeVisible();
  await expect(page.getByText("Continue Watching", { exact: true })).toHaveCount(0);
});

test("viewer preferences use the full desktop workspace", async ({ page, rivune }) => {
  rivune.configureCategoryScope(CATEGORY_IDS.kids);
  await page.setViewportSize({ width: 1568, height: 899 });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Preferences" }).click();

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

test("a global administrator session exposes server administration only through its manager profile", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  await page.goto("/");

  const mainNavigation = page.getByRole("navigation", { name: "Main navigation" });
  await expect(mainNavigation.getByRole("button", { name: "Administration", exact: true })).toBeVisible();
  await mainNavigation.getByRole("button", { name: "Administration", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Categories\b/ })).toBeVisible();

  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await page.getByRole("button", { name: "Bob Profile" }).click();
  await expect(page.getByRole("heading", { name: "Bob's Fresh Picks" })).toBeVisible();
  await expect(mainNavigation.getByRole("button", { name: "Preferences", exact: true })).toBeVisible();
  await expect(mainNavigation.getByRole("button", { name: "Administration", exact: true })).toHaveCount(0);
  await mainNavigation.getByRole("button", { name: "Preferences", exact: true }).click();

  await expect(page.locator(".admin-layout--preferences .admin-tabs")).toHaveCount(0);
  await expect(page.getByText("Profile access", { exact: true })).toBeVisible();
  await expect(page.locator(".settings-profile-picker")).toHaveCount(0);
  await expect(page.getByText("Server defaults", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Bob preferences" })).toBeVisible();
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
  await page.getByRole("button", { name: /Settings/ }).click();
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
