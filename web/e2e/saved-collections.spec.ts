import { test, expect } from "./fixtures/rivune";

test("profile can save and reopen a search", async ({ page, rivune }) => {
  void rivune;
  rivune.setNotificationDuration(2);
  let savedSearches: Array<Record<string, unknown>> = [];
  await page.route(/\/api\/v1\/saved-searches(?:\/.*)?$/, async (route) => {
    const request = route.request();
    if (request.method() === "GET") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ savedSearches }) });
      return;
    }
    if (request.method() === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      const saved = { ...body, id: "saved-1", revision: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" };
      savedSearches = [saved];
      await route.fulfill({ contentType: "application/json", status: 201, body: JSON.stringify(saved) });
      return;
    }
    if (request.method() === "DELETE") {
      savedSearches = [];
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#search");
  const search = page.getByRole("searchbox", { name: "Search" });
  await search.fill("space opera");
  await page.getByLabel("Search name").fill("Weekend movies");
  await page.getByRole("button", { name: "Save this search" }).click();
  await expect(page.getByText("Weekend movies", { exact: true })).toBeVisible();
  const savedToast = page.getByRole("status").filter({ hasText: "Saved searches" });
  await expect(savedToast).toBeVisible();
  await savedToast.hover();
  await page.waitForTimeout(2_200);
  await page.mouse.move(0, 0);
  await search.focus();
  await expect(savedToast).toHaveCount(0, { timeout: 3_000 });
  await expect(search).toBeFocused();
  await search.fill("another query");
  await page.getByRole("button", { name: "Open Weekend movies" }).click();
  await expect(search).toHaveValue("space opera");
  await page.getByRole("button", { name: "Delete Weekend movies" }).click();
  const deletedToastDismiss = page.getByRole("button", { name: "Dismiss notification: Saved searches — Saved search deleted" });
  await deletedToastDismiss.focus();
  await deletedToastDismiss.click();
  await expect(page.getByRole("heading", { name: "Saved searches" })).toBeFocused();
});

test("profile creates and opens a live smart collection", async ({ page, rivune }) => {
  void rivune;
  let collections: Array<Record<string, unknown>> = [];
  await page.route(/\/api\/v1\/smart-collections(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "GET" && path.endsWith("/items")) {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        items: [{ id: "11111111-1111-4111-8111-111111111111", mediaType: "movie", title: "Catalog Drama", releaseInfo: "2024", genres: ["Drama"], resourceId: "42", resourceProvider: "tmdb" }],
        page: 1, pageSize: 100, total: 1, totalPages: 1,
      }) });
      return;
    }
    if (request.method() === "GET") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ smartCollections: collections }) });
      return;
    }
    if (request.method() === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>;
      const saved = { ...body, id: "smart-1", revision: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" };
      collections = [saved];
      await route.fulfill({ contentType: "application/json", status: 201, body: JSON.stringify(saved) });
      return;
    }
    if (request.method() === "DELETE") {
      collections = [];
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#library");
  await page.getByLabel("Collection name").fill("Recent dramas");
  await page.getByLabel("Rule value").fill("Drama");
  await page.getByRole("button", { name: "Create smart collection" }).click();
  await expect(page.getByRole("button", { name: "Open Recent dramas" })).toBeVisible();
  await page.getByRole("button", { name: "Open Recent dramas" }).click();
  await expect(page.getByRole("button", { name: /Catalog Drama/ })).toBeVisible();
  await page.getByRole("button", { name: "Delete Recent dramas" }).click();
  await expect(page.getByRole("heading", { name: "Smart collections" })).toBeFocused();
});

test("saved searches expose retry without false empty state and item-specific actions", async ({ page, rivune: _rivune }) => {
  let attempts = 0;
  await page.route("**/api/v1/saved-searches", async (route) => {
    attempts += 1;
    if (attempts <= 2) {
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "unavailable" } }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ savedSearches: [{ id: "saved-retry", name: "Recovered search", query: "space", sort: "relevance", revision: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" }] }) });
  });

  await page.goto("/#search");
  const panel = page.getByRole("region", { name: "Saved searches" });
  await expect(panel.getByRole("button", { name: "Retry" })).toBeVisible();
  await expect(panel.getByText("No saved searches yet.")).toHaveCount(0);
  await panel.getByRole("button", { name: "Retry" }).click();
  await expect(panel.getByRole("button", { name: "Edit Recovered search" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Delete Recovered search" })).toBeVisible();
});

test("smart collections expose retry without false empty state and item-specific actions", async ({ page, rivune: _rivune }) => {
  let attempts = 0;
  await page.route("**/api/v1/smart-collections", async (route) => {
    attempts += 1;
    if (attempts <= 2) {
      await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "unavailable" } }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ smartCollections: [{ id: "smart-retry", name: "Recovered collection", rules: { type: "genre", operator: "equals", value: "Drama" }, sort: "title", revision: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" }] }) });
  });

  await page.goto("/#library");
  const panel = page.getByRole("region", { name: "Smart collections" });
  await expect(panel.getByRole("button", { name: "Retry" })).toBeVisible();
  await expect(panel.getByText("No smart collections yet.")).toHaveCount(0);
  await panel.getByRole("button", { name: "Retry" }).click();
  await expect(panel.getByRole("button", { name: "Edit Recovered collection" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Delete Recovered collection" })).toBeVisible();
});
