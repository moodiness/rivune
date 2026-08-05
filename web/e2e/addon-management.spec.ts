import { expect, test } from "./fixtures/rivune";

const addon = {
  id: "30000000-0000-4000-8000-000000000001",
  manifest: {
    id: "metadata-fixture",
    name: "Metadata fixture",
    version: "1.0.0",
    description: "Deterministic addon management fixture",
    types: ["movie", "series"],
  },
  position: 0,
  profileIds: ["alice"],
  installedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

const transportUrl = "https://addon.example/nested/provider/manifest.json?token=fixture-credential&locale=fr";

test("global addon editor loads the protected full transport URL", async ({ page, rivune: _rivune }) => {
  const managementRequests: string[] = [];
  await page.route(/\/api\/v1\/addons(?:\/.*)?$/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "GET" && url.pathname === "/api/v1/addons") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ addons: [addon] }) });
      return;
    }
    if (request.method() === "GET" && url.pathname === `/api/v1/addons/${addon.id}/management`) {
      managementRequests.push(url.pathname);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...addon, transportUrl }) });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#admin?tab=addons");
  const card = page.locator(".addon-card").filter({ has: page.getByRole("heading", { name: addon.manifest.name, exact: true }) });
  await expect(card).toBeVisible();
  const logoBounds = await card.locator(".addon-card__logo").boundingBox();
  const bodyBounds = await card.locator(".addon-card__body").boundingBox();
  expect(logoBounds).not.toBeNull();
  expect(bodyBounds).not.toBeNull();
  expect((bodyBounds?.x ?? 0) - ((logoBounds?.x ?? 0) + (logoBounds?.width ?? Number.POSITIVE_INFINITY))).toBeGreaterThanOrEqual(12);
  await card.getByRole("button", { name: "Edit", exact: true }).click();

  await expect(page.locator("dialog").getByLabel("Transport URL", { exact: true })).toHaveValue(transportUrl);
  expect(managementRequests).toEqual([`/api/v1/addons/${addon.id}/management`]);
});
