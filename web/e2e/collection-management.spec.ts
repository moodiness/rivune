import { test, expect } from "./fixtures/rivune";

const artwork = {
  backdropImageUrl: "https://images.example/private/collection/backdrop.jpg?token=collection-secret&size=original",
  coverImageUrl: "https://images.example/private/folder/cover.jpg?token=cover-secret&size=original",
  titleLogoUrl: "https://images.example/private/folder/logo.png?token=logo-secret&size=original",
  heroBackdropUrl: "https://images.example/private/folder/hero.jpg?token=hero-secret&size=original",
};

test("collection editor loads exact artwork sources while ordinary collection reads stay proxied", async ({ page, rivune }) => {
  rivune.setCollectionArtwork("alice", artwork);
  const homeCollections = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === "GET" && url.pathname === "/api/v1/collections";
  });

  await page.goto("/");
  const homeBody = await (await homeCollections).json();
  expect(homeBody.collections[0].backdropImageUrl).toBe("/api/v1/artwork/alice-collection-backdrop");
  expect(homeBody.collections[0].folders[0].coverImageUrl).toBe("/api/v1/artwork/alice-folder-cover");
  expect(JSON.stringify(homeBody)).not.toContain("collection-secret");
  expect(JSON.stringify(homeBody)).not.toContain("cover-secret");

  await page.locator("#main-sidebar nav:visible, .mobile-nav:visible").getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Collections\b/ }).click();
  const card = page.locator(".collection-admin-card").filter({ hasText: "Alice's Slow Shelf" });
  await expect(card).toBeVisible();

  const managementResponse = page.waitForResponse((response) => new URL(response.url()).pathname === "/api/v1/collections/alice-collection/management");
  await card.getByRole("button", { name: "Edit" }).click();
  const managedBody = await (await managementResponse).json();
  expect(managedBody.backdropImageUrl).toBe(artwork.backdropImageUrl);
  expect(managedBody.folders[0].coverImageUrl).toBe(artwork.coverImageUrl);
  expect(managedBody.folders[0].titleLogoUrl).toBe(artwork.titleLogoUrl);
  expect(managedBody.folders[0].heroBackdropUrl).toBe(artwork.heroBackdropUrl);

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Backdrop URL")).toHaveValue(artwork.backdropImageUrl);
  await expect(dialog.getByLabel("Cover image URL")).toHaveValue(artwork.coverImageUrl);
  await expect(dialog.getByLabel("Backdrop URL")).not.toHaveValue(/\/api\/v1\/artwork\//);
  await expect(dialog.getByLabel("Cover image URL")).not.toHaveValue(/\/api\/v1\/artwork\//);
});
