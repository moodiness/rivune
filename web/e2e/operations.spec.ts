import { expect, test } from "./fixtures/rivune";

test("administrator monitors operations and runs fixed maintenance controls", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 1568, height: 1000 });
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();

  await expect(page.getByRole("heading", { name: "Metadata health" })).toBeVisible();
  const metadataMetrics = page.getByLabel("Metadata cache metrics");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Payload entries" })).toContainText("48");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Missing payloads" })).toContainText("12");
  const streamMetrics = page.getByLabel("Live stream activity metrics");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Active sessions" })).toContainText("2");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Temporary media" })).toContainText("12 MB");

  const notifications = page.getByRole("region", { name: "Device notifications" });
  await expect(notifications).toBeVisible();
  await expect(notifications.getByRole("combobox", { name: "Switch scope" })).toHaveCount(0);
  await expect(notifications.locator("xpath=following-sibling::*[1]")).toHaveClass(/maintenance-settings/);
  const maintenance = page.locator(".maintenance-settings");
  await expect(maintenance.locator(".maintenance-settings__body").getByRole("button", { name: "Discard changes" })).toBeVisible();
  await expect(maintenance.locator(".maintenance-settings__body").getByRole("button", { name: "Save maintenance settings" })).toBeVisible();
  await expect(maintenance.locator(":scope > footer")).toHaveCount(0);
  await notifications.getByLabel("Session notifications").uncheck();
  await notifications.getByRole("button", { name: "Save preferences" }).click();
  const notificationRequest = await rivune.waitForRequest("/api/v1/settings", "PATCH");
  expect(notificationRequest.body).toEqual({ notificationsEnabled: false });
  expect(rivune.requests.filter((request) => request.method === "PATCH" && /^\/api\/v1\/profiles\/[^/]+\/settings$/.test(request.pathname))).toHaveLength(0);

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

  const metadataCard = page.locator(".operation-action-card").filter({ has: page.getByRole("heading", { name: "Fetch missing metadata" }) });
  await expect(metadataCard).toContainText("Refresh all missing metadata in the saved language. Work continues in batches until every payload is attempted.");
  const actionCards = page.locator(".operation-action-card");
  const initialGeometry = await actionCards.evaluateAll((cards) => cards.map((card) => {
    const cardRect = card.getBoundingClientRect();
    const scopeRect = card.querySelector(".operation-action-card__scope")!.getBoundingClientRect();
    const actionRect = card.querySelector(":scope > .button")!.getBoundingClientRect();
    return {
      cardHeight: cardRect.height,
      actionHeight: actionRect.height,
      actionBottom: actionRect.bottom,
      scopeToActionGap: actionRect.top - scopeRect.bottom,
    };
  }));
  expect(initialGeometry).toHaveLength(4);
  expect(initialGeometry[0]!.cardHeight).toBe(initialGeometry[1]!.cardHeight);
  expect(initialGeometry[2]!.cardHeight).toBe(initialGeometry[3]!.cardHeight);
  expect(new Set(initialGeometry.map((geometry) => geometry.actionHeight)).size).toBe(1);
  expect(initialGeometry[0]!.actionBottom).toBe(initialGeometry[1]!.actionBottom);
  expect(initialGeometry[2]!.actionBottom).toBe(initialGeometry[3]!.actionBottom);
  for (const geometry of initialGeometry) {
    expect(geometry.scopeToActionGap).toBeGreaterThanOrEqual(24);
    expect(geometry.scopeToActionGap).toBeLessThanOrEqual(48);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileGeometry = await actionCards.evaluateAll((cards) => cards.map((card) => {
    const cardRect = card.getBoundingClientRect();
    const scopeRect = card.querySelector(".operation-action-card__scope")!.getBoundingClientRect();
    const actionRect = card.querySelector(":scope > .button")!.getBoundingClientRect();
    return { left: cardRect.left, top: cardRect.top, bottom: cardRect.bottom, scopeToActionGap: actionRect.top - scopeRect.bottom };
  }));
  expect(new Set(mobileGeometry.map(({ left }) => Math.round(left))).size).toBe(1);
  for (const geometry of mobileGeometry) expect(geometry.scopeToActionGap).toBe(24);
  for (let index = 1; index < mobileGeometry.length; index += 1) {
    expect(mobileGeometry[index]!.top).toBeGreaterThan(mobileGeometry[index - 1]!.bottom);
  }
  await page.setViewportSize({ width: 1568, height: 1000 });
  await page.getByRole("button", { name: "Run Fetch missing metadata" }).click();
  const fetchRequest = await rivune.waitForRequest("/api/v1/operations/actions/fetch-missing-metadata", "POST");
  expect(fetchRequest.body).toBeUndefined();
  const metadataSuccess = page.locator(".app-notification--success").filter({ hasText: "12 of 12 candidates refreshed; 0 failed." });
  await expect(metadataSuccess).toHaveCount(1);
  await expect(metadataSuccess).toBeVisible();
  await expect(metadataCard.locator(".notice--success")).toHaveCount(0);
  const completedGeometry = await metadataCard.evaluate((card) => {
    const cardRect = card.getBoundingClientRect();
    const actionRect = card.querySelector(":scope > .button")!.getBoundingClientRect();
    return { cardHeight: cardRect.height, actionHeight: actionRect.height };
  });
  expect(completedGeometry).toEqual({
    cardHeight: initialGeometry[0]!.cardHeight,
    actionHeight: initialGeometry[0]!.actionHeight,
  });

  await page.getByRole("button", { name: "Run Run housekeeping" }).click();
  const housekeepingRequest = await rivune.waitForRequest("/api/v1/operations/actions/run-housekeeping", "POST");
  expect(housekeepingRequest.body).toBeUndefined();
  const housekeepingCard = page.locator(".operation-action-card").filter({ has: page.getByRole("heading", { name: "Run housekeeping" }) });
  const housekeepingSuccess = page.locator(".app-notification--success").filter({ hasText: "The maintenance action completed." });
  await expect(housekeepingSuccess).toHaveCount(1);
  await expect(housekeepingCard.locator(".operation-action-card__result.is-succeeded")).toHaveCount(0);
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

  await page.locator('[data-admin-tab="settings"]').click();
  await expect(page.getByRole("heading", { name: "Maintenance mode" })).toHaveCount(0);
  await expect(page.getByLabel("Block member access")).toHaveCount(0);
  await expect(page.getByLabel("Public message")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Device notifications" })).toHaveCount(0);
});

