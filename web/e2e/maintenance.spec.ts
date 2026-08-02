import { expect, test } from "./fixtures/rivune";

test("maintenance holds viewer profiles at selection and lets a manager profile enter", async ({ page, rivune }) => {
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
  const savePreferences = page.locator(".preferences-workspace .settings-card__actions button").last();
  await scope.selectOption("server");

  const language = page.locator('select[name="interfaceLanguage"]');
  await expect(language.locator("option")).toHaveCount(46);
  await expect.poll(() => language.locator("option").evaluateAll((options) => options.map((option) => (option as HTMLOptionElement).value))).toEqual([
    "", "en", "fr", "fr-CA", "es", "es-MX", "es-AR", "es-CL", "es-CO", "es-PE", "it", "de", "ru", "pt-PT", "pt-BR", "ar", "ja", "ko", "zh-CN", "zh-TW", "pl", "hy", "nl", "sv", "da", "fi", "nb", "tr", "uk", "cs", "sk", "ro", "el", "he", "hi", "id", "vi", "th", "hu", "bg", "hr", "sr", "ms", "ca", "fa", "fil",
  ]);
  await language.selectOption("fr");
  await savePreferences.click();

  const serverRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(serverRequest.body).toMatchObject({ interfaceLanguage: "fr" });
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await expect(page.getByRole("navigation", { name: "Navigation principale" }).getByRole("button", { name: "Accueil" })).toBeVisible();
  await expect(language).toBeVisible();

  await scope.selectOption("alice");
  await expect(language).toHaveValue("");
  const effectiveRequestCount = rivune.matching("/api/v1/profiles/alice/settings/effective", "GET").length;
  rivune.delayNextEffectiveSettings(250);
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
  await page.waitForTimeout(300);
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

test("the selected interface language localizes Home copy", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  await page.locator(".settings-profile-picker select").selectOption("server");

  await page.locator('select[name="interfaceLanguage"]').selectOption("fr");
  await page.locator(".preferences-workspace .settings-card__actions button").last().click();
  await rivune.waitForRequest("/api/v1/settings", "PATCH");
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");

  await page.getByRole("navigation", { name: "Navigation principale" }).getByRole("button", { name: "Accueil", exact: true }).click();
  await expect(page.getByText("Continuer à regarder", { exact: true })).toBeVisible();
  await expect(page.getByText("Continue Watching", { exact: true })).toHaveCount(0);
});

test("viewer preferences use the full desktop workspace", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1568, height: 899 });
  await page.goto("/");
  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await page.getByRole("button", { name: "Bob Profile" }).click();
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Preferences" }).click();

  const layout = page.locator(".admin-layout--preferences");
  const preferences = layout.locator(".preferences-admin");
  await expect(layout.locator(".admin-tabs")).toHaveCount(0);
  await expect(preferences).toBeVisible();
  await expect.poll(async () => (await preferences.boundingBox())?.width ?? 0).toBeGreaterThan(1100);
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
