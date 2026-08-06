import { expect, test } from "./fixtures/rivune";

const channel = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  type: "tv",
  name,
  logo: `https://fixtures.rivune.test/${id}.svg`,
  background: `https://fixtures.rivune.test/${id}-wide.svg`,
  country: "FR",
  language: "fr",
  category: "News",
  currentProgram: { title: `${name} Live`, start: "2026-08-03T10:00:00Z", end: "2026-08-03T11:00:00Z" },
  ...extra,
});

const result = (addonId: string, catalogId: string, metas: unknown[], type = "tv", search = "news", manifestId = `${addonId}-manifest`) => ({
  addonId,
  manifestId,
  resource: "catalog",
  type,
  id: catalogId,
  extra: [{ name: "search", value: search }],
  payload: { metas },
});

test("Search discovers anime catalogs and targets only the selected custom type", async ({ page, rivune }) => {
  rivune.setSearchResponse("anime", 0, {
    results: [
      result("anime-secondary-addon", "anime-secondary-search", [{
        id: "shared-anime",
        type: "anime",
        name: "Fixture Anime Alternate",
      }], "anime", "fixture", "anime-secondary-manifest"),
      result("anime-primary-addon", "anime-primary-search", [{
        id: "shared-anime",
        type: "anime",
        name: "Fixture Anime",
        poster: "https://fixtures.rivune.test/fixture-anime.svg",
      }], "anime", "fixture", "anime-primary-manifest"),
    ],
    errors: [],
  });

  await page.goto("/#search");
  const animeFilter = page.getByRole("button", { name: "Anime", exact: true });
  await expect(animeFilter).toBeVisible();
  await expect(page.getByRole("button", { name: "Documentary", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Community", exact: true })).toHaveCount(0);
  await expect(page.locator(".search-page .filter-pills button")).toHaveText(["All", "Movies", "Series", "Anime", "Other", "Live TV"]);
  await expect(page.getByRole("button", { name: "Other", exact: true }).locator(".lucide-shapes")).toBeVisible();

  await animeFilter.click();
  await page.locator(".search-page .search-box input").fill("fixture");
  const sourceSections = page.locator(".search-result-section");
  await expect(sourceSections).toHaveCount(2);
  await expect(sourceSections.locator(".section-heading h2")).toHaveText(["Fixture Source One · Anime Premieres", "Fixture Source Two · Anime Archive"]);
  await expect(sourceSections.nth(0).getByRole("button", { name: "Open Fixture Anime", exact: true })).toBeVisible();
  await expect(sourceSections.nth(0).getByRole("button", { name: "Open Fixture Anime Alternate", exact: true })).toHaveCount(0);
  await expect(sourceSections.nth(1).getByRole("button", { name: "Open Fixture Anime Alternate", exact: true })).toBeVisible();
  await expect(sourceSections.nth(1).getByRole("button", { name: "Open Fixture Anime", exact: true })).toHaveCount(0);

  const animeRequests = rivune.matching("/api/v1/addons/catalogs/search/anime", "GET");
  expect(animeRequests).toHaveLength(1);
  expect(animeRequests[0].search.get("search")).toBe("fixture");
  expect(animeRequests[0].search.get("skip")).toBe("0");
  expect(animeRequests[0].search.get("limit")).toBe("24");
  expect(rivune.requests.filter((request) => ["movie", "series", "tv", "other"].some((type) => request.pathname === `/api/v1/addons/catalogs/search/${type}`))).toHaveLength(0);
});

test("TV search keeps partial results, warns without inline diagnostics, and retries a failed pagination offset", async ({ page, rivune }) => {
  const firstPage = Array.from({ length: 24 }, (_, index) => channel(`station-${index + 1}`, index === 0 ? "World News" : `Station ${index + 1}`));
  rivune.setSearchResponse("tv", 0, {
    results: [
      result("addon-a", "catalog-a", firstPage),
      result("addon-b", "catalog-b", [channel("world-news", "World News"), channel("world-news", "World News")]),
    ],
    errors: [{ addonId: "broken-addon", manifestId: "private-manifest", code: "upstream_timeout", message: "Private network timeout" }],
  });
  rivune.setSearchResponse("tv", 24, {
    error: { code: "bad_gateway", message: "Private upstream socket failed" },
    addonId: "pagination-addon",
    manifestId: "pagination-manifest",
  }, { status: 502 });

  await page.goto("/");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  const search = page.locator(".search-page .search-box input");
  await search.fill("n");
  await search.fill("ne");
  await search.fill("news");

  await expect(page.getByRole("button", { name: "Open World News" })).toHaveCount(2);
  await expect(page.locator(".tv-media-tile")).toHaveCount(25);
  await expect(page.locator(".search-result-section > .section-heading h2")).toHaveText(["catalog-a", "catalog-b"]);
  await expect(page.getByText("Some sources are temporarily unavailable.", { exact: true })).toBeVisible();
  await expect(page.locator(".search-page .notice--warning")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  await expect(page.getByText("Private network timeout", { exact: true })).toHaveCount(0);
  await expect(page.getByText("broken-addon", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-manifest", { exact: true })).toHaveCount(0);
  await expect(page.getByText("addon-a", { exact: true })).toHaveCount(0);
  await expect(page.getByText("addon-a-manifest", { exact: true })).toHaveCount(0);
  const initialRequests = rivune.matching("/api/v1/addons/catalogs/search/tv", "GET");
  expect(initialRequests).toHaveLength(1);
  expect(initialRequests[0].search.get("search")).toBe("news");
  expect(initialRequests[0].search.get("skip")).toBe("0");
  expect(initialRequests[0].search.get("limit")).toBe("24");
  expect(rivune.requests.filter((request) => request.pathname.includes("/addons/catalogs/search/movie") || request.pathname.includes("/addons/catalogs/search/series"))).toHaveLength(0);
  expect(rivune.matching("/api/v1/library/membership", "POST")).toHaveLength(1);

  await page.getByRole("button", { name: "Load more" }).click();
  await expect.poll(() => rivune.matching("/api/v1/addons/catalogs/search/tv", "GET").length).toBe(2);
  await expect(page.locator(".tv-media-tile")).toHaveCount(25);
  await expect(page.getByRole("button", { name: "Load more" })).toBeVisible();
  await expect(page.getByText("Some sources are temporarily unavailable.", { exact: true })).toBeVisible();
  await expect(page.getByText("Private upstream socket failed", { exact: true })).toHaveCount(0);
  await expect(page.getByText("pagination-addon", { exact: true })).toHaveCount(0);
  await expect(page.getByText("pagination-manifest", { exact: true })).toHaveCount(0);
  expect(rivune.matching("/api/v1/library/membership", "POST")).toHaveLength(1);

  rivune.setSearchResponse("tv", 24, { results: [result("addon-a", "catalog-a", [channel("station-25", "Station 25")])], errors: [] });
  await page.getByRole("button", { name: "Load more" }).click();
  await expect(page.getByRole("button", { name: "Open Station 25" })).toBeVisible();
  const firstSourceSection = page.locator(".search-result-section").filter({ has: page.getByRole("heading", { name: "catalog-a", exact: true }) });
  await expect(firstSourceSection.getByRole("button", { name: "Open Station 25" })).toBeVisible();
  const searchRequests = rivune.matching("/api/v1/addons/catalogs/search/tv", "GET");
  expect(searchRequests).toHaveLength(3);
  expect(searchRequests[1].search.get("skip")).toBe("24");
  expect(searchRequests[2].search.get("skip")).toBe("24");
  expect(rivune.matching("/api/v1/library/membership", "POST")).toHaveLength(2);
});

test("TV search checks only the displayed page against a 5000-entry library", async ({ page, rivune }) => {
  const addonId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  rivune.setLibraryItems(Array.from({ length: 5000 }, (_, index) => ({
    titleId: `saved-${index + 1}`,
    mediaType: "tv",
    resourceId: `large-${index + 1}`,
    title: `Large Channel ${index + 1}`,
    sourceAddonId: addonId,
    addedAt: "2026-08-01T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
  })));
  rivune.setSearchResponse("tv", 0, {
    results: [result(addonId, "large-catalog", Array.from({ length: 100 }, (_, index) => channel(`large-${4901 + index}`, `Large Channel ${4901 + index}`)))],
    errors: [],
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("large");

  await expect(page.locator(".tv-media-tile")).toHaveCount(100);
  await expect(page.getByRole("button", { name: "In your library: Large Channel 5000" })).toBeVisible();
  expect(rivune.matching("/api/v1/library", "GET")).toHaveLength(0);
  const membershipRequests = rivune.matching("/api/v1/library/membership", "POST");
  expect(membershipRequests).toHaveLength(1);
  const membershipBody = membershipRequests[0].body;
  expect(membershipBody && typeof membershipBody === "object" && "identities" in membershipBody && Array.isArray(membershipBody.identities)
    ? membershipBody.identities
    : []).toHaveLength(100);
});

test("TV membership cancellation cannot publish stale search state", async ({ page, rivune }) => {
  rivune.setLibraryMembershipDelay(400);
  rivune.setSearchResponse("tv", 0, {
    results: [result("addon-old", "catalog-old", [channel("old-channel", "Old Channel")])],
    errors: [],
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  const search = page.locator(".search-page .search-box input");
  await search.fill("old");
  await expect.poll(() => rivune.matching("/api/v1/library/membership", "POST").length).toBe(1);

  rivune.setSearchResponse("tv", 0, {
    results: [result("addon-new", "catalog-new", [channel("new-channel", "New Channel")])],
    errors: [],
  });
  await search.fill("new");

  await expect(page.getByRole("button", { name: "Open New Channel" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add to library: New Channel" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Open Old Channel" })).toHaveCount(0);
  expect(rivune.matching("/api/v1/library/membership", "POST")).toHaveLength(2);
});

test("TV search shows a generic retry state when every source fails", async ({ page, rivune }) => {
  rivune.setSearchResponse("tv", 0, {
    results: [],
    errors: [{ addonId: "private-addon", manifestId: "private-manifest", code: "addon_unavailable", message: "Connection refused at private host" }],
  });

  await page.goto("/#search");
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("outage");

  await expect(page.locator(".search-page .notice")).toContainText("Search sources are unavailable.");
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.locator(".search-page .media-card")).toHaveCount(0);
  await expect(page.getByText("Some sources are temporarily unavailable.", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Connection refused at private host", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-addon", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-manifest", { exact: true })).toHaveCount(0);

  rivune.setSearchResponse("tv", 0, { results: [result("recovered-addon", "recovered-catalog", [channel("recovered", "Recovered News")])], errors: [] });
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(page.getByRole("button", { name: "Open Recovered News" })).toBeVisible();
  await expect(page.locator(".search-page .notice")).toHaveCount(0);
});

test("TV library actions update immediately, refresh every surface, keep a stable route, and fetch streams only after play", async ({ page, rivune }) => {
  rivune.setSearchResponse("tv", 0, { results: [result("tv-addon", "tv-search", [channel("fixture-tv", "Fixture TV")], "tv", "TV", "tv-manifest")], errors: [] });

  await page.goto("/");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("TV");
  const add = page.getByRole("button", { name: "Add to library: Fixture TV" });
  await expect(add).toBeVisible();
  await add.click();
  const saved = page.getByRole("button", { name: "In your library: Fixture TV" });
  await expect(saved).toBeVisible();
  await saved.click();
  await expect(add).toBeVisible();
  await add.click();
  await expect(saved).toBeVisible();

  await page.getByRole("button", { name: "Home", exact: true }).click();
  await expect(page.getByRole("heading", { name: "TV — In your library" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Open Fixture TV" })).toHaveCount(0);
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.getByRole("button", { name: "Open Fixture TV" }).click();
  await expect(page).toHaveURL(/\/media\/tv\/tv-addon\/fixture-tv$/);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Fixture TV" })).toBeVisible();
  expect(rivune.matching("/api/v1/playback/sources", "POST")).toHaveLength(0);

  await page.locator(".details-actions").getByRole("button", { name: /Play.*Live TV/ }).click();
  const playback = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(playback.body).toMatchObject({ mediaType: "tv", resourceId: "fixture-tv", addonId: "tv-addon" });
});

test("Library exposes TV filtering and unavailable channels remain removable", async ({ page, rivune }) => {
  rivune.setLibraryItems([
    { titleId: "movie-kept", mediaType: "movie", resourceId: "movie-kept", title: "Kept Movie", addedAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z" },
    { titleId: "series-kept", mediaType: "series", resourceId: "series-kept", title: "Kept Series", addedAt: "2026-08-02T00:00:00Z", updatedAt: "2026-08-02T00:00:00Z" },
    { titleId: "tv-offline", mediaType: "tv", resourceId: "offline", title: "Offline Channel", sourceAddonId: "offline-addon", sourceCatalogId: "offline-catalog", sourceName: "Fixture TV", country: "US", language: "en", category: "News", available: false, addedAt: "2026-08-03T00:00:00Z", updatedAt: "2026-08-03T00:00:00Z" },
  ]);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/#library");
  await expect(page.locator(".library-page .media-card")).toHaveCount(3);
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await expect(page.getByRole("button", { name: "Open Offline Channel" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Offline Channel" })).toHaveClass(/media-card--poster/);
  await expect(page.getByText("Unavailable", { exact: true })).toBeVisible();
  const libraryRequest = rivune.matching("/api/v1/library", "GET").at(-1)!;
  expect(libraryRequest.search.get("mediaType")).toBe("tv");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await page.getByRole("button", { name: "Open Offline Channel" }).click();
  await expect(page.locator(".details-actions").getByRole("button", { name: "In your library" })).toBeVisible();
  await expect(page.locator(".details-actions").getByRole("button", { name: /Play.*Live TV/ })).toBeDisabled();
  await page.locator(".details-actions").getByRole("button", { name: "In your library" }).click();
  await page.getByRole("button", { name: "Back to browse" }).click();
  await expect(page.getByRole("button", { name: "Open Offline Channel" })).toHaveCount(0);
});

test("Home omits Library TV rows and all-search groups populated media types", async ({ page, rivune }) => {
  rivune.setSearchResponse("movie", 0, { results: [result("movie-addon", "movie-search", [{ id: "movie-result", type: "movie", name: "Search Movie" }])], errors: [] });
  rivune.setSearchResponse("series", 0, { results: [result("series-addon", "series-search", [{ id: "series-result", type: "series", name: "Search Series" }])], errors: [] });
  rivune.setSearchResponse("tv", 0, { results: [result("tv-addon", "tv-search", [channel("search-tv", "Search Live TV")], "tv", "search", "tv-manifest")], errors: [] });
  rivune.setSearchResponse("anime", 0, { results: [result("anime-primary-addon", "anime-primary-search", [{ id: "anime-result", type: "anime", name: "Search Anime" }], "anime", "search", "anime-primary-manifest")], errors: [] });
  rivune.setSearchResponse("other", 0, { results: [result("other-addon", "other-search", [{ id: "other-result", type: "other", name: "Search Other" }], "other", "search", "other-manifest")], errors: [] });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "TV — In your library" })).toHaveCount(0);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("search");
  await expect(page.getByRole("button", { name: "Open Search Movie" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Search Series" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Search Anime" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Search Other" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Search Live TV" })).toBeVisible();
  await expect(page.locator(".search-result-section > .section-heading h2")).toHaveText(["Movies", "Series", "Anime", "Other", "Live TV"]);
  await expect(page.getByRole("heading", { name: "Fixture Movies · Movies", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Fixture Source One · Anime Premieres", exact: true })).toHaveCount(0);
  const movieCard = page.getByRole("button", { name: "Open Search Movie" });
  const seriesCard = page.getByRole("button", { name: "Open Search Series" });
  const animeCard = page.getByRole("button", { name: "Open Search Anime" });
  const otherCard = page.getByRole("button", { name: "Open Search Other" });
  const liveTVCard = page.getByRole("button", { name: "Open Search Live TV" });
  await movieCard.focus();
  await movieCard.press("ArrowDown");
  await expect(seriesCard).toBeFocused();
  await seriesCard.press("ArrowDown");
  await expect(animeCard).toBeFocused();
  await animeCard.press("ArrowDown");
  await expect(otherCard).toBeFocused();
  await otherCard.press("ArrowDown");
  await expect(liveTVCard).toBeFocused();
  expect(rivune.matching("/api/v1/addons/catalogs/search/movie", "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/addons/catalogs/search/series", "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/addons/catalogs/search/anime", "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/addons/catalogs/search/other", "GET")).toHaveLength(1);
});

test("Search clears results from the previous filter when the replacement filter fails", async ({ page, rivune }) => {
  rivune.setSearchResponse("movie", 0, {
    results: [{
      ...result("movie-addon", "movie-search", [{ id: "previous-result", type: "movie", name: "Previous Search Result" }]),
      type: "movie",
    }],
    errors: [],
  });
  rivune.setSearchResponse("tv", 0, {
    error: { code: "bad_gateway", message: "Private TV search host" },
    addonId: "private-tv-addon",
  }, { status: 502, delay: 150 });

  await page.goto("/#search");
  await page.getByRole("button", { name: "Movies", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("previous");
  await expect(page.getByRole("button", { name: "Open Previous Search Result" })).toBeVisible();

  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await expect(page.getByRole("button", { name: "Open Previous Search Result" })).toHaveCount(0);
  await expect(page.locator(".search-page .browse-skeleton-grid")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await expect(page.getByText("Private TV search host", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-tv-addon", { exact: true })).toHaveCount(0);
});
