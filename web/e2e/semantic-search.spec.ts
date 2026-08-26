import { expect, test } from "./fixtures/rivune";
import { mergeUniqueMedia } from "../src/pages/Explore";

const result = (metas: unknown[], search: string) => ({
  results: [{
    addonId: "movie-addon",
    manifestId: "movie-manifest",
    resource: "catalog",
    type: "movie",
    id: "movie-search",
    extra: [{ name: "search", value: search }],
    payload: { metas },
  }],
  errors: [],
});

const typedResult = (type: string, metas: unknown[], search: string) => ({
  results: [{
    addonId: `${type}-addon`,
    manifestId: `${type}-manifest`,
    resource: "catalog",
    type,
    id: `${type}-search`,
    extra: [{ name: "search", value: search }],
    payload: { metas },
  }],
  errors: [],
});

const semanticPage = (items: unknown[], page = 1, hasMore = false) => ({
  intents: [{ id: "media_type:movie", kind: "media_type", value: "movie", label: "Movies" }],
  titleQuery: "Dune",
  mediaTypes: ["movie"],
  items,
  page,
  hasMore,
  partial: false,
});

test("semantic search applies residual titles and inferred types, then appends unique discovery results", async ({ page, rivune }) => {
  const directItems = [
    { id: "tt1234567", type: "movie", name: "Addon direct match" },
    ...Array.from({ length: 23 }, (_, index) => ({ id: `direct-${index + 1}`, type: "movie", name: `Direct result ${index + 1}` })),
  ];
  rivune.setSemanticSearchResponse(1, semanticPage([
    { id: "tmdb:42", mediaType: "movie", title: "Semantic duplicate", externalIds: { imdb: "tt1234567", tmdb: "42" }, sources: [] },
    { id: "tmdb:84", mediaType: "movie", title: "Semantic discovery", externalIds: { tmdb: "84" }, sources: [] },
  ], 1, true));
  rivune.setSemanticSearchResponse(2, semanticPage([
    { id: "tmdb:126", mediaType: "movie", title: "Semantic page two", externalIds: { tmdb: "126" }, sources: [] },
  ], 2));
  rivune.setSearchResponse("movie", 0, result(directItems, "Dune"));
  rivune.setSearchResponse("movie", 24, result([], "Dune"));

  await page.goto("/#search");
  await page.locator(".search-page .search-box input").fill("movie Dune from France");

  const movies = page.locator(".search-result-section").filter({ has: page.getByRole("heading", { name: "Movies", exact: true }) });
  await expect(movies.getByRole("button", { name: "Open Addon direct match", exact: true })).toBeVisible();
  await expect(movies.getByRole("button", { name: "Open Semantic duplicate", exact: true })).toHaveCount(0);
  await expect(movies.getByRole("button", { name: "Open Semantic discovery", exact: true })).toBeVisible();
  const orderedTitles = await movies.locator(".media-card").evaluateAll((cards) => cards.map((card) => card.getAttribute("aria-label")));
  expect(orderedTitles.at(0)).toBe("Open Addon direct match");
  expect(orderedTitles.at(-1)).toBe("Open Semantic discovery");

  const semanticRequest = await rivune.waitForRequest("/api/v1/search/semantic", "POST");
  expect(semanticRequest.body).toMatchObject({ query: "movie Dune from France", page: 1, limit: 24, excludedIntentIds: [] });
  const movieRequests = rivune.matching("/api/v1/addons/catalogs/search/movie", "GET");
  expect(movieRequests.find((request) => request.search.get("search") === "Dune")?.search.get("skip")).toBe("0");
  expect(rivune.matching("/api/v1/addons/catalogs/search/series", "GET").some((request) => request.search.get("search") === "movie Dune from France")).toBe(true);
  expect(rivune.matching("/api/v1/addons/catalogs/search/tv", "GET").some((request) => request.search.get("search") === "movie Dune from France")).toBe(true);

  await page.locator(".search-page > .load-more").getByRole("button", { name: "Load more", exact: true }).click();
  await expect(movies.getByRole("button", { name: "Open Semantic page two", exact: true })).toBeVisible();
  const semanticRequests = rivune.matching("/api/v1/search/semantic", "POST");
  expect(semanticRequests).toHaveLength(2);
  expect(semanticRequests[1]?.body).toMatchObject({ page: 2, limit: 24 });
  const pagedAddon = rivune.matching("/api/v1/addons/catalogs/search/movie", "GET").find((request) => request.search.get("skip") === "24" && request.search.get("search") === "Dune");
  expect(pagedAddon).toBeDefined();

  await page.getByRole("button", { name: "Live TV", exact: true }).click();
  await rivune.waitForRequest("/api/v1/addons/catalogs/search/tv", "GET");
  expect(rivune.matching("/api/v1/search/semantic", "POST")).toHaveLength(2);
});

