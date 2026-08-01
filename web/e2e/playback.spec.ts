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
