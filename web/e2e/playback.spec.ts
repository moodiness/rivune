import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures/rivune";

async function installDeterministicMedia(page: Page) {
  await page.addInitScript(() => {
    type MediaState = { currentTime: number; paused: boolean; src: string };
    const states = new WeakMap<HTMLMediaElement, MediaState>();
    const state = (element: HTMLMediaElement) => {
      let value = states.get(element);
      if (!value) {
        value = { currentTime: 0, paused: true, src: "" };
        states.set(element, value);
      }
      return value;
    };
    const prototype = HTMLMediaElement.prototype;
    Object.defineProperties(prototype, {
      src: {
        configurable: true,
        get() { return state(this).src; },
        set(value: string) {
          state(this).src = value;
          queueMicrotask(() => {
            this.dispatchEvent(new Event("durationchange"));
            this.dispatchEvent(new Event("loadedmetadata"));
            this.dispatchEvent(new Event("canplay"));
          });
        },
      },
      currentTime: {
        configurable: true,
        get() { return state(this).currentTime; },
        set(value: number) {
          state(this).currentTime = Number(value);
          queueMicrotask(() => this.dispatchEvent(new Event("timeupdate")));
        },
      },
      duration: { configurable: true, get() { return 1800; } },
      readyState: { configurable: true, get() { return HTMLMediaElement.HAVE_ENOUGH_DATA; } },
      paused: { configurable: true, get() { return state(this).paused; } },
      ended: { configurable: true, get() { return state(this).currentTime >= 1800; } },
      buffered: { configurable: true, get() { return { length: 0, start: () => 0, end: () => 0 }; } },
    });
    prototype.play = function () {
      state(this).paused = false;
      this.dispatchEvent(new Event("play"));
      this.dispatchEvent(new Event("playing"));
      return Promise.resolve();
    };
    prototype.pause = function () {
      state(this).paused = true;
      this.dispatchEvent(new Event("pause"));
    };
    prototype.load = function () {
      this.dispatchEvent(new Event("durationchange"));
      this.dispatchEvent(new Event("loadedmetadata"));
      this.dispatchEvent(new Event("canplay"));
    };
  });
}

