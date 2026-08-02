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
