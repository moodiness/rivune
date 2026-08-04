import type { Locator } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";

async function expectResponsiveTextareaSurface(textarea: Locator, viewportWidth: number) {
  await expect(textarea).toBeVisible();
  await textarea.evaluate((element) => element.blur());
  const restingBorderColor = await textarea.evaluate((element) => getComputedStyle(element).borderTopColor);
  const surface = await textarea.evaluate((element) => {
    const style = getComputedStyle(element);
    const placeholder = getComputedStyle(element, "::placeholder");
    const bounds = element.getBoundingClientRect();
    return {
      backgroundColor: style.backgroundColor,
      borderRadius: Number.parseFloat(style.borderTopLeftRadius),
      borderStyle: style.borderTopStyle,
      borderWidth: Number.parseFloat(style.borderTopWidth),
      left: bounds.left,
      paddingInline: Number.parseFloat(style.paddingInlineStart),
      paddingBlock: Number.parseFloat(style.paddingBlockStart),
      placeholderColor: placeholder.color,
      placeholderOpacity: Number.parseFloat(placeholder.opacity),
      resize: style.resize,
      right: bounds.right,
    };
  });
  expect(surface.borderStyle).toBe("solid");
  expect(surface.borderWidth).toBeGreaterThan(0);
  expect(surface.borderRadius).toBeGreaterThan(0);
  expect(surface.backgroundColor).not.toBe("rgba(0, 0, 0, 0)");
  expect(surface.paddingInline).toBeGreaterThan(0);
  expect(surface.paddingBlock).toBeGreaterThan(0);
  expect(surface.placeholderColor).not.toBe("rgba(0, 0, 0, 0)");
  expect(surface.placeholderOpacity).toBeGreaterThanOrEqual(.8);
  expect(surface.resize).toBe("vertical");
  expect(surface.left).toBeGreaterThanOrEqual(0);
  expect(surface.right).toBeLessThanOrEqual(viewportWidth);

  await textarea.focus();
  await expect.poll(() => textarea.evaluate((element) => getComputedStyle(element).borderTopColor)).not.toBe(restingBorderColor);
}

async function buttonDangerUsage(action: Locator) {
  return action.evaluate((element) => {
    const colorChannels = (value: string) => {
      const values = value.match(/[\d.]+/g)?.slice(0, 3).map(Number);
      if (!values || values.length !== 3) return null;
      return value.startsWith("color(srgb") ? values.map((channel) => channel * 255) : values;
    };
    const probe = document.createElement("span");
    probe.style.color = "var(--danger)";
    document.body.append(probe);
    const dangerChannels = colorChannels(getComputedStyle(probe).color);
    probe.remove();
    const usesDangerHue = (value: string) => {
      const channels = colorChannels(value);
      return Boolean(channels && dangerChannels && channels.every((channel, index) => Math.abs(channel - dangerChannels[index]) < 1.5));
    };
    const style = getComputedStyle(element);
    const icon = element.querySelector("svg");
    return {
      background: usesDangerHue(style.backgroundColor),
      border: usesDangerHue(style.borderTopColor),
      icon: icon ? usesDangerHue(getComputedStyle(icon).color) : false,
      text: usesDangerHue(style.color),
    };
  });
}

test("admin broadcasts bounded plain text once from the responsive compose dialog", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Broadcast message" }).click();

  await expect(page.getByRole("heading", { name: "Message everyone." })).toBeVisible();
  const modal = page.locator("dialog[open] .session-message-modal");
  const bounds = await modal.boundingBox();
  expect(bounds).not.toBeNull();
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(390);

  const message = page.getByRole("textbox", { name: "Message" });
  await expectResponsiveTextareaSurface(message, 390);
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

test("session Message stays neutral while Revoke and Delete remain destructive", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Settings", exact: true }).click();

  const bob = page.locator(".profile-admin-card").filter({ has: page.getByRole("heading", { name: "Bob", exact: true }) });
  await expect(bob.getByRole("button", { name: "Delete" })).toHaveClass(/admin-destructive-action/);
  await bob.getByRole("button", { name: "Devices" }).click();

  const sessions = page.locator("dialog[open] .profile-sessions-modal");
  const messageAction = sessions.getByRole("button", { name: "Message" });
  const revokeAction = sessions.getByRole("button", { name: "Revoke" });
  await expect(messageAction).toHaveClass(/button--secondary/);
  await expect(messageAction).not.toHaveClass(/button--danger/);
  await expect(revokeAction).toHaveClass(/button--danger/);

  expect(await buttonDangerUsage(messageAction)).toEqual({ background: false, border: false, icon: false, text: false });
  await messageAction.hover();
  expect(await buttonDangerUsage(messageAction)).toEqual({ background: false, border: false, icon: false, text: false });
  await page.mouse.move(0, 0);
  await messageAction.focus();
  expect(await buttonDangerUsage(messageAction)).toEqual({ background: false, border: false, icon: false, text: false });
  const revokeDangerUsage = await buttonDangerUsage(revokeAction);
  expect(revokeDangerUsage.background).toBe(true);
  expect(revokeDangerUsage.border).toBe(true);

  await messageAction.click();
  await expectResponsiveTextareaSurface(page.getByRole("textbox", { name: "Message" }), 390);
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
