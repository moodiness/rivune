import type { Locator, Page } from "@playwright/test";
import { CATEGORY_IDS, DEVICE_IDS, expect, test } from "./fixtures/rivune";
import { selectListbox, selectOption } from "./helpers/select";

async function openAdministration(page: Page, tab?: "Categories" | "Profiles" | "Devices") {
  await page.goto("/");
  await page.locator("#main-sidebar nav:visible, .mobile-nav:visible").getByRole("button", { name: "Settings", exact: true }).click();
  if (tab && tab !== "Profiles") {
    await page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: new RegExp(`^${tab}\\b`) }).click();
  }
}

function categoryCard(page: Page, name: string) {
  return page.locator(".category-card").filter({ has: page.getByRole("heading", { name, exact: true }) });
}

function profileCard(page: Page, name: string) {
  return page.locator(".profile-admin-card").filter({ has: page.getByRole("heading", { name, exact: true }) });
}

function deviceCard(page: Page, name: string) {
  return page.locator(".device-admin-card").filter({ has: page.getByRole("heading", { name, exact: true }) });
}

async function expectTextareaSurface(textarea: Locator) {
  await expect(textarea).toBeVisible();
  const surface = await textarea.evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      bordered: style.borderTopStyle === "solid" && Number.parseFloat(style.borderTopWidth) > 0,
      padded: Number.parseFloat(style.paddingBlockStart) > 0 && Number.parseFloat(style.paddingInlineStart) > 0,
      rounded: Number.parseFloat(style.borderTopLeftRadius) > 0,
      surfaced: style.backgroundColor !== "rgba(0, 0, 0, 0)",
      resize: style.resize,
    };
  });
  expect(surface).toEqual({ bordered: true, padded: true, rounded: true, surfaced: true, resize: "vertical" });
}

test("global administrators can create a category with the complete server request", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await page.getByRole("button", { name: "New category" }).click();

  const dialog = page.locator("dialog");
  await dialog.getByLabel("Name", { exact: true }).fill("Studio");
  await expect(dialog.getByLabel("Color", { exact: true })).toHaveAttribute("type", "color");
  await expectTextareaSurface(dialog.getByLabel("Description"));
  await dialog.getByLabel("Description").fill("Editing workstations");
  await dialog.getByLabel("Color").fill("#123ABC");
  await dialog.getByLabel("Icon name").fill("monitor");
  await dialog.getByRole("button", { name: "Create category" }).click();

  const request = await rivune.waitForRequest("/api/v1/categories", "POST");
  expect(request.body).toEqual({ name: "Studio", description: "Editing workstations", color: "#123ABC", icon: "monitor" });
  await expect(categoryCard(page, "Studio")).toContainText("Editing workstations");
  await expect(categoryCard(page, "Studio")).toContainText("0 profiles");
  await expect(categoryCard(page, "Studio")).toContainText("0 devices");
});

test("a category save closes and reports success before a failed account refresh without retrying", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await page.getByRole("button", { name: "New category" }).click();
  const dialog = page.locator("dialog");
  await dialog.getByLabel("Name", { exact: true }).fill("Studio");
  rivune.failNextAccountRefresh(3_000);

  await dialog.getByRole("button", { name: "Create category" }).click();
  await rivune.waitForRequest("/api/v1/categories", "POST");

  await expect(dialog).toHaveCount(0, { timeout: 750 });
  await expect(categoryCard(page, "Studio")).toBeVisible();
  const success = page.locator(".app-notification--success").filter({ hasText: "Studio" });
  await expect(success).toHaveCount(1);
  await expect(success).toBeVisible();
  await expect(page.locator(".categories-admin .notice--success")).toHaveCount(0);
  await expect.poll(() => rivune.accountRefreshCompletions).toEqual([503]);
  expect(rivune.matching("/api/v1/categories", "POST")).toHaveLength(1);
});

test("a server-detected duplicate category remains an explicit conflict", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await expect(categoryCard(page, "Guest")).toBeVisible();
  rivune.seedCategory("Studio");

  await page.getByRole("button", { name: "New category" }).click();
  const dialog = page.locator("dialog");
  await dialog.getByLabel("Name", { exact: true }).fill("Studio");
  await dialog.getByRole("button", { name: "Create category" }).click();

  const request = await rivune.waitForRequest("/api/v1/categories", "POST");
  expect(request.body).toEqual({ name: "Studio", description: null, color: null, icon: null });
  await expect(dialog.getByText("A category with this name already exists.")).toBeVisible();
  await expect(dialog).toBeVisible();
});