test("metadata refresh keeps successful work and warns with safe failed titles", async ({ page, rivune }) => {
  rivune.queueMetadataOperationResponses({
    status: "partial",
    metadata: { candidates: 100, refreshed: 99, failed: 1, failedTitles: ["L'expresso (bein sport)"] },
  });

  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();
  await page.getByRole("button", { name: "Run Fetch missing metadata" }).click();

  const metadataCard = page.locator(".operation-action-card").filter({ has: page.getByRole("heading", { name: "Fetch missing metadata" }) });
  const outcome = metadataCard.locator(".notice--warning");
  await expect(outcome).toContainText("99 of 100 candidates refreshed; 1 failed.");
  await expect(outcome).toContainText("Not refreshed: L'expresso (bein sport).");
  await expect(outcome).toContainText("Existing metadata was kept");
  const expandedGeometry = await metadataCard.evaluate((card) => {
    const cardRect = card.getBoundingClientRect();
    const feedbackRect = card.querySelector(".operation-action-card__feedback")!.getBoundingClientRect();
    const actionRect = card.querySelector(":scope > .button")!.getBoundingClientRect();
    return {
      feedbackHeight: feedbackRect.height,
      feedbackToActionGap: actionRect.top - feedbackRect.bottom,
      actionBottomInset: cardRect.bottom - actionRect.bottom,
    };
  });
  expect(expandedGeometry.feedbackHeight).toBeGreaterThanOrEqual(44);
  expect(expandedGeometry.feedbackToActionGap).toBeGreaterThanOrEqual(12);
  expect(expandedGeometry.feedbackToActionGap).toBeLessThanOrEqual(13);
  expect(expandedGeometry.actionBottomInset).toBeGreaterThanOrEqual(16);
  await expect(page.locator(".app-notification--warning")).toContainText("Operation partially completed");
  await expect(page.getByRole("heading", { name: "Metadata health" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Run Fetch missing metadata" })).toBeEnabled();
});

test("total metadata failure offers one retry and recovers without exposing diagnostics", async ({ page, rivune }) => {
  const technicalText = {
    providerUrl: "https://api.themoviedb.org/3/tv/86311",
    providerId: "tmdb:86311",
    responseBody: "{\"status_message\":\"Invalid provider identifier\"}",
    stackTrace: "metadata/provider.go:42",
  };
  rivune.queueMetadataOperationResponses(
    {
      status: "failed",
      metadata: { candidates: 100, refreshed: 0, failed: 100, failedTitles: ["L'expresso (bein sport)"] },
      technicalPayload: technicalText,
    },
    {
      status: "succeeded",
      metadata: { candidates: 100, refreshed: 100, failed: 0 },
      delayMilliseconds: 500,
    },
  );

  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();
  await page.getByRole("button", { name: "Run Fetch missing metadata" }).click();

  const metadataCard = page.locator(".operation-action-card").filter({ has: page.getByRole("heading", { name: "Fetch missing metadata" }) });
  const failedOutcome = metadataCard.locator(".notice--error");
  await expect(failedOutcome).toContainText("0 of 100 candidates refreshed; 100 failed.");
  await expect(failedOutcome).toContainText("No metadata was refreshed. Existing metadata was kept.");
  await expect(failedOutcome).toContainText("Not refreshed: L'expresso (bein sport).");
  for (const value of Object.values(technicalText)) await expect(page.getByText(value, { exact: false })).toHaveCount(0);

  const retry = page.getByRole("button", { name: "Retry metadata refresh" });
  await retry.click();
  await expect(retry).toBeDisabled();
  await expect.poll(() => rivune.matching("/api/v1/operations/actions/fetch-missing-metadata", "POST").length).toBe(2);
  await expect(page.locator(".app-notification--success").filter({ hasText: "100 of 100 candidates refreshed; 0 failed." })).toBeVisible();
  await expect(metadataCard.locator(".notice--success")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Retry metadata refresh" })).toHaveCount(0);
  for (const value of Object.values(technicalText)) await expect(page.getByText(value, { exact: false })).toHaveCount(0);
});
