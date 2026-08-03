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
  await expect(page.getByRole("button", { name: /Building a World/ }).first()).toBeVisible();
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
  await expect(artwork.locator("img")).toHaveAttribute("src", "https://fixtures.rivune.test/episode-1.svg");
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
  const mobilePageWidth = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(mobilePageWidth.scrollWidth).toBeLessThanOrEqual(mobilePageWidth.clientWidth);
});

test("direct anime route exposes canonical cast in a focus-safe drawer", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/media/series/tt21209876");

  await expect(page.getByRole("heading", { name: "Solo Leveling" })).toBeVisible();
  const cast = page.getByRole("region", { name: "Cast" });
  await expect(cast).toBeVisible();
  await expect(cast.locator(".details-cast-member")).toHaveCount(6);
  await expect(cast.getByText("Taito Ban")).toBeVisible();
  await expect(cast.getByText("Sung Jinwoo")).toBeVisible();
  const castOverflow = await cast.locator(".details-cast__list").evaluate((element) => ({
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

  const viewAll = cast.getByRole("button", { name: "View all" });
  await viewAll.click();
  const drawer = page.getByRole("dialog", { name: "Cast" });
  await expect(drawer).toBeVisible();
  await expect(drawer.locator(".details-cast-member")).toHaveCount(8);
  const closeDrawer = drawer.getByRole("button", { name: "Close" });
  await expect(closeDrawer).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(closeDrawer).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(closeDrawer).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(viewAll).toBeFocused();
});

test("movie details retain cast and one playback action per source", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/media/movie/tt0137523");

  await expect(page.getByRole("heading", { name: "Fight Club" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Cast" }).getByText("Edward Norton")).toBeVisible();
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
  await page.goto("/media/movie/tt0137523");
  await page.getByRole("button", { name: "Trailers" }).click();
  await expect(page.getByRole("region", { name: /Trailers for/ }).locator("iframe")).toHaveAttribute("title", /Fight Club Trailer/);
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
  await page.goto("/media/movie/tt0137523");
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
    body: JSON.stringify({ id: "movie-1", mediaType: "movie", title: "Fight Club", overview: "No artwork fixture", externalIds: { imdb: "tt0137523" } }),
  }));
  await page.goto("/media/movie/tt0137523");
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
  await page.goto("/media/movie/tt0137523");
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
  await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/episode-1.svg");
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
    await expect(page.locator(".details-artwork img")).toHaveAttribute("src", "https://fixtures.rivune.test/episode-1.svg", { timeout: 500 });
  } finally {
    releaseRequest.resolve();
  }
  await expect(page.getByRole("heading", { name: "First Light" })).toBeVisible();
});

test("series guide switches to a selected TVDB episode order", async ({ page, rivune }) => {
  await page.goto("/media/series/tt9000/season/1");

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
  await page.goto("/media/series/tt9000/season/1");

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

  await expect(page.getByRole("heading", { name: "Episode 241" })).toBeVisible();
  await expect(page.locator(".details-meta").getByText("Season 9 · Episode 241")).toBeVisible();
  await expect(page.locator(".series-browser")).toHaveCount(0);
  expect(requestedSeasons).toEqual(["official-season-2", "official-season-9"]);

  await page.getByRole("button", { name: /Back.*Episodes/ }).click();
  await expect(page).toHaveURL(/\/media\/series\/tvdb:331147\/season\/9$/);
  await expect(page.getByRole("tab", { name: /Season 9/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab", { name: /^Season 2\b/ })).toHaveAttribute("aria-selected", "false");
  expect(requestedSeasons).toEqual(["official-season-2", "official-season-9", "official-season-9"]);
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