test("category description, color, and icon edits are persisted together", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await categoryCard(page, "Guest").getByRole("button", { name: "Edit" }).click();

  const dialog = page.locator("dialog");
  await dialog.getByLabel("Description").fill("Short-term visitor access");
  await dialog.getByLabel("Color").fill("#224466");
  await dialog.getByLabel("Icon name").fill("users");
  await dialog.getByRole("button", { name: "Save category" }).click();

  const request = await rivune.waitForRequest(`/api/v1/categories/${CATEGORY_IDS.guest}`, "PATCH");
  expect(request.body).toEqual({ name: "Guest", description: "Short-term visitor access", color: "#224466", icon: "users" });
  await expect(categoryCard(page, "Guest")).toContainText("Short-term visitor access");
  await expect(categoryCard(page, "Guest").locator(".category-card__mark")).toHaveText("users");
});

test("the default category can be changed and becomes the profile creation default", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await categoryCard(page, "Kids").getByRole("button", { name: "Default", exact: true }).click();

  const request = await rivune.waitForRequest(`/api/v1/categories/${CATEGORY_IDS.kids}`, "PATCH");
  expect(request.body).toEqual({ isDefault: true });
  await expect(categoryCard(page, "Kids").locator(".category-badge--default")).toHaveText("Default");
  await expect(categoryCard(page, "Household").locator(".category-badge--default")).toHaveCount(0);

  await page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Profiles\b/ }).click();
  await page.getByRole("button", { name: "New profile" }).click();
  await expect(page.locator("dialog").getByLabel("Category")).toHaveAttribute("data-value", CATEGORY_IDS.kids);
});

test("category reorder sends and renders the complete authoritative order", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await page.getByRole("button", { name: "Move Guest up" }).click();

  const request = await rivune.waitForRequest("/api/v1/categories/order", "PUT");
  expect(request.body).toEqual({ categoryIds: [CATEGORY_IDS.household, CATEGORY_IDS.guest, CATEGORY_IDS.kids] });
  await expect(page.locator(".category-card h3")).toHaveText(["Household", "Guest", "Kids"]);
  await expect(page.locator(".app-notification--success").filter({ hasText: "Category order was saved." })).toHaveCount(1);
  await expect(page.locator(".categories-admin .notice--success")).toHaveCount(0);
});

test("All categories, profile and device filters, counts, and badges reflect server state", async ({ page, rivune }) => {
  await openAdministration(page, "Profiles");
  const administrationTabs = page.getByRole("navigation", { name: "Administration sections" });
  await expect(administrationTabs.getByRole("button", { name: /^Categories\b/ })).toBeVisible();
  await expect(administrationTabs.getByRole("button", { name: /^Devices\b/ })).toBeVisible();
  const profiles = page.locator(".profiles-admin");
  const profileFilter = profiles.getByRole("combobox");

  await expect(profiles.locator(".profile-admin-card")).toHaveCount(3);
  await selectOption(profileFilter, CATEGORY_IDS.kids);
  await expect(profiles.locator(".profile-admin-card")).toHaveCount(2);
  await expect(profileCard(page, "Bob").getByLabel("Category: Kids")).toBeVisible();
  await expect(profileCard(page, "Casey").getByLabel("Category: Kids")).toBeVisible();
  await expect(profileCard(page, "Alice")).toHaveCount(0);
  await selectOption(profileFilter, "all");
  await expect(profiles.locator(".profile-admin-card")).toHaveCount(3);
  await expect(profileCard(page, "Alice").getByLabel("Category: Household")).toBeVisible();
  await expect(profileCard(page, "Alice")).toContainText("Manager");
  await expect(profileCard(page, "Bob")).toContainText("Viewer");

  await page.getByRole("navigation", { name: "Administration sections" }).getByRole("button", { name: /^Devices\b/ }).click();
  const devices = page.locator(".devices-admin");
  const deviceFilter = devices.getByRole("combobox");
  await selectOption(deviceFilter, CATEGORY_IDS.kids);
  await expect.poll(() => rivune.matching("/api/v1/devices", "GET").at(-1)?.search.get("categoryId")).toBe(CATEGORY_IDS.kids);
  const filteredRequest = rivune.matching("/api/v1/devices", "GET").at(-1)!;
  expect(filteredRequest.search.get("categoryId")).toBe(CATEGORY_IDS.kids);
  await expect(devices.locator(".device-admin-card")).toHaveCount(1);
  await expect(deviceCard(page, "Kids tablet").getByLabel("Category: Kids")).toBeVisible();
  await selectOption(deviceFilter, "all");
  await expect(devices.locator(".device-admin-card")).toHaveCount(2);
  await expect(deviceCard(page, "Living room TV").getByLabel("Category: Household")).toBeVisible();
});

