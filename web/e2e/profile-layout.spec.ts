import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";

test("global administrator chooser stays inside the device category", async ({ page, rivune }) => {
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");
  await page.getByRole("button", { name: "Switch profile" }).first().click();

  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Alice Administrator" })).toBeVisible();
  await expect(page.getByText("Primary household profile.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /Bob/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Casey/ })).toHaveCount(0);
});


test("rapid profile switching never paints a late response from the prior profile", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
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
  await expect(mobileNavigation.getByRole("button", { name: "Settings", exact: true })).toHaveCount(1);
  await expect(mobileNavigation.getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
  const mobileBounds = await mobileNavigation.boundingBox();
  expect(mobileBounds).not.toBeNull();
  expect(844 - ((mobileBounds?.y ?? 0) + (mobileBounds?.height ?? 0))).toBeGreaterThanOrEqual(11);
  expect(844 - ((mobileBounds?.y ?? 0) + (mobileBounds?.height ?? 0))).toBeLessThanOrEqual(13);
  await mobileNavigation.getByRole("button", { name: "Calendar" }).click();
  await expect(page.getByRole("heading", { name: "Release calendar." })).toBeVisible();

  await page.setViewportSize({ width: 1024, height: 768 });
  const mainNavigation = page.locator("#main-sidebar nav");
  await expect(mainNavigation).toBeVisible();
  await expect(mainNavigation.getByRole("button", { name: "Settings", exact: true })).toHaveCount(1);
  await expect(mainNavigation.getByRole("button", { name: /^(Administration|Preferences|Manage)$/ })).toHaveCount(0);
  await expect(mobileNavigation).toBeHidden();
  await expect.poll(async () => (await viewStage.boundingBox())?.y ?? -1).toBe(0);
  await expect(page.getByRole("button", { name: "Compact sidebar" })).toBeVisible();
  await page.getByRole("button", { name: "Compact sidebar" }).click();
  await expect(page.getByRole("button", { name: "Expand sidebar" })).toBeVisible();
});

test("shell directional focus moves predictably between rail, content, and RTL mobile navigation", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");

  const mainNavigation = page.locator("#main-sidebar nav");
  const home = mainNavigation.getByRole("button", { name: "Home", exact: true });
  const search = mainNavigation.getByRole("button", { name: "Search", exact: true });
  await home.focus();
  await home.press("ArrowDown");
  await expect(search).toBeFocused();

  await search.press("ArrowRight");
  const focusedContent = page.locator(".view-stage :focus");
  await expect(focusedContent).toHaveCount(1);
  await focusedContent.press("ArrowLeft");
  await expect(search).toBeFocused();

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileNavigation = page.locator(".mobile-nav");
  const mobileHome = mobileNavigation.getByRole("button", { name: "Home", exact: true });
  const mobileSearch = mobileNavigation.getByRole("button", { name: "Search", exact: true });
  await mobileHome.focus();
  await mobileHome.press("ArrowRight");
  await expect(mobileSearch).toBeFocused();

  await page.evaluate(() => { document.documentElement.dir = "rtl"; });
  await mobileHome.focus();
  await mobileHome.press("ArrowLeft");
  await expect(mobileSearch).toBeFocused();
});

test("portrait keeps all destinations and exposes profile, server, and sign-out without hover", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");

  const mobileNavigation = page.locator(".mobile-nav");
  const destinations = mobileNavigation.getByRole("button");
  await expect(destinations).toHaveCount(5);
  for (const destination of await destinations.all()) {
    await expect(destination).toBeVisible();
    expect((await destination.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(48);
  }

  const account = page.locator(".mobile-account-toggle");
  await expect(account).toBeVisible();
  await expect(account).toContainText("Alice Administrator");
  expect((await account.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(48);
  await account.click();

  const panel = page.locator("#mobile-account-panel");
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("group", { name: /Connected to/ })).toBeVisible();
  await expect(panel.getByRole("button", { name: /Switch profile/ })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Disconnect device" })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  await expect(account).toBeFocused();

  await account.click();
  await panel.getByRole("button", { name: /Switch profile/ }).click();
  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
});

test("horizontal rows keep keyboard focus visible in RTL and stop inertia for reduced motion", async ({ page, rivune: _rivune }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.route("**/api/v1/continue-watching*", async (route) => {
    const items = Array.from({ length: 8 }, (_, index) => ({
      titleId: `directional-movie-${index}`,
      mediaType: "movie",
      positionSeconds: 120,
      durationSeconds: 1200,
      version: 1,
      reason: "resume",
      title: `Directional movie ${index + 1}`,
      resourceId: `directional-movie-${index}`,
      resourceProvider: "tmdb",
      lastWatchedAt: "2024-01-01T00:00:00Z",
    }));
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items }) });
  });
  await page.setViewportSize({ width: 820, height: 720 });
  await page.goto("/");

  const row = page.locator(".media-row--continue");
  const cards = row.locator(".media-card");
  await expect(cards).toHaveCount(8);
  await page.evaluate(() => { document.documentElement.dir = "rtl"; });

  await cards.first().focus();
  await cards.first().press("ArrowLeft");
  await expect(cards.nth(1)).toBeFocused();
  await cards.nth(1).press("End");
  await expect(cards.last()).toBeFocused();
  await expect.poll(() => cards.last().evaluate((card) => {
    const cardBounds = card.getBoundingClientRect();
    const rowBounds = card.parentElement?.getBoundingClientRect();
    return Boolean(rowBounds && cardBounds.left >= rowBounds.left - 1 && cardBounds.right <= rowBounds.right + 1);
  })).toBe(true);
  await page.evaluate(() => {
    document.documentElement.dir = "ltr";
    const rowElement = document.querySelector<HTMLElement>(".media-row--continue");
    if (rowElement) rowElement.scrollLeft = 120;
  });

  const rowBounds = await row.boundingBox();
  expect(rowBounds).not.toBeNull();
  if (rowBounds) {
    await page.mouse.move(rowBounds.x + rowBounds.width * 0.65, rowBounds.y + rowBounds.height * 0.5);
    await page.mouse.down();
    await page.mouse.move(rowBounds.x + rowBounds.width * 0.35, rowBounds.y + rowBounds.height * 0.5, { steps: 2 });
    await page.mouse.up();
  }
  const releasedPosition = await row.evaluate((element) => element.scrollLeft);
  await page.waitForTimeout(120);
  await expect.poll(() => row.evaluate((element) => element.scrollLeft)).toBeCloseTo(releasedPosition, 0);
  await expect.poll(() => cards.last().locator(".media-card__visual").evaluate((visual) => Math.max(...getComputedStyle(visual).transitionDuration.split(",").map((duration) => Number.parseFloat(duration))))).toBeLessThanOrEqual(0.001);
});

