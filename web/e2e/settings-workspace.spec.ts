import type { Page } from "@playwright/test";

import { CATEGORY_IDS, expect, test } from "./fixtures/rivune";

const profileSections = ["appearance", "playback", "language", "subtitles", "connections"];
const serverSections = ["appearance", "playback", "transcoding", "language", "subtitles"];

async function openSettings(page: Page, section?: string) {
  const sectionQuery = section ? `&section=${section}` : "";
  await page.goto(`/#admin?tab=settings${sectionQuery}`);
  await expect(page.locator(".settings-workspace")).toBeVisible();
}

async function sectionIds(page: Page) {
  return page.locator("[data-settings-section]").evaluateAll((buttons) => buttons.map((button) => button.getAttribute("data-settings-section")));
}

test("settings categories keep one panel active and preserve deep links", async ({ page, rivune: _rivune }) => {
  await openSettings(page, "subtitles");

  const navigation = page.locator(".settings-navigation");
  const content = page.locator(".settings-content");
  const subtitles = navigation.locator('[data-settings-section="subtitles"]');
  await expect(subtitles).toHaveAttribute("aria-current", "page");
  await expect(subtitles).toHaveAttribute("aria-controls", "settings-section-subtitles");
  await expect(content.locator("#settings-section-subtitles")).toBeVisible();
  await expect(content.locator('section[id^="settings-section-"]')).toHaveCount(1);
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=subtitles$/);

  const playback = navigation.locator('[data-settings-section="playback"]');
  await playback.click();
  await expect(playback).toHaveAttribute("aria-current", "page");
  await expect(content.locator("#settings-section-playback")).toBeVisible();
  await expect(content.locator("#settings-section-subtitles")).toHaveCount(0);
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=playback$/);
});

test("settings search opens the single matching category", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const navigation = page.locator(".settings-navigation");
  const search = navigation.locator('.settings-navigation__search input[type="search"]');
  const appearance = navigation.locator('[data-settings-section="appearance"]');
  const subtitles = navigation.locator('[data-settings-section="subtitles"]');
  await expect(search).toHaveAccessibleName("Search");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);

  await search.fill("subtitle");
  await expect(subtitles).toBeVisible();
  await expect(appearance).toHaveCount(0);
  await expect.poll(() => sectionIds(page)).toEqual(["subtitles"]);
  await expect(subtitles).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-subtitles")).toBeVisible();
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=subtitles$/);

  await search.clear();
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);
  await expect(subtitles).toHaveAttribute("aria-current", "page");
});

test("scope switching exposes only the categories valid for that target", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const scope = page.locator(".settings-profile-picker select");
  await expect(scope).toHaveValue("alice");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);

  await page.locator('[data-settings-section="connections"]').click();
  await expect(page.locator("#settings-section-connections")).toBeVisible();
  await scope.selectOption("server");
  await expect.poll(() => sectionIds(page)).toEqual(serverSections);
  await expect(page.locator('[data-settings-section="connections"]')).toHaveCount(0);
  await expect(page.locator('[data-settings-section="appearance"]')).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-appearance")).toBeVisible();

  await scope.selectOption("alice");
  await expect.poll(() => sectionIds(page)).toEqual(profileSections);
  await expect(page.locator('[data-settings-section="transcoding"]')).toHaveCount(0);
});

test("dirty preferences can be discarded or saved from the persistent action bar", async ({ page, rivune }) => {
  await openSettings(page);

  const animations = page.getByRole("checkbox", { name: "Interface animations" });
  const saveBar = page.locator(".settings-save-bar");
  const discard = saveBar.getByRole("button", { name: "Discard changes" });
  const save = saveBar.getByRole("button", { name: "Save preferences" });
  await expect(animations).toBeChecked();
  await expect(saveBar).toHaveCount(0);

  await animations.uncheck();
  await expect(saveBar.getByRole("status")).toContainText("Unsaved changes");
  await expect(discard).toBeEnabled();
  await expect(save).toBeEnabled();
  await expect(page.locator(".settings-profile-picker select")).toBeDisabled();

  await discard.click();
  await expect(animations).toBeChecked();
  await expect(saveBar).toHaveCount(0);
  expect(rivune.matching("/api/v1/profiles/alice/settings", "PATCH")).toHaveLength(0);

  await animations.uncheck();
  await save.click();
  const request = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(request.body).toMatchObject({ animationsEnabled: false });
  await expect(saveBar).toHaveCount(0);
  await expect(page.locator(".settings-profile-picker select")).toBeEnabled();
});

test("global administrator assigns transcoding per profile", async ({ page, rivune }) => {
  await openSettings(page, "playback");

  const scope = page.locator(".settings-profile-picker select");
  const transcoding = page.getByLabel("Transcoding");
  await expect(transcoding).toBeVisible();
  await scope.selectOption("bob");
  await expect(page.getByRole("heading", { name: "Bob preferences" })).toBeVisible();
  await transcoding.selectOption("disabled");
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/bob/settings", "PATCH");
  expect(request.body).toMatchObject({ transcoding: "disabled" });
});

