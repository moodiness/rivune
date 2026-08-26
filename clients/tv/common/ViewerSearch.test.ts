import { afterEach, describe, expect, it, vi } from "vitest";
import { canonicalSearchIdentity, performViewerSearch, type ViewerSearchResult } from "./ViewerSearch";
import type { AddonResourceBatch, SemanticSearchPage, SemanticSearchRequest } from "./types";

function addonBatch(type: string, items: Array<{ id: string; name: string }>): AddonResourceBatch {
  return {
    results: [{
      addonId: "addon-1",
      manifestId: "manifest-1",
      resource: "catalog",
      type,
      id: "search",
      payload: { metas: items.map((item) => ({ ...item, type })) },
      cache: {},
    }],
    errors: [],
  };
}

function semanticPage(overrides: Partial<SemanticSearchPage> = {}): SemanticSearchPage {
  return {
    intents: [{ id: "genre:war", kind: "genre", value: "war", label: "War" }],
    titleQuery: "Dune",
    mediaTypes: ["movie"],
    items: [
      { id: "tmdb:42", mediaType: "movie", title: "Semantic duplicate", externalIds: { tmdb: "42" }, sources: [] },
      { id: "tmdb:84", mediaType: "movie", title: "Semantic unique", externalIds: { tmdb: "84" }, sources: [] },
    ],
    page: 1,
    hasMore: false,
    partial: false,
    ...overrides,
  };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("TV viewer semantic search", () => {
  it("starts configured addon search before semantics, then uses the residual plan and prioritizes direct items", async () => {
    const events: string[] = [];
    const searchAddonCatalogs = vi.fn(async (type: string, query: string) => {
      events.push(`addon:${type}:${query}`);
      return addonBatch(type, [{ id: "tmdb:42", name: "Direct title" }]);
    });
    const semanticSearch = vi.fn(async () => {
      events.push("semantic");
      return semanticPage();
    });

    const result = await performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "war movie Dune",
      configuredTypes: ["movie", "series", "tv"],
      semanticAvailable: true,
      language: "en",
    });

    expect(events.slice(0, 4)).toEqual([
      "addon:movie:war movie Dune",
      "addon:series:war movie Dune",
      "addon:tv:war movie Dune",
      "semantic",
    ]);
    expect(semanticSearch).toHaveBeenCalledWith({
      query: "war movie Dune",
      language: "en",
      page: 1,
      limit: 30,
      excludedIntentIds: [],
    }, expect.any(AbortSignal));
    expect(searchAddonCatalogs).toHaveBeenCalledTimes(4);
    expect(searchAddonCatalogs).toHaveBeenLastCalledWith("movie", "Dune", 0, 30, "en", [], expect.any(AbortSignal));
    expect(result.items.map((item) => item.title)).toEqual(["Direct title", "Semantic unique"]);
    expect(result.intents).toEqual([{ id: "genre:war", kind: "genre", value: "war", label: "War" }]);
    expect(result.partial).toBe(false);
  });

  it("falls back to the original query and configured types when capability is absent", async () => {
    const searchAddonCatalogs = vi.fn(async (type: string, _query: string) => addonBatch(type, []));
    const semanticSearch = vi.fn(async () => semanticPage());

    await performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "  original query  ",
      configuredTypes: ["movie", "series"],
      semanticAvailable: false,
    });

    expect(semanticSearch).not.toHaveBeenCalled();
    expect(searchAddonCatalogs.mock.calls.map(([type, query]) => [type, query])).toEqual([
      ["movie", "original query"],
      ["series", "original query"],
    ]);
  });

  it("falls back after a semantic endpoint error", async () => {
    const searchAddonCatalogs = vi.fn(async (type: string, _query: string) => addonBatch(type, []));
    const semanticSearch = vi.fn(async () => { throw new Error("HTTP 404"); });

    const result = await performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "original query",
      configuredTypes: ["movie", "series"],
      semanticAvailable: true,
    });

    expect(searchAddonCatalogs.mock.calls.map(([type, query]) => [type, query])).toEqual([
      ["movie", "original query"],
      ["series", "original query"],
    ]);
    expect(result.partial).toBe(true);
  });

  it("keeps configured types when semantic types have no configured intersection", async () => {
    const searchAddonCatalogs = vi.fn(async (type: string) => addonBatch(type, []));
    const semanticSearch = vi.fn(async () => semanticPage({ mediaTypes: ["episode"] }));

    await performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "war Dune",
      configuredTypes: ["movie", "series"],
      semanticAvailable: true,
    });

    expect(searchAddonCatalogs.mock.calls.slice(-2).map(([type]) => type)).toEqual(["movie", "series"]);
  });

  it("drains cancelled speculation before starting a changed semantic plan", async () => {
    let drained = 0;
    const searchAddonCatalogs = vi.fn((type: string, query: string, _skip?: number, _limit?: number, _language?: string, _extras?: ReadonlyArray<readonly [string, string]>, signal?: AbortSignal) => {
      if (query === "war Dune") {
        return new Promise<AddonResourceBatch>((_resolve, reject) => {
          signal?.addEventListener("abort", () => {
            queueMicrotask(() => {
              drained += 1;
              reject(new DOMException("Superseded", "AbortError"));
            });
          }, { once: true });
        });
      }
      expect(drained).toBe(2);
      return Promise.resolve(addonBatch(type, []));
    });
    const semanticSearch = vi.fn(async () => semanticPage());

    await performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "war Dune",
      configuredTypes: ["movie", "series"],
      semanticAvailable: true,
    });

    expect(drained).toBe(2);
    expect(searchAddonCatalogs.mock.calls.slice(-1).map(([type, query]) => [type, query])).toEqual([["movie", "Dune"]]);
  });

  it("bounds unresponsive semantic assistance and falls back without waiting for its fetch", async () => {
    vi.useFakeTimers();
    const semanticSignals: AbortSignal[] = [];
    const semanticSearch = vi.fn((_request: SemanticSearchRequest, signal?: AbortSignal) => {
      if (signal) semanticSignals.push(signal);
      return new Promise<SemanticSearchPage>(() => undefined);
    });
    const searchAddonCatalogs = vi.fn(async (type: string) => addonBatch(type, []));

    const pending = performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "original query",
      configuredTypes: ["movie"],
      semanticAvailable: true,
      semanticDeadlineMs: 25,
    });
    expect(searchAddonCatalogs).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(25);
    const result = await pending;

    expect(semanticSignals[0]?.aborted).toBe(true);
    expect(searchAddonCatalogs).toHaveBeenCalledWith("movie", "original query", 0, 30, undefined, [], expect.any(AbortSignal));
    expect(result.partial).toBe(true);
  });

  it("propagates user cancellation after cancelling and draining speculative addons", async () => {
    const semanticSearch = vi.fn(() => new Promise<SemanticSearchPage>(() => undefined));
    const addonSignals: AbortSignal[] = [];
    const searchAddonCatalogs = vi.fn((_type: string, _query: string, _skip?: number, _limit?: number, _language?: string, _extras?: ReadonlyArray<readonly [string, string]>, signal?: AbortSignal) => {
      if (signal) addonSignals.push(signal);
      return new Promise<AddonResourceBatch>((_resolve, reject) => {
        signal?.addEventListener("abort", () => reject(new DOMException("Stopped", "AbortError")), { once: true });
      });
    });
    const controller = new AbortController();
    const pending = performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "Dune",
      configuredTypes: ["movie"],
      semanticAvailable: true,
      signal: controller.signal,
    });

    controller.abort(new DOMException("Stopped", "AbortError"));

    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(searchAddonCatalogs).toHaveBeenCalledTimes(1);
    expect(addonSignals.every((signal) => signal.aborted)).toBe(true);
  });

  it("publishes addon and semantic batches before the search finishes", async () => {
    const updates: string[][] = [];
    let releaseSemantic: ((page: SemanticSearchPage) => void) | undefined;
    const semanticSearch = vi.fn(() => new Promise<SemanticSearchPage>((resolve) => { releaseSemantic = resolve; }));
    let releaseAddon: ((batch: AddonResourceBatch) => void) | undefined;
    const searchAddonCatalogs = vi.fn(() => new Promise<AddonResourceBatch>((resolve) => { releaseAddon = resolve; }));

    const pending = performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "progressive",
      configuredTypes: ["movie"],
      semanticAvailable: true,
      onUpdate: (result) => updates.push(result.items.map((item) => item.title)),
    });
    releaseAddon?.(addonBatch("movie", [{ id: "addon-first", name: "Addon first" }]));
    await vi.waitFor(() => expect(updates).toContainEqual(["Addon first"]));
    releaseSemantic?.(semanticPage({
      titleQuery: "progressive",
      mediaTypes: ["movie"],
      items: [{ id: "tmdb:84", mediaType: "movie", title: "Semantic later", externalIds: { tmdb: "84" }, sources: [] }],
    }));
    await expect(pending).resolves.toMatchObject({
      items: [{ title: "Addon first" }, { title: "Semantic later" }],
    });
    expect(updates.some((titles) => titles.includes("Semantic later"))).toBe(true);
  });

  it("publishes the first content immediately, coalesces later sources for 32 ms, and flushes one terminal snapshot", async () => {
    vi.useFakeTimers();
    const releasedAddons = new Map<string, (batch: AddonResourceBatch) => void>();
    const searchAddonCatalogs = vi.fn((type: string) => new Promise<AddonResourceBatch>((resolve) => releasedAddons.set(type, resolve)));
    let releaseSemantic: ((page: SemanticSearchPage) => void) | undefined;
    const semanticSearch = vi.fn(() => new Promise<SemanticSearchPage>((resolve) => { releaseSemantic = resolve; }));
    const startedAt = Date.now();
    const updates: Array<{ at: number; result: string[] }> = [];

    const pending = performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "progressive",
      configuredTypes: ["movie", "series", "tv"],
      semanticAvailable: true,
      onUpdate: (result) => updates.push({ at: Date.now() - startedAt, result: result.items.map((item) => item.title) }),
    });
    releasedAddons.get("series")?.(addonBatch("series", [{ id: "series-1", name: "Series first" }]));
    await vi.advanceTimersByTimeAsync(0);
    expect(updates).toEqual([{ at: 0, result: ["Series first"] }]);

    await vi.advanceTimersByTimeAsync(16);
    releasedAddons.get("tv")?.(addonBatch("tv", [{ id: "tv-1", name: "TV third" }]));
    await vi.advanceTimersByTimeAsync(15);
    releasedAddons.get("movie")?.(addonBatch("movie", [{ id: "movie-1", name: "Movie second" }]));
    await vi.advanceTimersByTimeAsync(17);
    expect(updates).toEqual([
      { at: 0, result: ["Series first"] },
      { at: 48, result: ["Series first", "Movie second", "TV third"] },
    ]);

    releaseSemantic?.(semanticPage({ titleQuery: "progressive", mediaTypes: [], items: [] }));
    await expect(pending).resolves.toMatchObject({
      items: [{ title: "Series first" }, { title: "Movie second" }, { title: "TV third" }],
    });
    expect(updates).toHaveLength(3);
    expect(updates[2]).toEqual({ at: 48, result: ["Series first", "Movie second", "TV third"] });
    await vi.advanceTimersByTimeAsync(50);
    expect(updates).toHaveLength(3);
  });

  it("never publishes empty content or a scheduled update after cancellation", async () => {
    vi.useFakeTimers();
    const emptyUpdates: ViewerSearchResult[] = [];
    await performViewerSearch({
      semanticSearch: vi.fn(async () => semanticPage({ items: [], intents: [], mediaTypes: [] })),
      searchAddonCatalogs: vi.fn(async (type: string) => addonBatch(type, [])),
    }, {
      query: "empty",
      configuredTypes: ["movie", "series"],
      semanticAvailable: true,
      onUpdate: (result) => emptyUpdates.push(result),
    });
    expect(emptyUpdates).toEqual([]);

    const releases = new Map<string, (batch: AddonResourceBatch) => void>();
    const controller = new AbortController();
    const updates: string[][] = [];
    const pending = performViewerSearch({
      semanticSearch: vi.fn(() => new Promise<SemanticSearchPage>(() => undefined)),
      searchAddonCatalogs: vi.fn((type: string) => new Promise<AddonResourceBatch>((resolve) => releases.set(type, resolve))),
    }, {
      query: "cancel",
      configuredTypes: ["movie", "series"],
      semanticAvailable: true,
      signal: controller.signal,
      onUpdate: (result) => updates.push(result.items.map((item) => item.title)),
    });
    releases.get("movie")?.(addonBatch("movie", [{ id: "movie", name: "Visible" }]));
    await vi.advanceTimersByTimeAsync(0);
    releases.get("series")?.(addonBatch("series", [{ id: "series", name: "Too late" }]));
    await vi.advanceTimersByTimeAsync(0);
    controller.abort(new DOMException("Stopped", "AbortError"));
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    await vi.advanceTimersByTimeAsync(50);
    expect(updates).toEqual([["Visible"]]);
  });

  it("keeps a published representative and position when a later source returns the same identity", async () => {
    const updates: ViewerSearchResult[] = [];
    let releaseSemantic: ((page: SemanticSearchPage) => void) | undefined;
    const semanticSearch = vi.fn(() => new Promise<SemanticSearchPage>((resolve) => { releaseSemantic = resolve; }));
    const searchAddonCatalogs = vi.fn(async (type: string) => addonBatch(type, [{ id: "tmdb:42", name: "Direct representative" }]));
    const pending = performViewerSearch({ semanticSearch, searchAddonCatalogs }, {
      query: "stable",
      configuredTypes: ["movie"],
      semanticAvailable: true,
      onUpdate: (result) => updates.push(result),
    });
    await vi.waitFor(() => expect(updates).toHaveLength(1));
    const representative = updates[0].items[0];
    releaseSemantic?.(semanticPage({
      titleQuery: "stable",
      items: [
        { id: "tmdb:42", mediaType: "movie", title: "Replacement", externalIds: { tmdb: "42" }, sources: [] },
        { id: "tmdb:84", mediaType: "movie", title: "Appended", externalIds: { tmdb: "84" }, sources: [] },
      ],
    }));
    const result = await pending;
    expect(result.items.map((item) => item.title)).toEqual(["Direct representative", "Appended"]);
    expect(result.items[0]).toBe(representative);
    expect(updates[updates.length - 1]?.items[0]).toBe(representative);
  });

  it("deduplicates across every recognized external identity, not only the representative key", async () => {
    const result = await performViewerSearch({
      semanticSearch: vi.fn(async () => semanticPage({
        titleQuery: "aliases",
        mediaTypes: [],
        items: [
          { id: "first", mediaType: "movie", title: "First", externalIds: { imdb: "tt123", tmdb: "42" }, sources: [] },
          { id: "second", mediaType: "movie", title: "Same by TMDB", externalIds: { tmdb: "42" }, sources: [] },
        ],
      })),
      searchAddonCatalogs: vi.fn(),
    }, {
      query: "aliases",
      configuredTypes: [],
      semanticAvailable: true,
    });

    expect(canonicalSearchIdentity(result.items[0])).toBe("movie:imdb:tt123");
    expect(result.items.map((item) => item.title)).toEqual(["First"]);
  });

  it("keeps identical opaque IDs from distinct addon catalogs separate", async () => {
    const searchAddonCatalogs = vi.fn(async (): Promise<AddonResourceBatch> => ({
      results: [
        {
          addonId: "addon-a",
          manifestId: "manifest-a",
          resource: "catalog",
          type: "movie",
          id: "catalog-a",
          payload: { metas: [{ id: "opaque", name: "From A", type: "movie" }] },
          cache: {},
        },
        {
          addonId: "addon-b",
          manifestId: "manifest-b",
          resource: "catalog",
          type: "movie",
          id: "catalog-b",
          payload: { metas: [{ id: "opaque", name: "From B", type: "movie" }] },
          cache: {},
        },
      ],
      errors: [],
    }));
    const result = await performViewerSearch({ semanticSearch: vi.fn(), searchAddonCatalogs }, {
      query: "opaque",
      configuredTypes: ["movie"],
      semanticAvailable: false,
    });

    expect(result.items.map((item) => item.title)).toEqual(["From A", "From B"]);
    expect(result.items.map(canonicalSearchIdentity)).toEqual([
      "movie:addon:addon-a:catalog:catalog-a:id:opaque",
      "movie:addon:addon-b:catalog:catalog-b:id:opaque",
    ]);
  });

  it("scopes fallback identities by media type and never deduplicates on title", () => {
    expect(canonicalSearchIdentity({ id: "same", mediaType: "movie", title: "Same" }))
      .not.toBe(canonicalSearchIdentity({ id: "same", mediaType: "series", title: "Same" }));
    expect(canonicalSearchIdentity({ id: "one", mediaType: "movie", title: "Same" }))
      .not.toBe(canonicalSearchIdentity({ id: "two", mediaType: "movie", title: "Same" }));
  });

  it("stable-deduplicates configured types and caps addon fanout at 16 calls with four active", async () => {
    let active = 0;
    let maximumActive = 0;
    const configuredTypes = ["movie", "series", "movie", ...Array.from({ length: 18 }, (_, index) => `custom-${index}`)];
    const searchAddonCatalogs = vi.fn(async (type: string) => {
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      await Promise.resolve();
      active -= 1;
      return addonBatch(type, []);
    });

    const result = await performViewerSearch({ semanticSearch: vi.fn(), searchAddonCatalogs }, {
      query: "bounded", configuredTypes, semanticAvailable: false,
    });

    expect(searchAddonCatalogs).toHaveBeenCalledTimes(16);
    expect(maximumActive).toBeLessThanOrEqual(4);
    expect(searchAddonCatalogs.mock.calls.map(([type]) => type)).toEqual(["movie", "series", ...Array.from({ length: 14 }, (_, index) => `custom-${index}`)]);
    expect(result.partial).toBe(true);
  });
});
