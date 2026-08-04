import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";
import type { RivuneHarness } from "./fixtures/rivune";

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
    transportUrl: "https://fixtures.rivune.test/demo.json",
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
  await rivune.waitForRequest("/api/v1/auth/login", "POST");
  await rivune.waitForRequest("/api/v1/auth/me", "GET");
  expect(rivune.matching("/api/v1/demo/sessions", "POST")).toHaveLength(0);
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
  await rivune.waitForRequest("/api/v1/progress/episode-1", "GET");
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
    request.pathname === "/api/v1/auth/login" ||
    request.pathname === "/api/v1/auth/refresh" ||
    request.pathname === "/api/v1/auth/logout"
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

test("direct demo route is backend-refused after setup and shows the terminal message", async ({ page, rivune }) => {
  await rivune.configurePreSetup(page);
  rivune.completeSetup();
  await page.goto("/demo");

  await expect(page.getByText("The server setup has been completed. Demo mode is no longer available.", { exact: true })).toBeVisible();
  const start = await rivune.waitForRequest("/api/v1/demo/sessions", "POST");
  expect(start.body).toBeUndefined();
  expect(start.authorization).toBeNull();
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

test("unpaired devices render a local accessible QR and preserve the server approval URI", async ({ page, rivune }) => {
  await rivune.configureUnpaired(page);
  rivune.setDeviceAuthorization({
    verificationUri: "https://pairing.rivune.test/approve",
    verificationUriComplete: "/pair?code=BCDF-GHJK",
  });
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/auth/device-code", "POST");

  await expect(page.getByText("BCDF-GHJK", { exact: true })).toBeVisible();
  await expect(page.getByRole("img", { name: "Pairing code" })).toBeVisible();
  const approvalLink = page.getByRole("link", { name: "https://pairing.rivune.test/approve" });
  await expect(approvalLink).toHaveAttribute("href", "https://pairing.rivune.test/approve");
  await expect(page.locator(".pairing-card__code time")).toContainText("Code expires");
});

test("a terminal pairing expiry clears the stale code and retry resolves a relative server URI", async ({ page, rivune }) => {
  await rivune.configureUnpaired(page);
  rivune.setDeviceAuthorization({ intervalSeconds: 0.01 });
  rivune.setDeviceAuthorizationFailure("expired_device_code");
  await page.goto("/");

  const alert = page.locator(".pairing-card > [role=alert]");
  await expect(alert).toContainText("This pairing code expired");
  await expect(page.getByText("BCDF-GHJK", { exact: true })).toHaveCount(0);
  const retry = page.getByRole("button", { name: "Generate a new code" });
  await expect(retry).toBeEnabled();
  expect(rivune.matching("/api/v1/auth/device-code", "POST")).toHaveLength(1);

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
});
