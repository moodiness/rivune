import { expect, test } from "./fixtures/rivune";

const defaults = {
  revision: 0,
  reducedMotion: "system",
  highContrast: "system",
  textScale: 100,
  captions: "system",
  audioDescription: false,
  focusIndicators: "standard",
} as const;

test("profile accessibility preferences apply motion, focus, contrast, captions, and text scale", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", "10000000-0000-4000-8000-000000000001");
  await page.emulateMedia({ reducedMotion: "reduce", contrast: "no-preference" });
  await page.route(/\/api\/v1\/profiles\/[^/]+\/accessibility-preferences$/, async (route) => {
    const profile = new URL(route.request().url()).pathname.split("/").at(-2);
    const document = profile === "bob"
      ? { ...defaults, revision: 3, reducedMotion: "reduce", highContrast: "more", textScale: 130, captions: "on", focusIndicators: "enhanced" }
      : { ...defaults, revision: 7, reducedMotion: "no-preference", textScale: 115, captions: "off" };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(document) });
  });

  await page.goto("/");
  const root = page.locator("html");
  await expect(root).toHaveAttribute("data-reduced-motion", "no-preference");
  await expect(root).toHaveAttribute("data-text-scale", "115");
  await expect(root).toHaveAttribute("data-captions", "off");
  await expect.poll(() => root.evaluate((element) => getComputedStyle(element).getPropertyValue("--text-scale").trim())).toBe("1.15");
  await page.getByRole("button", { name: "Library", exact: true }).click();
  const initialBodySize = await page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  const initialQueueSize = await page.getByRole("region", { name: "Reading queue" }).getByRole("heading", { name: "Reading queue" }).evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));

  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await page.getByRole("button", { name: "Bob Profile" }).click();
  await expect(root).toHaveAttribute("data-reduced-motion", "reduce");
  await expect(root).toHaveAttribute("data-high-contrast", "more");
  await expect(root).toHaveAttribute("data-focus-indicators", "enhanced");
  await expect(root).toHaveAttribute("data-text-scale", "130");
  await expect(root).toHaveAttribute("data-captions", "on");
  await expect.poll(() => root.evaluate((element) => getComputedStyle(element).getPropertyValue("--focus-ring-width").trim())).toBe("4px");
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await expect.poll(() => page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThan(initialBodySize);
  await expect.poll(() => page.getByRole("region", { name: "Reading queue" }).getByRole("heading", { name: "Reading queue" }).evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThan(initialQueueSize);
});

test("accessibility settings are keyboard operable and expose save and conflict states", async ({ page, rivune: _rivune }) => {
  let revision = 4;
  let conflict = true;
  await page.route("**/api/v1/profiles/alice/accessibility-preferences", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...defaults, revision }) });
      return;
    }
    if (conflict) {
      conflict = false;
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "accessibility_preferences_conflict", message: "stale" } }) });
      return;
    }
    const body = route.request().postDataJSON();
    revision += 1;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...body, revision }) });
  });

  await page.goto("/#admin?tab=settings&section=appearance");
  const panel = page.locator(".accessibility-settings");
  const initialBodySize = await page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  const initialHeadingSize = await panel.getByRole("heading", { name: "Accessibility" }).evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  const initialSelectSize = await panel.getByLabel("Text size").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));
  await expect(panel.getByRole("heading", { name: "Accessibility" })).toBeVisible();
  const textSize = panel.getByLabel("Text size");
  await textSize.focus();
  await textSize.selectOption("130");
  await expect(panel.getByRole("status")).toContainText("Unsaved changes");
  await panel.getByRole("button", { name: "Save accessibility preferences" }).click();
  await expect(panel.getByRole("alert")).toContainText("changed on another device");
  await expect(panel.getByRole("button", { name: "Save accessibility preferences" })).toBeDisabled();

  await panel.getByRole("button", { name: "Reload preferences" }).click();
  await textSize.selectOption("130");
  await panel.getByRole("button", { name: "Save accessibility preferences" }).click();
  await expect(panel.getByRole("status")).toHaveText("Saved");
  await expect(page.locator("html")).toHaveAttribute("data-text-scale", "130");
  await expect(panel.getByRole("heading", { name: "Accessibility" })).toBeFocused();
  await expect.poll(() => page.locator("body").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBe(initialBodySize * 1.3);
  await expect.poll(() => panel.getByRole("heading", { name: "Accessibility" }).evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBe(initialHeadingSize * 1.3);
  await expect.poll(() => panel.getByLabel("Text size").evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBe(initialSelectSize * 1.3);
  await page.setViewportSize({ width: 360, height: 800 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect.poll(() => page.getByRole("region", { name: "Saved searches" }).evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))).toBe(initialBodySize * 1.3);
});
