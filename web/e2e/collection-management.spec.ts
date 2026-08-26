import { CATEGORY_IDS, test, expect } from "./fixtures/rivune";

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

test("collection assignment hydrates categories independently and persists exact durable policies", async ({ page, rivune }) => {
  rivune.setCollectionAssignments("alice", { profileIds: ["bob"], categoryIds: [CATEGORY_IDS.kids] });
  rivune.seedProfiles(1, CATEGORY_IDS.kids);
  const writes: Array<{ method: string; body: Record<string, unknown> }> = [];
  await page.route(/\/api\/v1\/collections(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if ((request.method() === "PUT" && pathname === "/api/v1/collections/alice-collection") || (request.method() === "POST" && pathname === "/api/v1/collections")) {
      const body = request.postDataJSON() as Record<string, unknown>;
      writes.push({ method: request.method(), body });
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...body, id: "alice-collection", position: 0, version: 2, createdAt: "2024-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=collections");
  const card = page.locator(".collection-admin-card").filter({ hasText: "Alice's Slow Shelf" });
  await expect(card).toContainText("3 Profiles reached");
  await card.getByRole("button", { name: "Edit", exact: true }).click();
  let dialog = page.getByRole("dialog");
  const categories = dialog.locator(".assignment-picker__categories");
  for (const name of ["Household", "Kids", "Guest"]) await expect(categories.getByText(name, { exact: true })).toBeVisible();
  const kidsCheckbox = categories.locator("label").filter({ hasText: "Kids" }).getByRole("checkbox");
  await expect(kidsCheckbox).toBeChecked();
  const profiles = dialog.locator(".assignment-picker__profiles");
  await expect(profiles).not.toHaveAttribute("open", "");
  await profiles.getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  const bobCheckbox = profiles.locator("label").filter({ hasText: "Bob" }).getByRole("checkbox");
  await expect(bobCheckbox).toBeChecked();
  await bobCheckbox.uncheck();
  await expect(kidsCheckbox).toBeChecked();
  await dialog.getByRole("button", { name: "Save collection", exact: true }).click();
  await expect(dialog).toHaveCount(0);

  expect(writes[0]?.method).toBe("PUT");
  expect(writes[0]?.body.profileIds).toEqual([]);
  expect(writes[0]?.body.categoryIds).toEqual([CATEGORY_IDS.kids]);

  await page.getByRole("button", { name: "New collection", exact: true }).click();
  dialog = page.getByRole("dialog");
  const newProfiles = dialog.locator(".assignment-picker__profiles");
  await newProfiles.getByText("Choose individual profiles (1 selected)", { exact: true }).click();
  await newProfiles.locator("label").filter({ hasText: "Alice" }).getByRole("checkbox").uncheck();
  const save = dialog.getByRole("button", { name: "Save collection", exact: true });
  await expect(save).toBeDisabled();
  await dialog.locator(".assignment-picker__categories label").filter({ hasText: "Guest" }).getByRole("checkbox").check();
  await expect(save).toBeEnabled();
  await save.click();
  await expect.poll(() => writes.length).toBe(2);

  expect(writes[1]?.method).toBe("POST");
  expect(writes[1]?.body.profileIds).toEqual([]);
  expect(writes[1]?.body.categoryIds).toEqual([CATEGORY_IDS.guest]);
});