test("device deletion requires confirmation, sends one DELETE, and removes the row and selection", async ({ page, rivune }) => {
  await openAdministration(page, "Devices");
  const card = deviceCard(page, "Living room TV");
  await card.getByLabel("Select Living room TV").check();
  await expect(page.locator(".bulk-move-bar")).toContainText("1 devices selected");

  await card.getByRole("button", { name: "Delete" }).click();
  expect(rivune.matching(`/api/v1/devices/${DEVICE_IDS.livingRoom}`, "DELETE")).toHaveLength(0);
  const confirmation = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "Delete Living room TV?" }) });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Delete device" }).click();

  await expect.poll(() => rivune.matching(`/api/v1/devices/${DEVICE_IDS.livingRoom}`, "DELETE").length).toBe(1);
  await expect(card).toHaveCount(0);
  await expect(page.locator(".bulk-move-bar")).toHaveCount(0);
  await expect(page.getByText("Living room TV was deleted.", { exact: true })).toBeVisible();
});

test("a failed device deletion remains recoverable without implicitly removing the device", async ({ page, rivune }) => {
  rivune.failNextDeviceDeletion(DEVICE_IDS.tablet);
  await openAdministration(page, "Devices");
  const card = deviceCard(page, "Kids tablet");
  await card.getByRole("button", { name: "Delete" }).click();
  const confirmation = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "Delete Kids tablet?" }) });
  await confirmation.getByRole("button", { name: "Delete device" }).click();

  await expect(page.locator(".devices-admin > .notice")).toContainText("The device could not be deleted");
  await expect(confirmation).toBeVisible();
  await expect(card).toHaveCount(1);
  await confirmation.getByRole("button", { name: "Delete device" }).click();

  await expect.poll(() => rivune.matching(`/api/v1/devices/${DEVICE_IDS.tablet}`, "DELETE").length).toBe(2);
  await expect(card).toHaveCount(0);
});

test("late device filter success and error responses cannot replace the latest filter state", async ({ page, rivune }) => {
  rivune.setDeviceResponse(CATEGORY_IDS.kids, { delay: 3_000 });
  rivune.setDeviceResponse(CATEGORY_IDS.guest, { status: 503, delay: 1_500 });
  await openAdministration(page, "Devices");
  const devices = page.locator(".devices-admin");
  const filter = devices.getByRole("combobox");

  await selectOption(filter, CATEGORY_IDS.kids);
  await expect.poll(() => rivune.matching("/api/v1/devices", "GET").at(-1)?.search.get("categoryId")).toBe(CATEGORY_IDS.kids);
  await selectOption(filter, CATEGORY_IDS.guest);
  await expect.poll(() => rivune.matching("/api/v1/devices", "GET").at(-1)?.search.get("categoryId")).toBe(CATEGORY_IDS.guest);
  await selectOption(filter, CATEGORY_IDS.household);
  await expect.poll(() => rivune.matching("/api/v1/devices", "GET").at(-1)?.search.get("categoryId")).toBe(CATEGORY_IDS.household);

  await expect(deviceCard(page, "Living room TV")).toBeVisible();
  await expect(devices.locator(".device-admin-card")).toHaveCount(1);
  await expect.poll(() => rivune.deviceResponseCompletions.slice(-3), { timeout: 8_000 }).toEqual([CATEGORY_IDS.household, CATEGORY_IDS.guest, CATEGORY_IDS.kids]);
  await expect(devices.locator(".admin-loading-state")).toHaveCount(0);
  await expect(page.getByText("The device list is temporarily unavailable", { exact: true })).toHaveCount(0);
  await expect(deviceCard(page, "Living room TV")).toBeVisible();
  await expect(deviceCard(page, "Kids tablet")).toHaveCount(0);
});

