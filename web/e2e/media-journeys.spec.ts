import { CATEGORY_IDS, DVD_EPISODE_RESOURCE_ID, DVD_METADATA_SEASON_ID, expect, test } from "./fixtures/rivune";
import { selectListbox, selectOption, selectOptions } from "./helpers/select";
const dvdMetadataSeasonPath = `/api/v1/metadata/seasons/${encodeURIComponent(DVD_METADATA_SEASON_ID)}`;


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

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

function isWatchedBatchItems(value: unknown): value is Array<{ titleId: string; completed: boolean; expectedVersion: number }> {
  return Array.isArray(value) && value.every((item) =>
    item !== null && typeof item === "object" &&
    "titleId" in item && typeof item.titleId === "string" &&
    "completed" in item && typeof item.completed === "boolean" &&
    "expectedVersion" in item && typeof item.expectedVersion === "number"
  );
}

test("metadata snapshots bound synchronous storage writes across a warm reopen", async ({ page, rivune: _rivune }) => {
  await page.addInitScript(() => {
    const state = window as typeof window & { __metadataSetItems?: number };
    state.__metadataSetItems = 0;
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (this === localStorage && key === "rivune.metadata-cache.v1") state.__metadataSetItems = (state.__metadataSetItems ?? 0) + 1;
      return original.call(this, key, value);
    };
  });
  await page.goto("/");
  const card = page.getByRole("button", { name: "Open Signal Horizon" });
  await card.click();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await page.waitForTimeout(3_000);
  const initialWrites = await page.evaluate(() => (window as typeof window & { __metadataSetItems?: number }).__metadataSetItems ?? 0);
  expect(initialWrites).toBeGreaterThan(0);
  expect(initialWrites).toBeLessThanOrEqual(2);

  await page.goBack();
  await expect(card).toBeVisible();
  await card.click();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await page.waitForTimeout(3_000);
  const finalWrites = await page.evaluate(() => (window as typeof window & { __metadataSetItems?: number }).__metadataSetItems ?? 0);
  expect(finalWrites - initialWrites).toBeLessThanOrEqual(1);
});

test("media details use a refresh-safe route with browser and in-page history", async ({ page, rivune }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  const invokingCard = page.getByRole("button", { name: "Open Signal Horizon" });
  const initialContinueRequests = rivune.matching("/api/v1/continue-watching", "GET").length;
  await invokingCard.click();

  await expect(page.locator(".details-page")).toBeVisible();
  await expect(page.locator("dialog .details-page")).toHaveCount(0);
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1\/episode\/1$/);
  await page.goBack();
  await expect(invokingCard).toBeFocused();
  await expect.poll(() => rivune.matching("/api/v1/continue-watching", "GET").length).toBeGreaterThan(initialContinueRequests);
  await page.goForward();
  await expect(page.locator(".details-page")).toBeVisible();
  const returnToEpisodes = page.getByRole("button", { name: /Back.*Episodes/ });
  await expect(returnToEpisodes).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);
  await returnToEpisodes.click();
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1$/);

  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await page.getByRole("button", { name: /Moonrise/ }).first().click();
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/2\/episode\/1$/);
  await page.evaluate(() => window.history.replaceState(null, "", window.location.href));

  await page.reload();
  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();
  await expect(page.locator(".details-description")).toHaveText("The team reunites on a distant moon.");
  await expect(page.getByRole("region", { name: "Playback sources" })).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);
  const stateFreeContinueRequests = rivune.matching("/api/v1/continue-watching", "GET").length;

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page).toHaveURL(/#home$/);
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
  await expect.poll(() => rivune.matching("/api/v1/continue-watching", "GET").length).toBeGreaterThan(stateFreeContinueRequests);
  await page.goForward();
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
  await page.goForward();
  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();
  await page.getByRole("button", { name: "Back to browse" }).click();
  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await expect(page.locator(".route-surface").getByRole("heading").first()).toBeFocused();
});

test("DVD continuation retains its TVDB hierarchy through playback and route reload", async ({ page, rivune }) => {
  rivune.useDvdContinuation();
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();

  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1\/episode\/2$/);
  await expect(page.getByRole("heading", { name: "Disc Middle" })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/metadata/series/series-1", "GET")
    .filter((request) => request.search.get("mappingProvider") === "tvdb" && request.search.get("episodeOrder") === "2").length).toBeGreaterThan(0);
  await expect.poll(() => rivune.matching(dvdMetadataSeasonPath, "GET")
    .filter((request) => request.search.get("mappingProvider") === "tvdb").length).toBeGreaterThan(0);
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").at(-1)?.body).toMatchObject({
    mediaType: "episode",
    resourceId: DVD_EPISODE_RESOURCE_ID,
  });
  await expect.poll(() => page.evaluate(() => window.history.state?.rivuneMediaItem?.raw)).toMatchObject({
    mappingProvider: "tvdb",
    episodeOrderId: "2",
    metadataSeasonId: DVD_METADATA_SEASON_ID,
    continueSeasonId: "persisted-dvd-season-1",
  });

  await page.locator(".details-stream-list__option").first().click();
  await page.locator('[data-media-action="play-selected-stream"]').click();
  await expect(page.locator(".player")).toBeVisible();
  await rivune.waitForRequest("/api/v1/playback/resolve", "POST");
  expect(rivune.matching("/api/v1/playback/markers", "GET")).toHaveLength(0);

  const orderedSeriesRequests = rivune.matching("/api/v1/metadata/series/series-1", "GET").length;
  const orderedSeasonRequests = rivune.matching(dvdMetadataSeasonPath, "GET").length;
  const sourceRequests = rivune.matching("/api/v1/playback/sources", "POST").length;
  await page.reload();

  await expect(page.getByRole("heading", { name: "Disc Middle" })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/metadata/series/series-1", "GET").length).toBeGreaterThan(orderedSeriesRequests);
  const reloadedSeries = rivune.matching("/api/v1/metadata/series/series-1", "GET").at(-1)!;
  expect(Object.fromEntries(reloadedSeries.search)).toMatchObject({ mappingProvider: "tvdb", episodeOrder: "2" });
  await expect.poll(() => rivune.matching(dvdMetadataSeasonPath, "GET").length).toBeGreaterThan(orderedSeasonRequests);
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").length).toBeGreaterThan(sourceRequests);
  expect(rivune.matching("/api/v1/playback/sources", "POST").at(-1)?.body).toMatchObject({
    mediaType: "episode",
    resourceId: DVD_EPISODE_RESOURCE_ID,
  });
  await expect.poll(() => page.evaluate(() => window.history.state?.rivuneMediaItem?.raw)).toMatchObject({
    mappingProvider: "tvdb",
    episodeOrderId: "2",
    metadataSeasonId: DVD_METADATA_SEASON_ID,
    continueSeasonId: "persisted-dvd-season-1",
  });
});

test("canonical episode playback retains IMDb coordinates and marker requests", async ({ page, rivune }) => {
  await page.goto("/media/series/tt9000/season/1/episode/1");
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").at(-1)?.body).toMatchObject({
    mediaType: "episode",
    resourceId: "tt9000:1:1",
  });

  await page.locator(".details-stream-list__option").first().click();
  await page.locator('[data-media-action="play-selected-stream"]').click();
  await expect(page.locator(".player")).toBeVisible();
  const markerRequest = await rivune.waitForRequest("/api/v1/playback/markers", "GET");
  expect(Object.fromEntries(markerRequest.search)).toMatchObject({ imdbId: "tt9000", season: "1", episode: "1" });
});

test("TMDB media routes canonicalize to IMDb identifiers after metadata resolves", async ({ page, rivune }) => {
  await page.goto("/media/movie/tmdb:900201");
  await expect(page.getByRole("heading", { name: "Fixture Movie" })).toBeVisible();
  await expect(page).toHaveURL(/\/media\/movie\/tt9000201$/);
  expect(rivune.matching("/api/v1/titles/resolve", "POST").at(-1)?.body).toMatchObject({ mediaType: "movie", provider: "tmdb", externalId: "900201" });

  await page.goto("/media/series/tmdb:9000");
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page).toHaveURL(/\/media\/series\/tt9000$/);
  expect(rivune.matching("/api/v1/titles/resolve", "POST").at(-1)?.body).toMatchObject({ mediaType: "series", provider: "tmdb", externalId: "9000" });

  await page.reload();
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page).toHaveURL(/\/media\/series\/tt9000$/);
});

test("legacy media fragments cannot restore artwork URLs from the route or a version 1 cache", async ({ page, rivune }) => {
  const sentinelURLs = {
    poster: "https://legacy-poster.invalid/legacy-media-probe/poster.svg",
    background: "https://legacy-background.invalid/legacy-media-probe/background.svg",
    logo: "/legacy-media-probe/logo.svg",
  };
  const sentinelRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/legacy-media-probe/")) sentinelRequests.push(request.url());
  });
  await page.route("**/legacy-media-probe/**", (route) => route.fulfill({
    status: 200,
    contentType: "image/svg+xml",
    body: "<svg xmlns=\"http://www.w3.org/2000/svg\"/>",
  }));
  await page.addInitScript((urls) => {
    localStorage.setItem("rivune.metadata-cache.v1", JSON.stringify({
      version: 1,
      snapshots: {
        "en|movie|external:tmdb:900201": {
          updatedAt: Date.now(),
          value: {
            title: "Poisoned cached title",
            posterUrl: urls.poster,
            backgroundUrl: urls.background,
            logoUrl: urls.logo,
          },
        },
      },
      aliases: {},
    }));
  }, sentinelURLs);
  const query = new URLSearchParams({
    title: "Legacy Fixture Movie",
    titleId: "movie-1",
    releaseInfo: "2024",
    released: "2024-01-01",
    posterUrl: sentinelURLs.poster,
    backgroundUrl: sentinelURLs.background,
    logoUrl: sentinelURLs.logo,
    "external.tmdb": "900201",
    from: "search",
  });

  await page.goto(`/#media/movie/tmdb%3A900201?${query}`);

  await expect(page.getByRole("heading", { name: "Fixture Movie" })).toBeVisible();
  await expect(page.locator(".details-description")).toHaveText("A fixture movie used for deterministic tests.");
  await expect(page).toHaveURL(/\/media\/movie\/tt9000201$/);
  await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/poster.svg");
  await expect.poll(() => rivune.matching("/api/v1/metadata/titles/movie-1", "GET").length).toBeGreaterThan(0);
  await expect.poll(() => page.evaluate(() => {
    const stored = JSON.parse(localStorage.getItem("rivune.metadata-cache.v1") ?? "null") as { version?: unknown } | null;
    return stored?.version ?? null;
  })).toBe(2);
  expect(sentinelRequests).toEqual([]);
});

test("legacy series fragments preserve episode context while canonicalizing the route", async ({ page, rivune }) => {
  const query = new URLSearchParams({
    title: "Legacy Moonrise",
    titleId: "episode-3",
    releaseInfo: "S2 · E1",
    released: "2025-01-01",
    season: "2",
    episode: "1",
    seriesId: "series-1",
    seasonId: "season-2",
    episodeId: "episode-3",
    from: "search",
  });

  await page.goto(`/#media/episode/${encodeURIComponent("tt9000:2:1")}?${query}`);

  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/2\/episode\/1$/);
  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();
  await expect(page.locator(".details-description")).toHaveText("The team reunites on a distant moon.");
  await expect.poll(() => rivune.matching("/api/v1/metadata/seasons/season-2", "GET").length).toBeGreaterThan(0);
});

test("season route overrides stale history state for numeric season zero", async ({ page, rivune }) => {
  await page.goto("/media/series/tt9000/season/1");
  await expect(page.getByRole("button", { name: /First Light/ }).first()).toBeVisible();
  const seasonOneRequests = rivune.matching("/api/v1/metadata/seasons/season-1", "GET").length;

  await page.evaluate(() => {
    window.history.replaceState({
      rivuneMedia: true,
      rivuneOrigin: "home",
      rivuneMediaItem: {
        id: "tt9000",
        mediaType: "series",
        title: "Signal Horizon",
        seasonNumber: 1,
        raw: {
          routeSeriesResourceId: "tt9000",
          continueSeasonId: "season-1",
          continueSeasonNumber: 1,
        },
      },
    }, "", "/media/series/tt9000/season/0");
  });
  await page.reload();

  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/0$/);
  await expect(page.getByRole("tab", { name: /^Specials\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: /Fixture Episode 1/ }).first()).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/metadata/seasons/season-specials", "GET").length).toBeGreaterThan(0);
  expect(rivune.matching("/api/v1/metadata/seasons/season-1", "GET")).toHaveLength(seasonOneRequests);
});

