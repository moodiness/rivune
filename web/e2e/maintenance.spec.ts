import { expect, test } from "./fixtures/rivune";

test("member clients replace stale application content and recover after maintenance", async ({ page, rivune }) => {
  rivune.setMaintenance(true, "Upgrading the media library");

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "This server is under maintenance." })).toBeVisible();
  await expect(page.getByText("Upgrading the media library")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Alice's Slow Shelf" })).toHaveCount(0);

  rivune.setMaintenance(false);
  await page.getByRole("button", { name: "Check again" }).click();

  await expect(page.getByRole("heading", { name: "This server is under maintenance." })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Alice's Slow Shelf" })).toBeVisible();
});

test("administrator can update the global maintenance settings", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Settings/ }).click();
  await page.getByLabel("Switch scope").selectOption("server");

  await page.getByLabel("Block member access").check();
  await page.getByLabel("Public message").fill("Back after the upgrade");
  await page.getByRole("button", { name: "Save maintenance settings" }).click();

  const request = await rivune.waitForRequest("/api/v1/settings/maintenance", "PUT");
  expect(request.body).toEqual({ enabled: true, message: "Back after the upgrade" });
  await expect(page.getByText("Maintenance settings updated.")).toBeVisible();
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
