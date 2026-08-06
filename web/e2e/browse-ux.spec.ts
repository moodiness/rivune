import { expect, test } from "./fixtures/rivune";
import { selectOption } from "./helpers/select";

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

  await page.setViewportSize({ width: 355, height: 600 });
  await page.goto("/#library");
  const cards = page.locator(".library-page .media-card");
  await expect(cards).toHaveCount(3);
  await expect(cards.first()).toContainText("Zulu");

  const sort = page.getByRole("combobox", { name: "Sort by" });
  await sort.click();
  await expect(sort).toHaveAttribute("aria-expanded", "true");
  await sort.press("ArrowDown");
  await sort.press("Enter");
  await expect(sort).toHaveAttribute("data-value", "title");
  await sort.click();
  await sort.press("End");
  await sort.press("Escape");
  await expect(sort).toHaveAttribute("aria-expanded", "false");
  await expect(sort).toHaveAttribute("data-value", "title");
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

test("Home restores persistent rows before revalidation and refreshes opened collections on demand", async ({ page, rivune }) => {
  const folderPath = "/api/v1/collections/alice-collection/folders/alice-folder/items";
  await page.goto("/");
  await expect(page.getByText("Alice Exclusive", { exact: true })).toBeVisible();
  expect(rivune.matching(folderPath, "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/metadata/seasons/season-1", "GET")).toHaveLength(1);

  await page.evaluate(() => {
    for (const key of Object.keys(sessionStorage)) {
      if (key.startsWith("rivune.home-cache.")) sessionStorage.removeItem(key);
    }
  });
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

test("Home applies the active profile direct-title limit without truncating View all", async ({ page, rivune }) => {
  const folderPath = "/api/v1/collections/alice-collection/folders/alice-folder/items";
  const items = Array.from({ length: 8 }, (_, index) => ({
    id: `direct-title-${index + 1}`,
    mediaType: "movie",
    title: `Direct title ${index + 1}`,
    posterUrl: `https://fixtures.rivune.test/direct-title-${index + 1}.svg`,
    description: `Direct title ${index + 1} fixture`,
    sources: [],
  }));
  let folderRequests = 0;
  await page.route(`**${folderPath}*`, async (route) => {
    folderRequests += 1;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        collectionId: "alice-collection",
        folder: { id: "alice-folder", title: "Alice's Slow Shelf", tileShape: "poster", focusGifEnabled: false, hideTitle: false, sources: [] },
        items,
        page: 1,
        hasMore: false,
        errors: [],
      }),
    });
  });

  await page.goto("/");
  const collection = page.locator(".folder-collection-section").filter({ hasText: "Alice's Slow Shelf" });
  const directCards = collection.locator(".media-card");
  await expect(directCards).toHaveCount(8);
  await expect.poll(() => folderRequests).toBe(1);

  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true }).click();
  await page.locator('[data-admin-tab="settings"]').click();
  await page.locator('[data-settings-section="appearance"]').click();
  const mode = page.getByRole("combobox", { name: "Direct title limit mode" });
  const limit = page.locator('input[name="maximumDirectTitles"]');
  await expect(mode).toHaveAttribute("data-value", "inherit");
  await selectOption(mode, "custom");
  await limit.fill("3");
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();
  const profileRequest = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(profileRequest.body).toMatchObject({ maximumDirectTitles: 3 });

  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Home", exact: true }).click();
  await expect(directCards).toHaveCount(3);
  await expect(collection.getByText("Direct title 4", { exact: true })).toHaveCount(0);

  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(directCards).toHaveCount(3);
  await expect(collection.getByText("Direct title 4", { exact: true })).toHaveCount(0);

  await collection.getByRole("button", { name: "View all" }).click();
  await expect.poll(() => folderRequests).toBe(2);
  await expect(page.locator(".folder-page .media-card")).toHaveCount(8);
  await expect(page.getByText("Direct title 8", { exact: true })).toBeVisible();
});

