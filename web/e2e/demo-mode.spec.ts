import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";
import type { RivuneHarness } from "./fixtures/rivune";

const INSTALLATION_ID_KEY = "rivune.installation-id.v1";
const INSTALLATION_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function installationIdFrom(body: unknown): string {
  if (!body || typeof body !== "object" || !("installationId" in body) || typeof body.installationId !== "string") {
    throw new Error("Pairing request did not contain an installationId");
  }
  return body.installationId;
}

const demoChannel = {
  id: "world-news",
  type: "tv",
  name: "World News",
  logo: "/api/v1/demo/assets/world-news.svg",
  background: "/api/v1/demo/assets/world-news-wide.svg",
  country: "US",
  language: "en",
  category: "News",
};

const demoSearch = {
  results: [{
    addonId: "demo-addon",
    manifestId: "demo-manifest",
    resource: "catalog",
    type: "tv",
    id: "demo-tv",
    extra: [{ name: "search", value: "world" }],
    payload: { metas: [demoChannel] },
  }],
  errors: [],
};

async function enterDemo(page: Page, rivune: RivuneHarness) {
  await rivune.configurePreSetup(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Try demo", exact: true }).click();
  await expect(page.locator(".demo-badge")).toHaveText("Demo mode");
}

test("pre-setup welcome separates setup credentials from the tokenless demo entry", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.goto("/");

  await expect(page.getByRole("button", { name: "Set up server", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Try demo", exact: true })).toBeVisible();
  await expect(page.getByLabel("Setup token")).toHaveCount(0);

  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  await expect(page.getByLabel("Setup token")).toBeVisible();
  expect(rivune.matching("/api/v1/demo/sessions", "POST")).toHaveLength(0);
});

test("localized sign-in keeps its accessible name while loading", async ({ page, rivune }) => {
  await rivune.configureUnpaired(page);
  rivune.setInterfaceLanguage("fr");
  await page.goto("/");
  await page.getByRole("button", { name: "Connexion du propriétaire", exact: true }).click();
  await page.getByLabel("Nom d’utilisateur").fill("owner");
  await page.getByLabel("Mot de passe", { exact: true }).fill("correct-horse-battery-staple");

  const loginStarted = Promise.withResolvers<void>();
  const releaseLogin = Promise.withResolvers<void>();
  await page.route("**/api/v1/auth/web/login", async (route) => {
    loginStarted.resolve();
    await releaseLogin.promise;
    await route.fallback();
  });

  const signIn = page.getByRole("button", { name: "Se connecter", exact: true });
  await signIn.click();
  await loginStarted.promise;
  await expect(signIn).toBeDisabled();
  await expect(signIn).toHaveAttribute("aria-busy", "true");
  await expect(signIn.locator(".spin")).toHaveAttribute("aria-hidden", "true");

  releaseLogin.resolve();
  await rivune.waitForRequest("/api/v1/auth/web/login", "POST");
  await expect(signIn).toHaveCount(0);
});

test("field placeholders meet 4.5 to 1 contrast on their rendered surface", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  const setupToken = page.getByLabel("Setup token");

  const contrast = await setupToken.evaluate((input) => {
    type Color = [number, number, number, number];
    const parse = (value: string): Color => {
      const channels = value.match(/[\d.]+/g)?.map(Number) ?? [];
      return [channels[0] ?? 0, channels[1] ?? 0, channels[2] ?? 0, channels[3] ?? 1];
    };
    const composite = (foreground: Color, background: Color): Color => {
      const alpha = foreground[3] + background[3] * (1 - foreground[3]);
      if (alpha === 0) return [0, 0, 0, 0];
      return [
        (foreground[0] * foreground[3] + background[0] * background[3] * (1 - foreground[3])) / alpha,
        (foreground[1] * foreground[3] + background[1] * background[3] * (1 - foreground[3])) / alpha,
        (foreground[2] * foreground[3] + background[2] * background[3] * (1 - foreground[3])) / alpha,
        alpha,
      ];
    };
    const layers: HTMLElement[] = [];
    for (let element: HTMLElement | null = input.parentElement; element; element = element.parentElement) layers.push(element);
    let background: Color = [0, 0, 0, 0];
    for (const element of layers.reverse()) background = composite(parse(getComputedStyle(element).backgroundColor), background);
    const foreground = composite(parse(getComputedStyle(input, "::placeholder").color), background);
    const luminance = (color: Color) => {
      const linear = color.slice(0, 3).map((channel) => {
        const normalized = channel / 255;
        return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * linear[0]! + 0.7152 * linear[1]! + 0.0722 * linear[2]!;
    };
    const lighter = Math.max(luminance(foreground), luminance(background));
    const darker = Math.min(luminance(foreground), luminance(background));
    return (lighter + 0.05) / (darker + 0.05);
  });

  expect(contrast).toBeGreaterThanOrEqual(4.5);
});

test("real setup keeps its setup token confined to the setup request", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  await page.getByLabel("Space name").fill("Demo Review Server");
  await page.getByLabel("Administrator account").fill("owner");
  await page.getByLabel("First profile").fill("Alex");
  await page.getByLabel("Administrator password").fill("correct-horse-battery-staple");
  await page.getByLabel("Setup token").fill("fixture-setup-token");
  await page.getByRole("button", { name: /Create my space/ }).click();

  const setup = await rivune.waitForRequest("/api/v1/setup", "POST");
  expect(setup.authorization).toBe("Bearer fixture-setup-token");
  expect(setup.body).toEqual({
    instanceName: "Demo Review Server",
    admin: { username: "owner", password: "correct-horse-battery-staple" },
    profileName: "Alex",
  });
  await rivune.waitForRequest("/api/v1/auth/web/login", "POST");
  await rivune.waitForRequest("/api/v1/auth/me", "GET");
  expect(rivune.matching("/api/v1/demo/sessions", "POST")).toHaveLength(0);
});

test("setup and authentication remain usable when browser storage is blocked", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.addInitScript(() => {
    for (const method of ["getItem", "setItem", "removeItem"] as const) {
      Object.defineProperty(Storage.prototype, method, {
        configurable: true,
        value() { throw new DOMException("Browser storage is blocked", "SecurityError"); },
      });
    }
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  await page.getByLabel("Space name").fill("Storage-free server");
  await page.getByLabel("Administrator account").fill("owner");
  await page.getByLabel("First profile").fill("Alex");
  await page.getByLabel("Administrator password").fill("correct-horse-battery-staple");
  await page.getByLabel("Setup token").fill("fixture-setup-token");
  await page.getByRole("button", { name: /Create my space/ }).click();

  const account = await rivune.waitForRequest("/api/v1/auth/me", "GET");
  expect(account.authorization).toBe("Bearer fixture-access");
  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
});

test("failed browser storage writes cannot resurrect stale credentials", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.evaluate(() => {
    localStorage.setItem("rivune.access", "stale-access");
    localStorage.setItem("rivune.refresh", "stale-refresh");
    localStorage.setItem("rivune.session", "stale-session");
  });
  await page.addInitScript(() => {
    for (const method of ["setItem", "removeItem"] as const) {
      Object.defineProperty(Storage.prototype, method, {
        configurable: true,
        value() { throw new DOMException("Browser storage is read-only", "QuotaExceededError"); },
      });
    }
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Set up server", exact: true }).click();
  await page.getByLabel("Space name").fill("Read-only storage server");
  await page.getByLabel("Administrator account").fill("owner");
  await page.getByLabel("First profile").fill("Alex");
  await page.getByLabel("Administrator password").fill("correct-horse-battery-staple");
  await page.getByLabel("Setup token").fill("fixture-setup-token");
  await page.getByRole("button", { name: /Create my space/ }).click();

  const account = await rivune.waitForRequest("/api/v1/auth/me", "GET");
  expect(account.authorization).toBe("Bearer fixture-access");
});

test("intentional demo admission is bearer-free, purges legacy secrets, and preserves tab access until success", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.evaluate(() => {
    localStorage.setItem("rivune.access", "real-access");
    localStorage.setItem("rivune.refresh", "real-refresh");
    localStorage.setItem("rivune.session", "real-session");
    sessionStorage.setItem("rivune.access", "real-access");
  });
  const admitted = Promise.withResolvers<void>();
  const requested = Promise.withResolvers<string | null>();
  await page.route("**/api/v1/demo/sessions", async (route) => {
    requested.resolve(route.request().headers().authorization ?? null);
    await admitted.promise;
    await route.fallback();
  });
  await page.goto("/");

  const entering = page.getByRole("button", { name: "Try demo", exact: true }).click();
  expect(await requested.promise).toBeNull();
  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
  }))).toEqual({ access: null, refresh: null, session: null, sessionAccess: "real-access" });

  admitted.resolve();
  await entering;
  await expect(page.locator(".demo-badge")).toHaveText("Demo mode");
  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
  }))).toEqual({ access: null, refresh: null, session: null, sessionAccess: null });
});

