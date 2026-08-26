import { expect, test } from "./fixtures/rivune";
import { selectOption } from "./helpers/select";

const semanticOperationLocales = [
  {
    language: "de",
    operations: "Betrieb",
    serviceHealth: "Dienstzustand",
    classifierTitle: "Semantischer Klassifikator",
    cacheTitle: "Semantischer Zwischenspeicher und Leistung",
    enabled: "Erweiterung aktiviert",
    enabledStatus: "Aktiviert",
    warmup: "Vorwärmung",
    persistence: "Persistenz",
    executions: "Ausführungen",
    hits: "Treffer",
    ready: "Bereit",
  },
  {
    language: "es",
    operations: "Operaciones",
    serviceHealth: "Estado de los servicios",
    classifierTitle: "Clasificador semántico",
    cacheTitle: "Caché y rendimiento semánticos",
    enabled: "Extensión activada",
    enabledStatus: "Activado",
    warmup: "Precalentamiento",
    persistence: "Persistencia",
    executions: "Ejecuciones",
    hits: "Aciertos",
    ready: "Listo",
  },
  {
    language: "it",
    operations: "Operazioni",
    serviceHealth: "Stato dei servizi",
    classifierTitle: "Classificatore semantico",
    cacheTitle: "Cache e prestazioni semantiche",
    enabled: "Estensione attivata",
    enabledStatus: "Attivato",
    warmup: "Preriscaldamento",
    persistence: "Persistenza",
    executions: "Esecuzioni",
    hits: "Elementi trovati in cache",
    ready: "Pronto",
  },
  {
    language: "pt-BR",
    operations: "Operações",
    serviceHealth: "Integridade dos serviços",
    classifierTitle: "Classificador semântico",
    cacheTitle: "Cache e desempenho semânticos",
    enabled: "Extensão ativada",
    enabledStatus: "Ativado",
    warmup: "Pré-aquecimento",
    persistence: "Persistência",
    executions: "Execuções",
    hits: "Acertos",
    ready: "Pronto",
  },
] as const;

const englishSemanticFallback = /\b(?:Semantic classifier|Semantic cache and performance|Extension enabled|Warmup|Persistence|Active|Queued|Executions|Successes|Timeouts|Failures|Cancellations|Busy fallbacks|Memory entries|Persistent entries|Hits|Misses|Coalesced waiters|p50 latency|p95 latency|Enabled|Disabled|Pending|Ready|Failed)\b/i;

test("Operations resource health follows the selected locale", async ({ page, rivune }) => {
  rivune.setInterfaceLanguage("fr");
  await page.goto("/#admin");
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await page.getByRole("button", { name: /Opérations/ }).click();
  const serviceHealth = page.getByRole("region", { name: "État du service" });
  await expect(serviceHealth.getByRole("heading", { name: "Pool de connexions PostgreSQL" })).toBeVisible();
  await expect(serviceHealth).toContainText("Temps d’attente");
  await expect(serviceHealth).toContainText("Boîte d’envoi du suivi");
  await expect(serviceHealth).toContainText("Ancienneté de la dernière mise à jour");
  await expect(serviceHealth).toContainText("Transcodage");
  await expect(serviceHealth).not.toContainText("Service health");
  await expect(serviceHealth).not.toContainText("Tracking outbox");
  const semanticClassifier = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Classifieur sémantique" }) });
  await expect(semanticClassifier.locator("dt")).toHaveText(["Extension activée", "Préchauffage", "Persistance", "Actives", "En file d’attente", "Exécutions", "Réussites", "Délais dépassés", "Échecs", "Annulations", "Replis pour saturation"]);
  await expect(semanticClassifier.locator("dd").nth(0)).toHaveText("Activé");
  await expect(semanticClassifier.locator("dd").nth(1)).toHaveText("Prêt");
  await expect(semanticClassifier.locator("dd").nth(2)).toHaveText("Prêt");
  const semanticCache = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Cache et performances sémantiques" }) });
  await expect(semanticCache.locator("dt")).toHaveText(["Entrées en mémoire", "Entrées persistantes", "Résultats en cache", "Absences du cache", "Attentes regroupées", "Latence p50", "Latence p95"]);
  await expect(semanticClassifier).not.toContainText(/\b(request|model|key|digest|error|requête|modèle|clé|empreinte|erreur)\b/i);
  await expect(semanticCache).not.toContainText(/\b(request|model|key|digest|error|requête|modèle|clé|empreinte|erreur)\b/i);
});

