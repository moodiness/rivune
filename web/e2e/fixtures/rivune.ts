import { expect, test as base } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

export type CapturedRequest = {
  method: string;
  pathname: string;
  search: URLSearchParams;
  body: unknown;
  profileId: string | null;
};

type Profile = {
  id: string;
  name: string;
  isChild: boolean;
  canManage: boolean;
  enabled: boolean;
  availableFrom: null;
  availableUntil: null;
  accessStartTime: null;
  accessEndTime: null;
  accessTimezone: string;
  accessible: boolean;
  avatar: { kind: "preset"; presetId: string; url: string };
};

const expiresAt = "2099-01-01T00:00:00Z";
const createdAt = "2024-01-01T00:00:00Z";
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="100%" height="100%" fill="#241f35"/></svg>`;

function wait(milliseconds: number): Promise<void> {
  const { promise, resolve } = Promise.withResolvers<void>();
  setTimeout(resolve, milliseconds);
  return promise;
}

const profiles: Profile[] = [
  { id: "alice", name: "Alice", isChild: false, canManage: true, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "alice", url: "https://fixtures.rivune.test/alice.svg" } },
  { id: "bob", name: "Bob", isChild: false, canManage: false, enabled: true, availableFrom: null, availableUntil: null, accessStartTime: null, accessEndTime: null, accessTimezone: "UTC", accessible: true, avatar: { kind: "preset", presetId: "bob", url: "https://fixtures.rivune.test/bob.svg" } },
];

const seasonOne = {
  id: "season-1", mediaType: "season", seriesId: "series-1", name: "Season 1", overview: "The first voyage.", seasonNumber: 1, episodeCount: 2, airDate: "2024-01-01", posterUrl: "https://fixtures.rivune.test/season-1.svg", voteAverage: 8.2, externalIds: { tmdb: "101" },
  episodes: [
    { id: "episode-1", mediaType: "episode", seasonId: "season-1", name: "First Light", overview: "The crew follows a mysterious signal.", seasonNumber: 1, episodeNumber: 1, airDate: "2024-01-03", stillUrl: "https://fixtures.rivune.test/episode-1.svg", runtimeMinutes: 30, voteAverage: 8.1, voteCount: 100, externalIds: { imdb: "tt900001" } },
    { id: "episode-2", mediaType: "episode", seasonId: "season-1", name: "Second Orbit", overview: "A new course changes everything.", seasonNumber: 1, episodeNumber: 2, airDate: "2024-01-10", stillUrl: "https://fixtures.rivune.test/episode-2.svg", runtimeMinutes: 31, voteAverage: 8.3, voteCount: 95, externalIds: { imdb: "tt900002" } },
  ],
};

const seasonTwo = {
  id: "season-2", mediaType: "season", seriesId: "series-1", name: "Season 2", overview: "The second voyage.", seasonNumber: 2, episodeCount: 1, airDate: "2024-06-01", posterUrl: "https://fixtures.rivune.test/season-2.svg", voteAverage: 8.6, externalIds: { tmdb: "102" },
  episodes: [
    { id: "episode-3", mediaType: "episode", seasonId: "season-2", name: "Moonrise", overview: "The team reunites on a distant moon.", seasonNumber: 2, episodeNumber: 1, airDate: "2024-06-01", stillUrl: "https://fixtures.rivune.test/episode-3.svg", runtimeMinutes: 34, voteAverage: 8.7, voteCount: 88, externalIds: { imdb: "tt900003" } },
  ],
};

const dvdSeason = {
  id: "dvd-season-1", mediaType: "season", seriesId: "series-1", name: "DVD Season 1", overview: "The disc order.", seasonNumber: 1, episodeCount: 3, airDate: "2024-01-01", posterUrl: "https://fixtures.rivune.test/dvd-season-1.svg", voteAverage: 8.4, externalIds: { tvdb: "2001" },
  episodes: [
    { id: "dvd-episode-1", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Opening", overview: "The DVD order begins.", seasonNumber: 1, episodeNumber: 1, airDate: "2024-01-03", runtimeMinutes: 30, voteAverage: 8.1, voteCount: 100, externalIds: { tvdb: "2101" } },
    { id: "dvd-episode-2", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Middle", overview: "The DVD order continues.", seasonNumber: 1, episodeNumber: 2, airDate: "2024-01-10", runtimeMinutes: 31, voteAverage: 8.3, voteCount: 95, externalIds: { tvdb: "2102" } },
    { id: "dvd-episode-3", mediaType: "episode", seasonId: "dvd-season-1", name: "Disc Finale", overview: "The DVD order concludes.", seasonNumber: 1, episodeNumber: 3, airDate: "2024-01-17", runtimeMinutes: 32, voteAverage: 8.4, voteCount: 90, externalIds: { tvdb: "2103" } },
  ],
};

const seasonSummary = <T extends { episodes: unknown[] }>(season: T) => {
  const { episodes: _episodes, ...summary } = season;
  return summary;
};

const extraSeasonSummaries = Array.from({ length: 10 }, (_, index) => {
  const seasonNumber = index + 3;
  return {
    ...seasonSummary(seasonTwo),
    id: `season-${seasonNumber}`,
    name: `Season ${seasonNumber}`,
    seasonNumber,
    posterUrl: `https://fixtures.rivune.test/season-${seasonNumber}.svg`,
    externalIds: { tmdb: String(100 + seasonNumber) },
  };
});

