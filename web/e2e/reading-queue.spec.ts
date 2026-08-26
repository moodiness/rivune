import { expect, test } from "./fixtures/rivune";

const firstId = "81000000-0000-4000-8000-000000000001";
const secondId = "81000000-0000-4000-8000-000000000002";

function queueItem(id: string, title: string, position: number) {
  return {
    id,
    mediaType: "movie",
    resourceId: `resource:${id}`,
    title,
    position,
    createdAt: "2026-08-26T10:00:00Z",
    updatedAt: "2026-08-26T10:00:00Z",
  };
}

test("duplicate queue titles have unique positioned controls and restore focus after removal", async ({ page, rivune }) => {
  rivune.setReadingQueue([queueItem(firstId, "Duplicate", 0), queueItem(secondId, "Duplicate", 1)], 1);
  await page.goto("/#library");
  const panel = page.getByRole("region", { name: "Reading queue" });
  const firstRemove = panel.getByRole("button", { name: "Remove Duplicate from the queue, position 1", exact: true });
  const secondRemove = panel.getByRole("button", { name: "Remove Duplicate from the queue, position 2", exact: true });
  await expect(firstRemove).toHaveCount(1);
  await expect(secondRemove).toHaveCount(1);
  await firstRemove.click();
  await expect(panel.getByRole("button", { name: "Remove Duplicate from the queue, position 1", exact: true })).toBeFocused();
  await panel.getByRole("button", { name: "Play and consume Duplicate, position 1", exact: true }).click();
  await expect(page.getByRole("button", { name: "Back to browse" })).toBeFocused();
});

test("persistent reading queue exposes order, empty and stale-conflict states accessibly", async ({ page, rivune }) => {
  rivune.setReadingQueue([queueItem(firstId, "First queued title", 0), queueItem(secondId, "Second queued title", 1)], 4);
  rivune.rejectNextQueueReorder();

  await page.goto("/#library");
  const panel = page.getByRole("region", { name: "Reading queue" });
  await expect(panel).toBeVisible();
  await expect(panel.getByText("First queued title")).toBeVisible();
  await panel.getByRole("button", { name: "Move First queued title down" }).click();
  await expect(panel.getByText(/changed on another device/i)).toBeVisible();
  await expect(panel.locator("li").first()).toContainText("Second queued title");

  await panel.getByRole("button", { name: "Remove Second queued title from the queue" }).click();
  await panel.getByRole("button", { name: "Remove First queued title from the queue" }).click();
  await expect(panel.getByRole("heading", { name: "Your queue is empty" })).toBeVisible();
});