test("refused intentional demo admission keeps tab access while legacy secrets stay purged", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  await page.evaluate(() => {
    localStorage.setItem("rivune.access", "real-access");
    localStorage.setItem("rivune.refresh", "real-refresh");
    localStorage.setItem("rivune.session", "real-session");
    sessionStorage.setItem("rivune.access", "real-access");
  });
  await page.goto("/");
  const demoButton = page.getByRole("button", { name: "Try demo", exact: true });
  await expect(demoButton).toBeVisible();
  rivune.completeSetup();

  await demoButton.click();
  const start = await rivune.waitForRequest("/api/v1/demo/sessions", "POST");

  expect(start.authorization).toBeNull();
  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
  }))).toEqual({ access: null, refresh: null, session: null, sessionAccess: "real-access" });
  await expect(page.locator(".demo-badge")).toHaveCount(0);
});

test("demo entry sends no setup secret or bearer token and exposes content, profiles, library, playback, and reset", async ({ page, rivune }) => {
  rivune.setSearchResponse("tv", 0, demoSearch);
  await enterDemo(page, rivune);

  const start = await rivune.waitForRequest("/api/v1/demo/sessions", "POST");
  expect(start.body).toBeUndefined();
  expect(start.authorization).toBeNull();
  await expect(page.getByText("Signal Horizon", { exact: true }).first()).toBeVisible();
  await expect(page.locator(".sidebar-profile")).toContainText("Alex");
  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
    hint: localStorage.getItem("rivune.demo"),
  }))).toEqual({ access: null, refresh: null, sessionAccess: null, hint: "1" });
  await expect(page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true })).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await rivune.waitForRequest("/api/v1/progress/batch", "POST");
  await page.getByRole("button", { name: "Back to browse" }).click();

  await page.getByRole("button", { name: /Switch profile/ }).click();
  await page.getByRole("button", { name: /Kids/ }).click();
  await expect(page.locator(".sidebar-profile")).toContainText("Kids");

  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("world");
  await page.getByRole("button", { name: "Add to library: World News" }).click();
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.getByRole("button", { name: "Open World News" }).click();
  await page.locator(".details-actions").getByRole("button", { name: /Play.*Live TV/ }).click();
  await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  await page.keyboard.press("Escape");

  await page.evaluate(() => {
    localStorage.setItem("rivune.home-cache.v2.demo", "stale");
    localStorage.setItem("rivune.metadata-cache.v1", "stale");
    sessionStorage.setItem("rivune.search.demo", "stale");
  });
  await page.getByRole("button", { name: "Reset demo", exact: true }).click();
  await rivune.waitForRequest("/api/v1/demo/session/reset", "POST");
  await expect(page.locator(".sidebar-profile")).toContainText("Alex");
  expect(await page.evaluate(() => ({
    home: localStorage.getItem("rivune.home-cache.v2.demo"),
    metadata: localStorage.getItem("rivune.metadata-cache.v1"),
    search: sessionStorage.getItem("rivune.search.demo"),
  }))).toEqual({ home: null, metadata: null, search: null });
  expect(rivune.requests.filter((request) =>
    request.pathname === "/api/v1/setup" ||
    request.pathname.startsWith("/api/v1/settings") ||
    request.pathname.startsWith("/api/v1/operations") ||
    request.pathname === "/api/v1/auth/web/login" ||
    request.pathname === "/api/v1/auth/web/refresh"
  )).toHaveLength(0);
});

