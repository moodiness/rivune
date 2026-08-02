import { expect, test } from "./fixtures/rivune";

function longSeason(episodeCount: number) {
  return {
    id: "season-1",
    mediaType: "season",
    seriesId: "series-1",
    name: "Season 1",
    overview: "A long-running season.",
    seasonNumber: 1,
    episodeCount,
    airDate: "2024-01-01",
    voteAverage: 8.2,
    externalIds: { tmdb: "101" },
    episodes: Array.from({ length: episodeCount }, (_, index) => ({
      id: `episode-${index + 1}`,
      mediaType: "episode",
      seasonId: "season-1",
      name: `Episode ${index + 1}`,
      overview: `Synopsis for episode ${index + 1}.`,
      seasonNumber: 1,
      episodeNumber: index + 1,
      airDate: "2024-01-03",
      runtimeMinutes: 30,
      voteAverage: 8.1,
      voteCount: 100,
      externalIds: { imdb: `tt9${String(index + 1).padStart(5, "0")}` },
    })),
  };
}

test("media details use a refresh-safe route with browser and in-page history", async ({ page, rivune }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  const invokingCard = page.getByRole("button", { name: "Open Signal Horizon" });
  const initialContinueRequests = rivune.matching("/api/v1/continue-watching", "GET").length;
  await invokingCard.click();

  await expect(page.locator(".details-page")).toBeVisible();
  await expect(page.locator("dialog .details-page")).toHaveCount(0);
  await expect(page).toHaveURL(/#media\/episode\/[^?]+\?/);
  await page.goBack();
  await expect(invokingCard).toBeFocused();
  await expect.poll(() => rivune.matching("/api/v1/continue-watching", "GET").length).toBeGreaterThan(initialContinueRequests);
  await page.goForward();
  await expect(page.locator(".details-page")).toBeVisible();
  const viewSeriesToggle = page.getByRole("button", { name: "View series & season" });
  if (await viewSeriesToggle.isVisible()) await viewSeriesToggle.click();

  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await page.getByRole("button", { name: /Moonrise/ }).first().click();
  await expect(page).toHaveURL(/seasonId=season-2/);
  await expect(page).toHaveURL(/episodeId=episode-3/);
  await page.evaluate(() => window.history.replaceState(null, "", window.location.href));

  await page.reload();
  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();
  await expect(page.getByText("The team reunites on a distant moon.")).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toBeVisible();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveCount(0);
  const stateFreeContinueRequests = rivune.matching("/api/v1/continue-watching", "GET").length;

  await page.goBack();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page).toHaveURL(/#home$/);
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
  await expect.poll(() => rivune.matching("/api/v1/continue-watching", "GET").length).toBeGreaterThan(stateFreeContinueRequests);
  await page.goForward();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await page.goForward();
  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();

  await page.getByRole("button", { name: "Back to browse" }).click();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
});

test("series episodes open dedicated detail pages that own playback sources", async ({ page, rivune }) => {
  await page.goto("/#media/series/series-1?from=home&title=Signal%20Horizon&titleId=series-1");

  const seriesHeading = page.getByRole("heading", { name: "Signal Horizon" });
  await expect(seriesHeading).toBeAttached();
  await expect(page.locator(".details-logo")).toHaveAttribute("src", "https://fixtures.rivune.test/series-logo.svg");
  await expect(seriesHeading).toHaveClass(/visually-hidden/);
  await expect(page.getByRole("button", { name: /First Light/ }).first()).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
  expect(rivune.matching("/api/v1/playback/sources", "POST")).toHaveLength(0);

  await page.getByRole("button", { name: /First Light/ }).first().click();

  await expect(page).toHaveURL(/#media\/episode\/tt9000%3A1%3A1\?/);
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await expect(page.getByText("The crew follows a mysterious signal.")).toBeVisible();
  await expect(page.getByText("Season 1 · Episode 1")).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toBeVisible();
  const sourceRequest = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(sourceRequest.body).toMatchObject({ mediaType: "episode", resourceId: "tt9000:1:1" });

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
});

test("continue-watching episode opens its series and requests trailers for each selected season", async ({ page, rivune }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page).toHaveURL(/#media\/episode\//);
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S01E01.*First Light/ })).toBeVisible();

  await page.getByRole("button", { name: "View series & season" }).click();
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: /^Season 1\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: /First Light/ }).first()).toBeVisible();

  await page.getByRole("button", { name: "Trailers" }).click();
  const trailerRegion = page.getByRole("region", { name: /Trailers for/ });
  await expect(trailerRegion).toBeVisible();
  await expect(trailerRegion.locator("iframe")).toHaveAttribute("title", /Season One Trailer/);
  const seasonOneRequest = await rivune.waitForRequest("/api/v1/metadata/titles/series-1/trailers", "GET");
  expect(seasonOneRequest.search.get("seasonNumber")).toBe("1");
  expect(seasonOneRequest.search.get("language")).toBe("en");
  expect(seasonOneRequest.search.get("captionLanguage")).toBe("en");

  await page.getByRole("button", { name: "Dismiss trailer" }).click();
  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: /Moonrise/ }).first()).toBeVisible();
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Season Two Trailer/);

  await expect.poll(() => rivune.matching("/api/v1/metadata/titles/series-1/trailers", "GET").map((request) => request.search.get("seasonNumber"))).toEqual(["1", "2"]);
});

