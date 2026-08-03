import { expect, test } from "./fixtures/rivune";

const now = "2026-07-31T12:00:00Z";

const activity = {
  summary: { activeSessions: 2, activeJobs: 2, processingSlots: 1, processingLimit: 2, storageBytes: 0, storageLimitBytes: 1_073_741_824 },
  diagnostics: { videoEncoder: "h264", hardwareToneMap: false },
  sessions: [
    {
      id: "11111111-1111-4111-8111-111111111111",
      titleId: "episode-1",
      artworkUrl: "https://fixtures.rivune.test/activity-artwork.jpg",
      externalIds: { imdb: "tt0149460", tmdb: "615", tvdb: "11704240" },
      externalIdMediaTypes: { imdb: "series", tmdb: "series", tvdb: "episode" },
      title: "Futurama · S05E06 · Astéroïque",
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
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Administration" }).click();
  await page.getByRole("button", { name: /Activity/ }).click();

  const artworkSession = page.locator(".activity-session").filter({ hasText: "Futurama · S05E06 · Astéroïque" });
  const artwork = artworkSession.getByRole("img", { name: "Futurama · S05E06 · Astéroïque" });
  await expect(artwork).toBeVisible();
  await expect(artwork).toHaveAttribute("loading", "lazy");
  await expect(artworkSession.locator(".activity-session__copy > strong")).toHaveText("Futurama · S05E06 · Astéroïque");
  await expect(artworkSession.getByLabel("IMDb · tt0149460")).toHaveAttribute("href", "https://www.imdb.com/title/tt0149460/");
  const tmdb = artworkSession.getByLabel("TMDB · 615");
  await expect(tmdb).toHaveAttribute("href", "https://www.themoviedb.org/tv/615");
  await expect(tmdb.locator("svg")).toHaveCount(1);
  await expect(artworkSession.getByLabel("TVDB · 11704240")).toHaveAttribute("href", "https://thetvdb.com/dereferrer/episode/11704240");
  await expect(artworkSession.locator(".activity-session__provider")).toHaveCount(3);
  await expect(artworkSession.locator(".activity-session__time > strong")).toHaveText("10 min / 22 min");
  await expect(artworkSession.getByRole("status")).toHaveText("42% · 1.04×");
  await expect(artworkSession.getByText("Video conversion required")).toBeVisible();
  await expect(artworkSession.getByText("Video H265 → H264")).toBeVisible();
  await expect(artworkSession.getByText("Audio DTS → AAC")).toBeVisible();
  await expect(artworkSession.getByText("Resolution 2160p → 1080p")).toBeVisible();
  await expect(artworkSession.getByText(/Target bitrate 8.?000/)).toBeVisible();
  await expect(artworkSession.getByText("Burn-in subtitles")).toBeVisible();
  await expect(artworkSession.getByText("Tone mapping")).toBeVisible();
  await expect(artworkSession.getByText("Transcode", { exact: true })).toHaveCount(2);

  const missingSession = page.locator(".activity-session").filter({ hasText: "Metadata pending" });
  await expect(missingSession.locator(".activity-session__artwork > svg")).toBeVisible();
  await expect(missingSession.locator(".activity-session__provider")).toHaveCount(0);
  await expect(missingSession.getByRole("status")).toHaveCount(0);

  const transcodingJob = page.locator(".activity-job").filter({ hasText: "episode-1-source" });
  await expect(transcodingJob.getByRole("status")).toHaveText("42% · 1.04×");
  const invalidJob = page.locator(".activity-job").filter({ hasText: "queued-movie-source" });
  await expect(invalidJob.getByRole("status")).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(artworkSession).toBeVisible();
  await expect(artworkSession.locator(".activity-session__providers")).toBeVisible();
  const bounds = await artworkSession.boundingBox();
  expect(bounds).not.toBeNull();
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(390);
});