test("player resumes, selects tracks, and autoplays the next episode", async ({ page, rivune }) => {
  await installDeterministicMedia(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();

  const stream = page.getByRole("radio", { name: /Fixture 1080p/ });
  await expect(stream).toBeVisible();
  await stream.click();
  const sourceRequest = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(sourceRequest.body).toMatchObject({
    capabilities: {
      processingModes: ["remux", "transcode_audio", "transcode"],
      subtitleModes: ["external", "burn"],
      maximumHeight: expect.any(Number),
      maximumVideoBitrateKbps: expect.any(Number),
      maximumAudioChannels: 2,
      mediaProfiles: expect.any(Array),
      externalPlayers: ["system"],
    },
  });
  await expect(page.getByRole("button", { name: "Play episode" })).toBeEnabled();

  const preparation = await rivune.waitForRequest("/api/v1/playback/prepare", "POST");
  expect(preparation.body).toMatchObject({ sourceRef: "source-tt9000:1:1", startSeconds: 321 });
  await page.getByRole("button", { name: "Play episode" }).click();

  await expect(page.getByRole("dialog", { name: "Playing First Light" })).toBeVisible();
  const markerRequest = await rivune.waitForRequest("/api/v1/playback/markers", "GET");
  expect(markerRequest.search.get("imdbId")).toBe("tt9000");
  expect(markerRequest.search.get("season")).toBe("1");
  expect(markerRequest.search.get("episode")).toBe("1");
  const firstResolve = await rivune.waitForRequest("/api/v1/playback/resolve", "POST");
  expect(firstResolve.body).toMatchObject({ sourceRef: "source-tt9000:1:1", titleId: "episode-1", startSeconds: 321 });
  expect(firstResolve.body).not.toHaveProperty("preferredSubtitleId");
  await expect(page.getByRole("slider", { name: "Playback position" })).toHaveValue("321");

  const audioTrigger = page.getByRole("button", { name: "Audio track" });
  await audioTrigger.click();
  await expect(audioTrigger).toHaveAttribute("aria-expanded", "true");
  const englishAudio = page.getByRole("radio", { name: /English.*AAC.*2\.0/ });
  const frenchAudio = page.getByRole("radio", { name: /French.*AAC.*2\.0/ });
  await expect(englishAudio).toBeFocused();
  await expect(englishAudio).toHaveAttribute("aria-checked", "true");
  await englishAudio.press("ArrowDown");
  await expect(frenchAudio).toBeFocused();
  await frenchAudio.press("Enter");
  await expect(audioTrigger).toBeFocused();
  await expect(audioTrigger).toHaveAttribute("aria-expanded", "false");
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ titleId: "episode-1", startSeconds: 321, preferredAudioTrack: 2 }));
  await audioTrigger.click();
  await expect(frenchAudio).toHaveAttribute("aria-checked", "true");
  await frenchAudio.press("Escape");
  await expect(audioTrigger).toBeFocused();
  await expect(audioTrigger).toHaveAttribute("aria-expanded", "false");

  const resolvesBeforeSubtitleChanges = rivune.matching("/api/v1/playback/resolve", "POST").length;
  const activeSessionBeforeBurn = `session-${resolvesBeforeSubtitleChanges}`;
  await page.getByRole("button", { name: "Subtitles" }).click();
  await page.getByRole("radio", { name: /FR.*Subtitle track/ }).click();
  await expect(page.locator("video track[srclang='fr']")).toHaveAttribute("src", "https://fixtures.rivune.test/subtitles-fr.vtt");
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").length).toBe(resolvesBeforeSubtitleChanges);

  await page.getByRole("button", { name: "Subtitles" }).click();
  await page.getByRole("radio", { name: /ES.*Subtitle track/ }).click();
  await expect(page.locator("video track")).toHaveCount(0);
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").length).toBe(resolvesBeforeSubtitleChanges);

  await page.getByRole("button", { name: "Subtitles" }).click();
  await page.getByRole("radio", { name: /FR.*Subtitle track/ }).click();
  await expect(page.locator("video track[srclang='fr']")).toHaveAttribute("src", "https://fixtures.rivune.test/subtitles-fr.vtt");
  await page.locator("video").evaluate((video) => {
    video.currentTime = 345;
  });

  rivune.delayNextPlaybackStop(500);
  await page.getByRole("button", { name: "Subtitles" }).click();
  await page.getByRole("radio", { name: /JA.*Subtitle track/ }).click();
  await expect.poll(() => rivune.matching(`/api/v1/playback/sessions/${activeSessionBeforeBurn}`, "DELETE").length).toBe(1);
  expect(rivune.matching("/api/v1/playback/resolve", "POST")).toHaveLength(resolvesBeforeSubtitleChanges);
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").length).toBe(resolvesBeforeSubtitleChanges + 1);
  const burnResolve = rivune.matching("/api/v1/playback/resolve", "POST").at(-1)!;
  expect(burnResolve.body).toMatchObject({
    titleId: "episode-1",
    startSeconds: 345,
    preferredSubtitleId: "sub-burn",
  });
  const burnRelease = rivune.matching(`/api/v1/playback/sessions/${activeSessionBeforeBurn}`, "DELETE")[0];
  expect(rivune.requests.indexOf(burnRelease)).toBeLessThan(rivune.requests.indexOf(burnResolve));
  await expect.poll(() => rivune.requests.some((request) => request.pathname.endsWith("/assets/master.m3u8") && request.search.get("file") === "master.m3u8")).toBe(true);
  const burnedSession = `session-${resolvesBeforeSubtitleChanges + 1}`;
  expect(rivune.matching(`/api/v1/playback/sessions/${burnedSession}`, "DELETE")).toHaveLength(0);
  await page.getByRole("button", { name: "Subtitles" }).click();
  await expect(page.getByRole("radio", { name: /JA.*Subtitle track/ })).toHaveAttribute("aria-checked", "true");
  rivune.delayNextPlaybackStop(500);
  await page.getByRole("radio", { name: "Off" }).click();
  await expect.poll(() => rivune.matching(`/api/v1/playback/sessions/${burnedSession}`, "DELETE").length).toBe(1);
  expect(rivune.matching("/api/v1/playback/resolve", "POST")).toHaveLength(resolvesBeforeSubtitleChanges + 1);
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").length).toBe(resolvesBeforeSubtitleChanges + 2);
  const offResolve = rivune.matching("/api/v1/playback/resolve", "POST").at(-1)!;
  expect(offResolve.body).toMatchObject({
    titleId: "episode-1",
    startSeconds: 345,
    preferredSubtitleId: "none",
  });
  const burnReleaseForOff = rivune.matching(`/api/v1/playback/sessions/${burnedSession}`, "DELETE")[0];
  expect(rivune.requests.indexOf(burnReleaseForOff)).toBeLessThan(rivune.requests.indexOf(offResolve));
  const replacementSession = `session-${resolvesBeforeSubtitleChanges + 2}`;
  expect(rivune.matching(`/api/v1/playback/sessions/${replacementSession}`, "DELETE")).toHaveLength(0);
  await page.getByRole("button", { name: "Subtitles" }).click();
  await expect(page.getByRole("radio", { name: "Off" })).toHaveAttribute("aria-checked", "true");
  await page.getByRole("button", { name: "Close settings" }).click();
  const speedTrigger = page.locator('[data-player-action="speed"]');
  await speedTrigger.click();
  const normalSpeed = page.getByRole("radio", { name: "1×", exact: true });
  const fasterSpeed = page.getByRole("radio", { name: "1.25×", exact: true });
  await expect(normalSpeed).toBeFocused();
  await expect(normalSpeed).toHaveAttribute("aria-checked", "true");
  await normalSpeed.press("ArrowRight");
  await expect(fasterSpeed).toBeFocused();
  await fasterSpeed.press("Enter");
  await expect(speedTrigger).toBeFocused();
  await expect(page.locator("video")).toHaveJSProperty("playbackRate", 1.25);
  expect(rivune.matching(`/api/v1/playback/sessions/${activeSessionBeforeBurn}`, "DELETE")).toHaveLength(1);
  expect(rivune.matching(`/api/v1/playback/sessions/${burnedSession}`, "DELETE")).toHaveLength(1);
  expect(rivune.requests.filter((request) => request.pathname.includes("/playback/sessions/") && request.search.get("fallback") === "1")).toHaveLength(0);
  await page.locator("video").evaluate((video) => {
    video.currentTime = 330;
  });
  const skipIntro = page.getByRole("button", { name: "Skip intro" });
  await expect(skipIntro).toBeVisible();
  await skipIntro.click();
  await expect(page.getByRole("slider", { name: "Playback position" })).toHaveValue("400");
  await expect(skipIntro).toHaveCount(0);


  await page.locator("video").evaluate((video) => {
    video.currentTime = 1800;
    video.dispatchEvent(new Event("timeupdate"));
    video.dispatchEvent(new Event("ended"));
  });

  await expect(page.getByRole("dialog", { name: "Playing Second Orbit" })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ mediaType: "episode", resourceId: "tt9000:1:2" }));
  await expect.poll(() => rivune.matching("/api/v1/playback/prepare", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ sourceRef: "source-tt9000:1:2", startSeconds: 0 }));
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ sourceRef: "source-tt9000:1:2", titleId: "episode-2", startSeconds: 0 }));
});