test("resolved artwork remains visible while revisiting metadata is revalidated", async ({ page, rivune: _rivune }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/series.svg");
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();

  const requestStarted = Promise.withResolvers<void>();
  const releaseRequest = Promise.withResolvers<void>();
  await page.route("**/api/v1/metadata/series/series-1*", async (route) => {
    requestStarted.resolve();
    await releaseRequest.promise;
    await route.fallback();
  });

  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await requestStarted.promise;
  try {
    await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/series.svg", { timeout: 500 });
  } finally {
    releaseRequest.resolve();
  }
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S01E01.*First Light/ })).toBeVisible();
});

test("series guide switches to a selected TVDB episode order", async ({ page, rivune }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("button", { name: "View series & season" }).click();

  const order = page.getByRole("combobox", { name: "Episode order" });
  await expect(order).toBeVisible();
  await expect(order.locator("option")).toHaveText([
    "Profile default",
    "Aired Order",
    "DVD Order",
    "Absolute Order",
    "Story Order",
    "Streaming Order",
  ]);

  await order.selectOption("2");
  await expect(order).toHaveValue("2");
  await expect(page.getByRole("tab", { name: /^Season 1.*3 episodes/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Disc Opening/ }).first()).toBeVisible();

  await expect.poll(() => rivune.matching("/api/v1/metadata/series/series-1", "GET")
    .some((request) => request.search.get("mappingProvider") === "tvdb" && request.search.get("episodeOrder") === "2")).toBe(true);
  await expect.poll(() => rivune.matching("/api/v1/metadata/seasons/dvd-season-1", "GET")
    .some((request) => request.search.get("mappingProvider") === "tvdb")).toBe(true);
});

test("season selector supports horizontal mouse dragging without changing the active season", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("button", { name: "View series & season" }).click();

  const seasons = page.locator(".season-tabs");
  const activeSeason = page.getByRole("tab", { name: /^Season 1\b/ });
  await expect(seasons).toBeVisible();
  await expect(activeSeason).toHaveAttribute("aria-selected", "true");
  await seasons.scrollIntoViewIfNeeded();
  const bounds = await seasons.boundingBox();
  if (!bounds) throw new Error("Missing season selector bounds");

  const dragStartX = bounds.x + Math.min(bounds.width - 60, 720);
  await page.mouse.move(dragStartX, bounds.y + bounds.height / 2);
  await page.mouse.down();
  await page.mouse.move(dragStartX - 180, bounds.y + bounds.height / 2, { steps: 4 });
  await page.mouse.up();

  const releasedScrollLeft = await seasons.evaluate((element) => element.scrollLeft);
  expect(releasedScrollLeft).toBeGreaterThan(100);
  await expect.poll(() => seasons.evaluate((element) => element.scrollLeft)).toBeGreaterThan(releasedScrollLeft + 20);
  await expect(activeSeason).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
});