test("publishes fast addon results while semantic classification continues", async ({ page, rivune }) => {
  rivune.setSemanticSearchResponse(1, semanticPage([
    { id: "tmdb:progressive", mediaType: "movie", title: "Semantic later", externalIds: { tmdb: "progressive" }, sources: [] },
  ]), { delay: 3_000 });
  rivune.setSearchResponse("movie", 0, result([
    { id: "progressive-addon", type: "movie", name: "Addon first" },
  ], "progressive"), { delay: 100 });

  await page.goto("/#search");
  const startedAt = Date.now();
  await page.locator(".search-page .search-box input").fill("progressive");
  await expect(page.getByRole("button", { name: "Open Addon first", exact: true })).toBeVisible({ timeout: 2_000 });
  expect(Date.now() - startedAt).toBeLessThan(1_500);
  await expect(page.locator(".search-page .browse-skeleton-grid")).toHaveCount(0);
  await expect(page.locator(".search-page .search-result-groups")).toHaveAttribute("aria-busy", "true");
  await expect(page.getByRole("button", { name: "Open Semantic later", exact: true })).toBeVisible({ timeout: 5_000 });
  await expect(page.locator(".search-page .search-result-groups")).not.toHaveAttribute("aria-busy", "true");
});

test("aborts semantic classification at its deadline and drains search completion", async ({ page, rivune }) => {
  rivune.setSemanticSearchResponse(1, semanticPage([
    { id: "tmdb:too-late", mediaType: "movie", title: "Too late semantic result", externalIds: { tmdb: "too-late" }, sources: [] },
  ]), { delay: 30_000 });
  rivune.setSearchResponse("movie", 0, result([
    { id: "deadline-addon", type: "movie", name: "Deadline addon result" },
  ], "deadline"), { delay: 50 });

  await page.goto("/#search");
  await page.getByRole("button", { name: "Movies", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("deadline");
  await expect(page.getByRole("button", { name: "Open Deadline addon result", exact: true })).toBeVisible({ timeout: 2_000 });
  await expect(page.locator(".search-page .search-result-groups")).not.toHaveAttribute("aria-busy", "true", { timeout: 14_000 });
  await expect(page.getByRole("button", { name: "Open Too late semantic result", exact: true })).toHaveCount(0);
});

test("coalesces inverted multi-type results without reordering focused cards or publishing stale batches", async ({ page, rivune }) => {
  rivune.setSearchResponse("movie", 0, typedResult("movie", [
    { id: "movie-late", type: "movie", name: "Movie arrives last" },
  ], "inverted"), { delay: 650 });
  rivune.setSearchResponse("series", 0, typedResult("series", [
    { id: "series-first", type: "series", name: "Series arrives first" },
  ], "inverted"), { delay: 100 });
  rivune.setSearchResponse("anime", 0, typedResult("anime", [
    { id: "anime-middle", type: "anime", name: "Anime arrives second" },
  ], "inverted"), { delay: 120 });

  await page.goto("/#search");
  await page.evaluate(() => {
    const pageRoot = document.querySelector(".search-page");
    const signatures: string[] = [];
    const observer = new MutationObserver(() => {
      const signature = [...document.querySelectorAll(".search-result-groups .media-card")]
        .map((card) => card.getAttribute("aria-label"))
        .join("|");
      if (signature && signatures.at(-1) !== signature) signatures.push(signature);
    });
    if (pageRoot) observer.observe(pageRoot, { childList: true, subtree: true });
    Object.assign(window, { searchCommitSignatures: signatures, searchCommitObserver: observer });
  });

  await page.locator(".search-page .search-box input").fill("inverted");
  const focusedCard = page.getByRole("button", { name: "Open Series arrives first", exact: true });
  await expect(focusedCard).toBeVisible();
  await focusedCard.focus();
  const focusedNode = await focusedCard.elementHandle();
  await expect(page.getByRole("button", { name: "Open Movie arrives last", exact: true })).toBeVisible();
  await expect(page.locator(".search-page .search-result-groups")).not.toHaveAttribute("aria-busy", "true");

  const headings = await page.locator(".search-result-section > .section-heading h2").allTextContents();
  expect(headings).toEqual(["Movies", "Series", "Anime"]);
  expect(await focusedNode?.evaluate((node) => node.isConnected && document.activeElement === node)).toBe(true);
  const commitCount = await page.evaluate(() => (window as Window & { searchCommitSignatures?: string[] }).searchCommitSignatures?.length ?? 0);
  expect(commitCount).toBeLessThanOrEqual(3);

  await page.getByRole("button", { name: "Movies", exact: true }).click();
  await expect(page.locator(".search-page")).not.toHaveAttribute("aria-busy", "true");
  rivune.setSearchResponse("movie", 0, typedResult("movie", [
    { id: "obsolete-movie", type: "movie", name: "Obsolete movie" },
  ], "stale query"), { delay: 600 });
  await page.locator(".search-page .search-box input").fill("stale query");
  await expect.poll(() => rivune.matching("/api/v1/addons/catalogs/search/movie", "GET").some((request) => request.search.get("search") === "stale query")).toBe(true);
  rivune.setSearchResponse("movie", 0, typedResult("movie", [
    { id: "fresh-movie", type: "movie", name: "Fresh movie" },
  ], "fresh query"), { delay: 50 });
  await page.locator(".search-page .search-box input").fill("fresh query");
  await expect(page.getByRole("button", { name: "Open Fresh movie", exact: true })).toBeVisible();
  await page.waitForTimeout(700);
  await expect(page.getByRole("button", { name: "Open Obsolete movie", exact: true })).toHaveCount(0);
});

test("reconciles provisional semantic sections and locks their controls until terminal commit", async ({ page, rivune }) => {
  rivune.setSemanticSearchResponse(1, {
    ...semanticPage([
      { id: "tmdb:bridge", mediaType: "movie", title: "Semantic duplicate", externalIds: { imdb: "tt7654321", tmdb: "bridge" }, sources: [] },
      { id: "tmdb:only", mediaType: "movie", title: "Semantic only", externalIds: { tmdb: "only" }, sources: [] },
    ]),
    titleQuery: "reconciled",
  }, { delay: 100 });
  rivune.setSearchResponse("movie", 0, result([
    { id: "tt7654321", type: "movie", name: "Addon duplicate" },
  ], "reconciled"), { delay: 700 });

  await page.goto("/#search");
  await page.getByRole("button", { name: "Movies", exact: true }).click();
  await page.locator(".search-page .search-box input").fill("section reconciliation");
  const semanticSection = page.locator(".search-source-section").filter({ has: page.getByRole("heading", { name: "Movies", exact: true }) });
  const provisionalToggle = semanticSection.locator(".search-source-heading__toggle");
  await expect(provisionalToggle).toBeDisabled();
  await provisionalToggle.evaluate((button: HTMLButtonElement) => button.click());
  await expect(provisionalToggle).toHaveAttribute("aria-expanded", "true");

  await expect(page.locator(".search-page")).not.toHaveAttribute("aria-busy", "true");
  await expect(provisionalToggle).toBeEnabled();
  await expect(semanticSection.getByRole("button", { name: "Open Semantic duplicate", exact: true })).toHaveCount(0);
  await expect(semanticSection.getByRole("button", { name: "Open Semantic only", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open Addon duplicate", exact: true })).toBeVisible();
});

test("collapses identities bridged after both representatives were published", async ({ page, rivune }) => {
  rivune.setSemanticSearchResponse(1, semanticPage([
    { id: "tmdb:42", mediaType: "movie", title: "Identity bridge", externalIds: { imdb: "tt4242424", tmdb: "42" }, sources: [] },
  ]), { delay: 600 });
  rivune.setSearchResponse("movie", 0, result([
    { id: "tt4242424", type: "movie", name: "Stable representative" },
    { id: "tmdb:42", type: "movie", name: "Duplicate representative" },
  ], "identity bridge"), { delay: 100 });

  await page.goto("/#search");
  await page.locator(".search-page .search-box input").fill("identity bridge");
  const representative = page.getByRole("button", { name: "Open Stable representative", exact: true });
  await expect(representative).toBeVisible();
  await representative.focus();
  const representativeNode = await representative.elementHandle();
  await expect(page.getByRole("button", { name: "Open Duplicate representative", exact: true })).toBeVisible();
  await expect(page.locator(".search-page")).not.toHaveAttribute("aria-busy", "true");
  await expect(page.getByRole("button", { name: "Open Duplicate representative", exact: true })).toHaveCount(0);
  await expect(page.locator(".search-result-groups .media-card")).toHaveCount(1);
  expect(await representativeNode?.evaluate((node) => node.isConnected && document.activeElement === node)).toBe(true);
});

test("a hanging semantic endpoint does not add addon latency after its deadline", async ({ page, rivune }) => {
  rivune.setSemanticSearchResponse(1, semanticPage([]), { delay: 10_000 });
  rivune.setSearchResponse("movie", 0, result([{ id: "fallback-movie", type: "movie", name: "Ordinary addon fallback" }], "fallback"), { delay: 1_800 });

  await page.goto("/#search");
  const startedAt = Date.now();
  await page.locator(".search-page .search-box input").fill("fallback");
  const [, addonRequest] = await Promise.all([
    rivune.waitForRequest("/api/v1/search/semantic", "POST"),
    rivune.waitForRequest("/api/v1/addons/catalogs/search/movie", "GET"),
  ]);
  expect(Date.now() - startedAt).toBeLessThan(3_000);
  expect(addonRequest.search.get("search")).toBe("fallback");
  await expect(page.getByRole("button", { name: "Open Ordinary addon fallback", exact: true })).toBeVisible({ timeout: 7_500 });
});

test("bounds a large catalog fan-out and exposes partial results", async ({ page, rivune: _rivune }) => {
  const descriptors = Array.from({ length: 256 }, (_, index) => ({
    addonId: `addon-${index}`,
    addonName: `Addon ${index}`,
    manifestId: `manifest-${index}`,
    position: index,
    catalog: { type: `type-${index}`, id: `search-${index}`, name: `Type ${index}`, extra: [{ name: "search" }] },
    addonCatalog: false,
    searchable: true,
  }));
  let active = 0;
  let maximumActive = 0;
  const calls: string[] = [];
  await page.route("**/api/v1/addons/catalogs", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ catalogs: descriptors }) }));
  await page.route(/\/api\/v1\/addons\/catalogs\/search\//, async (route) => {
    calls.push(decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-1) ?? ""));
    active += 1;
    maximumActive = Math.max(maximumActive, active);
    await new Promise((resolve) => setTimeout(resolve, 40));
    active -= 1;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ results: [], errors: [] }) });
  });
  await page.goto("/#search");
  await page.locator(".search-page .search-box input").fill("bounded catalogs");
  await expect.poll(() => calls.length).toBe(16);
  await expect.poll(() => active).toBe(0);
  expect(calls).toEqual(Array.from({ length: 16 }, (_, index) => `type-${index}`));
  expect(maximumActive).toBeLessThanOrEqual(4);
  await expect(page.locator(".search-page .notice--warning")).toContainText("Some sources are temporarily unavailable.");
});
test("aggregates thousands of same-component sources and external IDs in stable order", () => {
  const rootSource = { id: "root", kind: "catalog", title: "Root source", addonId: "root-addon", manifestId: "root-manifest", catalogId: "root-catalog" };
  const representative = {
    id: "tmdb:shared",
    mediaType: "movie" as const,
    title: "Stable representative",
    externalIds: { tmdb: "shared" },
    sources: [rootSource],
  };
  const untouched = {
    id: "tmdb:untouched",
    mediaType: "movie" as const,
    title: "Untouched",
    externalIds: { tmdb: "untouched" },
    sources: [{ id: "untouched", kind: "catalog", title: "Untouched source" }],
  };
  const current = [representative, untouched];
  const duplicateCount = 4_000;
  const duplicates = Array.from({ length: duplicateCount }, (_, index) => ({
    id: `bridge-${index}`,
    mediaType: "movie" as const,
    title: `Duplicate ${index}`,
    externalIds: { tmdb: "shared", shared: `first-wins-${index}`, [`provider-${index}`]: `external-${index}` },
    sources: [
      rootSource,
      { id: `source-${index}`, kind: "catalog", title: `Source ${index}`, addonId: `addon-${index}`, manifestId: `manifest-${index}`, catalogId: `catalog-${index}` },
    ],
  }));

  const merged = mergeUniqueMedia(current, duplicates);

  expect(merged).toHaveLength(2);
  expect(merged[0]?.title).toBe("Stable representative");
  expect(merged[1]).toBe(untouched);
  expect(merged[0]?.sources?.map((source) => source.id)).toEqual([
    "root",
    ...Array.from({ length: duplicateCount }, (_, index) => `source-${index}`),
  ]);
  expect(Object.entries(merged[0]?.externalIds ?? {})).toEqual([
    ["tmdb", "shared"],
    ["shared", "first-wins-0"],
    ...Array.from({ length: duplicateCount }, (_, index) => [`provider-${index}`, `external-${index}`]),
  ]);
  expect(mergeUniqueMedia(current, [{ ...representative }])).toBe(current);

  const authoritative = mergeUniqueMedia([{
    id: "tmdb:authoritative",
    mediaType: "movie",
    title: "Provisional",
    externalIds: { tmdb: "authoritative" },
    sources: [],
  }], [{
    id: "sourced-authoritative",
    mediaType: "movie",
    title: "Authoritative source",
    externalIds: { tmdb: "authoritative", imdb: "tt7654321" },
    sources: [{ id: "authoritative-source", kind: "catalog", title: "Authoritative source" }],
  }]);
  expect(authoritative[0]).toMatchObject({
    id: "sourced-authoritative",
    title: "Authoritative source",
    externalIds: { tmdb: "authoritative", imdb: "tt7654321" },
  });
});