test("series episodes open dedicated detail pages that own playback sources", async ({ page, rivune }) => {
  await page.goto("/media/series/tt9000/season/1");

  const seriesHeading = page.getByRole("heading", { name: "Signal Horizon" });
  await expect(seriesHeading).toBeAttached();
  await expect(page.locator(".details-logo")).toHaveAttribute("src", "https://fixtures.rivune.test/series-logo.svg");
  await expect(seriesHeading).toHaveClass(/visually-hidden/);
  await expect(page.getByRole("button", { name: /First Light/ }).first()).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
  expect(rivune.matching("/api/v1/playback/sources", "POST")).toHaveLength(0);

  await page.getByRole("button", { name: /First Light/ }).first().click();

  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1\/episode\/1$/);
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await expect(page.getByText("The crew follows a mysterious signal.")).toBeVisible();
  await expect(page.locator(".details-meta").getByText("Season 1 · Episode 1")).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toBeVisible();
  const sourceRequest = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(sourceRequest.body).toMatchObject({ mediaType: "episode", resourceId: "tt9000:1:1" });

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Signal Horizon" })).toBeAttached();
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
});

test("custom metadata preserves opaque playback while watchstate uses resolved UUIDs", async ({ page, rivune }) => {
  const sourceAddonID = "40000000-0000-4000-8000-000000000001";
  const sourceRequests: Array<Record<string, unknown>> = [];
  const rawArtworkRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("custom-anime-poster") || request.url().includes("raw-addon.invalid")) rawArtworkRequests.push(request.url());
  });
  await page.route(/\/api\/v1\/artwork\/(?:custom-anime-episode-(?:5|6|s2|loose|5-background)|custom-series-(?:poster|background)|custom-cast)$/i, (route) => route.fulfill({
    status: 200,
    contentType: "image/svg+xml",
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="320" height="180" fill="#345"/></svg>',
  }));
  await page.route(/\/api\/v1\/addons\/resources\/meta\/anime\/fixture-anime(?:%3A|:)1(?:\?.*)?$/i, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      results: [
        {
          addonId: "40000000-0000-4000-8000-000000000099",
          manifestId: "fixture-decoy-meta",
          resource: "meta",
          type: "anime",
          id: "fixture-anime:1",
          payload: { meta: [] },
        },
        {
          addonId: sourceAddonID,
          manifestId: "fixture-custom-meta",
          resource: "meta",
          type: "anime",
          id: "fixture-anime:1",
          payload: {
            meta: {
              id: "fixture-anime:1",
              type: "anime",
              name: "Fixture Anime",
              description: "",
              overview: "An authoritative custom series overview.",
              poster: "",
              posterUrl: "/api/v1/artwork/custom-series-poster",
              background: "",
              backdropUrl: "/api/v1/artwork/custom-series-background",
              cast: {
                actors: [
                  "String Performer",
                  { actor: "Name Only Performer" },
                  { id: "localized-cast", name: "Localized Portrait", role: "Guide", profileUrl: "", imageUrl: "/api/v1/artwork/custom-cast" },
                  { id: "remote-cast", name: "Remote Portrait", profileUrl: "https://raw-addon.invalid/portrait.jpg" },
                ],
              },
              links: [
                { name: "Action", category: "Genres", url: "stremio:///discover/action" },
                { name: "Fixture Performer One", category: "Cast", url: "stremio:///search?search=Fixture%20Performer%20One" },
                { name: "Fixture Performer Two", category: "Cast", url: "stremio:///search?search=Fixture%20Performer%20Two" },
              ],
              videos: [
                { id: "fixture-anime:1:5", title: "", name: "Fixture Episode Five", overview: "", description: "The fifth custom episode overview.", season: 1, episode: 5, released: "", releaseInfo: "2025-01-05T00:00:00.000Z", thumbnail: "", thumbnailUrl: "/api/v1/artwork/custom-anime-episode-5", background: "", backgroundUrl: "/api/v1/artwork/custom-anime-episode-5-background" },
                { id: "fixture-anime:1:6", title: "Fixture Episode Six", season: 1, episode: 6, released: "2025-01-12T00:00:00.000Z", thumbnail: "/api/v1/artwork/custom-anime-episode-6" },
                { id: "fixture-anime:1:s2:opaque-1", title: "Fixture Season Two Premiere", season: 2, episode: 1, released: "2099-01-04T00:00:00.000Z", thumbnail: "/api/v1/artwork/custom-anime-episode-s2" },
                { id: "constructor", title: "Fixture Unseasoned Video", episode: 99, released: "2026-02-01T00:00:00.000Z", thumbnail: "/api/v1/artwork/custom-anime-episode-loose" },
              ],
            },
          },
        },
      ],
      errors: [],
    }),
  }));
  await page.route("**/api/v1/playback/sources", (route) => {
    sourceRequests.push(route.request().postDataJSON() as Record<string, unknown>);
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        sources: [{
          id: "fixture-custom-source",
          sourceRef: "fixture-custom-source",
          stableIdentity: "stable-custom-source",
          addonId: "fixture-stream-addon",
          manifestId: "fixture-stream-addon",
          streamIndex: 0,
          name: "Custom Fixture 1080p",
          description: "Deterministic custom video stream",
          protocol: "http",
          container: "mp4",
          expiresAt: "2099-01-01T00:00:00.000Z",
        }],
        providerErrors: [],
      }),
    });
  });
  let releaseCustomResolution = () => undefined;
  const customResolutionGate = new Promise<void>((resolve) => { releaseCustomResolution = resolve; });
  let delayCustomResolution = true;
  await page.route("**/api/v1/titles/custom-series/resolve", async (route) => {
    if (!delayCustomResolution) return route.fallback();
    delayCustomResolution = false;
    await customResolutionGate;
    return route.fallback();
  });

  await page.goto("/media/anime/fixture-anime:1");

  await expect(page.getByRole("heading", { name: "Episodes" })).toBeVisible();
  const pendingRowWatch = page.getByRole("button", { name: "Mark Fixture Episode Five watched" });
  await expect(pendingRowWatch).toBeVisible();
  await expect(pendingRowWatch).toBeDisabled();
  await expect(pendingRowWatch).toHaveAttribute("aria-busy", "true");
  const pendingSeasonState = page.locator(".season-watch-state");
  await expect(pendingSeasonState).toContainText("0 of 2 watched");
  await expect(pendingSeasonState.getByRole("button")).toHaveCount(0);
  const animeActions = page.locator(".details-actions");
  const pendingSeasonWatch = animeActions.getByRole("button", { name: "Mark watched", exact: true });
  await expect(pendingSeasonWatch).toBeDisabled();
  await expect(pendingSeasonWatch).toHaveAttribute("aria-busy", "true");
  expect(await pendingSeasonWatch.evaluate((button) => button.previousElementSibling?.textContent?.trim())).toBe("Add to library");
  releaseCustomResolution();
  await expect.poll(() => rivune.matching("/api/v1/titles/custom-series/resolve", "POST").length).toBe(1);
  const resolverRequest = rivune.matching("/api/v1/titles/custom-series/resolve", "POST")[0];
  expect(resolverRequest.body).toMatchObject({
    sourceAddonId: sourceAddonID,
    sourceType: "anime",
    series: { resourceId: "fixture-anime:1", title: "Fixture Anime" },
    videos: [
      { resourceId: "fixture-anime:1:5", seasonNumber: 1, episodeNumber: 5, released: "2025-01-05" },
      { resourceId: "fixture-anime:1:6", seasonNumber: 1, episodeNumber: 6, released: "2025-01-12" },
      { resourceId: "fixture-anime:1:s2:opaque-1", seasonNumber: 2, episodeNumber: 1, released: "2099-01-04" },
    ],
  });
  const resolverBody = resolverRequest.body;
  if (!resolverBody || typeof resolverBody !== "object" || !("videos" in resolverBody) || !Array.isArray(resolverBody.videos)) throw new Error("custom resolver request omitted videos");
  expect(resolverBody.videos).toHaveLength(3);
  expect(JSON.stringify(resolverBody)).not.toContain("custom-anime-poster");
  expect(JSON.stringify(resolverBody)).not.toContain("raw-addon.invalid");
  expect(rivune.matching("/api/v1/titles/resolve", "POST")).toHaveLength(0);
  const progressRequest = await rivune.waitForRequest("/api/v1/progress/batch", "POST");
  const progressBody = progressRequest.body;
  if (!progressBody || typeof progressBody !== "object" || !("titleIds" in progressBody) || !isStringArray(progressBody.titleIds)) throw new Error("custom progress request omitted title IDs");
  const customTitleIDs = progressBody.titleIds;
  expect(customTitleIDs).toHaveLength(3);
  expect(customTitleIDs.every((titleID) => /^70000000-0000-4000-8000-\d{12}$/.test(titleID))).toBe(true);
  expect(customTitleIDs.some((titleID) => titleID.startsWith("fixture-anime:"))).toBe(false);
  const firstTitleID = customTitleIDs[0];
  if (!firstTitleID) throw new Error("custom progress request returned no title IDs");
  const hero = page.locator(".details-hero");
  const heroArtwork = page.locator(".details-artwork img");
  await expect(page.getByRole("heading", { name: "Fixture Anime" })).toBeVisible();
  await expect(page.locator(".details-meta").getByText("Anime", { exact: true })).toBeVisible();
  await expect(heroArtwork).toHaveAttribute("src", "/api/v1/artwork/custom-series-poster");
  await expect(hero).toHaveCSS("background-image", /custom-series-background/);
  await expect(page.locator(".details-description")).toHaveText("An authoritative custom series overview.");
  await expect(animeActions.getByRole("button", { name: "Mark watched", exact: true })).toHaveCount(1);

  const cast = page.getByRole("region", { name: "Cast" });
  await expect(cast.getByText("String Performer")).toBeVisible();
  await expect(cast.getByText("Name Only Performer")).toBeVisible();
  await expect(cast.getByText("Localized Portrait")).toBeVisible();
  await expect(cast.getByText("Fixture Performer One")).toBeVisible();
  await expect(cast.getByText("Fixture Performer Two")).toBeVisible();
  await expect(cast.getByText("Action")).toHaveCount(0);
  await expect(cast.locator("article", { hasText: "Localized Portrait" }).locator("img")).toHaveAttribute("src", "/api/v1/artwork/custom-cast");
  await expect(cast.getByText("Guide")).toBeVisible();
  await expect(cast.locator("article", { hasText: "Remote Portrait" }).locator("img")).toHaveCount(0);
  expect(rawArtworkRequests).toEqual([]);

  const seasonOneTab = page.getByRole("tab", { name: /^Season 1\b.*2 episodes$/ });
  const seasonTwoTab = page.getByRole("tab", { name: /^Season 2\b.*1 episode$/ });
  const unseasonedTab = page.getByRole("tab", { name: /^Unknown\b.*1 episode$/ });
  await expect(page.getByRole("tablist", { name: "Seasons" })).toBeVisible();
  await expect(seasonOneTab).toHaveAttribute("aria-selected", "true");
  await expect(seasonTwoTab).toHaveAttribute("aria-selected", "false");
  await expect(unseasonedTab).toHaveAttribute("aria-selected", "false");
  await expect(page.getByRole("button", { name: /Fixture Episode Six/ }).first()).toBeVisible();
  const firstCustomRow = page.locator(".episode-list--custom > div").filter({ hasText: "Fixture Episode Five" });
  await expect(firstCustomRow).toBeVisible();
  expect(await firstCustomRow.evaluate((row) => {
    const play = row.querySelector(".episode-play")?.getBoundingClientRect();
    const watched = row.querySelector(".episode-watched")?.getBoundingClientRect();
    const bounds = row.getBoundingClientRect();
    return Boolean(play && watched
      && watched.left >= play.right
      && Math.abs(watched.top - bounds.top) <= 1
      && Math.abs(watched.bottom - bounds.bottom) <= 1);
  })).toBe(true);
  const markSeasonWatched = animeActions.getByRole("button", { name: "Mark watched", exact: true });
  const watchedBatchesBeforeSeason = rivune.matching("/api/v1/titles/watched/batch", "PUT").length;
  await markSeasonWatched.click();
  await expect(animeActions.getByRole("button", { name: "Mark unwatched", exact: true })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/titles/watched/batch", "PUT").length).toBeGreaterThan(watchedBatchesBeforeSeason);
  expect(rivune.matching("/api/v1/titles/watched/batch", "PUT").at(-1)?.body).toEqual({
    items: customTitleIDs.slice(0, 2).map((titleId) => ({ titleId, completed: true, expectedVersion: 0 })),
  });
  const watchedBatchesBeforeReset = rivune.matching("/api/v1/titles/watched/batch", "PUT").length;
  await animeActions.getByRole("button", { name: "Mark unwatched", exact: true }).click();
  await expect(markSeasonWatched).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/titles/watched/batch", "PUT").length).toBeGreaterThan(watchedBatchesBeforeReset);
  expect(rivune.matching("/api/v1/titles/watched/batch", "PUT").at(-1)?.body).toEqual({
    items: customTitleIDs.slice(0, 2).map((titleId) => ({ titleId, completed: false, expectedVersion: 1 })),
  });
  expect(sourceRequests).not.toContainEqual(expect.objectContaining({ resourceId: "fixture-anime:1" }));
  await unseasonedTab.click();
  await expect(animeActions.getByRole("button", { name: /Mark (?:un)?watched/, exact: true })).toHaveCount(0);
  const looseVideo = page.getByRole("button", { name: /Fixture Unseasoned Video/ }).first();
  await expect(looseVideo).toBeVisible();
  await expect(page.getByRole("button", { name: /Mark Fixture Unseasoned Video/ })).toHaveCount(0);
  await looseVideo.click();
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "constructor" });
  expect(sourceRequests.at(-1)).not.toHaveProperty("addonId");
  await expect(page.locator(".details-actions").getByRole("button", { name: /Mark (?:un)?watched/ })).toHaveCount(0);
  const looseSource = page.getByRole("radio", { name: /Custom Fixture 1080p/ });
  await looseSource.click();
  await page.getByRole("button", { name: /Play selected stream.*Custom Fixture 1080p/ }).click();
  const loosePlaybackRequest = await rivune.waitForRequest("/api/v1/playback/resolve", "POST");
  expect(loosePlaybackRequest.body).toMatchObject({ sourceRef: "fixture-custom-source" });
  expect(loosePlaybackRequest.body).not.toHaveProperty("titleId");
  expect(rivune.matching("/api/v1/titles/resolve", "POST")).toHaveLength(0);
  await page.getByRole("button", { name: "Go back" }).click();

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page.getByRole("heading", { name: "Fixture Anime" })).toBeVisible();
  await expect(heroArtwork).toHaveAttribute("src", "/api/v1/artwork/custom-series-poster");
  await expect(hero).toHaveCSS("background-image", /custom-series-background/);
  await expect(page.locator(".details-description")).toHaveText("An authoritative custom series overview.");
  await seasonOneTab.click();
  const firstVideo = page.getByRole("button", { name: /Fixture Episode Five/ }).first();
  await expect(firstVideo.locator("img")).toHaveAttribute("src", "/api/v1/artwork/custom-anime-episode-5");
  await firstVideo.click();
  await expect(page.getByRole("heading", { name: "Fixture Episode Five" })).toBeVisible();
  await expect(page.locator(".details-artwork__episode-code")).toHaveText("S01 · E05");
  await expect(heroArtwork).toHaveAttribute("src", "/api/v1/artwork/custom-series-poster");
  await expect(hero).toHaveCSS("background-image", /custom-anime-episode-5-background/);
  await expect(page.locator(".details-description")).toHaveText("The fifth custom episode overview.");
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "fixture-anime:1:5" });
  expect(sourceRequests.at(-1)).not.toHaveProperty("addonId");

  const heroWatch = page.locator(".details-actions").getByRole("button", { name: "Mark watched" });
  await expect(heroWatch).toBeVisible();
  await heroWatch.click();
  await expect(page.locator(".details-actions").getByRole("button", { name: "Mark unwatched" })).toBeVisible();
  const watchedRequest = await rivune.waitForRequest(`/api/v1/titles/${firstTitleID}/watched`, "POST");
  expect(watchedRequest.body).toEqual({ expectedVersion: 2 });

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page.getByRole("heading", { name: "Fixture Anime" })).toBeVisible();
  await expect(heroArtwork).toHaveAttribute("src", "/api/v1/artwork/custom-series-poster");
  await expect(hero).toHaveCSS("background-image", /custom-series-background/);
  await expect(page.locator(".details-description")).toHaveText("An authoritative custom series overview.");
  const rowUnwatch = page.getByRole("button", { name: "Mark Fixture Episode Five unwatched" });
  await expect(rowUnwatch).toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: "Mark Fixture Episode Five unwatched" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Fixture Episode Five/ }).first().locator(".episode-progress")).toBeVisible();
  const progressBatchesBeforeConflict = rivune.matching("/api/v1/progress/batch", "POST").length;
  let rejectUnwatch = true;
  await page.route(new RegExp(`/api/v1/titles/${firstTitleID}/watched(?:\\?.*)?$`), (route) => {
    if (rejectUnwatch && route.request().method() === "DELETE") {
      rejectUnwatch = false;
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "version_conflict", message: "Progress changed elsewhere" } }),
      });
    }
    return route.fallback();
  });
  await page.getByRole("button", { name: "Mark Fixture Episode Five unwatched" }).click();
  await expect.poll(() => rivune.matching("/api/v1/progress/batch", "POST").length).toBeGreaterThan(progressBatchesBeforeConflict);
  await expect(page.getByRole("button", { name: "Mark Fixture Episode Five unwatched" })).toBeVisible();
  await page.getByRole("button", { name: "Mark Fixture Episode Five unwatched" }).click();
  await expect(page.getByRole("button", { name: "Mark Fixture Episode Five watched" })).toBeVisible();
  const unwatchedRequest = await rivune.waitForRequest(`/api/v1/titles/${firstTitleID}/watched`, "DELETE");
  expect(unwatchedRequest.search.get("expectedVersion")).toBe("3");

  await page.getByRole("button", { name: /Fixture Episode Five/ }).first().click();
  await expect(page.locator(".details-actions").getByRole("button", { name: "Mark watched" })).toBeVisible();
  const source = page.getByRole("radio", { name: /Custom Fixture 1080p/ });
  await source.click();
  expect(sourceRequests.at(-1)).not.toHaveProperty("addonId");
  const play = page.getByRole("button", { name: /Play selected stream.*Custom Fixture 1080p/ });
  await expect(play).toBeEnabled();
  const playbackRequestCount = rivune.matching("/api/v1/playback/resolve", "POST").length;
  await play.click();
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").length).toBeGreaterThan(playbackRequestCount);
  const playbackRequest = rivune.matching("/api/v1/playback/resolve", "POST").at(-1)!;
  expect(playbackRequest.body).toMatchObject({ sourceRef: "fixture-custom-source", titleId: firstTitleID });
  expect(rivune.matching(`/api/v1/progress/${firstTitleID}`, "GET").length).toBeGreaterThan(0);
  await page.getByRole("button", { name: "Go back" }).click();
  expect(rivune.requests.some((request) => request.pathname.startsWith("/api/v1/progress/fixture-anime%3A") || request.pathname.startsWith("/api/v1/titles/fixture-anime%3A"))).toBe(false);

  expect(sourceRequests).toContainEqual(expect.objectContaining({ mediaType: "anime", resourceId: "fixture-anime:1:5" }));
  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await seasonTwoTab.click();
  await expect(seasonTwoTab).toHaveAttribute("aria-selected", "true");
  const seasonTwoVideo = page.getByRole("button", { name: /Fixture Season Two Premiere/ }).first();
  await expect(seasonTwoVideo.locator("img")).toHaveAttribute("src", "/api/v1/artwork/custom-anime-episode-s2");
  await expect(page.getByRole("button", { name: "Mark Fixture Season Two Premiere watched" })).toBeDisabled();
  await seasonTwoVideo.click();
  await expect(page.locator(".details-actions").getByRole("button", { name: "Mark watched" })).toBeDisabled();
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "fixture-anime:1:s2:opaque-1" });
  expect(sourceRequests.at(-1)).not.toHaveProperty("addonId");
});