test("long seasons use a one-column bounded episode scroller", async ({ page, rivune }) => {
  rivune.setSeason("season-1", longSeason(200));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("button", { name: "View series & season" }).click();

  const list = page.locator(".episode-list");
  const rows = list.locator(":scope > div");
  await expect(rows).toHaveCount(200);
  const externalPages = page.getByRole("group", { name: "External title pages" });
  await expect(externalPages).toBeVisible();
  await expect(externalPages.getByRole("link")).toHaveCount(3);
  await expect(externalPages.getByRole("link", { name: /Open IMDb title page/ })).toHaveAttribute("href", "https://www.imdb.com/title/tt900001/");
  await expect(externalPages.getByRole("link", { name: /Open TMDB title page/ })).toHaveAttribute("href", "https://www.themoviedb.org/tv/9000/season/1/episode/1");
  await expect(externalPages.getByRole("link", { name: /Open TVDB title page/ })).toHaveAttribute("href", "https://thetvdb.com/dereferrer/series/9900");
  await list.scrollIntoViewIfNeeded();
  const metrics = await list.evaluate((element) => {
    const listRect = element.getBoundingClientRect();
    const rowRects = Array.from(element.children, (row) => row.getBoundingClientRect());
    const fullyVisibleRows = rowRects.filter((row) => row.top >= listRect.top && row.bottom <= listRect.bottom).length;
    return {
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      fullyVisibleRows,
      firstRowX: rowRects[0]?.x,
      secondRowX: rowRects[1]?.x,
    };
  });
  expect(metrics.scrollHeight).toBeGreaterThan(metrics.clientHeight);
  expect(metrics.clientHeight).toBeLessThanOrEqual(744);
  expect(metrics.fullyVisibleRows).toBeGreaterThanOrEqual(5);
  expect(metrics.fullyVisibleRows).toBeLessThanOrEqual(6);
  expect(Math.abs(metrics.firstRowX! - metrics.secondRowX!)).toBeLessThan(1);

  await list.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect.poll(() => list.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
  const lastRowVisible = await list.evaluate((element) => {
    const listRect = element.getBoundingClientRect();
    const lastRowRect = element.lastElementChild?.getBoundingClientRect();
    return Boolean(lastRowRect && lastRowRect.top >= listRect.top && lastRowRect.bottom <= listRect.bottom);
  });
  expect(lastRowVisible).toBe(true);
});

test("calendar episode opens the matching series season and episode", async ({ page, rivune }) => {
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Calendar" }).click();

  await expect(page.getByRole("heading", { name: "Release calendar." })).toBeVisible();
  await page.getByRole("button", { name: "Open Moonrise details" }).first().click();
  await expect(page).toHaveURL(/#media\/episode\//);

  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S02E01.*Moonrise/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Moonrise/ }).first()).toBeVisible();
  await rivune.waitForRequest("/api/v1/metadata/seasons/season-2", "GET");
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ mediaType: "episode", resourceId: "tt9000:2:1" }));

  const calendarRequest = await rivune.waitForRequest("/api/v1/calendar", "GET");
  expect(calendarRequest.search.get("from")).toMatch(/^\d{4}-\d{2}-01$/);
  expect(calendarRequest.search.get("to")).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  expect(calendarRequest.search.get("language")).toBe("en-US");
});