test("demo exit purges state and returns to the pre-setup welcome without real logout", async ({ page, rivune }) => {
  await enterDemo(page, rivune);
  await page.evaluate(() => sessionStorage.setItem("rivune.search.demo", "signal"));

  await page.getByRole("button", { name: "Exit demo", exact: true }).click();
  await rivune.waitForRequest("/api/v1/demo/session", "DELETE");
  await expect(page.getByRole("button", { name: "Try demo", exact: true })).toBeVisible();
  expect(rivune.matching("/api/v1/auth/logout", "POST")).toHaveLength(0);
  expect(await page.evaluate(() => ({ hint: localStorage.getItem("rivune.demo"), search: sessionStorage.getItem("rivune.search.demo") }))).toEqual({ hint: null, search: null });
});

test("refresh resumes only through the backend-validated cookie session", async ({ page, rivune }) => {
  await enterDemo(page, rivune);
  const starts = rivune.matching("/api/v1/demo/sessions", "POST").length;

  await page.reload();
  await expect(page.locator(".demo-badge")).toHaveText("Demo mode");
  await rivune.waitForRequest("/api/v1/demo/session", "GET");
  expect(rivune.matching("/api/v1/demo/sessions", "POST")).toHaveLength(starts);
  expect(rivune.matching("/api/v1/demo/session", "GET").at(-1)?.authorization).toBeNull();
});