test("source folders use Fanart collection posters instead of the first title artwork", async ({ page, rivune }) => {
  rivune.setCollectionViewMode("alice", "tabbed_grid");
  rivune.setCollectionSourcePosters("alice", false);
  rivune.setCollectionFolders("alice", [{
    id: "alice-folder",
    title: "Fixture Movie Collection",
    sourceView: "folders",
    sources: [
      {
        id: "fixture-source",
        kind: "tmdb",
        title: "Fixture Movie",
        tmdb: { sourceType: "collection", tmdbId: 900401, mediaType: "movie", sort: "release_date.desc", filters: {} },
      },
      {
        id: "fixture-alternate-source",
        kind: "tmdb",
        title: "Fixture Movie Alternate",
        tmdb: { sourceType: "collection", tmdbId: 900402, mediaType: "movie", sort: "release_date.desc", filters: {} },
      },
    ],
  }]);

  await page.goto("/");
  const openFolder = page.getByRole("button", { name: "Open Fixture Movie Collection", exact: true });
  await expect(openFolder).toBeEnabled();
  rivune.setCollectionSourcePosters("alice", true);
  await openFolder.click();

  const fixture = page.getByRole("button", { name: "Open Fixture Movie", exact: true });
  const alternate = page.getByRole("button", { name: "Open Fixture Movie Alternate", exact: true });
  await expect(fixture.locator("img")).toHaveAttribute("src", "/api/v1/artwork/fixture-source-collection-poster");
  await expect(alternate.locator("img")).toHaveAttribute("src", "/api/v1/artwork/fixture-alternate-source-collection-poster");
});

test("Home checkpoints resolved folders while slower rows are still loading", async ({ page, rivune }) => {
  rivune.setCollectionFolders("alice", [
    { id: "alice-fast", title: "Fast" },
    { id: "alice-slow", title: "Slow" },
  ]);
  rivune.delayFolder("alice-slow", 1_500);
  const fastPath = "/api/v1/collections/alice-collection/folders/alice-fast/items";
  const slowPath = "/api/v1/collections/alice-collection/folders/alice-slow/items";

  await page.goto("/");
  await expect(page.getByText("Fast Exclusive", { exact: true })).toBeVisible();
  expect(rivune.matching(fastPath, "GET")).toHaveLength(1);
  expect(rivune.matching(slowPath, "GET")).toHaveLength(1);

  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.getByText("Fast Exclusive", { exact: true })).toBeVisible({ timeout: 800 });
  expect(rivune.matching(fastPath, "GET")).toHaveLength(1);
  await expect.poll(() => rivune.matching(slowPath, "GET").length).toBe(2);
});

test("Home publishes resolved folders in frame-bounded commits without preloading offscreen artwork", async ({ page, rivune }) => {
  const folders = Array.from({ length: 30 }, (_, index) => ({
    id: `lazy-${index + 1}`,
    title: `Lazy ${index + 1}`,
  }));
  rivune.setCollectionFolders("alice", folders);
  rivune.setCollectionViewMode("alice", "rows");
  await page.addInitScript(() => {
    (window as typeof window & { __homeFolderCommits?: number }).__homeFolderCommits = 0;
    window.addEventListener("DOMContentLoaded", () => {
      const observer = new MutationObserver((records) => {
        if (records.some((record) => record.type === "attributes" && (record.target as Element).classList.contains("folder-cover-card"))) {
          const state = window as typeof window & { __homeFolderCommits?: number };
          state.__homeFolderCommits = (state.__homeFolderCommits ?? 0) + 1;
        }
      });
      observer.observe(document.body, { subtree: true, attributes: true, attributeFilter: ["disabled"] });
    });
  });
  await page.setViewportSize({ width: 800, height: 600 });

  await page.goto("/");
  await expect(page.locator(".folder-cover-card:not(:disabled)")).toHaveCount(30);
  await page.waitForTimeout(500);

  expect(await page.evaluate(() => (window as typeof window & { __homeFolderCommits?: number }).__homeFolderCommits ?? 0)).toBeLessThanOrEqual(10);
  expect(rivune.matching("/api/v1/artwork/alice-lazy-30-exclusive", "GET")).toHaveLength(0);
});

test("Explore views reuse the profile settings loaded by App", async ({ page, rivune }) => {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Home", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.getByRole("button", { name: "Home", exact: true }).click();

  expect(rivune.matching("/api/v1/profiles/alice/settings/effective", "GET")).toHaveLength(1);
});

