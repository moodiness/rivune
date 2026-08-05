import type { Route } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";

const expiredAccessToken = "expired-shared-access";
const initialRefreshToken = "shared-refresh-before-rotation";
const rotatedAccessToken = "rotated-shared-access";
const rotatedRefreshToken = "shared-refresh-after-rotation";

test("concurrent tabs share one refresh rotation without clearing the new session", async ({ page, rivune }) => {
  await page.goto("/");
  const secondPage = await page.context().newPage();
  await rivune.install(secondPage);
  await secondPage.goto("/");
  await Promise.all([page.waitForLoadState("networkidle"), secondPage.waitForLoadState("networkidle")]);

  for (const candidate of [page, secondPage]) {
    await candidate.evaluate(({ accessToken, refreshToken }) => {
      sessionStorage.setItem("rivune.access", accessToken);
      localStorage.setItem("rivune.access", accessToken);
      localStorage.setItem("rivune.refresh", refreshToken);
      localStorage.setItem("rivune.session", "session-1");
    }, { accessToken: expiredAccessToken, refreshToken: initialRefreshToken });
  }

  let refreshCalls = 0;
  const handleRefresh = async (route: Route) => {
    refreshCalls++;
    expect(route.request().postDataJSON()).toEqual({ refreshToken: initialRefreshToken });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        tokenType: "Bearer",
        accessToken: rotatedAccessToken,
        accessTokenExpiresAt: "2099-01-01T00:05:00Z",
        refreshToken: rotatedRefreshToken,
        refreshTokenExpiresAt: "2099-01-01T01:00:00Z",
        sessionId: "session-1",
        deviceId: "fixture-device",
        authorizationScope: "global_admin",
        category: null,
      }),
    });
  };
  await page.route("**/api/v1/auth/refresh", handleRefresh);
  await secondPage.route("**/api/v1/auth/refresh", handleRefresh);
  // Load the browser-only API module in the page realm; the Playwright worker has no navigator or storage.

  const restored = await Promise.all([
    page.evaluate(() => import("/src/api.ts").then(({ api }) => api.restore())),
    secondPage.evaluate(() => import("/src/api.ts").then(({ api }) => api.restore())),
  ]);

  expect(restored).toEqual([true, true]);
  expect(refreshCalls).toBe(1);
  for (const candidate of [page, secondPage]) {
    await expect.poll(() => candidate.evaluate(() => ({
      sessionAccess: sessionStorage.getItem("rivune.access"),
      sharedAccess: localStorage.getItem("rivune.access"),
      sharedRefresh: localStorage.getItem("rivune.refresh"),
      sharedSession: localStorage.getItem("rivune.session"),
    }))).toEqual({
      sessionAccess: rotatedAccessToken,
      sharedAccess: rotatedAccessToken,
      sharedRefresh: rotatedRefreshToken,
      sharedSession: "session-1",
    });
  }

  await secondPage.close();
});

test("a failed request is not replayed under a session opened in another tab", async ({ page, rivune }) => {
  await page.goto("/");
  const secondPage = await page.context().newPage();
  await rivune.install(secondPage);
  await secondPage.goto("/");
  await Promise.all([page.waitForLoadState("networkidle"), secondPage.waitForLoadState("networkidle")]);

  await page.evaluate(() => {
    sessionStorage.setItem("rivune.access", "account-a-access");
    localStorage.setItem("rivune.access", "account-a-access");
    localStorage.setItem("rivune.refresh", "account-a-refresh");
    localStorage.setItem("rivune.session", "account-a-session");
  });
  await secondPage.evaluate(() => {
    let releaseLock = () => {};
    const lockHeld = new Promise<void>((resolve) => { releaseLock = resolve; });
    const state = window as typeof window & { refreshLockHeld?: boolean; releaseRefreshLock?: () => void };
    state.releaseRefreshLock = releaseLock;
    void navigator.locks.request("rivune.auth.refresh", async () => {
      state.refreshLockHeld = true;
      await lockHeld;
    });
  });
  await expect.poll(() => secondPage.evaluate(() =>
    Boolean((window as typeof window & { refreshLockHeld?: boolean }).refreshLockHeld),
  )).toBe(true);

  const authorizationHeaders: Array<string | null> = [];
  await page.route("**/api/v1/categories", async (route) => {
    authorizationHeaders.push(route.request().headers()["authorization"] ?? null);
    await route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "invalid_token", message: "expired" } }),
    });
  });
  // Load the browser-only API module in the page realm; the Playwright worker has no navigator or storage.
  const requestOutcome = page.evaluate(async () => {
    try {
      await (await import("/src/api.ts")).api.categories();
      return { status: 200 };
    } catch (error) {
      return { status: (error as { status?: number }).status ?? 0 };
    }
  });
  await expect.poll(() => authorizationHeaders.length).toBe(1);

  await secondPage.evaluate(() => {
    sessionStorage.setItem("rivune.access", "account-b-access");
    localStorage.setItem("rivune.access", "account-b-access");
    localStorage.setItem("rivune.refresh", "account-b-refresh");
    localStorage.setItem("rivune.session", "account-b-session");
    (window as typeof window & { releaseRefreshLock?: () => void }).releaseRefreshLock?.();
  });

  await expect(requestOutcome).resolves.toEqual({ status: 401 });
  expect(authorizationHeaders).toEqual(["Bearer account-a-access"]);
  await expect.poll(() => page.evaluate(() => ({
    sharedAccess: localStorage.getItem("rivune.access"),
    sharedRefresh: localStorage.getItem("rivune.refresh"),
    sharedSession: localStorage.getItem("rivune.session"),
    firstTabAccess: sessionStorage.getItem("rivune.access"),
  }))).toEqual({
    sharedAccess: "account-b-access",
    sharedRefresh: "account-b-refresh",
    sharedSession: "account-b-session",
    firstTabAccess: "account-a-access",
  });

  await secondPage.close();
});