test("profile chooser stays directional, scrollable, and recoverable on a 360px RTL viewport", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  rivune.setProfileCategory("casey", CATEGORY_IDS.household);
  rivune.setProfileAvailability("bob", { enabled: false, accessible: false });
  rivune.seedProfiles(12);
  rivune.setInterfaceLanguage("ar");
  await page.setViewportSize({ width: 1024, height: 800 });
  await page.goto("/");
  await rivune.waitForRequest("/api/v1/collections", "GET");
  await page.locator(".sidebar-profile").click();
  await page.setViewportSize({ width: 360, height: 800 });

  const gate = page.locator(".profile-gate");
  await expect(gate).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  const alice = page.locator(".profile-card").filter({ hasText: "Alice" });
  const bob = page.locator(".profile-card").filter({ hasText: "Bob" });
  const casey = page.locator(".profile-card").filter({ hasText: "Casey" });
  await expect(bob).toHaveAttribute("aria-disabled", "true");
  expect(await bob.evaluate((button) => ({
    nativeDisabled: (button as HTMLButtonElement).disabled,
    tabIndex: (button as HTMLButtonElement).tabIndex,
  }))).toEqual({ nativeDisabled: false, tabIndex: 0 });
  await bob.dispatchEvent("click");
  await expect(gate).toBeVisible();
  expect(rivune.matching("/api/v1/profiles/bob/select", "POST")).toHaveLength(0);
  await bob.focus();
  await page.keyboard.press("Enter");
  await expect(gate).toBeVisible();
  expect(rivune.matching("/api/v1/profiles/bob/select", "POST")).toHaveLength(0);

  await alice.focus();
  await page.keyboard.press("ArrowLeft");
  await expect(bob).toBeFocused();
  await casey.focus();
  await page.keyboard.press("Enter");
  const dialog = page.locator("dialog");
  await expect(dialog.locator(".pin-input")).toBeFocused();
  const dialogBounds = await dialog.locator(".pin-modal").boundingBox();
  expect(dialogBounds).not.toBeNull();
  expect(dialogBounds?.width ?? Infinity).toBeLessThanOrEqual(336);
  expect(dialogBounds?.height ?? Infinity).toBeLessThanOrEqual(776);
  await page.keyboard.press("Escape");
  await expect(casey).toBeFocused();

  expect(await gate.evaluate((element) => ({
    horizontal: element.scrollWidth <= element.clientWidth,
    vertical: element.scrollHeight > element.clientHeight,
  }))).toEqual({ horizontal: true, vertical: true });
});