test("profile creation requires and submits the chosen category", async ({ page, rivune }) => {
  await openAdministration(page, "Profiles");
  await page.getByRole("button", { name: "New profile" }).click();

  const dialog = page.locator("dialog");
  const category = dialog.getByLabel("Category");
  await expect(category).toHaveAttribute("aria-required", "true");
  await selectOption(category, CATEGORY_IDS.kids);
  await dialog.getByLabel("Name").fill("Jordan");
  await expectTextareaSurface(dialog.getByLabel("Description"));
  await dialog.getByLabel("Description").fill("Weekend guest profile");
  await dialog.getByRole("button", { name: "Save profile" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles", "POST");
  expect(request.body).toEqual({ name: "Jordan", description: "Weekend guest profile", categoryId: CATEGORY_IDS.kids, isChild: false, enabled: true });
  await expect(profileCard(page, "Jordan").getByLabel("Category: Kids")).toBeVisible();
  await expect(profileCard(page, "Jordan")).toContainText("Weekend guest profile");
});

test("avatar previews keep one blob URL and revoke it when the editor closes", async ({ page, rivune: _rivune }) => {
  await page.addInitScript(() => {
    const state = window as typeof window & { __avatarURLs?: { created: string[]; revoked: string[] } };
    state.__avatarURLs = { created: [], revoked: [] };
    URL.createObjectURL = () => {
      const url = `blob:avatar-${state.__avatarURLs!.created.length + 1}`;
      state.__avatarURLs!.created.push(url);
      return url;
    };
    URL.revokeObjectURL = (url) => {
      state.__avatarURLs!.revoked.push(url);
    };
  });
  await openAdministration(page, "Profiles");
  await page.getByRole("button", { name: "New profile" }).click();
  const dialog = page.locator("dialog");
  await dialog.locator('input[type="file"]').setInputFiles({
    name: "avatar.png",
    mimeType: "image/png",
    buffer: Buffer.from("avatar"),
  });
  await expect(dialog.locator(".avatar-editor img").first()).toHaveAttribute("src", "blob:avatar-1");
  await dialog.getByLabel("Name").fill("A profile name that triggers the editor to render repeatedly");
  await dialog.getByLabel("Description").fill("A description that also changes editor state.");
  expect(await page.evaluate(() => (window as typeof window & { __avatarURLs?: { created: string[] } }).__avatarURLs?.created)).toEqual(["blob:avatar-1"]);

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect(await page.evaluate(() => (window as typeof window & { __avatarURLs?: { revoked: string[] } }).__avatarURLs?.revoked)).toEqual(["blob:avatar-1"]);
});

test("a single profile move preserves the profile identity and exact request body", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 604, height: 500 });
  await openAdministration(page, "Profiles");
  await profileCard(page, "Bob").getByRole("button", { name: "Move", exact: true }).click();
  const moveDialog = page.locator("dialog");
  const destination = moveDialog.getByLabel("Destination category");
  await selectOption(destination, CATEGORY_IDS.household);
  await destination.click();
  const listbox = await selectListbox(destination);
  await expect(listbox.locator('[role="option"][data-value=""]')).toHaveCount(0);
  await destination.hover();
  const triggerStyle = await destination.evaluate((trigger) => {
    const style = getComputedStyle(trigger);
    return { backgroundColor: style.backgroundColor, borderLeftWidth: style.borderLeftWidth };
  });
  expect(triggerStyle).toEqual({ backgroundColor: "rgba(0, 0, 0, 0)", borderLeftWidth: "0px" });
  const triggerBounds = await destination.boundingBox();
  const listboxBounds = await listbox.boundingBox();
  expect(listboxBounds?.width ?? Infinity).toBeLessThanOrEqual(360);
  expect(listboxBounds?.width ?? Infinity).toBeLessThan(triggerBounds?.width ?? 0);
  await destination.press("Escape");
  await moveDialog.getByRole("button", { name: "Confirm move" }).click();

  const move = await rivune.waitForRequest("/api/v1/profiles/category-moves", "POST");
  expect(move.body).toEqual({ profileIds: ["bob"], categoryId: CATEGORY_IDS.household });
  await expect(profileCard(page, "Bob").getByLabel("Category: Household")).toBeVisible();

  await profileCard(page, "Bob").getByRole("button", { name: "Edit" }).click();
  await page.locator("dialog").getByLabel("Description").fill("Updated profile description");
  await page.locator("dialog").getByRole("button", { name: "Save profile" }).click();
  const update = await rivune.waitForRequest("/api/v1/profiles/bob", "PATCH");
  expect(update.body).toMatchObject({ description: "Updated profile description" });
  await expect(profileCard(page, "Bob")).toContainText("Updated profile description");
});