for (const locale of semanticOperationLocales) {
  test(`Semantic Operations metrics are localized in ${locale.language}`, async ({ page, rivune }) => {
    rivune.setInterfaceLanguage(locale.language);
    await page.goto("/#admin");
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    await page.getByRole("button", { name: locale.operations }).click();

    const serviceHealth = page.getByRole("region", { name: locale.serviceHealth });
    const semanticClassifier = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: locale.classifierTitle }) });
    const semanticCache = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: locale.cacheTitle }) });
    await expect(semanticClassifier.getByRole("heading")).toHaveText(locale.classifierTitle);
    await expect(semanticCache.getByRole("heading")).toHaveText(locale.cacheTitle);
    await expect(semanticClassifier.locator("dl > div").filter({ hasText: locale.enabled })).toHaveText(`${locale.enabled}${locale.enabledStatus}`);
    await expect(semanticClassifier.locator("dl > div").filter({ hasText: locale.warmup })).toHaveText(`${locale.warmup}${locale.ready}`);
    await expect(semanticClassifier.locator("dl > div").filter({ hasText: locale.persistence })).toHaveText(`${locale.persistence}${locale.ready}`);
    await expect(semanticClassifier.locator("dl > div").filter({ hasText: locale.executions })).toHaveText(`${locale.executions}901`);
    await expect(semanticCache.locator("dl > div").filter({ hasText: locale.hits })).toHaveText(`${locale.hits}845`);
    await expect(semanticClassifier).not.toContainText(englishSemanticFallback);
    await expect(semanticCache).not.toContainText(englishSemanticFallback);
  });
}