test("custom metadata honors default and returned video identifiers", async ({ page, rivune: _rivune }) => {
  const sourceRequests: Array<Record<string, unknown>> = [];
  await page.route(/\/api\/v1\/addons\/resources\/meta\/anime\/(catalog-default|catalog-single)(?:\?.*)?$/i, (route) => {
    const catalogID = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-1) ?? "");
    const usesDefault = catalogID === "catalog-default";
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        results: [{
          addonId: "40000000-0000-4000-8000-000000000001",
          manifestId: "fixture-custom-meta",
          resource: "meta",
          type: "anime",
          id: catalogID,
          payload: {
            meta: usesDefault
              ? {
                id: "meta-default",
                type: "anime",
                name: "Default video fixture",
                behaviorHints: { defaultVideoId: "video-default" },
                videos: [
                  { id: "video-default", title: "Default custom video", season: 1, episode: 1 },
                  { id: "video-explicit", title: "Explicit custom video", season: 1, episode: 2 },
                ],
              }
              : { id: "meta-single", type: "anime", name: "Single custom video" },
          },
        }],
        errors: [],
      }),
    });
  });
  await page.route("**/api/v1/playback/sources", (route) => {
    sourceRequests.push(route.request().postDataJSON() as Record<string, unknown>);
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ sources: [], providerErrors: [] }),
    });
  });

  await page.goto("/media/anime/catalog-default");
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "video-default" });
  expect(sourceRequests).not.toContainEqual(expect.objectContaining({ resourceId: "catalog-default" }));
  expect(sourceRequests.find((request) => request.resourceId === "video-default")).not.toHaveProperty("addonId");
  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page.getByRole("heading", { name: "Episodes" })).toBeFocused();
  await expect(page.getByRole("button", { name: /Default custom video/ }).first()).toHaveAttribute("aria-current", "true");
  await page.getByRole("button", { name: /Explicit custom video/ }).first().click();
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "video-explicit" });
  await expect(page.locator("#details-streams-title")).toBeFocused();

  await page.goto("/media/anime/catalog-single");
  await expect.poll(() => sourceRequests.at(-1)).toMatchObject({ mediaType: "anime", resourceId: "meta-single" });
  expect(sourceRequests).not.toContainEqual(expect.objectContaining({ resourceId: "catalog-single" }));
  expect(sourceRequests.find((request) => request.resourceId === "meta-single")).not.toHaveProperty("addonId");
});