const episodeOrders = [
  { id: "1", name: "Aired Order", type: "official", isDefault: true },
  { id: "2", name: "DVD Order", type: "dvd", isDefault: false },
  { id: "3", name: "Absolute Order", type: "absolute", isDefault: false },
  { id: "4", name: "Story Order", type: "alternate", isDefault: false },
  { id: "7", name: "Streaming Order", type: "alttwo", isDefault: false },
];

const series = {
  id: "series-1",
  mediaType: "series",
  name: "Signal Horizon",
  originalName: "Signal Horizon",
  originalLanguage: "en",
  overview: "Explorers cross the edge of known space.",
  firstAirDate: "2024-01-03",
  posterUrl: "https://fixtures.rivune.test/series.svg",
  backdropUrl: "https://fixtures.rivune.test/backdrop.svg",
  logoUrl: "https://fixtures.rivune.test/series-logo.svg",
  tagline: "Beyond the map.",
  status: "Returning Series",
  numberOfSeasons: 12,
  numberOfEpisodes: 13,
  genres: [{ id: 1, name: "Science Fiction" }],
  voteAverage: 8.5,
  voteCount: 500,
  cast: [
    { id: "101", name: "Avery Stone", character: "Commander Ilya Voss", profileUrl: "https://fixtures.rivune.test/cast-1.svg" },
    { id: "102", name: "Mina Park", character: "Dr. Sera Vale", profileUrl: "https://fixtures.rivune.test/cast-2.svg" },
    { id: "103", name: "Omar Reed", character: "Elias Ward", profileUrl: "https://fixtures.rivune.test/cast-3.svg" },
    { id: "104", name: "Lucia Chen", character: "Captain Nia Sol", profileUrl: "https://fixtures.rivune.test/cast-4.svg" },
    { id: "105", name: "Noah Bennett", character: "Theo Quinn", profileUrl: "https://fixtures.rivune.test/cast-5.svg" },
  ],
  seasons: [seasonSummary(seasonOne), seasonSummary(seasonTwo), ...extraSeasonSummaries],
  episodeOrders,
  mappingProvider: "tmdb",
  externalIds: { imdb: "tt9000", tmdb: "9000", tvdb: "9900" },
};

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

function collection(profileId: string) {
  const name = profileId === "bob" ? "Bob's Fresh Picks" : "Alice's Slow Shelf";
  return {
    id: `${profileId}-collection`, title: name, heroEnabled: false, pinToTop: false, focusGlowEnabled: false, viewMode: "follow_layout", folderCoverShape: "poster",
    folders: [{ id: `${profileId}-folder`, title: name, tileShape: "poster", focusGifEnabled: false, hideTitle: false, sources: [] }],
    profileIds: [profileId], position: 0, version: 1, createdAt, updatedAt: createdAt,
  };
}