test("top-level demo navigation preserves an established real session without starting demo", async ({ page, rivune }) => {
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");
  await page.evaluate(() => {
    sessionStorage.setItem("rivune.search.alice", "principal-owned-query");
    localStorage.setItem("rivune.demo", "1");
  });
  const before = await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
    hint: localStorage.getItem("rivune.demo"),
  }));

  await page.goto("/demo");
  await expect(page.locator(".sidebar-profile")).toContainText("Alice");

  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    hint: localStorage.getItem("rivune.demo"),
    sessionAccess: sessionStorage.getItem("rivune.access"),
    search: sessionStorage.getItem("rivune.search.alice"),
  }))).toEqual({ ...before, search: "principal-owned-query" });
  expect(rivune.matching("/api/v1/demo/sessions", "POST")).toHaveLength(0);
  expect(rivune.matching("/api/v1/demo/session", "GET")).toHaveLength(0);
  expect(rivune.matching("/api/v1/auth/logout", "POST")).toHaveLength(0);
  await expect(page).not.toHaveURL(/\/demo/);
});

test("demo polling reacts to completed setup, purges caches, and routes to login", async ({ page, rivune }) => {
  await enterDemo(page, rivune);
  await page.evaluate(() => localStorage.setItem("rivune.home-cache.v2.demo", "stale"));
  rivune.completeSetup();

  await expect(page.getByText("The server setup has been completed. Demo mode is no longer available.", { exact: true })).toBeVisible({ timeout: 7_000 });
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
  expect(await page.evaluate(() => ({ hint: localStorage.getItem("rivune.demo"), cache: localStorage.getItem("rivune.home-cache.v2.demo") }))).toEqual({ hint: null, cache: null });
});