test("Library keeps loaded pages, retries the failed page, and never exposes private diagnostics", async ({ page, rivune: _rivune }) => {
  const items = Array.from({ length: 125 }, (_, index) => ({
    titleId: `library-${index + 1}`,
    mediaType: "movie",
    title: `Library title ${index + 1}`,
    addedAt: new Date(Date.UTC(2026, 0, 1, 0, 0, index)).toISOString(),
    updatedAt: "2026-01-01T00:00:00Z",
  }));
  const requestedPages: number[] = [];
  let secondPageAttempts = 0;
  await page.route("**/api/v1/library*", async (route) => {
    const url = new URL(route.request().url());
    const mediaType = url.searchParams.get("mediaType") ?? "";
    const requestedPage = Number(url.searchParams.get("page") ?? "1");
    if (!mediaType) requestedPages.push(requestedPage);
    if (mediaType) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [], page: requestedPage, totalPages: 0, totalResults: 0 }) });
      return;
    }
    if (!mediaType && requestedPage === 2 && secondPageAttempts++ === 0) {
      await route.fulfill({
        status: 502,
        contentType: "application/json",
        body: JSON.stringify({ error: { message: "Private library shard unavailable" }, shard: "internal-library-7" }),
      });
      return;
    }
    const start = (requestedPage - 1) * 100;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ items: items.slice(start, start + 100), page: requestedPage, totalPages: 2, totalResults: items.length }),
    });
  });

  await page.goto("/#library");
  const cards = page.locator(".library-page .media-card");
  await expect(cards).toHaveCount(100);
  await page.getByRole("button", { name: "Load more", exact: true }).click();

  await expect(cards).toHaveCount(100);
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.getByText("Private library shard unavailable", { exact: true })).toHaveCount(0);
  await expect(page.getByText("internal-library-7", { exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(cards).toHaveCount(125);
  await expect(page.getByRole("button", { name: "Open Library title 125" })).toBeVisible();
  expect(requestedPages).toEqual([1, 2, 2]);
});

test("Library clears the previous filter immediately when the replacement request fails", async ({ page, rivune: _rivune }) => {
  await page.route("**/api/v1/library*", async (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get("mediaType") === "series") {
      await new Promise((resolve) => setTimeout(resolve, 150));
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: { message: "Private series database address" } }),
      });
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        items: [{ titleId: "previous-movie", mediaType: "movie", title: "Previous movie", addedAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" }],
        page: 1,
        totalPages: 1,
        totalResults: 1,
      }),
    });
  });

  await page.goto("/#library");
  await expect(page.getByRole("button", { name: "Open Previous movie" })).toBeVisible();
  await page.getByRole("button", { name: "Series", exact: true }).click();
  await expect(page.getByRole("button", { name: "Open Previous movie" })).toHaveCount(0);
  await expect(page.locator(".library-page .browse-skeleton-grid")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.getByText("Private series database address", { exact: true })).toHaveCount(0);
});

test("browse filters and media grids support directional TV focus", async ({ page, rivune }) => {
  rivune.setLibraryItems(Array.from({ length: 4 }, (_, index) => ({
    titleId: `focus-${index + 1}`,
    mediaType: "movie",
    title: `Focus title ${index + 1}`,
    addedAt: `2026-01-0${index + 1}T00:00:00Z`,
    updatedAt: "2026-01-01T00:00:00Z",
  })));
  await page.setViewportSize({ width: 390, height: 844 });

  await page.goto("/#library");
  const allTitles = page.getByRole("button", { name: "All titles", exact: true });
  await allTitles.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("button", { name: "Movies", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");

  const cards = page.locator(".library-page .media-card");
  await expect(cards).toHaveCount(4);
  await cards.first().focus();
  await page.keyboard.press("ArrowRight");
  await expect(cards.nth(1)).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(cards.nth(3)).toBeFocused();
});

test("Home keeps successful folder results beside a safe partial-error warning", async ({ page, rivune: _rivune }) => {
  await page.route("**/api/v1/collections/alice-collection/folders/alice-folder/items*", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        collectionId: "alice-collection",
        folder: { id: "alice-folder", title: "Alice picks", sources: [], tileShape: "poster", sourceView: "merged" },
        items: [{ id: "partial-home", mediaType: "movie", title: "Partial Home Title" }],
        page: 1,
        hasMore: false,
        errors: [{ sourceId: "private-source", kind: "addon", code: "timeout", message: "Private upstream hostname" }],
      }),
    });
  });

  await page.goto("/");
  await expect(page.getByRole("button", { name: "Open Partial Home Title" })).toBeVisible();
  const warning = page.locator(".home-page .notice--warning");
  await expect(warning).toContainText("Some titles could not be loaded.");
  const warningWidth = await warning.evaluate((element) => element.getBoundingClientRect().width);
  const contentWidth = await page.locator(".home-page .content-stack").evaluate((element) => element.getBoundingClientRect().width);
  expect(warningWidth).toBeLessThanOrEqual(576);
  expect(warningWidth).toBeLessThan(contentWidth * 0.75);
  await expect(page.getByText("Private upstream hostname", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-source", { exact: true })).toHaveCount(0);
});