export class RivuneHarness {
  readonly requests: CapturedRequest[] = [];
  readonly collectionResponses: string[] = [];
  private activeProfileId: string | null = "alice";
  private userRole: "admin" | "member" = "admin";
  private maintenance = { enabled: false, message: null as string | null };
  private instanceSettings: Record<string, unknown> = {};
  private readonly profileSettings = new Map<string, Record<string, unknown>>();
  private readonly effectiveSettingsDelays: number[] = [];
  private readonly collectionDelays = new Map<string, number>();
  private readonly collectionFolders = new Map<string, Array<{ id: string; title: string }>>();
  private readonly folderDelays = new Map<string, number>();
  private readonly seasonOverrides = new Map<string, unknown>();
  private libraryItems: Array<Record<string, unknown>> = [];
  private operations = {
    metadataCache: {
      entries: 48,
      freshEntries: 41,
      expiredEntries: 7,
      rootTitles: 32,
      missingTitles: 12,
      artworkSnapshots: 29,
    },
    metadataRefresh: {
      task: "metadata-refresh" as const,
      enabled: false,
      intervalHours: 24,
      language: "en",
      batchSize: 25,
      nextRunAt: null as string | null,
      lastStartedAt: null as string | null,
      lastCompletedAt: null as string | null,
      lastStatus: null as "succeeded" | "partial" | "failed" | null,
      lastResult: null as { candidates: number; refreshed: number; failed: number } | null,
    },
    housekeepingIntervalMinutes: 15,
  };
  private playbackActivity = {
    summary: {
      activeSessions: 2,
      activeJobs: 1,
      processingSlots: 1,
      processingLimit: 3,
      storageBytes: 12_582_912,
      storageLimitBytes: 1_073_741_824,
    },
    diagnostics: { videoEncoder: "h264", hardwareToneMap: false },
    sessions: [],
    jobs: [],
  };

  setMaintenance(enabled: boolean, message: string | null = null) {
    if (enabled) this.activeProfileId = "bob";
    this.maintenance = { enabled, message };
  }

  delayCollections(profileId: string, milliseconds: number) {
    this.collectionDelays.set(profileId, milliseconds);
  }

  setCollectionFolders(profileId: string, folders: Array<{ id: string; title: string }>) {
    this.collectionFolders.set(profileId, folders.map((folder) => ({ ...folder })));
  }

  delayFolder(folderId: string, milliseconds: number) {
    this.folderDelays.set(folderId, milliseconds);
  }

  private collectionFor(profileId: string) {
    const value = collection(profileId);
    const configured = this.collectionFolders.get(profileId);
    if (configured) {
      value.folders = configured.map((folder) => ({ ...value.folders[0], ...folder }));
    }
    return value;
  }

  delayNextEffectiveSettings(milliseconds: number) {
    this.effectiveSettingsDelays.push(milliseconds);
  }

  setSeason(id: string, season: unknown) {
    this.seasonOverrides.set(id, season);
  }

  setLibraryItems(items: Array<Record<string, unknown>>) {
    this.libraryItems = items;
  }

  matching(pathname: string, method?: string) {
    return this.requests.filter((request) => request.pathname === pathname && (!method || request.method === method));
  }

  async waitForRequest(pathname: string, method?: string) {
    await expect.poll(() => this.matching(pathname, method).length).toBeGreaterThan(0);
    return this.matching(pathname, method).at(-1)!;
  }

  async install(page: Page) {
    await page.route("**/*", (route) => this.handle(route));
    await page.goto("/__e2e_seed__");
    await page.evaluate(() => {
      localStorage.setItem("rivune.refresh", "fixture-refresh");
      localStorage.setItem("rivune.device", "fixture-device");
    });
  }

  private account() {
    return {
      user: { id: "user-1", username: "fixture-owner", role: this.userRole },
      session: { id: "session-1", deviceId: "fixture-device", activeProfile: this.activeProfileId ? { id: this.activeProfileId, expiresAt } : null },
      profiles,
      maintenance: this.maintenance,
    };
  }

  private async handle(route: Route) {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/__e2e_seed__") {
      await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>Rivune E2E seed</title>" });
      return;
    }
    if (url.hostname === "fixtures.rivune.test") {
      const contentType = url.pathname.endsWith(".vtt") ? "text/vtt" : url.pathname.endsWith(".mp4") ? "video/mp4" : "image/svg+xml";
      await route.fulfill({ status: 200, contentType, body: contentType === "text/vtt" ? "WEBVTT\n\n00:00.000 --> 00:02.000\nFixture caption" : contentType === "video/mp4" ? "" : svg });
      return;
    }
    if (url.hostname === "www.youtube-nocookie.com") {
      await route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>Fixture trailer</title>" });
      return;
    }
    if (!url.pathname.startsWith("/api/v1") && url.pathname !== "/.well-known/rivune") {
      await route.continue();
      return;
    }

