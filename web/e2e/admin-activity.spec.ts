import { expect, test } from "./fixtures/rivune";

const now = "2026-07-31T12:00:00Z";

const activity = {
  summary: { activeSessions: 3, activeJobs: 2, processingSlots: 1, processingLimit: 2, storageBytes: 0, storageLimitBytes: 1_073_741_824 },
  diagnostics: { ffmpegVersion: "7.1", ffprobeVersion: "7.1", hardwareAcceleration: "auto", videoEncoder: "vaapi", preferredVideoCodec: "hevc", encodeCodecs: ["h264", "hevc", "av1"], decodeCodecs: ["h264", "hevc"], qualityPreset: "quality", hardwareToneMap: true, toneMapBackend: "vulkan", transcodeThreads: 4, maximumReadRate: 1.5, totals: { started: 7, succeeded: 5, failed: 1, softwareFallbacks: 1 }, pools: { process: { active: 1, limit: 2 }, probe: { active: 0, limit: 2 }, subtitle: { active: 0, limit: 2 }, trickplay: { active: 0, limit: 1 } } },
  sessions: [
    {
      id: "11111111-1111-4111-8111-111111111111",
      titleId: "episode-1",
      artworkUrl: "https://fixtures.rivune.test/activity-artwork.jpg",
      externalIds: { imdb: "tt9000001", tmdb: "900001", tvdb: "9000006" },
      externalIdMediaTypes: { imdb: "series", tmdb: "series", tvdb: "episode" },
      title: "Fixture Animated Series · S05E06 · Fixture Episode",
      mediaType: "episode",
      mode: "transcode",
      username: "fixture-owner",
      profileId: "11111111-1111-4111-8111-111111111112",
      profile: "Alice",
      device: "Living room",
      platform: "Web",
      processing: true,
      positionSeconds: 605,
      durationSeconds: 1320,
      createdAt: now,
      lastSeenAt: now,
      expiresAt: "2026-07-31T13:00:00Z",
      decision: {
        reason: "video_transcode_required",
        videoAction: "transcode",
        audioAction: "transcode",
        subtitleAction: "burn",
        toneMapping: true,
        source: { container: "mkv", videoCodec: "h265", audioCodec: "dts", height: 2160, videoBitrateKbps: 28_000, hdrFormat: "hdr10" },
        target: { protocol: "hls", container: "mpegts", videoCodec: "h264", audioCodec: "aac", height: 1080, videoBitrateKbps: 8_000 },
      },
    },
    {
      id: "22222222-2222-4222-8222-222222222222",
      title: "Metadata pending",
      mediaType: "movie",
      mode: "direct",
      username: "fixture-owner",
      profileId: "11111111-1111-4111-8111-111111111112",
      profile: "Alice",
      device: "Phone",
      platform: "Web",
      processing: false,
      positionSeconds: 0,
      durationSeconds: 0,
      createdAt: now,
      lastSeenAt: now,
      expiresAt: "2026-07-31T13:00:00Z",
      artworkUrl: "https://fixtures.rivune.test/missing-artwork.jpg",
      externalIds: {},
    },
    {
      id: "33333333-3333-4333-8333-333333333333",
      title: "Long feature",
      mediaType: "movie",
      mode: "direct",
      username: "fixture-owner",
      profileId: "11111111-1111-4111-8111-111111111112",
      profile: "Alice",
      device: "Bedroom",
      platform: "Web",
      processing: false,
      positionSeconds: 605,
      durationSeconds: 10_140,
      createdAt: now,
      lastSeenAt: now,
      expiresAt: "2026-07-31T15:00:00Z",
      externalIds: {},
    },
  ],
  jobs: [
    {
      sessionId: "11111111-1111-4111-8111-111111111111",
      assetId: "episode-1-source",
      mode: "transcode",
      state: "processing",
      prewarming: false,
      createdAt: now,
      lastSeenAt: now,
      progressPercent: 42.4,
      speed: 1.037,
      startupDurationSeconds: 3.25,
    },
    {
      assetId: "queued-movie-source",
      mode: "transcode",
      state: "processing",
      prewarming: true,
      createdAt: now,
      lastSeenAt: now,
      progressPercent: -4,
      speed: 1.2,
    },
  ],
};

