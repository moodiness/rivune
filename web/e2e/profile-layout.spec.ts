import { expect, test } from "./fixtures/rivune";

test("rapid profile switching never paints a late response from the prior profile", async ({ page, rivune }) => {
  rivune.delayCollections("alice", 2_000);
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");
  expect(rivune.collectionResponses).not.toContain("alice");

  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await page.getByRole("button", { name: "Bob Profile" }).click();

  await expect(page.getByRole("heading", { name: "Bob's Fresh Picks" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Bob Queue" })).toBeVisible();

  await expect.poll(() => rivune.collectionResponses).toContain("alice");
  await expect(page.getByText("Alice's Slow Shelf")).toHaveCount(0);
  await expect(page.getByText("Alice Exclusive")).toHaveCount(0);
  expect(rivune.matching("/api/v1/collections", "GET").map((request) => request.profileId)).toEqual(["alice", "bob"]);
});

test("portrait uses bottom navigation while landscape tablet uses the sidebar", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");
  await expect(page.locator(".topbar")).toHaveCount(0);
  const viewStage = page.locator(".app-main > .view-stage");
  await expect(viewStage).toHaveCount(1);
  await expect.poll(async () => (await viewStage.boundingBox())?.y ?? -1).toBe(0);

  const mobileNavigation = page.locator(".mobile-nav");
  await expect(mobileNavigation).toBeVisible();
  await expect(page.locator("#main-sidebar nav")).toBeHidden();
  const mobileBounds = await mobileNavigation.boundingBox();
  expect(mobileBounds).not.toBeNull();
  expect(844 - ((mobileBounds?.y ?? 0) + (mobileBounds?.height ?? 0))).toBeGreaterThanOrEqual(9);
  expect(844 - ((mobileBounds?.y ?? 0) + (mobileBounds?.height ?? 0))).toBeLessThanOrEqual(11);
  await mobileNavigation.getByRole("button", { name: "Calendar" }).click();
  await expect(page.getByRole("heading", { name: "Release calendar." })).toBeVisible();

  await page.setViewportSize({ width: 1024, height: 768 });
  const mainNavigation = page.locator("#main-sidebar nav");
  await expect(mainNavigation).toBeVisible();
  await expect(mobileNavigation).toBeHidden();
  await expect.poll(async () => (await viewStage.boundingBox())?.y ?? -1).toBe(0);
  await expect(page.getByRole("button", { name: "Compact sidebar" })).toBeVisible();
  await page.getByRole("button", { name: "Compact sidebar" }).click();
  await expect(page.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
});