test("calendar TVDB episode opens the mapped season containing its canonical episode ID", async ({ page, rivune: _rivune }) => {
  const episodeID = "c51c40ea-594f-4ee1-9502-b13144533733";
  const releaseDate = new Date().toISOString().slice(0, 10);
  const requestedSeasons: string[] = [];
  const seasonTwo = {
    id: "official-season-2", mediaType: "season", seriesId: "series-1", name: "Season 2", overview: "", seasonNumber: 2, episodeCount: 1, airDate: releaseDate, voteAverage: 0, externalIds: { tvdb: "2" },
    episodes: [
      { id: "different-official-season-2-episode", mediaType: "episode", seasonId: "official-season-2", name: "Another episode 241", overview: "", seasonNumber: 2, episodeNumber: 241, airDate: releaseDate, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "2241" } },
    ],
  };
  const seasonNine = {
    id: "official-season-9", mediaType: "season", seriesId: "series-1", name: "Season 9", overview: "", seasonNumber: 9, episodeCount: 1, airDate: "2025-08-25", voteAverage: 0, externalIds: { tvdb: "9" },
    episodes: [
      { id: episodeID, mediaType: "episode", seasonId: "official-season-9", name: "Episode 241", overview: "", seasonNumber: 9, episodeNumber: 241, airDate: releaseDate, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "9241" } },
    ],
  };
  const seasonSummaries = [seasonTwo, seasonNine].map(({ episodes: _episodes, ...seasonSummary }) => seasonSummary);

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.slice("/api/v1".length);
    const fulfill = (body: unknown) => route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
    if (path === "/calendar") {
      await fulfill({ events: [{ id: "calendar-episode-241", titleId: episodeID, mediaType: "episode", title: "Episode 241", releaseDate, resourceId: "9241", resourceProvider: "tvdb", seriesTitle: "Demain nous appartient", seriesId: "series-1", seasonId: "canonical-season-2", seasonNumber: 2, episodeNumber: 241 }] });
      return;
    }
    if (path === "/metadata/series/series-1") {
      await fulfill({ id: "series-1", mediaType: "series", name: "Demain nous appartient", originalName: "Demain nous appartient", originalLanguage: "fr", overview: "", firstAirDate: "2017-07-17", numberOfSeasons: 9, numberOfEpisodes: 241, genres: [], voteAverage: 0, voteCount: 0, seasons: seasonSummaries, episodeOrders: [], mappingProvider: "tvdb", externalIds: { tvdb: "331147", tmdb: "72879" } });
      return;
    }
    const seasonMatch = path.match(/^\/metadata\/seasons\/(official-season-[29])$/);
    if (seasonMatch) {
      requestedSeasons.push(seasonMatch[1]);
      await fulfill(seasonMatch[1] === seasonNine.id ? seasonNine : seasonTwo);
      return;
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Calendar" }).click();
  await page.getByRole("button", { name: "Open Episode 241 details" }).click();

  await expect(page.getByRole("tab", { name: /Season 9/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "false");
  await expect(page.getByRole("heading", { name: /Demain nous appartient.*S09E241.*Episode 241/ })).toBeVisible();
  expect(requestedSeasons).toEqual(["official-season-2", "official-season-9"]);
});

test("home honors a collection's landscape folder cover shape", async ({ page, rivune: _rivune }) => {
  const folder = {
    id: "streaming-folder",
    title: "Streaming",
    tileShape: "poster",
    coverImageUrl: "https://fixtures.rivune.test/streaming-landscape.svg",
    focusGifEnabled: false,
    hideTitle: false,
    sources: [],
  };
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.slice("/api/v1".length);
    const fulfill = (body: unknown) => route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
    if (path === "/collections") {
      await fulfill({ collections: [{
        id: "streaming-collection",
        title: "Streaming",
        heroEnabled: false,
        pinToTop: false,
        focusGlowEnabled: false,
        viewMode: "rows",
        folderCoverShape: "landscape",
        folders: [folder],
        profileIds: ["alice"],
        position: 0,
        version: 1,
        createdAt: "2024-01-01T00:00:00Z",
        updatedAt: "2024-01-01T00:00:00Z",
      }] });
      return;
    }
    if (path === "/collections/streaming-collection/folders/streaming-folder/items") {
      await fulfill({
        collectionId: "streaming-collection",
        folder,
        items: [{ id: "streaming-title", mediaType: "movie", title: "Landscape fixture", posterUrl: "https://fixtures.rivune.test/poster.svg" }],
        page: 1,
        hasMore: false,
        errors: [],
      });
      return;
    }
    await route.fallback();
  });

  await page.goto("/");
  const card = page.getByRole("button", { name: "Open Streaming" });
  await expect(card).toBeVisible();
  const visual = card.locator(".folder-cover-card__visual");
  const size = await visual.evaluate((element) => {
    const bounds = element.getBoundingClientRect();
    return { width: bounds.width, height: bounds.height };
  });
  expect(size.width / size.height).toBeCloseTo(16 / 9, 1);
});
