import { expect, test } from "./fixtures/rivune";

test("administrator monitors operations and runs fixed maintenance controls", async ({ page, rivune }) => {
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();

  await expect(page.getByRole("heading", { name: "Metadata health" })).toBeVisible();
  const metadataMetrics = page.getByLabel("Metadata cache metrics");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Payload entries" })).toContainText("48");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Missing payloads" })).toContainText("12");
  const streamMetrics = page.getByLabel("Live stream activity metrics");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Active sessions" })).toContainText("2");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Temporary media" })).toContainText("12 MB");

  await page.getByLabel("Run scheduled refreshes").check();
  await page.getByLabel("Refresh interval").selectOption("12");
  await page.getByLabel("Metadata language").fill("fr-CA");
  await page.getByLabel("Batch size").fill("40");
  await page.getByRole("button", { name: "Save refresh schedule" }).click();

  const scheduleRequest = await rivune.waitForRequest("/api/v1/operations/schedules/metadata-refresh", "PUT");
  expect(scheduleRequest.body).toEqual({ enabled: true, intervalHours: 12, language: "fr-CA", batchSize: 40 });
  await expect(page.getByText("The metadata refresh schedule was updated.")).toBeVisible();

  const overviewRequests = rivune.matching("/api/v1/operations", "GET").length;
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect.poll(() => rivune.matching("/api/v1/operations", "GET").length).toBeGreaterThan(overviewRequests);
  await expect(page.getByLabel("Run scheduled refreshes")).toBeChecked();
  await expect(page.getByLabel("Refresh interval")).toHaveValue("12");
  await expect(page.getByLabel("Metadata language")).toHaveValue("fr-CA");
  await expect(page.getByLabel("Batch size")).toHaveValue("40");

  await page.getByRole("button", { name: "Run Fetch missing metadata" }).click();
  const fetchRequest = await rivune.waitForRequest("/api/v1/operations/actions/fetch-missing-metadata", "POST");
  expect(fetchRequest.body).toBeUndefined();
  await expect(page.getByText("9 of 12 candidates refreshed; 3 failed.")).toBeVisible();

  const clearMetadataPath = "/api/v1/operations/actions/clear-metadata-cache";
  await page.getByRole("button", { name: "Run Clear metadata cache" }).click();
  await expect(page.getByRole("heading", { name: "Clear all localized metadata?" })).toBeVisible();
  expect(rivune.matching(clearMetadataPath, "POST")).toHaveLength(0);
  await page.getByRole("button", { name: "Clear metadata cache", exact: true }).click();
  const clearMetadataRequest = await rivune.waitForRequest(clearMetadataPath, "POST");
  expect(clearMetadataRequest.body).toBeUndefined();
  await expect(page.getByText("48 localized metadata entries deleted.")).toBeVisible();

  const clearStreamPath = "/api/v1/operations/actions/clear-stream-cache";
  await page.getByRole("button", { name: "Run Clear stream cache" }).click();
  await expect(page.getByRole("heading", { name: "Stop playback and clear stream data?" })).toBeVisible();
  expect(rivune.matching(clearStreamPath, "POST")).toHaveLength(0);
  await page.getByRole("button", { name: "Stop streams and clear cache" }).click();
  const clearStreamRequest = await rivune.waitForRequest(clearStreamPath, "POST");
  expect(clearStreamRequest.body).toBeUndefined();
  await expect(page.getByText("2 sessions removed, 1 jobs stopped, and 12582912 bytes cleared.")).toBeVisible();

  await page.getByRole("button", { name: /Settings/ }).click();
  await expect(page.getByRole("heading", { name: "Maintenance mode" })).toHaveCount(0);
  await expect(page.getByLabel("Block member access")).toHaveCount(0);
  await expect(page.getByLabel("Public message")).toHaveCount(0);
});