test("now-playing sessions render artwork, provider badges, transcoding progress, and a stable missing-metadata fallback", async ({ page, rivune: _rivune }) => {
  await page.route("**/api/v1/playback/activity", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(activity) });
  });
  await page.route("https://fixtures.rivune.test/activity-artwork.jpg", async (route) => {
    await route.fulfill({ status: 200, contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="80" height="120"><rect width="80" height="120" fill="#345"/></svg>' });
  });
  await page.route("https://fixtures.rivune.test/missing-artwork.jpg", async (route) => {
    await route.fulfill({ status: 404, body: "missing" });
  });

  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: /Activity/ }).click();

  const artworkSession = page.locator(".activity-session").filter({ hasText: "Fixture Animated Series · S05E06 · Fixture Episode" });
  const artwork = artworkSession.getByRole("img", { name: "Fixture Animated Series · S05E06 · Fixture Episode" });
  await expect(artwork).toBeVisible();
  await expect(artwork).toHaveAttribute("loading", "lazy");
  await expect(artworkSession.locator(".activity-session__copy > strong")).toHaveText("Fixture Animated Series · S05E06 · Fixture Episode");
  await expect(artworkSession.getByLabel("IMDb · tt9000001")).toHaveAttribute("href", "https://www.imdb.com/title/tt9000001/");
  const tmdb = artworkSession.getByLabel("TMDB · 900001");
  await expect(tmdb).toHaveAttribute("href", "https://www.themoviedb.org/tv/900001");
  await expect(tmdb.locator("svg")).toHaveCount(1);
  await expect(artworkSession.getByLabel("TVDB · 9000006")).toHaveAttribute("href", "https://thetvdb.com/dereferrer/episode/9000006");
  await expect(artworkSession.locator(".activity-session__provider")).toHaveCount(3);
  const sessionProgress = artworkSession.locator(".activity-session__progress");
  await expect(sessionProgress).toContainText("10m 5s / 22m");
  await expect(sessionProgress).toContainText("46%");
  const playbackProgress = artworkSession.getByRole("progressbar", { name: "10m 5s / 22m" });
  await expect(playbackProgress).toBeVisible();
  expect(await playbackProgress.evaluate((progress: HTMLProgressElement) => progress.value / progress.max)).toBeCloseTo(605 / 1320, 4);
  await expect(artworkSession.locator(".activity-progress-status")).toHaveText("42% · 1.04× · start 3.25s");
  await expect(artworkSession.getByText("Video conversion required")).toBeVisible();
  await expect(artworkSession.getByText("Video H265 → H264")).toBeVisible();
  await expect(artworkSession.getByText("Audio DTS → AAC")).toBeVisible();
  await expect(artworkSession.getByText("Resolution 2160p → 1080p")).toBeVisible();
  await expect(artworkSession.getByText(/Target bitrate 8.?000/)).toBeVisible();
  await expect(artworkSession.getByText("Burn-in subtitles")).toBeVisible();
  await expect(artworkSession.getByText("Tone mapping")).toBeVisible();
  await expect(artworkSession.getByText("Transcode", { exact: true })).toHaveCount(2);
  await expect(page.locator(".activity-overview")).toContainText("1.50× max");
  await expect(page.locator(".activity-overview")).toContainText("total 7/5/1 · fallback 1");
  await expect(page.locator(".activity-overview")).toContainText("Hardware tone mapping (VULKAN)");
  await expect(page.locator(".activity-overview")).toContainText("Preferred HEVC");
  await expect(page.locator(".activity-overview")).toContainText("Encode · H264, HEVC, AV1");
  await expect(page.locator(".activity-overview")).toContainText("Decode · H264, HEVC");
  await expect(page.locator(".activity-overview")).toContainText("Quality · QUALITY");

  const longSession = page.locator(".activity-session").filter({ hasText: "Long feature" });
  await expect(longSession.getByRole("progressbar", { name: "10m 5s / 2h 49m" })).toBeVisible();
  const desktopTimeBounds = await artworkSession.locator(".activity-session__time").boundingBox();
  const desktopStopBounds = await artworkSession.getByRole("button", { name: "Stop", exact: true }).boundingBox();
  expect(desktopTimeBounds).not.toBeNull();
  expect(desktopStopBounds).not.toBeNull();
  expect((desktopTimeBounds?.x ?? 0) + (desktopTimeBounds?.width ?? Number.POSITIVE_INFINITY)).toBeLessThanOrEqual(desktopStopBounds?.x ?? 0);
  expect(Math.abs((desktopTimeBounds?.y ?? 0) - (desktopStopBounds?.y ?? Number.POSITIVE_INFINITY))).toBeLessThanOrEqual(1);
  const desktopProgressBounds = await artworkSession.locator(".activity-session__progress").boundingBox();
  const desktopAgeBounds = await artworkSession.locator(".activity-session__time small").boundingBox();
  expect(desktopProgressBounds).not.toBeNull();
  expect(desktopAgeBounds).not.toBeNull();
  expect((desktopAgeBounds?.y ?? Number.POSITIVE_INFINITY) - ((desktopProgressBounds?.y ?? 0) + (desktopProgressBounds?.height ?? 0))).toBeLessThanOrEqual(8);
  const desktopArtworkBounds = await artworkSession.locator(".activity-session__artwork").boundingBox();
  const desktopSessionBounds = await artworkSession.boundingBox();
  expect(desktopArtworkBounds).not.toBeNull();
  expect(desktopSessionBounds).not.toBeNull();
  expect(desktopArtworkBounds?.width ?? 0).toBeGreaterThanOrEqual(96);
  expect((desktopArtworkBounds?.height ?? 0) / (desktopArtworkBounds?.width ?? 1)).toBeCloseTo(1.5, 1);
  expect((desktopArtworkBounds?.y ?? Number.POSITIVE_INFINITY) - (desktopSessionBounds?.y ?? 0)).toBeLessThanOrEqual(20);
  expect((desktopArtworkBounds?.x ?? 0) + (desktopArtworkBounds?.width ?? Number.POSITIVE_INFINITY)).toBeLessThanOrEqual((desktopSessionBounds?.x ?? 0) + (desktopSessionBounds?.width ?? 0) + 1);
  expect((desktopArtworkBounds?.y ?? 0) + (desktopArtworkBounds?.height ?? Number.POSITIVE_INFINITY)).toBeLessThanOrEqual((desktopSessionBounds?.y ?? 0) + (desktopSessionBounds?.height ?? 0) + 1);
  expect(await artworkSession.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  const missingSession = page.locator(".activity-session").filter({ hasText: "Metadata pending" });
  await expect(missingSession.locator(".activity-session__artwork > svg")).toBeVisible();
  await expect(missingSession.locator(".activity-session__provider")).toHaveCount(0);
  await expect(missingSession.locator(".activity-session__progress")).toHaveText("Duration unavailable");
  await expect(missingSession.getByRole("progressbar")).toHaveCount(0);

  const transcodingJob = page.locator(".activity-job").filter({ hasText: "episode-1-source" });
  await expect(transcodingJob.getByRole("status")).toHaveText("42% · 1.04× · start 3.25s");
  const invalidJob = page.locator(".activity-job").filter({ hasText: "queued-movie-source" });
  await expect(invalidJob.getByRole("status")).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(artworkSession).toBeVisible();
  await expect(artworkSession.locator(".activity-session__providers")).toBeVisible();
  const mobileArtworkBounds = await artworkSession.locator(".activity-session__artwork").boundingBox();
  expect(mobileArtworkBounds).not.toBeNull();
  expect(mobileArtworkBounds?.width ?? 0).toBeGreaterThanOrEqual(72);
  expect(mobileArtworkBounds?.width ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(88);
  const bounds = await artworkSession.boundingBox();
  expect(bounds).not.toBeNull();
  expect((mobileArtworkBounds?.y ?? Number.POSITIVE_INFINITY) - (bounds?.y ?? 0)).toBeLessThanOrEqual(20);
  expect((mobileArtworkBounds?.x ?? 0) + (mobileArtworkBounds?.width ?? Number.POSITIVE_INFINITY)).toBeLessThanOrEqual((bounds?.x ?? 0) + (bounds?.width ?? 0) + 1);
  expect((mobileArtworkBounds?.y ?? 0) + (mobileArtworkBounds?.height ?? Number.POSITIVE_INFINITY)).toBeLessThanOrEqual((bounds?.y ?? 0) + (bounds?.height ?? 0) + 1);
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(390);
  expect(await artworkSession.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
});

test("stopping a playback session keeps confirmation semantics and returns focus to activity controls", async ({ page, rivune: _rivune }) => {
  let deletedSessionID = "";
  let currentActivity = { ...activity, summary: { ...activity.summary }, sessions: [...activity.sessions], jobs: [...activity.jobs] };
  await page.route("**/api/v1/playback/activity**", async (route) => {
    const request = route.request();
    const sessionMatch = new URL(request.url()).pathname.match(/\/playback\/activity\/sessions\/([^/]+)$/);
    if (request.method() === "DELETE" && sessionMatch) {
      deletedSessionID = decodeURIComponent(sessionMatch[1]);
      currentActivity = {
        ...currentActivity,
        summary: { ...currentActivity.summary, activeSessions: currentActivity.summary.activeSessions - 1 },
        sessions: currentActivity.sessions.filter((session) => session.id !== deletedSessionID),
      };
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(currentActivity) });
  });

  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: /Activity/ }).click();

  const session = page.locator(".activity-session").filter({ hasText: "Fixture Animated Series · S05E06 · Fixture Episode" });
  await session.getByRole("button", { name: "Stop" }).click();
  await expect(page.getByRole("heading", { name: /Stop Fixture Animated Series/ })).toBeVisible();
  await page.getByRole("button", { name: "Stop playback" }).click();

  await expect.poll(() => deletedSessionID).toBe("11111111-1111-4111-8111-111111111111");
  await expect(session).toHaveCount(0);
  const remainingStop = page.locator(".activity-session").filter({ hasText: "Metadata pending" }).getByRole("button", { name: "Stop" });
  await expect(remainingStop).toBeFocused();
});