test("an episode opened from its season toggles its resolved watched state once per action", async ({ page, rivune }) => {
  await page.route("**/api/v1/titles/episode-1/watched*", async (route) => {
    const { promise, resolve } = Promise.withResolvers<void>();
    setTimeout(resolve, 100);
    await promise;
    await route.fallback();
  });
  await page.goto("/media/series/tt9000/season/1");

  const aggregateActions = page.locator(".details-actions");
  const seasonWatchedButton = aggregateActions.getByRole("button", { name: "Mark watched", exact: true });
  await expect(seasonWatchedButton).toBeVisible();
  expect(await seasonWatchedButton.evaluate((button) => button.previousElementSibling?.textContent?.trim())).toBe("Trailers");
  await expect(page.locator(".season-watch-state").getByRole("button")).toHaveCount(0);
  await expect.poll(() => rivune.matching("/api/v1/progress/batch", "POST").length).toBe(1);
  expect(rivune.matching("/api/v1/progress/batch", "POST")[0]?.body).toEqual({ titleIds: ["episode-1", "episode-2"] });
  await page.getByRole("button", { name: /First Light/ }).first().click();

  const detailsActions = page.locator(".details-actions");
  const watchedButton = detailsActions.getByRole("button", { name: /Mark (?:un)?watched/ });
  await expect(watchedButton).toHaveText("Mark watched");
  await expect(watchedButton.locator("svg.lucide-eye")).toHaveCount(1);
  const watchedPath = "/api/v1/titles/episode-1/watched";
  expect(rivune.matching(watchedPath)).toHaveLength(0);

  await watchedButton.click();
  await expect(watchedButton).toBeDisabled();
  await expect(watchedButton.locator("svg.spin")).toHaveCount(1);
  await expect(watchedButton).toHaveText("Mark unwatched");
  await expect(watchedButton.locator("svg.lucide-eye-off")).toHaveCount(1);
  await expect(page.locator(".app-notification--success")).toContainText("First Light is marked as watched.");
  await expect.poll(() => rivune.matching(watchedPath, "POST").length).toBe(1);
  const markWatched = rivune.matching(watchedPath, "POST")[0]!;
  expect(markWatched.body).toEqual({ expectedVersion: 4 });
  expect(markWatched.search.toString()).toBe("");

  await watchedButton.click();
  await expect(watchedButton).toBeDisabled();
  await expect(watchedButton.locator("svg.spin")).toHaveCount(1);
  await expect(watchedButton).toHaveText("Mark watched");
  await expect(watchedButton.locator("svg.lucide-eye")).toHaveCount(1);
  await expect(page.locator(".app-notification--success").filter({ hasText: "First Light is marked as unwatched." })).toHaveCount(1);
  await expect.poll(() => rivune.matching(watchedPath, "DELETE").length).toBe(1);
  const markUnwatched = rivune.matching(watchedPath, "DELETE")[0]!;
  expect(markUnwatched.body).toBeUndefined();
  expect(markUnwatched.search.get("expectedVersion")).toBe("5");
  expect(rivune.matching(watchedPath)).toHaveLength(2);
});

test("a 100-episode season reads and mutates progress in one bounded request", async ({ page, rivune }) => {
  rivune.setSeason("season-1", longSeason(100));
  let readRequests = 0;
  let writeRequests = 0;
  let readTitleIds: string[] = [];
  let writeItems: Array<{ titleId: string; completed: boolean; expectedVersion: number }> = [];
  await page.route("**/api/v1/progress/batch", async (route) => {
    readRequests += 1;
    const payload: unknown = route.request().postDataJSON();
    if (payload === null || typeof payload !== "object" || !("titleIds" in payload) || !isStringArray(payload.titleIds)) {
      throw new Error("invalid progress batch request");
    }
    readTitleIds = payload.titleIds;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: readTitleIds.map((titleId, index) => ({
          titleId,
          progress: index === 0
            ? { titleId, mediaType: "episode", positionSeconds: 321, durationSeconds: 1800, completed: false, version: 4 }
            : null,
        })),
      }),
    });
  });
  await page.route("**/api/v1/titles/watched/batch", async (route) => {
    writeRequests += 1;
    const payload: unknown = route.request().postDataJSON();
    if (payload === null || typeof payload !== "object" || !("items" in payload) || !isWatchedBatchItems(payload.items)) {
      throw new Error("invalid watched batch request");
    }
    writeItems = payload.items;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: writeItems.map((input) => ({
          titleId: input.titleId,
          progress: {
            titleId: input.titleId,
            mediaType: "episode",
            positionSeconds: 1800,
            durationSeconds: 1800,
            completed: input.completed,
            version: input.expectedVersion + 1,
          },
        })),
      }),
    });
  });

  await page.goto("/media/series/tt9000/season/1");
  const markSeasonWatched = page.locator(".details-actions").getByRole("button", { name: "Mark watched", exact: true });
  await expect(markSeasonWatched).toBeVisible();
  await expect.poll(() => readRequests).toBe(1);
  expect(readTitleIds).toHaveLength(100);
  expect(readTitleIds[0]).toBe("episode-1");
  expect(readTitleIds[99]).toBe("episode-100");

  await markSeasonWatched.click();
  await expect(page.locator(".details-actions").getByRole("button", { name: "Mark unwatched", exact: true })).toBeVisible();
  await expect.poll(() => writeRequests).toBe(1);
  expect(writeItems).toHaveLength(100);
  expect(writeItems[0]).toEqual({ titleId: "episode-1", completed: true, expectedVersion: 4 });
  expect(writeItems[99]).toEqual({ titleId: "episode-100", completed: true, expectedVersion: 0 });
});