test("localized compact player controls do not depend on English accessible labels", async ({ page, rivune }) => {
  await installDeterministicMedia(page);
  rivune.setInterfaceLanguage("fr");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("button", { name: /Signal Horizon/ }).first().click();
  await page.getByRole("radio", { name: /Fixture 1080p/ }).click();
  const playSelectedStream = page.locator('[data-media-action="play-selected-stream"]');
  await expect(playSelectedStream).toBeVisible();
  await playSelectedStream.click();

  await expect(page.getByRole("dialog", { name: "Lecture de First Light" })).toBeVisible();
  const diagnostics = page.locator('[data-player-action="diagnostics"]');
  const speed = page.locator('[data-player-action="speed"]');
  await expect(diagnostics).toHaveAttribute("aria-label", "Diagnostic de lecture");
  await expect(speed).toHaveAttribute("aria-label", "Vitesse de lecture 1x");
  await expect(diagnostics).toBeHidden();
  await expect(speed).toBeHidden();
  await expect(page.locator('[data-player-action="playback"]')).toBeVisible();
  await expect(page.locator('[data-player-action="close"]')).toBeVisible();
});

test("server transcodes remain in the existing web video and HLS source pipeline", async ({ page, rivune: _rivune }) => {
  await installDeterministicMedia(page);
  let announcedCapabilities: unknown;
  const playbackAssetRequests: string[] = [];
  await page.route("**/api/v1/playback/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "GET" && path.includes("/playback/sessions/transcoded-session/assets/")) {
      const url = new URL(request.url());
      playbackAssetRequests.push(url.toString());
      if (url.searchParams.get("file")?.endsWith(".m3u8")) {
        await route.fulfill({
          contentType: "application/vnd.apple.mpegurl",
          body: "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:1,\nsegment.m4s\n#EXT-X-ENDLIST\n",
        });
      } else {
        await route.fulfill({ contentType: "video/mp4", body: "" });
      }
      return;
    }
    if (request.method() === "POST" && path.endsWith("/playback/sources")) {
      const input: unknown = request.postDataJSON();
      if (input && typeof input === "object" && "capabilities" in input) announcedCapabilities = input.capabilities;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ sources: [{ id: "audio-option", sourceRef: "audio-transcode-source", addonId: "fixture-addon", manifestId: "fixture-manifest", streamIndex: 0, name: "Server audio conversion", protocol: "http", container: "mkv", expiresAt: "2099-01-01T00:00:00Z" }], providerErrors: [] }) });
      return;
    }
    if (request.method() === "POST" && path.endsWith("/playback/prepare")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ sourceRef: "audio-transcode-source", mode: "transcode_audio", protocol: "hls", container: "mp4", media: { container: "mkv", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 1, type: "audio", codec: "dts", channels: 6 }], subtitleTracks: [] }, subtitleCount: 0, expiresAt: "2099-01-01T00:00:00Z" }) });
      return;
    }
    if (request.method() === "POST" && path.endsWith("/playback/resolve")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          id: "transcoded-session",
          selectedSourceId: "transcoded-source",
          selectedAudioTrack: 1,
          sources: [
            { id: "transcoded-source", addonId: "fixture-addon", manifestId: "fixture-manifest", name: "Server audio conversion", mode: "transcode_audio", url: "/api/v1/playback/sessions/transcoded-session/assets/master.m3u8?file=audio/master.m3u8", protocol: "hls", container: "mp4", compatible: true, media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 1, type: "audio", codec: "aac", channels: 6 }], subtitleTracks: [] } },
            { id: "video-transcoded-source", addonId: "fixture-addon", manifestId: "fixture-manifest", name: "Server video conversion", mode: "transcode", url: "/api/v1/playback/sessions/transcoded-session/assets/master.m3u8?file=video/master.m3u8", protocol: "hls", container: "mp4", compatible: true, media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1280, height: 720 }], audioTracks: [{ index: 1, type: "audio", codec: "aac", channels: 2 }], subtitleTracks: [] } },
          ],
          subtitles: [],
          providerErrors: [],
          expiresAt: "2099-01-01T00:00:00Z",
        }),
      });
      return;
    }
    if (path.endsWith("/playback/markers")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ markers: [] }) });
      return;
    }
    await route.fulfill({ status: 204 });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("radio", { name: /Server audio conversion/ }).click();
  await expect(page.getByRole("button", { name: "Play episode" })).toBeEnabled();
  await page.getByRole("button", { name: "Play episode" }).click();

  await expect(page.getByRole("dialog", { name: "Playing First Light" })).toBeVisible();
  await expect(page.getByText("Audio conversion", { exact: true }).first()).toBeVisible();
  await expect.poll(() => playbackAssetRequests.some((value) => new URL(value).searchParams.get("file") === "audio/master.m3u8")).toBe(true);
  const sourceTrigger = page.getByRole("button", { name: "Sources and quality" });
  await sourceTrigger.click();
  const audioTranscode = page.getByRole("radio", { name: /Server audio conversion.*Audio conversion/ });
  const videoTranscode = page.getByRole("radio", { name: /Server video conversion.*Video conversion/ });
  await expect(audioTranscode).toBeFocused();
  await expect(audioTranscode).toHaveAttribute("aria-checked", "true");
  await audioTranscode.press("ArrowDown");
  await expect(videoTranscode).toBeFocused();
  await videoTranscode.press("Enter");
  await expect(sourceTrigger).toBeFocused();
  await expect(page.getByText("Video conversion", { exact: true }).first()).toBeVisible();
  await expect.poll(() => playbackAssetRequests.some((value) => new URL(value).searchParams.get("file") === "video/master.m3u8")).toBe(true);
  expect(playbackAssetRequests.some((value) => new URL(value).searchParams.get("fallback") === "1")).toBe(false);
  expect(announcedCapabilities).toMatchObject({
    processingModes: ["remux", "transcode_audio", "transcode"],
    subtitleModes: ["external", "burn"],
    maximumHeight: expect.any(Number),
    maximumVideoBitrateKbps: expect.any(Number),
    maximumAudioChannels: 2,
  });
});