test("bulk profile move submits every selected identity once", async ({ page, rivune }) => {
  await openAdministration(page, "Profiles");
  await page.getByLabel("Select Bob").check();
  await page.getByLabel("Select Casey").check();
  await expect(page.locator(".profiles-admin .bulk-move-bar")).toContainText("2 profiles selected");
  await page.locator(".profiles-admin .bulk-move-bar").getByRole("button", { name: "Move selected" }).click();
  await selectOption(page.locator("dialog").getByLabel("Destination category"), CATEGORY_IDS.household);
  await page.locator("dialog").getByRole("button", { name: "Confirm move" }).click();

  const request = await rivune.waitForRequest("/api/v1/profiles/category-moves", "POST");
  expect(request.body).toEqual({ profileIds: ["bob", "casey"], categoryId: CATEGORY_IDS.household });
  await expect(profileCard(page, "Bob").getByLabel("Category: Household")).toBeVisible();
  await expect(profileCard(page, "Casey").getByLabel("Category: Household")).toBeVisible();
});

test("device name and note edits and a single category move use separate exact requests", async ({ page, rivune }) => {
  await openAdministration(page, "Devices");
  await deviceCard(page, "Kids tablet").getByRole("button", { name: "Edit" }).click();

  const editDialog = page.locator("dialog");
  const categoryBadge = editDialog.locator(".category-badge");
  await expect(categoryBadge).toBeVisible();
  expect((await categoryBadge.boundingBox())?.width ?? Number.POSITIVE_INFINITY).toBeLessThan(160);
  await editDialog.getByLabel("Device name").fill("  Travel tablet  ");
  await expectTextareaSurface(editDialog.getByLabel("Internal note"));
  await editDialog.getByLabel("Internal note").fill("  Road trips  ");
  await editDialog.getByRole("button", { name: "Save device" }).click();
  const edit = await rivune.waitForRequest(`/api/v1/devices/${DEVICE_IDS.tablet}`, "PATCH");
  expect(edit.body).toEqual({ name: "Travel tablet", internalNote: "Road trips" });
  await expect(deviceCard(page, "Travel tablet")).toContainText("Road trips");

  await deviceCard(page, "Travel tablet").getByRole("button", { name: "Move", exact: true }).click();
  await selectOption(page.locator("dialog").getByLabel("Destination category"), CATEGORY_IDS.household);
  await page.locator("dialog").getByRole("button", { name: "Confirm move" }).click();
  const move = await rivune.waitForRequest("/api/v1/devices/category-moves", "POST");
  expect(move.body).toEqual({ deviceIds: [DEVICE_IDS.tablet], categoryId: CATEGORY_IDS.household });
  await expect(deviceCard(page, "Travel tablet").getByLabel("Category: Household")).toBeVisible();
});

test("bulk device move submits every selected server device", async ({ page, rivune }) => {
  await openAdministration(page, "Devices");
  await page.getByLabel("Select Living room TV").check();
  await page.getByLabel("Select Kids tablet").check();
  await expect(page.locator(".devices-admin .bulk-move-bar")).toContainText("2 devices selected");
  await page.locator(".devices-admin .bulk-move-bar").getByRole("button", { name: "Move selected" }).click();
  await selectOption(page.locator("dialog").getByLabel("Destination category"), CATEGORY_IDS.kids);
  await page.locator("dialog").getByRole("button", { name: "Confirm move" }).click();

  const request = await rivune.waitForRequest("/api/v1/devices/category-moves", "POST");
  expect(request.body).toEqual({ deviceIds: [DEVICE_IDS.livingRoom, DEVICE_IDS.tablet], categoryId: CATEGORY_IDS.kids });
  await expect(deviceCard(page, "Living room TV").getByLabel("Category: Kids")).toBeVisible();
  await expect(deviceCard(page, "Kids tablet").getByLabel("Category: Kids")).toBeVisible();
});

test("an empty category deletes without a reassignment claim", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await categoryCard(page, "Guest").getByRole("button", { name: "Delete" }).click();
  const dialog = page.locator("dialog");
  await expect(dialog).toContainText("This category is unused and can be deleted safely.");
  await dialog.getByRole("button", { name: "Delete category" }).click();

  const request = await rivune.waitForRequest(`/api/v1/categories/${CATEGORY_IDS.guest}`, "DELETE");
  expect(request.body).toEqual({});
  await expect(categoryCard(page, "Guest")).toHaveCount(0);
});

