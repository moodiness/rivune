import { expect, test } from "./fixtures/rivune";

test("continue-watching episode opens its series and requests trailers for each selected season", async ({ page, rivune }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Continue Watching" })).toBeVisible();
  await page.getByRole("button", { name: "Open Signal Horizon" }).click();
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