test("switching series seasons cannot mutate the previously loaded season", async ({ page, rivune }) => {
  const { promise: seasonTwoGate, resolve: releaseSeasonTwo } = Promise.withResolvers<void>();
  await page.route("**/api/v1/metadata/seasons/season-2*", async (route) => {
    await seasonTwoGate;
    await route.fallback();
  });

  await page.goto("/media/series/tt9000/season/1");
  const actions = page.locator(".details-actions");
  const seasonAction = actions.getByRole("button", { name: "Mark watched", exact: true });
  await expect(seasonAction).toBeEnabled();
  const writesBeforeSwitch = rivune.matching("/api/v1/titles/watched/batch", "PUT").length;

  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await expect(seasonAction).toBeDisabled();
  await expect(seasonAction).toHaveAttribute("aria-busy", "true");
  await seasonAction.evaluate((button) => (button as HTMLButtonElement).click());
  expect(rivune.matching("/api/v1/titles/watched/batch", "PUT")).toHaveLength(writesBeforeSwitch);

  releaseSeasonTwo();
  await expect(seasonAction).toBeEnabled();
  await seasonAction.click();
  await expect(actions.getByRole("button", { name: "Mark unwatched", exact: true })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/titles/watched/batch", "PUT").length).toBe(writesBeforeSwitch + 1);
  expect(rivune.matching("/api/v1/titles/watched/batch", "PUT").at(-1)?.body).toEqual({
    items: [{ titleId: "episode-3", completed: true, expectedVersion: 0 }],
  });
});

test("upcoming episodes stay dimmed but can open available playback sources", async ({ page, rivune }) => {
  const futureSeason = longSeason(1);
  futureSeason.episodes[0] = {
    ...futureSeason.episodes[0],
    name: "Early Signal",
    overview: "A stream arrived before the official air date.",
    airDate: "2099-01-01",
  };
  rivune.setSeason("season-1", futureSeason);

  await page.goto("/media/series/tt9000/season/1");
  const upcomingEpisode = page.locator("button.episode-main").filter({ hasText: "Early Signal" });
  await expect(upcomingEpisode).toContainText("Upcoming");
  await expect(upcomingEpisode).toBeEnabled();
  await expect(upcomingEpisode).toHaveClass(/is-upcoming/);
  await expect(upcomingEpisode).toHaveCSS("opacity", "0.5");

  await upcomingEpisode.click();

  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1\/episode\/1$/);
  await expect(page.getByRole("heading", { name: "Early Signal" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Playback sources" })).toBeVisible();
  const sourceRequest = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(sourceRequest.body).toMatchObject({ mediaType: "episode", resourceId: "tt9000:1:1" });
});

test("series artwork keeps season posters separate from episode backgrounds", async ({ page, rivune: _rivune }) => {
  const artwork = page.locator(".details-artwork img");
  const hero = page.locator(".details-hero");

  await page.goto("/media/series/tt9000/season/1");
  const firstEpisode = page.getByRole("button", { name: /First Light/ }).first();
  await expect(firstEpisode).toBeVisible();
  await expect(firstEpisode.locator("img")).toHaveAttribute("src", "https://fixtures.rivune.test/episode-1-still.svg");
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/season-1-backdrop.svg")');

  await firstEpisode.click();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/episode-1-backdrop.svg")');

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await page.getByRole("button", { name: /Second Orbit/ }).first().click();
  await expect(page.getByRole("heading", { name: "Second Orbit" })).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/episode-2-backdrop.svg")');

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-2-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/series-backdrop.svg")');
});

test("a direct episode route and reload retain its season poster and episode background", async ({ page, rivune: _rivune }) => {
  const artwork = page.locator(".details-artwork img");
  const hero = page.locator(".details-hero");

  await page.goto("/media/series/tt9000/season/1/episode/2");
  await expect(page.getByRole("heading", { name: "Second Orbit" })).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/episode-2-backdrop.svg")');

  await page.reload();
  await expect(page.getByRole("heading", { name: "Second Orbit" })).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/episode-2-backdrop.svg")');
});

test("series artwork falls back by role when season or episode artwork is absent", async ({ page, rivune: _rivune }) => {
  const artwork = page.locator(".details-artwork img");
  const hero = page.locator(".details-hero");

  await page.goto("/media/series/tt9000/season/0");
  await expect(page.getByRole("button", { name: /Fixture Episode 1/ }).first()).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/series-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/season-specials-backdrop.svg")');
  await expect(page.getByRole("region", { name: "Cast" }).getByText("Avery Stone")).toBeVisible();

  await page.getByRole("button", { name: /Fixture Episode 4/ }).first().click();
  await expect(page.getByRole("heading", { name: "Fixture Episode 4" })).toBeVisible();
  await expect(artwork).toHaveAttribute("src", "https://fixtures.rivune.test/series-poster.svg");
  await expect(hero).toHaveCSS("background-image", 'url("https://fixtures.rivune.test/season-specials-backdrop.svg")');
});

test("series guide omits seasons whose episode count is zero", async ({ page, rivune: _rivune }) => {
  await page.goto("/media/series/tt9000/season/1");

  await expect(page.getByRole("tab", { name: /^Specials\b/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: /^Season 3\b/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: /^Season 4\b/ })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: /^Season 5\b/ })).toBeVisible();
});

test("episode details float beside a responsive contextual stream panel", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  const detailsPage = page.locator(".details-page");
  await expect.poll(async () => (await detailsPage.boundingBox())?.y ?? -1).toBe(0);
  await expect.poll(async () => (await detailsPage.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(1000);

  const artwork = page.locator(".details-artwork");
  const primary = page.locator(".details-primary");
  const overview = page.locator(".details-overview");
  const contextPanel = page.getByRole("region", { name: "Playback sources" });
  const cast = page.getByRole("region", { name: "Cast" });
  await expect(artwork).toBeVisible();
  await expect(artwork.locator("img")).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect(page.locator(".series-browser")).toHaveCount(0);
  await expect(page.locator(".details-utility-grid")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Back.*Episodes/ })).toBeVisible();
  const fixtureSource = contextPanel.getByRole("radio", { name: /Fixture 1080p/ });
  await expect(fixtureSource).toBeVisible();
  const sourceRows = contextPanel.locator(".details-stream-list > div");
  await expect(sourceRows).toHaveCount(1);
  await expect(sourceRows.locator(".episode-play")).toHaveCount(0);
  await fixtureSource.click();
  await expect(sourceRows.first().getByRole("button", { name: "Play episode" })).toBeEnabled();
  await expect(page.locator(".details-actions .episode-play")).toHaveCount(0);
  await expect(page.locator(".details-sources")).toHaveCount(0);
  await expect(cast).toBeVisible();
  await expect(cast.getByText("Avery Stone")).toBeVisible();
  await expect(cast.getByText("Commander Ilya Voss")).toBeVisible();
  const detailsActions = page.locator(".details-actions");
  const trailerAction = detailsActions.getByRole("button", { name: "Trailers" });
  const watchedAction = detailsActions.getByRole("button", { name: /Mark (?:un)?watched/ });
  await expect(watchedAction).toHaveClass(/button--secondary/);
  await expect(page.locator(".details-context-actions .button--ghost")).toHaveCount(0);
  const trailerActionBounds = await trailerAction.boundingBox();
  const watchedActionBounds = await watchedAction.boundingBox();
  expect(trailerActionBounds).not.toBeNull();
  expect(watchedActionBounds).not.toBeNull();
  expect(watchedActionBounds!.x).toBeGreaterThan(trailerActionBounds!.x + trailerActionBounds!.width);
  expect(watchedActionBounds!.y).toBeCloseTo(trailerActionBounds!.y, 0);
  const desktopArtwork = await artwork.boundingBox();
  const desktopPrimary = await primary.boundingBox();
  const desktopPanel = await contextPanel.boundingBox();
  expect(desktopArtwork).not.toBeNull();
  expect(desktopPrimary).not.toBeNull();
  expect(desktopPanel).not.toBeNull();
  expect(desktopArtwork!.width / desktopArtwork!.height).toBeCloseTo(2 / 3, 1);
  expect(desktopArtwork!.width).toBeGreaterThanOrEqual(200);
  expect(desktopPanel!.x - (desktopPrimary!.x + desktopPrimary!.width)).toBeGreaterThanOrEqual(55);
  const desktopOverview = await overview.boundingBox();
  expect(desktopOverview).not.toBeNull();
  expect(desktopOverview!.width).toBeGreaterThanOrEqual(370);
  const desktopCast = await cast.boundingBox();
  expect(desktopCast).not.toBeNull();
  expect(desktopCast!.y).toBeGreaterThan(desktopArtwork!.y + desktopArtwork!.height);
  const desktopPage = await page.evaluate(() => ({ scrollHeight: document.documentElement.scrollHeight, viewportHeight: window.innerHeight }));
  expect(desktopPage.scrollHeight).toBeLessThanOrEqual(desktopPage.viewportHeight + 1);

  await page.setViewportSize({ width: 1920, height: 1080 });
  const wideLayout = await page.locator(".details-hero__inner").boundingBox();
  const wideArtwork = await artwork.boundingBox();
  const widePrimary = await primary.boundingBox();
  const wideOverview = await overview.boundingBox();
  const widePanel = await contextPanel.boundingBox();
  expect(wideLayout).not.toBeNull();
  expect(wideArtwork).not.toBeNull();
  expect(widePrimary).not.toBeNull();
  expect(wideOverview).not.toBeNull();
  expect(widePanel).not.toBeNull();
  expect(wideLayout!.width).toBeGreaterThan(1500);
  expect(wideArtwork!.width).toBeGreaterThanOrEqual(220);
  expect(wideOverview!.width).toBeGreaterThanOrEqual(600);
  expect(widePanel!.x - (widePrimary!.x + widePrimary!.width)).toBeGreaterThanOrEqual(75);
  const widePage = await page.evaluate(() => ({ scrollHeight: document.documentElement.scrollHeight, viewportHeight: window.innerHeight }));
  expect(widePage.scrollHeight).toBeLessThanOrEqual(widePage.viewportHeight + 1);
  await expect(cast).toBeVisible();

  await page.setViewportSize({ width: 1280, height: 720 });
  const compactDesktopPrimary = await primary.boundingBox();
  const compactDesktopPanel = await contextPanel.boundingBox();
  expect(compactDesktopPrimary).not.toBeNull();
  expect(compactDesktopPanel).not.toBeNull();
  expect(compactDesktopPanel!.x).toBeGreaterThan(compactDesktopPrimary!.x + compactDesktopPrimary!.width - 1);
  const compactDesktopPage = await page.evaluate(() => ({ scrollHeight: document.documentElement.scrollHeight, viewportHeight: window.innerHeight }));
  expect(compactDesktopPage.scrollHeight).toBeLessThanOrEqual(compactDesktopPage.viewportHeight + 1);
  await expect(cast).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(async () => (await detailsPage.boundingBox())?.y ?? -1).toBe(0);
  await expect.poll(async () => (await detailsPage.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(844);
  await expect(artwork).toBeVisible();
  const mobileArtwork = await artwork.boundingBox();
  const mobilePrimary = await primary.boundingBox();
  const mobilePanel = await contextPanel.boundingBox();
  expect(mobileArtwork).not.toBeNull();
  expect(mobilePrimary).not.toBeNull();
  expect(mobilePanel).not.toBeNull();
  expect(mobileArtwork!.width / mobileArtwork!.height).toBeCloseTo(2 / 3, 1);
  expect(mobilePanel!.y).toBeGreaterThanOrEqual(mobilePrimary!.y + mobilePrimary!.height - 1);
  await expect(cast).toBeVisible();
  const mobileCastOverflow = await cast.locator(".details-cast__list").evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(mobileCastOverflow.scrollWidth).toBeGreaterThan(mobileCastOverflow.clientWidth);
  const mobileCarousel = cast.locator(".details-cast__list");
  const mobileCastBounds = await mobileCarousel.boundingBox();
  expect(mobileCastBounds).not.toBeNull();
  const touchSession = await page.context().newCDPSession(page);
  await touchSession.send("Input.synthesizeScrollGesture", {
    x: Math.round(mobileCastBounds!.x + mobileCastBounds!.width * .75),
    y: Math.round(mobileCastBounds!.y + mobileCastBounds!.height / 2),
    xDistance: -Math.round(mobileCastBounds!.width * .5),
    yDistance: 0,
    gestureSourceType: "touch",
    speed: 800,
  });
  await expect.poll(() => mobileCarousel.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await touchSession.detach();
  const mobilePageWidth = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(mobilePageWidth.scrollWidth).toBeLessThanOrEqual(mobilePageWidth.clientWidth);
});

test("direct anime route exposes canonical cast in one draggable keyboard carousel", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/media/series/tt21209876");

  await expect(page.getByRole("heading", { name: "Solo Leveling" })).toBeVisible();
  const cast = page.getByRole("region", { name: "Cast" });
  const carousel = cast.locator(".details-cast__list");
  await expect(cast).toBeVisible();
  await expect(cast.locator(".details-cast-member")).toHaveCount(8);
  await expect(cast.getByText("Taito Ban")).toBeVisible();
  await expect(cast.getByText("Sung Jinwoo")).toBeVisible();
  await expect(page.getByRole("button", { name: "View all" })).toHaveCount(0);
  await expect(page.locator(".cast-drawer, .cast-drawer-backdrop")).toHaveCount(0);
  const castOverflow = await carousel.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(castOverflow.scrollWidth).toBeGreaterThan(castOverflow.clientWidth);
  const castGeometry = await cast.locator(".details-cast-member").evaluateAll((members) => members.map((member) => {
    const bounds = member.getBoundingClientRect();
    return { x: bounds.x, width: bounds.width };
  }));
  const memberWidths = castGeometry.map(({ width }) => Math.round(width));
  const memberGaps = castGeometry.slice(1).map(({ x }, index) => Math.round(x - castGeometry[index].x - castGeometry[index].width));
  expect(Math.max(...memberWidths) - Math.min(...memberWidths)).toBeLessThanOrEqual(1);
  expect(Math.max(...memberGaps) - Math.min(...memberGaps)).toBeLessThanOrEqual(1);

  await carousel.focus();
  await expect(carousel).toBeFocused();
  const startScrollTolerance = await carousel.evaluate((element) => Number.parseFloat(getComputedStyle(element).paddingLeft) + 1);
  await page.keyboard.press("End");
  await expect.poll(() => carousel.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await page.keyboard.press("Home");
  await expect.poll(() => carousel.evaluate((element) => element.scrollLeft)).toBeLessThanOrEqual(startScrollTolerance);
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => carousel.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  await page.keyboard.press("Home");
  await expect.poll(() => carousel.evaluate((element) => element.scrollLeft)).toBeLessThanOrEqual(startScrollTolerance);

  const bounds = await carousel.boundingBox();
  expect(bounds).not.toBeNull();
  await page.mouse.move(bounds!.x + bounds!.width * .8, bounds!.y + bounds!.height / 2);
  await page.mouse.down();
  await page.mouse.move(bounds!.x + bounds!.width * .2, bounds!.y + bounds!.height / 2, { steps: 8 });
  await page.mouse.up();
  await expect.poll(() => carousel.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
  const pageWidth = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(pageWidth.scrollWidth).toBeLessThanOrEqual(pageWidth.clientWidth);
});

test("cast rendering stops at the effective profile limit", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 2560, height: 1440 });
  await page.evaluate(async () => {
    const response = await fetch("/api/v1/settings", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ maximumCastMembers: 5 }),
    });
    if (!response.ok) throw new Error(`Unable to configure cast fixture: ${response.status}`);
  });
  await page.goto("/media/series/tt21209876");

  const cast = page.getByRole("region", { name: "Cast" });
  await expect(cast.locator(".details-cast-member")).toHaveCount(5);
  await expect(cast.getByText("Hiroki Touchi")).toBeVisible();
  await expect(cast.getByText("Haruna Mikawa")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "View all" })).toHaveCount(0);
  await expect(page.getByRole("dialog", { name: "Cast" })).toHaveCount(0);
  const castBounds = await cast.boundingBox();
  const carouselBounds = await cast.locator(".details-cast__list").boundingBox();
  expect(castBounds).not.toBeNull();
  expect(carouselBounds).not.toBeNull();
  expect(carouselBounds!.width).toBeLessThan(castBounds!.width);
  const emptyRightSideIsDraggable = await page.evaluate(({ x, y }) => Boolean(
    document.elementFromPoint(x, y)?.closest(".details-cast__list"),
  ), {
    x: Math.round((carouselBounds!.x + carouselBounds!.width + castBounds!.x + castBounds!.width) / 2),
    y: Math.round(carouselBounds!.y + carouselBounds!.height / 2),
  });
  expect(emptyRightSideIsDraggable).toBe(false);
});

test("movie details retain cast and one playback action per source", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/media/movie/tt9000201");

  await expect(page.getByRole("heading", { name: "Fixture Movie" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Cast" }).getByText("Fixture Performer", { exact: true })).toBeVisible();
  await expect(page.locator(".details-actions").getByRole("button", { name: /Mark (?:un)?watched/ })).toHaveClass(/button--secondary/);
  const sources = page.getByRole("region", { name: "Playback sources" });
  const sourceRows = sources.locator(".details-stream-list > div");
  await expect(sourceRows).toHaveCount(1);
  const movieSource = sourceRows.getByRole("radio", { name: /Fixture 1080p/ });
  await expect(movieSource).toBeVisible();
  await expect(sourceRows.locator(".episode-play")).toHaveCount(0);
  await movieSource.click();
  await expect(sourceRows.getByRole("button", { name: /Play selected stream.*Fixture 1080p/ })).toBeEnabled();
  await expect(page.locator(".details-actions .episode-play")).toHaveCount(0);
  await expect(page.locator(".details-sources")).toHaveCount(0);
  const pageWidth = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(pageWidth.scrollWidth).toBeLessThanOrEqual(pageWidth.clientWidth);
});

test("continue-watching episode returns to its dedicated season panel", async ({ page, rivune }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1\/episode\/1$/);
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/1$/);
  await expect(page.getByRole("region", { name: "Playback sources" })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: /^Season 1\b/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: /First Light/ }).first()).toBeVisible();

  await page.getByRole("button", { name: "Trailers" }).click();
  const trailerRegion = page.getByRole("region", { name: /Trailers for/ });
  await expect(trailerRegion).toBeVisible();
  await expect(trailerRegion.locator("iframe")).toHaveAttribute("title", /Season One Trailer/);
  const trailerStage = page.locator(".details-trailer-stage");
  const stageBounds = await trailerStage.boundingBox();
  const trailerBounds = await trailerRegion.boundingBox();
  expect(stageBounds).not.toBeNull();
  expect(trailerBounds).not.toBeNull();
  expect(stageBounds!.width).toBeCloseTo(1280, 0);
  expect(stageBounds!.height).toBeCloseTo(720, 0);
  expect(trailerBounds!.width).toBeLessThan(stageBounds!.width);
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

test("trailers remain available for movie, series, season, and episode title contexts", async ({ page, rivune }) => {
  await page.goto("/media/movie/tt9000201");
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Fixture Movie Trailer/);
  const movieRequest = await rivune.waitForRequest("/api/v1/metadata/titles/movie-1/trailers", "GET");
  expect(movieRequest.search.get("seasonNumber")).toBeNull();

  await page.getByRole("button", { name: "Dismiss trailer" }).click();
  await page.goto("/media/series/tt9000/season/1");
  await expect(page.getByRole("tab", { name: /^Season 1\b/ })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Season One Trailer/);
  const seriesRequest = await rivune.waitForRequest("/api/v1/metadata/titles/series-1/trailers", "GET");
  expect(seriesRequest.search.get("seasonNumber")).toBe("1");

  await page.getByRole("button", { name: "Dismiss trailer" }).click();
  await page.goto("/media/series/tt9000/season/1/episode/1");
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Season One Trailer/);
  const episodeRequest = rivune.matching("/api/v1/metadata/titles/series-1/trailers", "GET").at(-1);
  expect(episodeRequest?.search.get("seasonNumber")).toBe("1");
});

