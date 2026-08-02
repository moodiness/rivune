import { expect, test } from "./fixtures/rivune";

test("global search shortcuts focus and preserve the profile query", async ({ page, rivune: _rivune }) => {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Home", exact: true })).toBeVisible();

  await page.keyboard.press("/");
  await expect(page).toHaveURL(/#search$/);
  const search = page.locator(".search-page .search-box input");
  await expect(search).toBeFocused();
  await search.fill("signal");

  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.keyboard.press("Control+K");
  await expect(search).toHaveValue("signal");
  await expect(search).toBeFocused();

  await search.press("Escape");
  await expect(search).toHaveValue("");
  await expect(page.locator('.sidebar button[aria-current="page"]')).toHaveText(/Search/);
});

test("library titles can be searched, sorted, and filtered", async ({ page, rivune }) => {
  rivune.setLibraryItems([
    { titleId: "alpha", mediaType: "movie", title: "Alpha", releaseInfo: "2022", released: "2022-01-01", addedAt: "2024-01-01T00:00:00Z", updatedAt: "2024-01-01T00:00:00Z" },
    { titleId: "zulu", mediaType: "movie", title: "Zulu", releaseInfo: "2024", released: "2024-01-01", addedAt: "2024-03-01T00:00:00Z", updatedAt: "2024-03-01T00:00:00Z" },
    { titleId: "orbit", mediaType: "series", title: "Orbit", releaseInfo: "2023", released: "2023-01-01", addedAt: "2024-02-01T00:00:00Z", updatedAt: "2024-02-01T00:00:00Z" },
  ]);

  await page.goto("/#library");
  const cards = page.locator(".library-page .media-card");
  await expect(cards).toHaveCount(3);
  await expect(cards.first()).toContainText("Zulu");

  await page.getByLabel("Sort by").selectOption("title");
  await expect(cards.first()).toContainText("Alpha");

  const librarySearch = page.locator(".library-page .search-box input");
  await librarySearch.fill("orbit");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("Orbit");
  await expect(page.getByRole("status")).toHaveText("1 result");

  await page.getByRole("button", { name: "Series", exact: true }).click();
  await expect(cards).toHaveCount(1);
  const request = await rivune.waitForRequest("/api/v1/library", "GET");
  expect(request.search.get("mediaType")).toBe("series");
});

test("horizontal media rows keep partial scroll positions between cards", async ({ page, rivune: _rivune }) => {
  await page.route("**/api/v1/continue-watching*", async (route) => {
    const items = Array.from({ length: 8 }, (_, index) => ({
      titleId: `movie-${index}`,
      mediaType: "movie",
      positionSeconds: 120,
      durationSeconds: 1200,
      version: 1,
      reason: "resume",
      title: `Movie ${index + 1}`,
      resourceId: `movie-${index}`,
      resourceProvider: "tmdb",
      lastWatchedAt: "2024-01-01T00:00:00Z",
    }));
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items }) });
  });
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto("/");

  const row = page.locator(".media-row--continue");
  const cards = row.locator(".media-card");
  await expect(cards).toHaveCount(8);
  const stride = await cards.evaluateAll((elements) => elements[1].getBoundingClientRect().left - elements[0].getBoundingClientRect().left);
  const partialPosition = stride * 0.5;
  await row.evaluate((element, left) => { element.scrollLeft = left; }, partialPosition);
  await page.waitForTimeout(300);

  await expect.poll(() => row.evaluate((element) => element.scrollLeft)).toBeCloseTo(partialPosition, 0);
});

test("Home restores cached rows before revalidation and refreshes opened collections on demand", async ({ page, rivune }) => {
  const folderPath = "/api/v1/collections/alice-collection/folders/alice-folder/items";
  await page.goto("/");
  await expect(page.getByText("Alice Exclusive", { exact: true })).toBeVisible();
  expect(rivune.matching(folderPath, "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/metadata/seasons/season-1", "GET")).toHaveLength(1);

  rivune.delayCollections("alice", 1_000);
  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByText("Alice Exclusive", { exact: true })).toBeVisible({ timeout: 800 });
  await expect.poll(() => rivune.matching("/api/v1/collections", "GET").length).toBe(2);
  expect(rivune.matching(folderPath, "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/metadata/seasons/season-1", "GET")).toHaveLength(1);

  await page.getByRole("button", { name: "View all" }).click();
  await expect.poll(() => rivune.matching(folderPath, "GET").length).toBe(2);
  await expect(page.getByText("Alice Exclusive", { exact: true })).toBeVisible();
});