for (const scenario of [
  { code: "playback_transcoding_disabled", message: "This media requires server transcoding, but transcoding is disabled by the server or profile setting." },
  { code: "playback_client_capability_missing", message: "This media requires a playback mode that this client did not declare. Try another source or player." },
]) {
  test(`player explains ${scenario.code}`, async ({ page, rivune: _rivune }) => {
    await page.route("**/api/v1/playback/resolve", (route) => route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: scenario.code, message: "backend detail must not leak" } }),
    }));
    await page.goto("/");
    await page.getByRole("button", { name: "Open Signal Horizon" }).click();
    await page.getByRole("radio", { name: /Fixture 1080p/ }).click();
    await page.getByRole("button", { name: "Play episode" }).click();

    const player = page.getByRole("dialog", { name: "Playing First Light" });
    await expect(player).toBeVisible();
    await expect(player).toHaveAttribute("data-player-state", "failed");
    await expect(player).toHaveAttribute("aria-busy", "false");
    await expect(page.getByRole("alert")).toContainText(scenario.message);
    await expect(page.getByRole("button", { name: "Retry" })).toBeFocused();
    await expect(page.getByText("backend detail must not leak")).toHaveCount(0);
  });
}

test("external-only sources are disclosed without starting web media", async ({ page, rivune }) => {
  await page.route("**/api/v1/playback/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "POST" && path.endsWith("/playback/sources")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          sources: [{ id: "external-option", sourceRef: "external-source-reference", addonId: "fixture-addon", manifestId: "fixture-manifest", streamIndex: 0, name: "External 4K source", protocol: "external", expiresAt: new Date(Date.now() + 60_000).toISOString() }],
          providerErrors: [],
        }),
      });
      return;
    }
    if (request.method() === "POST" && path.endsWith("/playback/prepare")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ sourceRef: "external-source-reference", mode: "external", protocol: "external", subtitleCount: 0, expiresAt: new Date(Date.now() + 60_000).toISOString() }),
      });
      return;
    }
    if (request.method() === "POST" && path.endsWith("/playback/resolve")) {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          id: "external-playback-session",
          selectedSourceId: "external-source",
          sources: [{ id: "external-source", addonId: "fixture-addon", manifestId: "fixture-manifest", name: "External 4K source", mode: "external", url: "https://external.example/watch?token=opaque", protocol: "http", compatible: true }],
          subtitles: [],
          providerErrors: [],
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
        }),
      });
      return;
    }
    await route.fallback();
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("radio", { name: /External 4K source/ }).click();
  await page.getByRole("button", { name: "Play episode" }).click();

  await expect(page.getByText("Continue in an external player")).toBeVisible();
  await expect(page.getByRole("link", { name: "Open external player" })).toHaveAttribute("href", "https://external.example/watch?token=opaque");
  await expect(page.locator("video")).toHaveCount(0);
  await page.getByRole("button", { name: "Choose another source" }).click();
  await expect.poll(() => rivune.matching("/api/v1/playback/sessions/external-playback-session", "DELETE").length).toBe(1);
});

