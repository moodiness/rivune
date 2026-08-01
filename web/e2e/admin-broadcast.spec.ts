import { expect, test } from "./fixtures/rivune";

test("admin broadcasts bounded plain text once from the responsive compose dialog", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Manage" }).click();
  await page.getByRole("button", { name: "Broadcast message" }).click();

  await expect(page.getByRole("heading", { name: "Message everyone." })).toBeVisible();
  const modal = page.locator("dialog[open] .session-message-modal");
  const bounds = await modal.boundingBox();
  expect(bounds).not.toBeNull();
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(390);

  const message = page.getByRole("textbox", { name: "Message" });
  const send = page.getByRole("button", { name: "Send to everyone" });
  await message.fill("x".repeat(501));
  await expect(send).toBeDisabled();

  const plainText = "<strong>Maintenance begins at 20:00.</strong>";
  await message.fill(plainText);
  await send.click();

  const request = await rivune.waitForRequest("/api/v1/auth/notifications/broadcast", "POST");
  const body = request.body as { idempotencyKey: string; message: string };
  expect(body.idempotencyKey).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  expect(body.message).toBe(plainText);
  expect(rivune.matching("/api/v1/auth/notifications/broadcast", "POST")).toHaveLength(1);
  await expect(page.getByText("Queued for 3 active sessions.")).toBeVisible();
});

test("session notification acknowledgement prevents reconnect duplicates and HTML stays inert", async ({ page, rivune: _rivune }) => {
  let acknowledged = false;
  let notificationPolls = 0;
  await page.route("**/api/v1/profiles/alice/settings/effective", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ schemaVersion: 1, settings: { notificationsEnabled: true, notificationDurationSeconds: 30, notificationPollIntervalSeconds: 5 }, sources: {} }),
    });
  });
  await page.route("**/api/v1/auth/notifications**", async (route) => {
    const request = route.request();
    if (request.method() === "DELETE") {
      acknowledged = true;
      await route.fulfill({ status: 204 });
      return;
    }
    notificationPolls += 1;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        notifications: acknowledged ? [] : [{
          id: "42",
          message: "<img src=x onerror=alert(1)>Server maintenance soon",
          senderUsername: "fixture-owner",
          createdAt: "2026-07-31T12:00:00Z",
        }],
      }),
    });
  });

  await page.goto("/");
  const toast = page.locator(".app-notification").filter({ hasText: "<img src=x onerror=alert(1)>Server maintenance soon" });
  await expect(toast).toBeVisible();
  await expect(toast.locator("img")).toHaveCount(0);
  await expect.poll(() => acknowledged).toBe(true);
  await toast.getByRole("button", { name: "Dismiss notification" }).click();

  await page.reload();
  await expect.poll(() => notificationPolls).toBeGreaterThanOrEqual(2);
  await expect(page.locator(".app-notification")).toHaveCount(0);
});