test("unpaired devices render a local accessible QR and preserve their installation identity across reloads", async ({ page, rivune }) => {
  await rivune.configureUnpaired(page);
  await page.evaluate(({ key, value }) => localStorage.setItem(key, value), { key: INSTALLATION_ID_KEY, value: "x".repeat(129) });
  rivune.setDeviceAuthorization({
    verificationUri: "https://pairing.rivune.test/approve",
    verificationUriComplete: "/pair?code=BCDF-GHJK",
  });
  await page.goto("/");
  const firstPairing = await rivune.waitForRequest("/api/v1/auth/device-code", "POST");
  expect(firstPairing.body).toEqual({
    deviceName: expect.any(String),
    platform: "web",
    installationId: expect.stringMatching(INSTALLATION_ID_PATTERN),
  });
  const installationId = installationIdFrom(firstPairing.body);
  expect(await page.evaluate((key) => localStorage.getItem(key), INSTALLATION_ID_KEY)).toBe(installationId);

  await expect(page.getByText("BCDF-GHJK", { exact: true })).toBeVisible();
  await expect(page.getByRole("img", { name: "Pairing code" })).toBeVisible();
  const approvalLink = page.getByRole("link", { name: "https://pairing.rivune.test/approve" });
  await expect(approvalLink).toHaveAttribute("href", "https://pairing.rivune.test/approve");
  await expect(page.locator(".pairing-card__code time")).toContainText("Code expires");

  await page.reload();
  await expect.poll(() => rivune.matching("/api/v1/auth/device-code", "POST").length).toBe(2);
  expect(installationIdFrom(rivune.matching("/api/v1/auth/device-code", "POST")[1]!.body)).toBe(installationId);
});

test("a terminal pairing expiry clears the stale code and retry resolves a relative server URI", async ({ page, rivune }) => {
  await rivune.configureUnpaired(page);
  await page.evaluate((key) => localStorage.setItem(key, "   "), INSTALLATION_ID_KEY);
  rivune.setDeviceAuthorization({ intervalSeconds: 0.01 });
  rivune.setDeviceAuthorizationFailure("expired_device_code");
  await page.goto("/");

  const alert = page.locator(".pairing-card > [role=alert]");
  await expect(alert).toContainText("This pairing code expired");
  await expect(page.getByText("BCDF-GHJK", { exact: true })).toHaveCount(0);
  const retry = page.getByRole("button", { name: "Generate a new code" });
  await expect(retry).toBeEnabled();
  expect(rivune.matching("/api/v1/auth/device-code", "POST")).toHaveLength(1);
  const firstPairing = rivune.matching("/api/v1/auth/device-code", "POST")[0]!;
  expect(firstPairing.body).toEqual({
    deviceName: expect.any(String),
    platform: "web",
    installationId: expect.stringMatching(INSTALLATION_ID_PATTERN),
  });
  const installationId = installationIdFrom(firstPairing.body);

  rivune.setDeviceAuthorizationFailure(null);
  rivune.setDeviceAuthorization({
    userCode: "JKLM-NPQR",
    verificationUri: "/approve-device",
    verificationUriComplete: "/approve-device?code=JKLM-NPQR",
    intervalSeconds: 60,
  });
  await retry.click();

  await expect(page.getByText("JKLM-NPQR", { exact: true })).toBeVisible();
  const origin = await page.evaluate(() => window.location.origin);
  await expect(page.getByRole("link", { name: `${origin}/approve-device` })).toHaveAttribute("href", `${origin}/approve-device`);
  expect(rivune.matching("/api/v1/auth/device-code", "POST")).toHaveLength(2);
  const retriedPairing = rivune.matching("/api/v1/auth/device-code", "POST")[1]!;
  expect(retriedPairing.body).toEqual({
    deviceName: expect.any(String),
    platform: "web",
    installationId,
  });
});