test("unsupported browser sources stop at preparation with an actionable choice", async ({ page, rivune }) => {
  await page.route("**/api/v1/playback/prepare", async (route) => {
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "playback_source_unsupported",
          message: "This source needs video conversion",
        },
      }),
    });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
  await page.getByRole("radio", { name: /Fixture 1080p/ }).click();

  await expect(page.getByText(/Rivune did not start a transcode/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Play episode" })).toBeDisabled();
  expect(rivune.matching("/api/v1/playback/resolve", "POST")).toHaveLength(0);
});

test("multiline stream metadata stays inside its source card and row actions play the exact stream", async ({ page, rivune }) => {
  await page.route("**/api/v1/playback/sources", async (route) => {
    const expiresAt = new Date(Date.now() + 60_000).toISOString();
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sources: Array.from({ length: 7 }, (_, index) => ({
          id: `multiline-${index}`,
          sourceRef: `multiline-source-${index}`,
          addonId: "fixture-addon",
          manifestId: "fixture-manifest",
          streamIndex: index,
          name: `📺 [AD ⚡] Lumio 2160p ${index + 1}`,
          description: "📁 Avatar Aang The Last Airbender (2026)\n🖥 WEB-DL 🎞 HEVC 💊 SUPPLY\n🌈 HDR10+ · DV 🎧 DD+ 🔊 5.1\n📦 17.8 GB",
          protocol: "http",
          container: "mkv",
          expiresAt,
        })),
        providerErrors: [],
      }),
    });
  });
  const preparationGate = Promise.withResolvers<void>();
  await page.route("**/api/v1/playback/prepare", async (route) => {
    await preparationGate.promise;
    await route.fallback();
  });

  await page.setViewportSize({ width: 1217, height: 680 });
  await page.goto("/");
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();

  const sourceRows = page.getByRole("radio");
  await expect(sourceRows).toHaveCount(7);
  const contained = await sourceRows.evaluateAll((rows) => rows.every((row) => {
    const content = row.firstElementChild;
    if (!content) return false;
    const rowRect = row.getBoundingClientRect();
    const contentRect = content.getBoundingClientRect();
    return contentRect.top >= rowRect.top && contentRect.bottom <= rowRect.bottom;
  }));
  expect(contained).toBe(true);
  const streamCards = page.locator(".details-stream-list > div");
  await expect(streamCards).toHaveCount(7);
  await expect(streamCards.locator(".episode-play")).toHaveCount(0);
  const targetCard = streamCards.nth(4);
  await targetCard.getByRole("radio").click();
  await expect(streamCards.locator(".episode-play")).toHaveCount(1);
  const playAction = targetCard.getByRole("button", { name: /Play episode/ });
  await expect(playAction).toBeDisabled();
  await expect(targetCard.locator(".details-stream-list__state .spin")).toHaveCount(1);
  await expect(playAction.locator(".spin")).toHaveCount(0);
  await expect(playAction.locator(".lucide-play")).toBeVisible();
  preparationGate.resolve();
  await expect(playAction).toBeEnabled();
  await playAction.click();
  await expect.poll(() => rivune.matching("/api/v1/playback/prepare", "POST").map((request) => (request.body as { sourceRef?: string }).sourceRef)).toContain("multiline-source-4");
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").map((request) => (request.body as { sourceRef?: string }).sourceRef)).toContain("multiline-source-4");
});