test("administrator monitors operations and runs fixed maintenance controls", async ({ page, rivune }) => {
  await page.setViewportSize({ width: 1568, height: 1000 });
  const operationsResponse = page.waitForResponse((response) => response.request().method() === "GET" && new URL(response.url()).pathname === "/api/v1/operations");
  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();
  const operationsPayload = await (await operationsResponse).json() as { semanticExtension: Record<string, unknown> };
  expect(Object.keys(operationsPayload.semanticExtension).sort()).toEqual([
    "active",
    "busyFallbacks",
    "coalescedWaiters",
    "enabled",
    "executions",
    "failures",
    "cancellations",
    "hits",
    "latencyP50Milliseconds",
    "latencyP95Milliseconds",
    "memoryEntries",
    "misses",
    "persistentEntries",
    "persistentStatus",
    "queued",
    "successes",
    "timeouts",
    "warmupStatus",
  ].sort());

  await expect(page.getByRole("heading", { name: "Metadata health" })).toBeVisible();
  const metadataMetrics = page.getByLabel("Metadata cache metrics");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Payload entries" })).toContainText("48");
  await expect(metadataMetrics.locator(".operation-metric").filter({ hasText: "Missing payloads" })).toContainText("12");
  const streamMetrics = page.getByLabel("Live stream activity metrics");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Active sessions" })).toContainText("2");
  await expect(streamMetrics.locator(".operation-metric").filter({ hasText: "Temporary media" })).toContainText("12 MB");
  const serviceHealth = page.getByRole("region", { name: "Service health" });
  const databaseAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "PostgreSQL pool" }) });
  await expect(databaseAggregate.locator("dl > div")).toHaveText(["Acquired2", "Idle3", "Total5", "Maximum10", "Waits7", "Wait time145 ms"]);
  const trackingAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Tracking outbox" }) });
  await expect(trackingAggregate.locator("dl > div")).toHaveText(["Pending12", "Due3", "Oldest age7 min"]);
  const addonAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Addons", exact: true }) });
  await expect(addonAggregate.locator("dl > div").nth(0)).toHaveText("Installed8");
  await expect(addonAggregate.locator("dl > div").nth(1)).toHaveText("Enabled7");
  await expect(addonAggregate.locator("dl > div").nth(2)).toHaveText(/Latest update age.*\d/);
  const playbackAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Playback", exact: true }) });
  await expect(playbackAggregate.locator("dl > div")).toHaveText(["Active4", "Transcoding2"]);
  const semanticClassifierAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Semantic classifier" }) });
  await expect(semanticClassifierAggregate.locator("dl > div")).toHaveText(["Extension enabledEnabled", "WarmupReady", "PersistenceReady", "Active2", "Queued4", "Executions901", "Successes867", "Timeouts9", "Failures5", "Cancellations20", "Busy fallbacks20"]);
  const semanticCacheAggregate = serviceHealth.locator(".operation-aggregate").filter({ has: page.getByRole("heading", { name: "Semantic cache and performance" }) });
  await expect(semanticCacheAggregate.locator("dl > div")).toHaveText(["Memory entries148", "Persistent entries1,205", "Hits845", "Misses37", "Coalesced waiters19", "p50 latency18 ms", "p95 latency74 ms"]);
  await expect(semanticClassifierAggregate).not.toContainText(/\b(request|model|key|digest|error)\b/i);
  await expect(semanticCacheAggregate).not.toContainText(/\b(request|model|key|digest|error)\b/i);

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
  await selectOption(page.getByLabel("Refresh interval"), "12");
  await page.getByLabel("Metadata language").fill("fr-CA");
  await page.getByLabel("Batch size").fill("40");
  await page.getByRole("button", { name: "Save refresh schedule" }).click();

  const scheduleRequest = await rivune.waitForRequest("/api/v1/operations/schedules/metadata-refresh", "PUT");
  expect(scheduleRequest.body).toEqual({ enabled: true, intervalHours: 12, language: "fr-CA", batchSize: 40 });
  await expect(page.getByText("The metadata refresh schedule was updated.")).toBeVisible();

  const overviewRequests = rivune.matching("/api/v1/operations", "GET").length;
  await page.getByRole("button", { name: "Refresh Operations", exact: true }).click();
  await expect.poll(() => rivune.matching("/api/v1/operations", "GET").length).toBeGreaterThan(overviewRequests);
  await expect(page.getByLabel("Run scheduled refreshes")).toBeChecked();
  await expect(page.getByLabel("Refresh interval")).toHaveAttribute("data-value", "12");
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
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect.poll(async () => serviceHealth.locator(".operation-aggregate").evaluateAll((aggregates) => aggregates.every((aggregate) => {
    const bounds = aggregate.getBoundingClientRect();
    return bounds.left >= 0 && bounds.right <= window.innerWidth;
  }))).toBe(true);
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
    metadata: { candidates: 100, refreshed: 99, failed: 1, failedTitles: ["Fixture Movie"] },
  });

  await page.goto("/#admin");
  await page.getByRole("button", { name: /Operations/ }).click();
  await page.getByRole("button", { name: "Run Fetch missing metadata" }).click();

  const metadataCard = page.locator(".operation-action-card").filter({ has: page.getByRole("heading", { name: "Fetch missing metadata" }) });
  const outcome = metadataCard.locator(".notice--warning");
  await expect(outcome).toContainText("99 of 100 candidates refreshed; 1 failed.");
  await expect(outcome).toContainText("Not refreshed: Fixture Movie.");
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
      metadata: { candidates: 100, refreshed: 0, failed: 100, failedTitles: ["Fixture Movie"] },
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
  await expect(failedOutcome).toContainText("Not refreshed: Fixture Movie.");
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
