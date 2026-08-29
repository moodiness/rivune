import type { Route } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";

const rotatedAccessToken = "rotated-tab-access";

async function fulfillWebRefresh(route: Route) {
  expect(route.request().method()).toBe("POST");
  expect(route.request().headers()["x-rivune-csrf"]).toBe("1");
  expect(route.request().postData()).toBeNull();
  await route.fulfill({
    status: 200,
    contentType: "application/json",
    headers: { "set-cookie": "rivune_web_refresh=opaque; HttpOnly; SameSite=Strict; Path=/api/v1/auth/web/refresh" },
    body: JSON.stringify({
      tokenType: "Bearer",
      accessToken: rotatedAccessToken,
      accessTokenExpiresAt: "2099-01-01T00:05:00Z",
      sessionId: "session-1",
      deviceId: "fixture-device",
      authorizationScope: "global_admin",
      category: null,
    }),
  });
}

test("two tabs refresh through the HttpOnly cookie without shared secrets", async ({ page, rivune }) => {
  await page.goto("/");
  const secondPage = await page.context().newPage();
  await rivune.install(secondPage);
  await secondPage.goto("/");
  await Promise.all([page.waitForLoadState("networkidle"), secondPage.waitForLoadState("networkidle")]);

  await page.route("**/api/v1/auth/web/refresh", fulfillWebRefresh);
  await secondPage.route("**/api/v1/auth/web/refresh", fulfillWebRefresh);
  const restored = await Promise.all([
    page.evaluate(() => import("/src/api.ts").then(({ api }) => api.restore())),
    secondPage.evaluate(() => import("/src/api.ts").then(({ api }) => api.restore())),
  ]);

  expect(restored).toEqual([true, true]);
  for (const candidate of [page, secondPage]) {
    await expect.poll(() => candidate.evaluate(() => ({
      access: sessionStorage.getItem("rivune.access"),
      localAccess: localStorage.getItem("rivune.access"),
      localRefresh: localStorage.getItem("rivune.refresh"),
      localSession: localStorage.getItem("rivune.session"),
      sharedProfileContext: localStorage.getItem("rivune.profile.shared-context"),
    }))).toEqual({ access: rotatedAccessToken, localAccess: null, localRefresh: null, localSession: null, sharedProfileContext: null });
  }
  await secondPage.close();
});

test("logout uses the cookie route and invalidates peer state without broadcasting secrets", async ({ page, rivune }) => {
  await page.goto("/");
  const secondPage = await page.context().newPage();
  await rivune.install(secondPage);
  await secondPage.goto("/");
  await Promise.all([page.waitForLoadState("networkidle"), secondPage.waitForLoadState("networkidle")]);
  for (const candidate of [page, secondPage]) {
    await candidate.evaluate(() => {
      sessionStorage.setItem("rivune.access", "tab-access");
      sessionStorage.setItem("rivune.tab.session", "session-1");
      sessionStorage.setItem("rivune.profile", "alice");
      sessionStorage.setItem("rivune.profile.context", "private-profile-context");
    });
  }

  let logoutRequest: { method: string; csrf?: string; body: string | null } | null = null;
  await page.route("**/api/v1/auth/web/refresh", async (route) => {
    if (route.request().method() !== "DELETE") return fulfillWebRefresh(route);
    logoutRequest = { method: route.request().method(), csrf: route.request().headers()["x-rivune-csrf"], body: route.request().postData() };
    await route.fulfill({ status: 204, headers: { "set-cookie": "rivune_web_refresh=; Max-Age=0; HttpOnly; SameSite=Strict; Path=/api/v1/auth/web/refresh" } });
  });

  await page.evaluate(() => import("/src/api.ts").then(({ api }) => api.logout()));
  expect(logoutRequest).toEqual({ method: "DELETE", csrf: "1", body: null });
  await expect.poll(() => secondPage.evaluate(() => ({
    access: sessionStorage.getItem("rivune.access"),
    profile: sessionStorage.getItem("rivune.profile"),
    context: sessionStorage.getItem("rivune.profile.context"),
  }))).toEqual({ access: null, profile: null, context: null });
  expect(await secondPage.evaluate(() => JSON.stringify({ ...localStorage }))).not.toContain("private-profile-context");
  await secondPage.close();
});

test("legacy browser auth secrets are deleted at module load", async ({ page }) => {
  await page.goto("/");
  await page.evaluate(() => {
    localStorage.setItem("rivune.access", "legacy-access");
    localStorage.setItem("rivune.refresh", "legacy-refresh");
    localStorage.setItem("rivune.session", "legacy-session");
    localStorage.setItem("rivune.profile.shared-context", "legacy-profile-context");
  });

  await page.evaluate(() => import(`/src/api.ts?cutover=${Date.now()}`));

  expect(await page.evaluate(() => ({
    access: localStorage.getItem("rivune.access"),
    refresh: localStorage.getItem("rivune.refresh"),
    session: localStorage.getItem("rivune.session"),
    context: localStorage.getItem("rivune.profile.shared-context"),
  }))).toEqual({ access: null, refresh: null, session: null, context: null });
});

test("password login offers remembered device identities across account switches", async ({ page }) => {
  const legacyDevice = "10000000-0000-4000-8000-000000000001";
  const aliceDevice = "20000000-0000-4000-8000-000000000002";
  const bobDevice = "30000000-0000-4000-8000-000000000003";
  await page.goto("/");
  await page.evaluate(({ legacyDevice, aliceDevice }) => {
    localStorage.setItem("rivune.device", legacyDevice);
    localStorage.setItem("rivune.devices.v1", JSON.stringify([aliceDevice, "not-a-device-id", 7, aliceDevice]));
  }, { legacyDevice, aliceDevice });

  let loginDevice: unknown = null;
  await page.route("**/api/v1/auth/web/login", async (route) => {
    loginDevice = route.request().postDataJSON().device;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "set-cookie": "rivune_web_refresh=opaque; HttpOnly; SameSite=Strict; Path=/api/v1/auth/web/refresh" },
      body: JSON.stringify({
        tokenType: "Bearer",
        accessToken: "account-switch-access",
        accessTokenExpiresAt: "2099-01-01T00:05:00Z",
        sessionId: "account-switch-session",
        deviceId: bobDevice,
        authorizationScope: "global_admin",
        category: null,
      }),
    });
  });

  await page.evaluate(() => import("/src/api.ts").then(({ api }) => api.login("bob", "password")));

  expect(loginDevice).toMatchObject({
    id: legacyDevice,
    ids: [legacyDevice, aliceDevice],
    platform: "web",
  });
  expect(await page.evaluate(() => ({
    current: localStorage.getItem("rivune.device"),
    remembered: JSON.parse(localStorage.getItem("rivune.devices.v1") ?? "[]"),
  }))).toEqual({ current: bobDevice, remembered: [bobDevice, legacyDevice, aliceDevice] });
});