    let body: unknown;
    try { body = request.postData() ? request.postDataJSON() : undefined; } catch { body = request.postData(); }
    const profileAtRequest = this.activeProfileId;
    this.requests.push({ method: request.method(), pathname: url.pathname, search: new URLSearchParams(url.search), body, profileId: profileAtRequest });

    if (url.pathname === "/.well-known/rivune") {
      await json(route, { name: "Rivune E2E", serverVersion: "1.2.3", protocolVersion: 17, apiBaseUrl: "/api/v1", setupRequired: false, timezone: "UTC", interfaceLanguage: typeof this.instanceSettings.interfaceLanguage === "string" ? this.instanceSettings.interfaceLanguage : "en" });
      return;
    }
    const path = url.pathname.slice("/api/v1".length);
    const maintenanceSelection = path.match(/^\/profiles\/([^/]+)\/select$/);
    const maintenanceExempt = path === "/auth/refresh" || path === "/auth/me" || path === "/auth/logout" ||
      path === "/profiles" || path === "/profiles/selection" || maintenanceSelection !== null;
    const activeProfile = profiles.find((profile) => profile.id === this.activeProfileId);
    if (this.maintenance.enabled && !activeProfile?.canManage && !maintenanceExempt) {
      await json(route, { error: { code: "maintenance_mode", message: "Rivune is temporarily unavailable for maintenance.", ...(this.maintenance.message ? { publicMessage: this.maintenance.message } : {}) } }, 503);
      return;
    }
    if (path === "/auth/refresh" && request.method() === "POST") {
      await json(route, { tokenType: "Bearer", accessToken: "fixture-access", accessTokenExpiresAt: expiresAt, refreshToken: "fixture-refresh", refreshTokenExpiresAt: expiresAt, sessionId: "session-1", deviceId: "fixture-device" });
      return;
    }
    if (path === "/auth/me") { await json(route, this.account()); return; }
    if (path === "/settings/maintenance" && request.method() === "GET") { await json(route, this.maintenance); return; }
    if (path === "/settings/maintenance" && request.method() === "PUT") {
      this.maintenance = body as { enabled: boolean; message: string | null };
      await json(route, this.maintenance);
      return;
    }
    if (path === "/operations" && request.method() === "GET") {
      await json(route, this.operations);
      return;
    }
    if (path === "/operations/schedules/metadata-refresh" && request.method() === "PUT") {
      const input = body as { enabled: boolean; intervalHours: number; language: string; batchSize: number };
      this.operations.metadataRefresh = {
        ...this.operations.metadataRefresh,
        ...input,
        nextRunAt: input.enabled ? "2026-08-03T00:00:00Z" : null,
      };
      await json(route, this.operations.metadataRefresh);
      return;
    }
    const operationAction = path.match(/^\/operations\/actions\/(fetch-missing-metadata|run-housekeeping|clear-metadata-cache|clear-stream-cache)$/);
    if (operationAction && request.method() === "POST") {
      const action = operationAction[1];
      let result: Record<string, unknown> = {};
      if (action === "fetch-missing-metadata") {
        result = { metadata: { candidates: 12, refreshed: 9, failed: 3 } };
      } else if (action === "clear-metadata-cache") {
        result = { metadataCache: { entriesDeleted: this.operations.metadataCache.entries } };
        this.operations.metadataCache = { ...this.operations.metadataCache, entries: 0, freshEntries: 0, expiredEntries: 0 };
      } else if (action === "clear-stream-cache") {
        const { activeSessions: sessionsRemoved, activeJobs: jobsStopped, storageBytes } = this.playbackActivity.summary;
        result = { playback: { sessionsRemoved, jobsStopped, storageBytes } };
        this.playbackActivity.summary = {
          ...this.playbackActivity.summary,
          activeSessions: 0,
          activeJobs: 0,
          processingSlots: 0,
          storageBytes: 0,
        };
      }
      await json(route, { action, startedAt: createdAt, completedAt: createdAt, status: "succeeded", result });
      return;
    }
    if (path === "/playback/activity" && request.method() === "GET") {
      await json(route, this.playbackActivity);
      return;
    }
    if (path === "/profiles" && request.method() === "GET") { await json(route, { profiles }); return; }
    if (path === "/profile-avatars" && request.method() === "GET") { await json(route, { presets: [] }); return; }
    if (path === "/settings" && request.method() === "GET") { await json(route, { schemaVersion: 1, settings: this.instanceSettings, updatedAt: createdAt }); return; }
    if (path === "/settings" && request.method() === "PATCH") {
      this.instanceSettings = { ...this.instanceSettings, ...(body as Record<string, unknown>) };
      await json(route, { schemaVersion: 1, settings: this.instanceSettings, updatedAt: createdAt });
      return;
    }
    const profileSettings = path.match(/^\/profiles\/([^/]+)\/settings$/);
    if (profileSettings && request.method() === "GET") { await json(route, { schemaVersion: 1, settings: this.profileSettings.get(profileSettings[1]) ?? {}, updatedAt: createdAt }); return; }
    if (profileSettings && request.method() === "PATCH") {
      const next = { ...(this.profileSettings.get(profileSettings[1]) ?? {}), ...(body as Record<string, unknown>) };
      this.profileSettings.set(profileSettings[1], next);
      await json(route, { schemaVersion: 1, settings: next, updatedAt: createdAt });
      return;
    }
    if (path === "/profiles/selection" && request.method() === "DELETE") { this.activeProfileId = null; await route.fulfill({ status: 204 }); return; }
    const profileSelection = path.match(/^\/profiles\/([^/]+)\/select$/);
    if (profileSelection && request.method() === "POST") {
      const selected = profiles.find((profile) => profile.id === profileSelection[1]);
      if (!selected) { await json(route, { error: { code: "not_found", message: "Profile not found" } }, 404); return; }
      if (this.maintenance.enabled && !selected.canManage) {
        await json(route, { error: { code: "maintenance_mode", message: "Rivune is temporarily unavailable for maintenance.", ...(this.maintenance.message ? { publicMessage: this.maintenance.message } : {}) } }, 503);
        return;
      }
      this.activeProfileId = selected.id;
      await json(route, { profile: selected, expiresAt });
      return;
    }
    const effectiveSettings = path.match(/^\/profiles\/([^/]+)\/settings\/effective$/);
    if (effectiveSettings) {
      const profileValues = this.profileSettings.get(effectiveSettings[1]) ?? {};
      const profileLanguage = profileValues.interfaceLanguage;
      const instanceLanguage = this.instanceSettings.interfaceLanguage;
      const interfaceLanguage = typeof profileLanguage === "string"
        ? profileLanguage
        : typeof instanceLanguage === "string" ? instanceLanguage : "en";
      const responseDelay = this.effectiveSettingsDelays.shift() ?? 0;
      if (responseDelay > 0) await wait(responseDelay);
      await json(route, { schemaVersion: 1, settings: { interfaceLanguage, autoplayNextEpisode: true, animationsEnabled: false, notificationsEnabled: false, metadataLanguage: "en-US", metadataRegion: "US", audioLanguage: "en", subtitleLanguage: "en" }, sources: { interfaceLanguage: typeof profileLanguage === "string" ? "profile" : typeof instanceLanguage === "string" ? "instance" : "default" } });
      return;
    }
    if (path === "/auth/notifications") { await json(route, { notifications: [] }); return; }
    if (path === "/auth/notifications/broadcast" && request.method() === "POST") {
      const input = body as { idempotencyKey: string; message: string };
      await json(route, { id: input.idempotencyKey, message: input.message, senderUsername: "fixture-owner", recipientCount: 3, createdAt }, 201);
      return;
    }
    if (path === "/collections") {
      const collectionProfile = profileAtRequest ?? "alice";
      const delay = this.collectionDelays.get(collectionProfile) ?? 0;
      if (delay > 0) await wait(delay);
      await json(route, { collections: [this.collectionFor(collectionProfile)] });
      this.collectionResponses.push(collectionProfile);
      return;
    }
    const folderItems = path.match(/^\/collections\/([^/]+)\/folders\/([^/]+)\/items$/);
    if (folderItems) {
      const collectionProfile = folderItems[1].split("-")[0];
      const folderID = folderItems[2];
      const responseDelay = this.folderDelays.get(folderID) ?? 0;
      if (responseDelay > 0) await wait(responseDelay);
      const resolvedCollection = this.collectionFor(collectionProfile);
      const folder = resolvedCollection.folders.find((candidate) => candidate.id === folderID) ?? resolvedCollection.folders[0];
      const configured = this.collectionFolders.has(collectionProfile);
      const title = configured ? `${folder.title} Exclusive` : collectionProfile === "bob" ? "Bob Exclusive" : "Alice Exclusive";
      const itemID = configured ? `${collectionProfile}-${folderID}-exclusive` : `${collectionProfile}-exclusive`;
      const posterURL = configured ? `/api/v1/artwork/${itemID}` : `https://fixtures.rivune.test/${itemID}.svg`;
      await json(route, { collectionId: folderItems[1], folder, items: [{ id: itemID, mediaType: "movie", title, posterUrl: posterURL, description: `${title} fixture` }], page: 1, hasMore: false, errors: [] });
      return;
    }
    if (path === "/continue-watching") {
      const items = profileAtRequest === "bob"
        ? [{ titleId: "bob-episode", mediaType: "episode", seriesId: "bob-series", seasonId: "bob-season", seasonNumber: 1, episodeNumber: 1, positionSeconds: 60, durationSeconds: 1200, version: 1, reason: "resume", title: "Bob Queue", resourceId: "bob-resource", resourceProvider: "imdb", lastWatchedAt: createdAt }]
        : [{ titleId: "episode-1", mediaType: "episode", seriesId: "series-1", seasonId: "season-1", seasonNumber: 1, episodeNumber: 1, positionSeconds: 321, durationSeconds: 1800, version: 4, reason: "resume", title: "Signal Horizon", resourceId: "tt9000:1:1", resourceProvider: "imdb", lastWatchedAt: createdAt }];
      await json(route, { items });
      return;
    }
    const seasonOverride = path.match(/^\/metadata\/seasons\/([^/]+)$/);
    if (seasonOverride && this.seasonOverrides.has(seasonOverride[1])) {
      await json(route, this.seasonOverrides.get(seasonOverride[1]));
      return;
    }
    if (path === "/metadata/seasons/season-1") { await json(route, seasonOne); return; }
    if (path === "/metadata/seasons/season-2") { await json(route, seasonTwo); return; }
    if (path === "/metadata/seasons/dvd-season-1") { await json(route, dvdSeason); return; }
    if (path === "/metadata/seasons/bob-season") { await json(route, { ...seasonOne, id: "bob-season", seriesId: "bob-series", episodes: [] }); return; }
    if (path === "/metadata/series/series-1") {
      if (url.searchParams.get("episodeOrder") === "2") {
        await json(route, {
          ...series,
          numberOfSeasons: 1,
          numberOfEpisodes: 3,
          seasons: [seasonSummary(dvdSeason)],
          selectedEpisodeOrderId: "2",
          mappingProvider: "tvdb",
        });
        return;
      }
      await json(route, series);
      return;
    }
    if (path.startsWith("/addons/resources/meta/")) { await json(route, { results: [], errors: [] }); return; }
    if (path === "/library") {
      const mediaType = url.searchParams.get("mediaType");
      const items = mediaType ? this.libraryItems.filter((item) => item.mediaType === mediaType) : this.libraryItems;
      await json(route, { items, page: 1, totalPages: items.length > 0 ? 1 : 0, totalResults: items.length });
      return;
    }
    if (path.startsWith("/progress/") && request.method() === "GET") {
      const titleId = decodeURIComponent(path.slice("/progress/".length));
      const positionSeconds = titleId === "episode-1" ? 321 : 0;
      await json(route, { titleId, positionSeconds, durationSeconds: 1800, completed: false, version: titleId === "episode-1" ? 4 : 0, updatedAt: createdAt });
      return;
    }
    if (path.startsWith("/progress/") && request.method() === "PUT") {
      const titleId = decodeURIComponent(path.slice("/progress/".length));
      const progress = body as { positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number };
      await json(route, { titleId, ...progress, version: progress.expectedVersion + 1, updatedAt: createdAt });
      return;
    }
    if (path === "/playback/markers" && request.method() === "GET") {
      await json(route, { markers: [{ type: "intro", startSeconds: 330, endSeconds: 400, confidence: 1, submissionCount: 2 }] });
      return;
    }
    if (path === "/playback/sources" && request.method() === "POST") {
      const resourceId = String((body as { resourceId?: string })?.resourceId ?? "unknown");
      await json(route, { sources: [{ id: `option-${resourceId}`, sourceRef: `source-${resourceId}`, addonId: "fixture-addon", manifestId: "fixture-manifest", streamIndex: 0, name: "Fixture 1080p", description: "Deterministic direct stream", protocol: "http", container: "mp4", expiresAt }], providerErrors: [] });
      return;
    }
    if (path === "/playback/prepare" && request.method() === "POST") {
      await json(route, { sourceRef: (body as { sourceRef: string }).sourceRef, mode: "direct", protocol: "http", container: "mp4", media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 0, type: "audio", codec: "aac", language: "en", title: "English", channels: 2 }, { index: 2, type: "audio", codec: "aac", language: "fr", title: "French", channels: 2 }], subtitleTracks: [] }, subtitleCount: 2, expiresAt });
      return;
    }
    if (path === "/playback/resolve" && request.method() === "POST") {
      const input = body as { sourceRef: string; titleId?: string; preferredAudioTrack?: number };
      await json(route, { id: `session-${this.matching("/api/v1/playback/resolve", "POST").length}`, selectedSourceId: "resolved-source", selectedAudioTrack: input.preferredAudioTrack ?? 0, sources: [{ id: "resolved-source", addonId: "fixture-addon", manifestId: "fixture-manifest", name: "Fixture 1080p", mode: "direct", url: "https://fixtures.rivune.test/video.mp4", protocol: "http", container: "mp4", compatible: true, media: { container: "mp4", durationSeconds: 1800, hdrFormat: "sdr", videoTracks: [{ index: 0, type: "video", codec: "h264", width: 1920, height: 1080 }], audioTracks: [{ index: 0, type: "audio", codec: "aac", language: "en", title: "English", channels: 2 }, { index: 2, type: "audio", codec: "aac", language: "fr", title: "French", channels: 2 }], subtitleTracks: [] } }], subtitles: [{ id: "sub-en", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "en", url: "https://fixtures.rivune.test/subtitles-en.vtt", default: false }, { id: "sub-fr", addonId: "fixture-addon", manifestId: "fixture-manifest", language: "fr", url: "https://fixtures.rivune.test/subtitles-fr.vtt", default: false }], providerErrors: [], expiresAt });
      return;
    }
    if (/^\/playback\/sessions\//.test(path) && request.method() === "DELETE") { await route.fulfill({ status: 204 }); return; }
    const trailers = path.match(/^\/metadata\/titles\/([^/]+)\/trailers$/);
    if (trailers) {
      const seasonNumber = url.searchParams.get("seasonNumber");
      const label = seasonNumber === "2" ? "Season Two Trailer" : "Season One Trailer";
      await json(route, { trailers: [{ youtubeId: seasonNumber === "2" ? "season-two" : "season-one", name: label, language: "en", isFallback: false, captionPreference: "en" }] });
      return;
    }
    if (path === "/calendar") {
      const today = new Date().toISOString().slice(0, 10);
      await json(route, { events: [{ id: "calendar-episode-3", titleId: "episode-3", mediaType: "episode", title: "Moonrise", releaseDate: today, resourceId: "tt9000:2:1", resourceProvider: "imdb", seriesTitle: "Signal Horizon", seriesId: "series-1", seasonId: "season-2", seasonNumber: 2, episodeNumber: 1 }] });
      return;
    }
    if (path === "/titles/resolve" && request.method() === "POST") {
      const input = body as { externalId?: string };
      await json(route, { ...(body as object), titleId: input.externalId === "tt9000" ? "series-1" : "resolved-title" });
      return;
    }
    await json(route, { error: { code: "fixture_route_missing", message: `No E2E fixture for ${request.method()} ${path}` } }, 501);
  }
}

export const test = base.extend<{ rivune: RivuneHarness }>({
  rivune: async ({ page }, use) => {
    const rivune = new RivuneHarness();
    await rivune.install(page);
    await use(rivune);
  },
});

export { expect };