test("viewer cannot change transcoding and unrelated saves omit its policy", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  rivune.configureGlobalAdmin("bob");
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main navigation" }).getByRole("button", { name: "Preferences", exact: true }).click();
  await page.locator('[data-settings-section="playback"]').click();

  await expect(page.getByLabel("Transcoding")).toHaveCount(0);
  await expect(page.locator(".settings-transcoding-state")).toBeVisible();

  await page.locator('[data-settings-section="appearance"]').click();
  await page.getByRole("checkbox", { name: "Interface animations" }).uncheck();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/bob/settings", "PATCH");
  expect(request.body).toMatchObject({ animationsEnabled: false });
  expect(request.body).not.toHaveProperty("transcoding");
});

test("appearance choices expose effective inheritance and persist explicit overrides", async ({ page, rivune }) => {
  await openSettings(page, "appearance");

  const theme = page.getByRole("radiogroup", { name: "Theme" });
  const density = page.getByRole("radiogroup", { name: "Card density" });
  await expect(theme.getByRole("radio", { name: "Follow this device" })).toBeVisible();
  await expect(density.getByRole("radio", { name: "Comfortable" })).toBeVisible();
  await expect(theme.locator(".is-effective.is-inherited")).toHaveCount(1);
  await expect(density.locator(".is-effective.is-inherited")).toHaveCount(1);

  await theme.getByRole("radio", { name: "Dark" }).check();
  await density.getByRole("radio", { name: "Compact" }).check();
  await expect(theme.getByRole("radio", { name: "Dark" })).toBeChecked();
  await expect(density.getByRole("radio", { name: "Compact" })).toBeChecked();
  await page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/alice/settings", "PATCH");
  expect(request.body).toMatchObject({ theme: "dark", cardDensity: "compact" });
});

test("subtitle controls update the preview before preferences are saved", async ({ page, rivune: _rivune }) => {
  await openSettings(page, "subtitles");

  const preview = page.locator(".subtitle-preview");
  await expect(preview).toBeVisible();
  await expect(preview.locator(".subtitle-preview__caption")).not.toBeEmpty();

  await page.getByRole("slider", { name: "Subtitle size" }).fill("150");
  await page.getByLabel("Subtitle text color").fill("#00ff00");
  await page.getByRole("slider", { name: "Background opacity" }).fill("75");

  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-scale").trim())).toBe("150");
  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-color").trim())).toBe("#00FF00");
  await expect.poll(() => preview.evaluate((element) => getComputedStyle(element).getPropertyValue("--subtitle-preview-opacity").trim())).toBe("75");
  await expect(page.locator(".settings-save-bar").getByRole("button", { name: "Save preferences" })).toBeEnabled();
});

test("mobile settings contain horizontal category overflow without widening the page", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSettings(page);

  const workspace = page.locator(".settings-workspace");
  const navigation = workspace.locator(".settings-navigation");
  const content = workspace.locator(".settings-content");
  const navigationList = navigation.getByRole("navigation", { name: "Settings" });
  const navigationBounds = await navigation.boundingBox();
  const contentBounds = await content.boundingBox();
  const workspaceBounds = await workspace.boundingBox();
  expect(navigationBounds).not.toBeNull();
  expect(contentBounds).not.toBeNull();
  expect(workspaceBounds).not.toBeNull();
  expect(contentBounds!.y).toBeGreaterThanOrEqual(navigationBounds!.y + navigationBounds!.height);
  expect(workspaceBounds!.x + workspaceBounds!.width).toBeLessThanOrEqual(390);
  expect(await navigationList.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await page.getByRole("checkbox", { name: "Interface animations" }).uncheck();
  const saveBarBounds = await page.locator(".settings-save-bar").boundingBox();
  expect(saveBarBounds).not.toBeNull();
  expect(saveBarBounds!.x + saveBarBounds!.width).toBeLessThanOrEqual(390);
  expect(saveBarBounds!.y + saveBarBounds!.height).toBeLessThanOrEqual(844);
});

test("category rail supports roving keyboard navigation", async ({ page, rivune: _rivune }) => {
  await openSettings(page);

  const navigation = page.locator(".settings-navigation");
  const appearance = navigation.locator('[data-settings-section="appearance"]');
  const playback = navigation.locator('[data-settings-section="playback"]');
  const connections = navigation.locator('[data-settings-section="connections"]');
  await appearance.focus();
  await appearance.press("ArrowDown");
  await expect(playback).toBeFocused();
  await playback.press("End");
  await expect(connections).toBeFocused();
  await connections.press("Home");
  await expect(appearance).toBeFocused();

  await appearance.press("ArrowDown");
  await playback.press("Enter");
  await expect(playback).toHaveAttribute("aria-current", "page");
  await expect(page.locator("#settings-section-playback")).toBeVisible();
});
