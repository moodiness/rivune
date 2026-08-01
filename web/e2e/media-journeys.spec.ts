import { expect, test } from "./fixtures/rivune";

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

  await page.getByRole("tab", { name: /Season 2/ }).click();
  await page.getByRole("button", { name: /Moonrise/ }).first().click();
  await expect(page).toHaveURL(/seasonId=season-2/);
  await expect(page).toHaveURL(/episodeId=episode-3/);
  await page.evaluate(() => window.history.replaceState(null, "", window.location.href));

  await page.reload();
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S02E01.*Moonrise/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: /Season 2/ })).toHaveAttribute("aria-selected", "true");
  const stateFreeContinueRequests = rivune.matching("/api/v1/continue-watching", "GET").length;

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page).toHaveURL(/#home$/);
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
  await expect.poll(() => rivune.matching("/api/v1/continue-watching", "GET").length).toBeGreaterThan(stateFreeContinueRequests);
  await page.goForward();
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S02E01.*Moonrise/ })).toBeVisible();

  await page.getByRole("button", { name: "Back to browse" }).click();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
});

test("continue-watching episode opens its series and requests trailers for each selected season", async ({ page, rivune }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page).toHaveURL(/#media\/episode\//);
  await expect(page.getByRole("heading", { name: /Signal Horizon.*S01E01.*First Light/ })).toBeVisible();

  await page.getByRole("button", { name: "View series & season" }).click();
  await expect(page.getByRole("tab", { name: /Season 1/ })).toHaveAttribute("aria-selected", "true");
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
  await page.getByRole("tab", { name: /Season 2/ }).click();
  await expect(page.getByRole("tab", { name: /Season 2/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: /Moonrise/ }).first()).toBeVisible();
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Season Two Trailer/);

  await expect.poll(() => rivune.matching("/api/v1/metadata/titles/series-1/trailers", "GET").map((request) => request.search.get("seasonNumber"))).toEqual(["1", "2"]);
});

test("calendar episode opens the matching series season and episode", async ({ page, rivune }) => {
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Calendar" }).click();

  await expect(page.getByRole("heading", { name: "Release calendar." })).toBeVisible();
  await page.getByRole("button", { name: "Open Moonrise details" }).first().click();
  await expect(page).toHaveURL(/#media\/episode\//);

  await expect(page.getByRole("tab", { name: /Season 2/ })).toHaveAttribute("aria-selected", "true");
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
  await expect(page.getByRole("tab", { name: /Season 2/ })).toHaveAttribute("aria-selected", "false");
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