test("trailer overlay keeps cinematic fallbacks and clips its card and embedded frame", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/media/movie/tt9000201");
  await page.getByRole("button", { name: "Trailers" }).click();

  const stage = page.locator(".details-trailer-stage");
  const backdrop = stage.locator(".details-trailer-stage__backdrop");
  const frame = stage.locator(".details-trailer__frame");
  await expect(backdrop).toHaveCSS("background-image", /backdrop\.svg.*poster\.svg/);
  await expect(backdrop).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  await expect(frame.locator("iframe")).toHaveAttribute("src", /[?&]vq=highres(?:&|$)/);
  await expect(backdrop).toHaveCSS("filter", "blur(18px)");
  expect(await stage.evaluate((element) => getComputedStyle(element, "::before").backdropFilter)).toBe("none");

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 1920, height: 1080 },
  ]) {
    await page.setViewportSize(viewport);
    await expect(frame).toBeVisible();
    const clipping = await frame.evaluate((element) => {
      const iframe = element.querySelector("iframe");
      if (!iframe) throw new Error("Trailer iframe is missing");
      const frameStyle = getComputedStyle(element);
      const iframeStyle = getComputedStyle(iframe);
      const frameBounds = element.getBoundingClientRect();
      const iframeBounds = iframe.getBoundingClientRect();
      return {
        frameOverflow: frameStyle.overflow,
        frameRadius: Number.parseFloat(frameStyle.borderTopLeftRadius),
        frameClip: frameStyle.clipPath,
        iframeRadius: Number.parseFloat(iframeStyle.borderTopLeftRadius),
        iframeClip: iframeStyle.clipPath,
        inset: Math.max(
          Math.abs(frameBounds.left - iframeBounds.left),
          Math.abs(frameBounds.top - iframeBounds.top),
          Math.abs(frameBounds.right - iframeBounds.right),
          Math.abs(frameBounds.bottom - iframeBounds.bottom),
        ),
      };
    });
    expect(clipping.frameOverflow).toBe("hidden");
    expect(clipping.frameRadius).toBeGreaterThan(0);
    expect(clipping.frameClip).not.toBe("none");
    expect(clipping.iframeRadius).toBeGreaterThan(0);
    expect(clipping.iframeClip).not.toBe("none");
    expect(clipping.inset).toBeLessThanOrEqual(1);
    const stageBounds = await stage.boundingBox();
    expect(stageBounds?.width).toBeCloseTo(viewport.width, 0);
    expect(stageBounds?.height).toBeCloseTo(viewport.height, 0);
    const backdropBounds = await backdrop.boundingBox();
    expect(backdropBounds).not.toBeNull();
    expect(backdropBounds!.x).toBeLessThan(0);
    expect(backdropBounds!.y).toBeLessThan(0);
    expect(backdropBounds!.x + backdropBounds!.width).toBeGreaterThan(viewport.width);
    expect(backdropBounds!.y + backdropBounds!.height).toBeGreaterThan(viewport.height);
  }
});

test("trailer overlay uses a solid fallback when title artwork is absent", async ({ page, rivune: _rivune }) => {
  await page.route(/\/api\/v1\/metadata\/titles\/movie-1(?:\?.*)?$/, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ id: "movie-1", mediaType: "movie", title: "Fixture Movie", overview: "No artwork fixture", externalIds: { imdb: "tt9000201" } }),
  }));
  await page.goto("/media/movie/tt9000201");
  await page.getByRole("button", { name: "Trailers" }).click();

  const backdrop = page.locator(".details-trailer-stage__backdrop");
  await expect(backdrop).toHaveCSS("background-image", "none");
  await expect(backdrop).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
});

test("an unavailable trailer warns once without rendering an empty stage", async ({ page, rivune: _rivune }) => {
  await page.route(/\/api\/v1\/metadata\/titles\/movie-1\/trailers(?:\?.*)?$/, (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ trailers: [] }),
  }));
  await page.goto("/media/movie/tt9000201");
  const trailerButton = page.getByRole("button", { name: "Trailers" });
  await trailerButton.click();

  await expect(page.locator(".details-trailer-stage")).toHaveCount(0);
  await expect(page.locator(".details-trailer")).toHaveCount(0);
  const warning = page.locator(".app-notification--warning");
  await expect(warning).toHaveCount(1);
  await expect(warning).toHaveAttribute("role", "status");
  await expect(warning).toContainText("Trailer unavailable");
  await expect(warning).toContainText("No trailer is available for this title.");
  await expect(warning.locator("svg").first()).toHaveClass(/lucide-triangle-alert/);
  await expect(trailerButton).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(warning).toHaveCount(1);
  await expect(page.locator(".details-trailer-stage")).toHaveCount(0);
});

test("resolved artwork remains visible while revisiting metadata is revalidated", async ({ page, rivune: _rivune }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("rivune.metadata-cache.v1") ?? "")).toContain("https://fixtures.rivune.test/season-1-poster.svg");
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
    await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/season-1-poster.svg", { timeout: 500 });
  } finally {
    releaseRequest.resolve();
  }
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
});

test("series guide switches to a selected TVDB episode order", async ({ page, rivune }) => {
  await page.goto("/media/series/tt9000/season/1");

  const order = page.getByRole("combobox", { name: "Episode order" });
  await expect(order).toBeVisible();
  await expect.poll(async () => (await selectOptions(order)).map((option) => option.label)).toEqual([
    "Profile default",
    "Aired Order",
    "DVD Order",
    "Absolute Order",
    "Story Order",
    "Streaming Order",
  ]);
  const orderListbox = await selectListbox(order);
  const triggerTypography = await order.evaluate((element) => {
    const style = getComputedStyle(element);
    return { fontFamily: style.fontFamily, fontSize: style.fontSize, fontWeight: style.fontWeight, letterSpacing: style.letterSpacing, lineHeight: style.lineHeight };
  });
  const optionTypography = await orderListbox.getByRole("option").nth(1).evaluate((element) => {
    const style = getComputedStyle(element);
    return { fontFamily: style.fontFamily, fontSize: style.fontSize, fontWeight: style.fontWeight, letterSpacing: style.letterSpacing, lineHeight: style.lineHeight };
  });
  expect(optionTypography).toEqual(triggerTypography);

  await selectOption(order, "2");
  await expect(order).toHaveAttribute("data-value", "2");
  await expect(page.getByRole("tab", { name: /^Season 1.*3 episodes/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Disc Opening/ }).first()).toBeVisible();

  await expect.poll(() => rivune.matching("/api/v1/metadata/series/series-1", "GET")
    .some((request) => request.search.get("mappingProvider") === "tvdb" && request.search.get("episodeOrder") === "2")).toBe(true);
  await expect.poll(() => rivune.matching(dvdMetadataSeasonPath, "GET")
    .some((request) => request.search.get("mappingProvider") === "tvdb")).toBe(true);
});

test("season selector supports horizontal mouse dragging without changing the active season", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.goto("/media/series/tt9000/season/1");

  const seasons = page.locator(".season-tabs");
  const activeSeason = page.getByRole("tab", { name: /^Season 1\b/ });
  await expect(seasons).toBeVisible();
  await expect(activeSeason).toHaveAttribute("aria-selected", "true");
  await seasons.scrollIntoViewIfNeeded();
  const bounds = await seasons.boundingBox();
  if (!bounds) throw new Error("Missing season selector bounds");

  const maxScrollLeft = await seasons.evaluate((element) => element.scrollWidth - element.clientWidth);
  expect(maxScrollLeft).toBeGreaterThan(80);
  const dragDistance = Math.min(120, Math.floor(maxScrollLeft / 2));
  const dragStartX = bounds.x + Math.min(bounds.width - 60, 720);
  await page.mouse.move(dragStartX, bounds.y + bounds.height / 2);
  await page.mouse.down();
  await page.mouse.move(dragStartX - dragDistance, bounds.y + bounds.height / 2, { steps: 4 });
  await expect.poll(() => seasons.evaluate((element) => element.scrollLeft)).toBeGreaterThan(dragDistance - 10);
  const draggedScrollLeft = await seasons.evaluate((element) => element.scrollLeft);
  await page.mouse.up();

  const momentumTarget = Math.min(maxScrollLeft - 1, draggedScrollLeft + 4);
  await expect.poll(() => seasons.evaluate((element) => element.scrollLeft)).toBeGreaterThan(momentumTarget);
  await expect(activeSeason).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: /^Season 2\b/ }).click();
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "true");
});

test("long seasons use a one-column bounded episode scroller", async ({ page, rivune }) => {
  rivune.setSeason("season-1", longSeason(200));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/media/series/tt9000/season/1");

  const list = page.locator(".episode-list");
  const rows = list.locator(":scope > div");
  await expect(rows).toHaveCount(200);
  const externalPages = page.getByRole("group", { name: "External title pages" });
  await expect(externalPages).toBeVisible();
  await expect(externalPages.getByRole("link")).toHaveCount(3);
  await expect(externalPages.getByRole("link", { name: /Open IMDb title page/ })).toHaveAttribute("href", "https://www.imdb.com/title/tt9000/");
  await expect(externalPages.getByRole("link", { name: /Open TMDB title page/ })).toHaveAttribute("href", "https://www.themoviedb.org/tv/9000");
  await expect(externalPages.getByRole("link", { name: /Open TVDB title page/ })).toHaveAttribute("href", "https://thetvdb.com/dereferrer/series/9900");
  await list.scrollIntoViewIfNeeded();
  const metrics = await list.evaluate((element) => {
    const rowRects = Array.from(element.children, (row) => row.getBoundingClientRect());
    const rowGap = Number.parseFloat(getComputedStyle(element).rowGap) || 0;
    const firstRowHeight = rowRects[0]?.height ?? 0;
    const visibleRowCapacity = firstRowHeight > 0 ? Math.floor((element.clientHeight + rowGap) / (firstRowHeight + rowGap)) : 0;
    return {
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      visibleRowCapacity,
      firstRowX: rowRects[0]?.x,
      secondRowX: rowRects[1]?.x,
    };
  });
  expect(metrics.scrollHeight).toBeGreaterThan(metrics.clientHeight);
  const pageMetrics = await page.evaluate(() => ({ scrollHeight: document.documentElement.scrollHeight, viewportHeight: window.innerHeight }));
  expect(pageMetrics.scrollHeight).toBeLessThanOrEqual(pageMetrics.viewportHeight + 1);
  expect(metrics.clientHeight).toBeLessThanOrEqual(744);
  expect(metrics.visibleRowCapacity).toBeGreaterThanOrEqual(5);
  expect(metrics.visibleRowCapacity).toBeLessThanOrEqual(6);
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
  await expect(page).toHaveURL(/\/media\/series\/tt9000\/season\/2\/episode\/1$/);

  await expect(page.getByRole("heading", { name: "Moonrise" })).toBeVisible();
  await expect(page.locator(".details-meta").getByText("Season 2 · Episode 1")).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Back.*Episodes/ })).toBeVisible();
  await rivune.waitForRequest("/api/v1/metadata/seasons/season-2", "GET");
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ mediaType: "episode", resourceId: "tt9000:2:1" }));

  const calendarRequest = await rivune.waitForRequest("/api/v1/calendar", "GET");
  expect(calendarRequest.search.get("from")).toMatch(/^\d{4}-\d{2}-01$/);
  expect(calendarRequest.search.get("to")).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  expect(calendarRequest.search.get("language")).toBe("en-US");
});

