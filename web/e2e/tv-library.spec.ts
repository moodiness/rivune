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

const result = (addonId: string, catalogId: string, metas: unknown[]) => ({
  addonId,
  manifestId: `${addonId}-manifest`,
  transportUrl: `https://fixtures.rivune.test/${addonId}.json`,
  resource: "catalog",
  type: "tv",
  id: catalogId,
  extra: [{ name: "search", value: "news" }],
  payload: { metas },
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

  await page.goto("/#search");
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  const search = page.locator(".search-page .search-box input");
  await search.fill("n");
  await search.fill("ne");
  await search.fill("news");

  await expect(page.getByRole("button", { name: "Open World News" })).toHaveCount(2);
  await expect(page.locator(".tv-media-tile")).toHaveCount(25);
  await expect(page.getByText("Some sources are temporarily unavailable.", { exact: true })).toBeVisible();
  await expect(page.locator(".search-page .notice")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  await expect(page.getByText("Private network timeout", { exact: true })).toHaveCount(0);
  await expect(page.getByText("broken-addon", { exact: true })).toHaveCount(0);
  await expect(page.getByText("private-manifest", { exact: true })).toHaveCount(0);
  await expect(page.getByText("addon-a", { exact: true })).toHaveCount(0);
  await expect(page.getByText("addon-a-manifest", { exact: true })).toHaveCount(0);
  await page.locator(".app-notification").getByRole("button", { name: "Dismiss notification" }).click();
  const initialRequests = rivune.matching("/api/v1/addons/catalogs/search/tv", "GET");
  expect(initialRequests).toHaveLength(1);
  expect(initialRequests[0].search.get("search")).toBe("news");
  expect(initialRequests[0].search.get("skip")).toBe("0");
  expect(initialRequests[0].search.get("limit")).toBe("24");
  expect(rivune.requests.filter((request) => request.pathname.includes("/addons/catalogs/search/movie") || request.pathname.includes("/addons/catalogs/search/series"))).toHaveLength(0);

  await page.getByRole("button", { name: "Load more" }).click();
  await expect.poll(() => rivune.matching("/api/v1/addons/catalogs/search/tv", "GET").length).toBe(2);
  await expect(page.locator(".tv-media-tile")).toHaveCount(25);
  await expect(page.getByRole("button", { name: "Load more" })).toBeVisible();
  await expect(page.getByText("Some sources are temporarily unavailable.", { exact: true })).toBeVisible();
  await expect(page.getByText("Private upstream socket failed", { exact: true })).toHaveCount(0);
  await expect(page.getByText("pagination-addon", { exact: true })).toHaveCount(0);
  await expect(page.getByText("pagination-manifest", { exact: true })).toHaveCount(0);

  rivune.setSearchResponse("tv", 24, { results: [result("addon-a", "catalog-a", [channel("station-25", "Station 25")])], errors: [] });
  await page.getByRole("button", { name: "Load more" }).click();
  await expect(page.getByRole("button", { name: "Open Station 25" })).toBeVisible();
  const searchRequests = rivune.matching("/api/v1/addons/catalogs/search/tv", "GET");
  expect(searchRequests).toHaveLength(3);
  expect(searchRequests[1].search.get("skip")).toBe("24");
  expect(searchRequests[2].search.get("skip")).toBe("24");
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
  rivune.setSearchResponse("tv", 0, { results: [result("iptv-addon", "news-catalog", [channel("france-info", "France Info")])], errors: [] });

  await page.goto("/#search");
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("france");
  const add = page.getByRole("button", { name: "Add to library: France Info" });
  await expect(add).toBeVisible();
  await add.click();
  const saved = page.getByRole("button", { name: "In your library: France Info" });
  await expect(saved).toBeVisible();
  await saved.click();
  await expect(add).toBeVisible();
  await add.click();
  await expect(saved).toBeVisible();

  await page.getByRole("button", { name: "Home", exact: true }).click();
  await expect(page.getByRole("heading", { name: "IPTV — In your library" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Open France Info" })).toHaveCount(0);
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await page.getByRole("button", { name: "Open France Info" }).click();
  await expect(page).toHaveURL(/\/media\/tv\/iptv-addon\/france-info$/);
  await page.reload();
  await expect(page.getByRole("heading", { name: "France Info" })).toBeVisible();
  expect(rivune.matching("/api/v1/playback/sources", "POST")).toHaveLength(0);

  await page.locator(".details-actions").getByRole("button", { name: /Play.*Live TV/ }).click();
  const playback = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(playback.body).toMatchObject({ mediaType: "tv", resourceId: "france-info", addonId: "iptv-addon" });
});

test("Library exposes TV filtering and unavailable channels remain removable", async ({ page, rivune }) => {
  rivune.setLibraryItems([
    { titleId: "movie-kept", mediaType: "movie", resourceId: "movie-kept", title: "Kept Movie", addedAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z" },
    { titleId: "series-kept", mediaType: "series", resourceId: "series-kept", title: "Kept Series", addedAt: "2026-08-02T00:00:00Z", updatedAt: "2026-08-02T00:00:00Z" },
    { titleId: "tv-offline", mediaType: "tv", resourceId: "offline", title: "Offline Channel", sourceAddonId: "offline-addon", sourceCatalogId: "offline-catalog", sourceName: "Fixture IPTV", country: "US", language: "en", category: "News", available: false, addedAt: "2026-08-03T00:00:00Z", updatedAt: "2026-08-03T00:00:00Z" },
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

test("Home omits Library TV rows and all-search still returns movies and series", async ({ page, rivune }) => {
  rivune.setSearchResponse("movie", 0, { results: [result("movie-addon", "movie-search", [{ id: "movie-result", type: "movie", name: "Search Movie" }])], errors: [] });
  rivune.setSearchResponse("series", 0, { results: [result("series-addon", "series-search", [{ id: "series-result", type: "series", name: "Search Series" }])], errors: [] });
  rivune.setSearchResponse("tv", 0, { results: [], errors: [] });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "IPTV — In your library" })).toHaveCount(0);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("search");
  await expect(page.getByRole("button", { name: "Open Search Movie" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Search Series" })).toBeVisible();
  expect(rivune.matching("/api/v1/addons/catalogs/search/movie", "GET")).toHaveLength(1);
  expect(rivune.matching("/api/v1/addons/catalogs/search/series", "GET")).toHaveLength(1);
});