test("a hidden category reference reveals reassignment after the empty delete is rejected", async ({ page, rivune }) => {
  rivune.seedHiddenCategoryReference(CATEGORY_IDS.guest);
  await openAdministration(page, "Categories");
  await expect(categoryCard(page, "Guest")).toContainText("0 profiles");
  await expect(categoryCard(page, "Guest")).toContainText("0 devices");
  await categoryCard(page, "Guest").getByRole("button", { name: "Delete" }).click();
  const dialog = page.locator("dialog");
  await expect(dialog).toContainText("This category is unused and can be deleted safely.");
  await dialog.getByRole("button", { name: "Delete category" }).click();

  await expect.poll(() => rivune.matching(`/api/v1/categories/${CATEGORY_IDS.guest}`, "DELETE").length).toBe(1);
  expect(rivune.matching(`/api/v1/categories/${CATEGORY_IDS.guest}`, "DELETE")[0]?.body).toEqual({});
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Reassign profiles and devices to")).toBeVisible();
  await selectOption(dialog.getByLabel("Reassign profiles and devices to"), CATEGORY_IDS.household);
  await dialog.getByRole("button", { name: "Delete and reassign" }).click();

  await expect.poll(() => rivune.matching(`/api/v1/categories/${CATEGORY_IDS.guest}`, "DELETE").length).toBe(2);
  expect(rivune.matching(`/api/v1/categories/${CATEGORY_IDS.guest}`, "DELETE").map((request) => request.body)).toEqual([
    {},
    { reassignToCategoryId: CATEGORY_IDS.household },
  ]);
  await expect(dialog).toHaveCount(0);
  await expect(categoryCard(page, "Guest")).toHaveCount(0);
});

test("a populated category deletes only with server-side profile and device reassignment", async ({ page, rivune }) => {
  await openAdministration(page, "Categories");
  await categoryCard(page, "Kids").getByRole("button", { name: "Delete" }).click();
  const dialog = page.locator("dialog");
  await expect(dialog).toContainText("Its 2 profiles and 1 devices must move to another category.");
  await selectOption(dialog.getByLabel("Reassign profiles and devices to"), CATEGORY_IDS.household);
  await dialog.getByRole("button", { name: "Delete and reassign" }).click();

  const request = await rivune.waitForRequest(`/api/v1/categories/${CATEGORY_IDS.kids}`, "DELETE");
  expect(request.body).toEqual({ reassignToCategoryId: CATEGORY_IDS.household });
  await expect(categoryCard(page, "Kids")).toHaveCount(0);
  await expect(categoryCard(page, "Household")).toContainText("3 profiles");
  await expect(categoryCard(page, "Household")).toContainText("2 devices");
});

test("profile selection keeps the refreshed account profile instead of the earlier selection snapshot", async ({ page, rivune }) => {
  rivune.setProfileCategory("bob", CATEGORY_IDS.household);
  rivune.refreshProfileNameAfterSelection("bob", "Bobby Refreshed");
  await page.goto("/");
  await page.getByRole("button", { name: "Switch profile" }).first().click();
  await expect(page.getByRole("heading", { name: "Who's watching?" })).toBeVisible();
  await page.getByRole("button", { name: "Bob Profile" }).click();

  await expect(page.locator(".sidebar-profile")).toContainText("Bobby Refreshed");
  expect(rivune.matching("/api/v1/profiles/bob/select", "POST")).toHaveLength(1);
});

test("category and device tabs are absent from a category-scoped administration session", async ({ page, rivune }) => {
  await rivune.configureCategoryScope(page, CATEGORY_IDS.household);
  await openAdministration(page, "Profiles");

  const tabs = page.getByRole("navigation", { name: "Administration sections" });
  await expect(tabs.getByRole("button", { name: /^Categories\b/ })).toHaveCount(0);
  await expect(tabs.getByRole("button", { name: /^Devices\b/ })).toHaveCount(0);
  await expect(page.locator(".profile-admin-card")).toHaveCount(1);
  await expect(profileCard(page, "Alice").getByLabel("Category: Household")).toBeVisible();
  await expect(profileCard(page, "Bob")).toHaveCount(0);
  expect(rivune.matching("/api/v1/categories", "GET")).toHaveLength(0);
});