test("calendar subscription stays private through create, copy, rotation, feed access, and revocation", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Calendar" }).click();

  const subscriptionAction = page.getByRole("button", { name: "Calendar subscription", exact: true });
  await expect(subscriptionAction).toBeVisible();
  await subscriptionAction.click();

  const subscriptionDialog = page.getByRole("dialog", { name: "Subscribe to this calendar" });
  const subscriptionHeading = subscriptionDialog.getByRole("heading", { name: "Subscribe to this calendar" });
  await expect(subscriptionHeading).toBeFocused();
  const statusRequest = await rivune.waitForRequest("/api/v1/profiles/alice/calendar-link", "GET");
  expect(statusRequest.authorization).toBe("Bearer fixture-access");
  expect(statusRequest.profileContext).toBe("fixture-profile-context-alice-0");
  expect(statusRequest.search.size).toBe(0);
  await expect(subscriptionDialog.getByText("For security, the full link is shown only when it is created or regenerated.")).toBeVisible();
  await expect(subscriptionDialog.getByRole("button", { name: "Create private link" })).toBeVisible();

  await subscriptionDialog.getByRole("button", { name: "Create private link" }).click();
  const createRequest = await rivune.waitForRequest("/api/v1/profiles/alice/calendar-link", "POST");
  expect(createRequest.body).toBeUndefined();
  expect(createRequest.authorization).toBe("Bearer fixture-access");
  expect(createRequest.profileContext).toBe("fixture-profile-context-alice-0");

  const linkInput = subscriptionDialog.getByLabel("Private calendar link");
  await expect(linkInput).toBeVisible();
  await expect(linkInput).toBeFocused();
  const firstURL = await linkInput.inputValue();
  expect(firstURL).toMatch(/^https?:\/\/[^/]+\/api\/v1\/calendar\.ics\?token=rivune_cal_[A-Za-z0-9_-]{43}$/);
  await expect(subscriptionDialog.getByText("Anyone with this link can read this profile's release calendar. Keep it private.")).toBeVisible();
  const token = new URL(firstURL).searchParams.get("token")!;
  expect(await page.evaluate((secret) => [...Object.values(localStorage), ...Object.values(sessionStorage)].some((value) => value.includes(secret)), token)).toBe(false);

  await subscriptionDialog.getByRole("button", { name: "Copy link" }).click();
  await expect(subscriptionDialog.getByText("Link copied", { exact: true })).toBeVisible();
  expect(await page.evaluate(async (expected) => (await navigator.clipboard.readText()) === expected, firstURL)).toBe(true);

  const geometry = await subscriptionDialog.locator(".calendar-subscription-modal").evaluate((dialog) => {
    const bounds = dialog.getBoundingClientRect();
    const controlBounds = Array.from(dialog.querySelectorAll<HTMLElement>("button, input"), (control) => control.getBoundingClientRect());
    return {
      left: bounds.left,
      right: bounds.right,
      viewportWidth: window.innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      controlsMeetTouchTarget: controlBounds.every((control) => control.height >= 48),
      controlsStayInside: controlBounds.every((control) => control.left >= bounds.left && control.right <= bounds.right),
    };
  });
  expect(geometry.left).toBeGreaterThanOrEqual(0);
  expect(geometry.right).toBeLessThanOrEqual(geometry.viewportWidth);
  expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth);
  expect(geometry.controlsMeetTouchTarget).toBe(true);
  expect(geometry.controlsStayInside).toBe(true);

  await page.evaluate(() => { document.documentElement.dir = "rtl"; });
  await expect(linkInput).toHaveAttribute("dir", "ltr");
  const regenerateAction = subscriptionDialog.getByRole("button", { name: "Regenerate link" });
  const disableAction = subscriptionDialog.getByRole("button", { name: "Disable link" });
  await regenerateAction.focus();
  await page.keyboard.press("ArrowDown");
  await expect(disableAction).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(regenerateAction).toBeFocused();
  await page.evaluate(() => { document.documentElement.dir = "ltr"; });

  await subscriptionDialog.getByRole("button", { name: "Close" }).click();
  await expect(subscriptionAction).toBeFocused();
  const statusRequestsBeforeReopen = rivune.matching("/api/v1/profiles/alice/calendar-link", "GET").length;
  await subscriptionAction.click();
  await expect.poll(() => rivune.matching("/api/v1/profiles/alice/calendar-link", "GET").length).toBeGreaterThan(statusRequestsBeforeReopen);
  await expect(subscriptionDialog.getByLabel("Private calendar link")).toHaveCount(0);
  await expect(subscriptionDialog.getByText("For security, the full link is shown only when it is created or regenerated.")).toBeVisible();

  await page.route("**/api/v1/profiles/alice/calendar-link/rotate", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "fixture_rotation_failure", message: "Fixture rotation failure" } }) }), { times: 1 });
  await subscriptionDialog.getByRole("button", { name: "Regenerate link" }).click();
  let confirmation = page.getByRole("dialog", { name: "Regenerate the calendar link?", exact: true });
  await expect(confirmation).toBeVisible();
  await expect(confirmation).toHaveAccessibleDescription("The current link will stop working immediately. Calendar apps using it must be updated with the new link.");
  await expect(confirmation.getByText("The current link will stop working immediately. Calendar apps using it must be updated with the new link.", { exact: true })).toBeVisible();
  expect(rivune.matching("/api/v1/profiles/alice/calendar-link/rotate", "POST")).toHaveLength(0);
  await confirmation.getByRole("button", { name: "Regenerate link" }).click();
  await expect(subscriptionDialog.getByText("The calendar subscription link could not be regenerated.")).toBeVisible();
  await expect(subscriptionDialog.getByLabel("Private calendar link")).toHaveCount(0);
  await expect(subscriptionDialog.getByText("A calendar subscription link is active for this profile.")).toBeVisible();

  await subscriptionDialog.getByRole("button", { name: "Regenerate link" }).click();
  confirmation = page.getByRole("dialog", { name: "Regenerate the calendar link?", exact: true });
  await expect(confirmation.getByText("The current link will stop working immediately. Calendar apps using it must be updated with the new link.", { exact: true })).toBeVisible();
  expect(rivune.matching("/api/v1/profiles/alice/calendar-link/rotate", "POST")).toHaveLength(0);
  await confirmation.getByRole("button", { name: "Regenerate link" }).click();
  const rotateRequest = await rivune.waitForRequest("/api/v1/profiles/alice/calendar-link/rotate", "POST");
  expect(rotateRequest.body).toBeUndefined();
  expect(rotateRequest.authorization).toBe("Bearer fixture-access");
  expect(rotateRequest.profileContext).toBe("fixture-profile-context-alice-0");
  await expect(linkInput).not.toHaveValue(firstURL);
  await expect(linkInput).toBeFocused();
  const secondURL = await linkInput.inputValue();

  const oldFeedStatus = await page.evaluate(async (url) => (await fetch(url, { credentials: "omit" })).status, firstURL);
  expect(oldFeedStatus).toBe(404);
  const liveFeed = await page.evaluate(async (url) => {
    const response = await fetch(url, { credentials: "omit" });
    return {
      status: response.status,
      contentType: response.headers.get("content-type"),
      disposition: response.headers.get("content-disposition"),
      cacheControl: response.headers.get("cache-control"),
      robots: response.headers.get("x-robots-tag"),
      bodyStartsWithCalendar: (await response.text()).startsWith("BEGIN:VCALENDAR\r\n"),
    };
  }, secondURL);
  expect(liveFeed).toEqual({
    status: 200,
    contentType: "text/calendar; charset=utf-8",
    disposition: "inline; filename=\"rivune-calendar.ics\"",
    cacheControl: "private, no-store",
    robots: "noindex, nofollow",
    bodyStartsWithCalendar: true,
  });
  expect(await page.evaluate(async (url) => (await fetch(url, { method: "HEAD", credentials: "omit" })).status, secondURL)).toBe(200);
  for (const publicRequest of rivune.matching("/api/v1/calendar.ics")) {
    expect(publicRequest.authorization).toBeNull();
    expect(publicRequest.profileContext).toBeNull();
    expect(publicRequest.search.size).toBe(0);
  }

  await subscriptionDialog.getByRole("button", { name: "Disable link" }).click();
  const disableConfirmation = page.getByRole("dialog", { name: "Disable the calendar link?", exact: true });
  await expect(disableConfirmation).toBeVisible();
  await expect(disableConfirmation).toHaveAccessibleDescription("Calendar apps using this link will stop receiving updates.");
  await expect(disableConfirmation.getByText("Calendar apps using this link will stop receiving updates.", { exact: true })).toBeVisible();
  expect(rivune.matching("/api/v1/profiles/alice/calendar-link", "DELETE")).toHaveLength(0);
  await disableConfirmation.getByRole("button", { name: "Disable link" }).click();
  const disableRequest = await rivune.waitForRequest("/api/v1/profiles/alice/calendar-link", "DELETE");
  expect(disableRequest.authorization).toBe("Bearer fixture-access");
  expect(disableRequest.profileContext).toBe("fixture-profile-context-alice-0");
  const createAgain = subscriptionDialog.getByRole("button", { name: "Create private link" });
  await expect(createAgain).toBeVisible();
  await expect(createAgain).toBeFocused();
  expect(await page.evaluate(async (url) => (await fetch(url, { credentials: "omit" })).status, secondURL)).toBe(404);

  await subscriptionDialog.getByRole("button", { name: "Close" }).click();
  await rivune.configureGlobalAdmin(page, "bob", CATEGORY_IDS.kids);
  await page.reload();
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Calendar" }).click();
  await expect(page.getByRole("button", { name: "Calendar subscription", exact: true })).toHaveCount(0);
});

test("calendar mobile agenda stays compact, ordered, accessible, and keyboard navigable", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("**/api/v1/calendar**", async (route) => {
    const from = new URL(route.request().url()).searchParams.get("from")!;
    const prefix = from.slice(0, 8);
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        events: [
          { id: "later", titleId: "later", mediaType: "movie", title: "Later", releaseDate: `${prefix}12` },
          { id: "zulu", titleId: "zulu", mediaType: "movie", title: "Zulu", releaseDate: `${prefix}02` },
          { id: "alpha", titleId: "alpha", mediaType: "movie", title: "Alpha", releaseDate: `${prefix}02` },
        ],
      }),
    });
  });

  await page.goto("/");
  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "Calendar" }).click();

  const agenda = page.locator(".calendar-agenda");
  await expect(agenda).toBeVisible();
  expect(await agenda.locator(".calendar-agenda__day time").evaluateAll((times) => times.map((time) => time.getAttribute("datetime")))).toEqual([
    expect.stringMatching(/-02$/),
    expect.stringMatching(/-12$/),
  ]);
  const cards = agenda.locator(".calendar-event");
  await expect(cards).toHaveCount(3);
  await expect(cards.locator(".calendar-event__copy > strong")).toHaveText(["Alpha", "Zulu", "Later"]);
  await expect(cards.first()).toHaveAttribute("title", /Alpha.*Movie/);
  await expect(cards.first()).toBeVisible();
  await expect(cards.last()).toBeVisible();

  const mobileMetrics = await page.evaluate(() => {
    const agenda = document.querySelector<HTMLElement>(".calendar-agenda")!;
    const cardElements = Array.from(agenda.querySelectorAll<HTMLElement>(".calendar-event"));
    const cardRects = cardElements.map((card) => card.getBoundingClientRect());
    const titles = Array.from(agenda.querySelectorAll<HTMLElement>(".calendar-event__copy > strong"));
    return {
      agendaHeight: agenda.getBoundingClientRect().height,
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      cardsStayInsideViewport: cardRects.every((rect) => rect.left >= 0 && rect.right <= window.innerWidth),
      cardsMeetTouchTarget: cardRects.every((rect) => rect.height >= 48),
      titlesFit: titles.every((title) => title.scrollWidth <= title.clientWidth && title.scrollHeight <= title.clientHeight),
    };
  });
  expect(mobileMetrics.scrollWidth).toBeLessThanOrEqual(mobileMetrics.clientWidth);
  expect(mobileMetrics.cardsStayInsideViewport).toBe(true);
  expect(mobileMetrics.cardsMeetTouchTarget).toBe(true);
  expect(mobileMetrics.titlesFit).toBe(true);
  expect(mobileMetrics.agendaHeight).toBeLessThan(300);

  await cards.first().focus();
  await page.keyboard.press("ArrowDown");
  await expect(cards.nth(1)).toBeFocused();
  await page.keyboard.press("End");
  await expect(cards.last()).toBeFocused();
  await page.keyboard.press("Home");
  await expect(cards.first()).toBeFocused();
});

