import { expect, test } from "./fixtures/rivune";

const movieID = "89000000-0000-4000-8000-000000000001";
const seriesID = "89000000-0000-4000-8000-000000000002";

test("profile notification inbox supports follow, read, dismiss and pagination", async ({ page, rivune }) => {
  rivune.setLibraryItems([
    { titleId: movieID, mediaType: "movie", title: "Midnight Atlas", releaseInfo: "2026", addedAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z" },
    { titleId: seriesID, mediaType: "series", title: "Signal Horizon", releaseInfo: "2025", addedAt: "2026-08-01T00:00:00Z", updatedAt: "2026-08-01T00:00:00Z" },
  ]);
  const firstPage = [
    { id: "4", kind: "movie-release", titleId: movieID, title: "Midnight Atlas", releaseDate: "2026-08-26", availableAt: "2026-08-26T00:00:00Z", createdAt: "2026-08-26T00:00:00Z" },
    { id: "3", kind: "episode-available", titleId: seriesID, title: "A New Signal", seriesTitle: "Signal Horizon", seasonNumber: 2, episodeNumber: 1, availableAt: "2026-08-25T00:00:00Z", createdAt: "2026-08-25T00:00:00Z" },
    { id: "2", kind: "season-available", titleId: seriesID, title: "Season 2", seriesTitle: "Signal Horizon", seasonNumber: 2, availableAt: "2026-08-24T00:00:00Z", createdAt: "2026-08-24T00:00:00Z" },
  ];
  let notifications = [...firstPage];
  let subscriptions: Array<Record<string, unknown>> = [];

  await page.route("**/api/v1/media-notification**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/media-notification-subscriptions" && request.method() === "GET") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ subscriptions }) });
      return;
    }
    const subscription = url.pathname.match(/^\/api\/v1\/media-notification-subscriptions\/([^/]+)$/);
    if (subscription && request.method() === "PUT") {
      const titleId = decodeURIComponent(subscription[1]!);
      subscriptions = [{ titleId, timezone: "UTC", horizonDays: 30, leadDays: 1, createdAt: "2026-08-26T00:00:00Z", updatedAt: "2026-08-26T00:00:00Z" }];
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(subscriptions[0]) });
      return;
    }
    if (subscription && request.method() === "DELETE") {
      subscriptions = [];
      await route.fulfill({ status: 204 });
      return;
    }
    if (url.pathname === "/api/v1/media-notifications" && request.method() === "GET") {
      const cursor = url.searchParams.get("cursor");
      const body = cursor
        ? { notifications: [{ id: "1", kind: "calendar-event-upcoming", titleId: seriesID, title: "Next Week", seriesTitle: "Signal Horizon", seasonNumber: 2, episodeNumber: 2, releaseDate: "2026-09-02", availableAt: "2026-09-01T00:00:00Z", createdAt: "2026-08-20T00:00:00Z" }] }
        : { notifications, nextCursor: "2" };
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
      return;
    }
    const acknowledgement = url.pathname.match(/^\/api\/v1\/media-notifications\/(\d+)\/acknowledgement$/);
    if (acknowledgement && request.method() === "POST") {
      const input = request.postDataJSON() as { state: "read" | "dismissed" };
      const id = acknowledgement[1];
      notifications = input.state === "dismissed"
        ? notifications.filter((notification) => notification.id !== id)
        : notifications.map((notification) => notification.id === id ? { ...notification, readAt: "2026-08-26T12:00:00Z" } : notification);
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fallback();
  });

  await page.goto("/#notifications");
  await expect(page.getByRole("heading", { name: "Notifications", exact: true })).toBeVisible();
  await expect(page.getByText("Midnight Atlas").first()).toBeVisible();
  await expect(page.getByText("New episode")).toBeVisible();
  await expect(page.getByText("New season", { exact: true })).toBeVisible();

  const firstAlert = page.locator(".notifications-list > li").first();
  await firstAlert.getByRole("button", { name: "Mark read" }).click();
  await expect(firstAlert).toHaveClass(/is-read/);
  await page.locator(".notifications-list > li").nth(1).getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByText("A New Signal")).toHaveCount(0);

  await page.getByRole("button", { name: "Load more" }).click();
  await expect(page.getByText("Coming up")).toBeVisible();

  const movieRow = page.locator(".notifications-tracking li").filter({ hasText: "Midnight Atlas" });
  await movieRow.getByRole("button", { name: "Follow" }).click();
  await expect(movieRow.getByRole("button", { name: "Following" })).toHaveAttribute("aria-pressed", "true");
  await movieRow.getByRole("button", { name: "Following" }).click();
  await expect(movieRow.getByRole("button", { name: "Follow" })).toHaveAttribute("aria-pressed", "false");
});