test("unauthorized admin deep links are replaced by the visible tab", async ({ page, rivune }) => {
  await rivune.configureCategoryScope(page, CATEGORY_IDS.household);
  await page.goto("/#admin?tab=devices");

  const rail = page.getByRole("navigation", { name: "Administration sections" });
  await expect(rail.getByRole("button", { name: /^Profiles\b/ })).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveURL(/\/#admin\?tab=profiles$/);
  await expect(page).not.toHaveURL(/devices/);
  await expect(page.locator(".profile-admin-card")).toHaveCount(1);
});

test("mobile admin rail keeps all destinations keyboard reachable without group controls", async ({ page, rivune: _rivune }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAdministration(page, "Profiles");

  const rail = page.getByRole("navigation", { name: "Administration sections" });
  await expect(rail.locator("[data-admin-tab]")).toHaveCount(8);
  await expect(rail.locator(".admin-tabs__label")).toHaveCount(4);
  expect(await rail.locator(".admin-tabs__label").evaluateAll((labels) => labels.every((label) => getComputedStyle(label).display === "none"))).toBe(true);

  const profiles = rail.locator('[data-admin-tab="profiles"]');
  const devices = rail.locator('[data-admin-tab="devices"]');
  const settings = rail.locator('[data-admin-tab="settings"]');
  await profiles.focus();
  await profiles.press("ArrowRight");
  await expect(devices).toBeFocused();
  await devices.press("End");
  await expect(settings).toBeFocused();
  await settings.press("Enter");
  await expect(settings).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveURL(/\/#admin\?tab=settings&section=appearance$/);
});

test("global pairing requires a category and submits normalized optional metadata", async ({ page, rivune }) => {
  await rivune.configureGlobalAdmin(page, "bob", CATEGORY_IDS.kids);
  await page.goto("/pair?code=bcdfghjk");
  const form = page.locator(".pairing-card form");
  const approve = form.getByRole("button", { name: "Approve device" });
  await expect(approve).toBeDisabled();
  await selectOption(form.getByLabel("Access category"), CATEGORY_IDS.kids);
  await form.getByLabel("Device name (optional)").fill("  Bedroom TV  ");
  await form.getByLabel("Internal note (optional)").fill("  Upstairs  ");
  await approve.click();

  const request = await rivune.waitForRequest("/api/v1/auth/device-code/approve", "POST");
  expect(request.body).toEqual({ userCode: "BCDF-GHJK", categoryId: CATEGORY_IDS.kids, deviceName: "Bedroom TV", internalNote: "Upstairs" });
  await expect(page.getByRole("heading", { name: "They can choose a profile now." })).toBeFocused();
});

test("category-scoped pairing is fixed to its server category on mobile RTL", async ({ page, rivune }) => {
  await rivune.configureCategoryScope(page, CATEGORY_IDS.household);
  rivune.setInterfaceLanguage("ar");
  await page.setViewportSize({ width: 360, height: 800 });
  await page.goto("/pair?code=bcdfghjk");

  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  const category = page.locator(".pairing-card__category");
  await expect(category.getByText("Household", { exact: true })).toBeVisible();
  expect(rivune.matching("/api/v1/categories", "GET")).toHaveLength(0);
  await expect(category.getByRole("combobox")).toHaveCount(0);
  const form = page.locator(".pairing-card form");
  const formBounds = await page.locator(".pairing-card").boundingBox();
  expect(formBounds).not.toBeNull();
  expect(formBounds?.x ?? -1).toBeGreaterThanOrEqual(0);
  expect((formBounds?.x ?? 0) + (formBounds?.width ?? 0)).toBeLessThanOrEqual(360);
  expect(await page.locator(".auth-page").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await form.locator('input[maxlength="120"]').fill("  Hall display  ");
  await form.locator("textarea").fill("  Shared space  ");
  await form.locator('button[type="submit"]').click();

  const request = await rivune.waitForRequest("/api/v1/auth/device-code/approve", "POST");
  expect(request.body).toEqual({ userCode: "BCDF-GHJK", categoryId: CATEGORY_IDS.household, deviceName: "Hall display", internalNote: "Shared space" });
  const bounds = await page.locator(".pairing-card").boundingBox();
  expect(bounds).not.toBeNull();
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(360);
});