test("calendar desktop grid is calm and readable while preserving month, day, RTL, and toolbar keyboard contracts", async ({ page, rivune: _rivune }) => {
  const requestedMonths: string[] = [];
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.route("**/api/v1/calendar**", async (route) => {
    const from = new URL(route.request().url()).searchParams.get("from")!;
    const initialFrom = requestedMonths[0] ?? from;
    requestedMonths.push(from);
    const prefix = from.slice(0, 8);
    const events = from === initialFrom ? [
      { id: "feature", titleId: "feature", mediaType: "movie", title: "A Calm and Unexpectedly Long Release Title", releaseDate: `${prefix}02`, posterUrl: "https://fixtures.rivune.test/poster.svg" },
      { id: "second", titleId: "second", mediaType: "movie", title: "Second Feature", releaseDate: `${prefix}02`, posterUrl: "https://fixtures.rivune.test/season-1-poster.svg" },
      { id: "third", titleId: "third", mediaType: "movie", title: "Behind the Release", releaseDate: `${prefix}02`, posterUrl: "https://fixtures.rivune.test/season-2-poster.svg" },
      { id: "fourth", titleId: "fourth", mediaType: "movie", title: "Fourth Item", releaseDate: `${prefix}02`, posterUrl: "https://fixtures.rivune.test/series-poster.svg" },
      { id: "episode", titleId: "episode", mediaType: "episode", title: "The Arrival", releaseDate: `${prefix}12`, seriesTitle: "Harbor Stories", seriesId: "harbor", seasonNumber: 2, episodeNumber: 4 },
    ] : [];
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ events }) });
  });

  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Calendar" }).click();

  const grid = page.locator(".calendar-grid");
  const days = grid.locator(".calendar-day[data-calendar-day]");
  const monthHeading = page.locator(".calendar-toolbar h3");
  await expect(grid).toBeVisible();
  await expect(days).toHaveCount(new Date(Number(requestedMonths[0].slice(0, 4)), Number(requestedMonths[0].slice(5, 7)), 0).getDate());
  await expect(page.locator(".calendar-weekdays span").first()).toHaveText("Sun");
  await expect(grid.locator(".calendar-event")).toHaveCount(5);
  await expect(grid.locator(".calendar-event__copy > strong").first()).toHaveText("A Calm and Unexpectedly Long Release Title");
  await expect(grid.locator(".calendar-event").first()).toHaveAttribute("title", /A Calm and Unexpectedly Long Release Title.*Movie/);
  const firstPosterImage = grid.locator(".calendar-event__poster img").first();
  await expect(firstPosterImage).toBeVisible();
  await expect.poll(() => firstPosterImage.evaluate((image) => image.naturalWidth)).toBeGreaterThan(0);
  await expect(grid.locator(".calendar-event__kind").first()).toBeVisible();
  await expect(grid.locator(".calendar-event__copy small").first()).toBeVisible();
  await expect(page.locator(".calendar-empty--grid")).toHaveCount(0);

  const desktopMetrics = await page.evaluate(() => {
    const grid = document.querySelector<HTMLElement>(".calendar-grid")!;
    const surface = document.querySelector<HTMLElement>(".calendar-surface")!;
    const eventCards = Array.from(grid.querySelectorAll<HTMLElement>(".calendar-event"));
    const firstPoster = eventCards[0].querySelector<HTMLElement>(".calendar-event__poster")!;
    const crowdedEvents = grid.querySelector<HTMLElement>('.calendar-day[data-calendar-day="2"] .calendar-day__events')!;
    const crowdedCards = Array.from(crowdedEvents.querySelectorAll<HTMLElement>(".calendar-event"));
    const gridRect = grid.getBoundingClientRect();
    const crowdedRect = crowdedEvents.getBoundingClientRect();
    return {
      gridHeight: gridRect.height,
      gridWidth: gridRect.width,
      surfaceHeight: surface.getBoundingClientRect().height,
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      eventHeight: eventCards[0].getBoundingClientRect().height,
      posterWidth: firstPoster.getBoundingClientRect().width,
      visibleCrowdedEvents: crowdedCards.filter((card) => {
        const rect = card.getBoundingClientRect();
        return rect.top >= crowdedRect.top && rect.bottom <= crowdedRect.bottom + 3;
      }).length,
      crowdedDayScrollable: crowdedEvents.scrollHeight > crowdedEvents.clientHeight,
      crowdedOverflowY: getComputedStyle(crowdedEvents).overflowY,
      fourthEventRequiresScroll: crowdedCards[3].getBoundingClientRect().bottom > crowdedRect.bottom + 1,
      eventsStayInsideGrid: eventCards.every((card) => {
        const rect = card.getBoundingClientRect();
        return rect.left >= gridRect.left && rect.right <= gridRect.right;
      }),
    };
  });
  expect(desktopMetrics.gridHeight).toBeGreaterThanOrEqual(578);
  expect(desktopMetrics.gridHeight).toBeLessThanOrEqual(720);
  expect(desktopMetrics.gridWidth).toBeGreaterThan(900);
  expect(desktopMetrics.surfaceHeight).toBeLessThan(850);
  expect(desktopMetrics.eventHeight).toBeGreaterThanOrEqual(48);
  expect(desktopMetrics.posterWidth).toBeGreaterThanOrEqual(32);
  expect(desktopMetrics.visibleCrowdedEvents).toBe(3);
  expect(desktopMetrics.crowdedDayScrollable).toBe(true);
  expect(desktopMetrics.crowdedOverflowY).toBe("auto");
  expect(desktopMetrics.fourthEventRequiresScroll).toBe(true);
  expect(desktopMetrics.eventsStayInsideGrid).toBe(true);
  expect(desktopMetrics.scrollWidth).toBeLessThanOrEqual(desktopMetrics.clientWidth);
  const crowdedDayEvents = grid.locator('.calendar-day[data-calendar-day="2"] .calendar-day__events');
  await crowdedDayEvents.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  expect(await crowdedDayEvents.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
  await crowdedDayEvents.evaluate((element) => { element.scrollTop = 0; });

  await days.first().focus();
  await page.keyboard.press("ArrowRight");
  await expect(days.nth(1)).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(days.first()).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(days.nth(7)).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(days.first()).toBeFocused();
  await page.keyboard.press("End");
  await expect(days.last()).toBeFocused();
  await page.keyboard.press("Home");
  await expect(days.first()).toBeFocused();

  await page.evaluate(() => { document.documentElement.dir = "rtl"; });
  await days.first().focus();
  await page.keyboard.press("ArrowLeft");
  await expect(days.nth(1)).toBeFocused();
  await page.evaluate(() => { document.documentElement.dir = "ltr"; });

  const initialMonthLabel = await monthHeading.textContent();
  await days.first().focus();
  await page.keyboard.press("PageDown");
  await expect(monthHeading).not.toHaveText(initialMonthLabel ?? "");
  await expect(days.first()).toBeFocused();
  await page.keyboard.press("PageUp");
  await expect(monthHeading).toHaveText(initialMonthLabel ?? "");
  await expect(days.first()).toBeFocused();

  const nextMonth = page.getByRole("button", { name: "Next month" });
  await nextMonth.focus();
  await nextMonth.click();
  await expect(monthHeading).not.toHaveText(initialMonthLabel ?? "");
  await expect(page.locator(".calendar-empty--grid")).toBeVisible();
  await expect(nextMonth).toBeFocused();
  expect(requestedMonths.some((requestedMonth) => requestedMonth !== requestedMonths[0])).toBe(true);

  const today = page.getByRole("button", { name: "Today" });
  await today.click();
  await expect(monthHeading).toHaveText(initialMonthLabel ?? "");
  await expect(today).toBeFocused();
});

test("calendar TVDB episode opens the mapped season containing its canonical episode ID", async ({ page, rivune: _rivune }) => {
  const episodeID = "c51c40ea-594f-4ee1-9502-b13144533733";
  const releaseDate = new Date().toISOString().slice(0, 10);
  const requestedSeasons: string[] = [];
  const seasonTwo = {
    id: "official-season-2", mediaType: "season", seriesId: "series-1", name: "Season 2", overview: "", seasonNumber: 2, episodeCount: 1, airDate: releaseDate, voteAverage: 0, externalIds: { tvdb: "930202" },
    episodes: [
      { id: "different-official-season-2-episode", mediaType: "episode", seasonId: "official-season-2", name: "Fixture Episode Alternate", overview: "", seasonNumber: 2, episodeNumber: 241, airDate: releaseDate, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "940241" } },
    ],
  };
  const seasonNine = {
    id: "official-season-9", mediaType: "season", seriesId: "series-1", name: "Season 9", overview: "", seasonNumber: 9, episodeCount: 1, airDate: "2025-08-25", voteAverage: 0, externalIds: { tvdb: "930209" },
    episodes: [
      { id: episodeID, mediaType: "episode", seasonId: "official-season-9", name: "Fixture Episode", overview: "", seasonNumber: 9, episodeNumber: 241, airDate: releaseDate, voteAverage: 0, voteCount: 0, externalIds: { tvdb: "949241" } },
    ],
  };
  const seasonSummaries = [seasonTwo, seasonNine].map(({ episodes: _episodes, ...seasonSummary }) => seasonSummary);

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.slice("/api/v1".length);
    const fulfill = (body: unknown) => route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
    if (path === "/calendar") {
      await fulfill({ events: [{ id: "calendar-episode-241", titleId: episodeID, mediaType: "episode", title: "Fixture Episode", releaseDate, resourceId: "949241", resourceProvider: "tvdb", seriesTitle: "Fixture Series Beta", seriesId: "series-1", seasonId: "canonical-season-2", seasonNumber: 2, episodeNumber: 241 }] });
      return;
    }
    if (path === "/metadata/series/series-1") {
      await fulfill({ id: "series-1", mediaType: "series", name: "Fixture Series Beta", originalName: "Fixture Series Beta", originalLanguage: "fr", overview: "", firstAirDate: "2024-01-01", numberOfSeasons: 9, numberOfEpisodes: 241, genres: [], voteAverage: 0, voteCount: 0, seasons: seasonSummaries, episodeOrders: [], mappingProvider: "tvdb", externalIds: { tvdb: "900302", tmdb: "900202" } });
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
  await page.getByRole("button", { name: "Open Fixture Episode details" }).click();

  await expect(page.getByRole("heading", { name: "Fixture Episode" })).toBeVisible();
  await expect(page.locator(".details-meta").getByText("Season 9 · Episode 241")).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);
  expect(requestedSeasons).toEqual(["official-season-2", "official-season-9"]);

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page).toHaveURL(/\/media\/series\/tvdb:900302\/season\/9$/);
  await expect(page.getByRole("tab", { name: /Season 9/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "false");
  expect(requestedSeasons).toEqual(["official-season-2", "official-season-9", "official-season-9"]);
});

test("home honors a collection's landscape covers and preferred title logo", async ({ page, rivune: _rivune }) => {
  const folder = {
    id: "streaming-folder",
    title: "Streaming",
    tileShape: "poster",
    coverImageUrl: "https://fixtures.rivune.test/streaming-landscape.svg",
    titleLogoUrl: "https://fixtures.rivune.test/streaming-title.svg",
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
        heroEnabled: true,
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
        items: [{ id: "streaming-title", mediaType: "movie", title: "Landscape fixture", posterUrl: "https://fixtures.rivune.test/poster.svg", logoUrl: "https://fixtures.rivune.test/item-title.svg" }],
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
  await expect(page.locator(".hero__content img")).toHaveAttribute("src", "https://fixtures.rivune.test/streaming-title.svg");
  const visual = card.locator(".folder-cover-card__visual");
  const size = await visual.evaluate((element) => {
    const bounds = element.getBoundingClientRect();
    return { width: bounds.width, height: bounds.height };
  });
  expect(size.width / size.height).toBeCloseTo(16 / 9, 1);
});
