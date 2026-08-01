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
  await page.getByRole("button", { name: "View series & season" }).click();

  const stream = page.getByRole("radio", { name: /Fixture 1080p/ });
  await expect(stream).toBeVisible();
  await stream.click();
  const sourceRequest = await rivune.waitForRequest("/api/v1/playback/sources", "POST");
  expect(sourceRequest.body).toMatchObject({
    capabilities: {
      processingModes: ["remux"],
      mediaProfiles: expect.any(Array),
      externalPlayers: ["system"],
    },
  });
  await expect(page.getByRole("button", { name: "Play episode" })).toBeEnabled();

  const preparation = await rivune.waitForRequest("/api/v1/playback/prepare", "POST");
  expect(preparation.body).toMatchObject({ sourceRef: "source-tt9000:1:1", startSeconds: 321 });
  await page.getByRole("button", { name: "Play episode" }).click();

  await expect(page.getByRole("dialog", { name: /Playing Signal Horizon.*S01E01.*First Light/ })).toBeVisible();
  const markerRequest = await rivune.waitForRequest("/api/v1/playback/markers", "GET");
  expect(markerRequest.search.get("imdbId")).toBe("tt9000");
  expect(markerRequest.search.get("season")).toBe("1");
  expect(markerRequest.search.get("episode")).toBe("1");
  const firstResolve = await rivune.waitForRequest("/api/v1/playback/resolve", "POST");
  expect(firstResolve.body).toMatchObject({ sourceRef: "source-tt9000:1:1", titleId: "episode-1", startSeconds: 321 });
  await expect(page.getByRole("slider", { name: "Playback position" })).toHaveValue("321");

  await page.getByRole("button", { name: "Audio track" }).click();
  await page.getByRole("button", { name: /French.*AAC.*2\.0/ }).click();
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ titleId: "episode-1", startSeconds: 321, preferredAudioTrack: 2 }));
  await page.getByRole("button", { name: "Audio track" }).click();
  await expect(page.getByRole("button", { name: /French.*AAC.*2\.0/ })).toHaveClass(/is-active/);
  await page.getByRole("button", { name: "Close settings" }).click();

  await page.getByRole("button", { name: "Subtitles" }).click();
  await page.getByRole("button", { name: /FR.*Subtitle track/ }).click();
  await expect(page.locator("video track[srclang='fr']")).toHaveAttribute("src", "https://fixtures.rivune.test/subtitles-fr.vtt");
  await page.getByRole("button", { name: "Subtitles" }).click();
  await expect(page.getByRole("button", { name: /FR.*Subtitle track/ })).toHaveClass(/is-active/);
  await page.getByRole("button", { name: "Close settings" }).click();
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

  await expect(page.getByRole("dialog", { name: /Playing Signal Horizon.*S01E02.*Second Orbit/ })).toBeVisible();
  await expect.poll(() => rivune.matching("/api/v1/playback/sources", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ mediaType: "episode", resourceId: "tt9000:1:2" }));
  await expect.poll(() => rivune.matching("/api/v1/playback/prepare", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ sourceRef: "source-tt9000:1:2", startSeconds: 0 }));
  await expect.poll(() => rivune.matching("/api/v1/playback/resolve", "POST").map((request) => request.body)).toContainEqual(expect.objectContaining({ sourceRef: "source-tt9000:1:2", titleId: "episode-2", startSeconds: 0 }));
});

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
  await page.getByRole("button", { name: "View series & season" }).click();
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
  await page.getByRole("button", { name: "View series & season" }).click();
  await page.getByRole("radio", { name: /Fixture 1080p/ }).click();

  await expect(page.getByText(/Rivune did not start a transcode/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Play episode" })).toBeDisabled();
  expect(rivune.matching("/api/v1/playback/resolve", "POST")).toHaveLength(0);
});
