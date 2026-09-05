import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_ACCESSIBILITY_PREFERENCES } from "./accessibility";
import type { RivuneTvClient } from "./api";
import { Detail } from "./Detail";
import { mediaFromContinue } from "./media";
import type { ContinueWatchingItem, Episode, Season, Series } from "./types";

const metadataSeasonId = "tvdb:0392d6ce-02f0-4c75-a73f-13badb1c85ba:2112814";

const variantSeries = {
  id: "series-1",
  mediaType: "series",
  name: "Signal Horizon",
  originalName: "Signal Horizon",
  originalLanguage: "en",
  overview: "Series overview",
  genres: [],
  cast: [],
  voteAverage: 8,
  voteCount: 10,
  seasons: [
    { id: "tvdb:0392d6ce-02f0-4c75-a73f-13badb1c85ba:9999999", mediaType: "season", seriesId: "series-1", name: "Ordinal decoy", overview: "", seasonNumber: 1, episodeCount: 1, voteAverage: 0, externalIds: { tvdb: "9999999" } },
    { id: metadataSeasonId, mediaType: "season", seriesId: "series-1", name: "DVD Season 1", overview: "", seasonNumber: 1, episodeCount: 1, voteAverage: 0, externalIds: { tvdb: "2112814" } },
  ],
  aliases: [],
  episodeOrders: [
    { id: "1", name: "Aired Order", type: "official", isDefault: true },
    { id: "2", name: "DVD Order", type: "dvd", isDefault: false },
  ],
  selectedEpisodeOrderId: "2",
  mappingProvider: "tvdb",
  externalIds: { imdb: "tt9000", tvdb: "9900" },
} satisfies Series;

const variantEpisode = {
  id: "dvd-episode-2",
  mediaType: "episode",
  seasonId: metadataSeasonId,
  name: "Disc Middle",
  overview: "The DVD order continues.",
  seasonNumber: 1,
  episodeNumber: 2,
  airDate: "2024-01-10",
  runtimeMinutes: 31,
  voteAverage: 8.3,
  voteCount: 95,
  externalIds: { tvdb: "10357450" },
} satisfies Episode;

const variantSeason = {
  id: metadataSeasonId,
  mediaType: "season",
  seriesId: "series-1",
  name: "DVD Season 1",
  overview: "The disc order.",
  seasonNumber: 1,
  voteAverage: 8.4,
  episodes: [variantEpisode],
  externalIds: { tvdb: "2112814" },
} satisfies Season;

const canonicalSeries = {
  ...variantSeries,
  seasons: [{ id: "season-1", mediaType: "season", seriesId: "series-1", name: "Season 1", overview: "", seasonNumber: 1, episodeCount: 1, voteAverage: 0, externalIds: { tmdb: "101" } }],
  selectedEpisodeOrderId: "1",
  mappingProvider: "tmdb",
} satisfies Series;

const canonicalEpisode = {
  ...variantEpisode,
  id: "episode-1",
  seasonId: "season-1",
  name: "First Light",
  episodeNumber: 1,
  externalIds: { imdb: "tt900001" },
} satisfies Episode;

const canonicalSeason = {
  ...variantSeason,
  id: "season-1",
  name: "Season 1",
  episodes: [canonicalEpisode],
  externalIds: { tmdb: "101" },
} satisfies Season;

function continuation(overrides: Partial<ContinueWatchingItem> = {}) {
  return mediaFromContinue({
    titleId: "dvd-episode-2",
    mediaType: "episode",
    seriesId: "series-1",
    seasonId: "persisted-dvd-season-1",
    seasonNumber: 1,
    episodeNumber: 2,
    mappingProvider: "tvdb",
    episodeOrderId: "2",
    metadataSeasonId,
    positionSeconds: 480,
    durationSeconds: 1860,
    version: 3,
    reason: "resume",
    resourceId: "tvdb:10357450",
    resourceProvider: "tvdb",
    title: "Signal Horizon",
    episodeTitle: "Disc Middle",
    lastWatchedAt: "2026-09-04T12:00:00Z",
    ...overrides,
  });
}

function sourceList() {
  return {
    sources: [{ id: "source", sourceRef: "source-ref", addonId: "addon", manifestId: "manifest", streamIndex: 0, name: "Direct", protocol: "http", expiresAt: "2099-01-01T00:00:00Z" }],
    providerErrors: [],
  };
}

describe("TV detail episode-order context", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
    window.RivunePlatformAdapter = {
      platform: "webos",
      deviceName: async () => "Living room TV",
      capabilities: () => ({
        streamingProtocols: ["http"], containers: ["mp4"], videoCodecs: ["h264"], audioCodecs: ["aac"], hdrFormats: [],
        processingModes: ["direct"], maximumHeight: 2160, maximumVideoBitrateKbps: 20_000,
        maximumAudioChannels: 6, subtitleModes: ["external"],
      }),
      createPlayer: vi.fn(),
      exitApp: vi.fn(),
    };
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("loads the exact DVD hierarchy and uses the raw TVDB episode resource", async () => {
    const series = vi.fn(async () => variantSeries);
    const season = vi.fn(async () => variantSeason);
    const playbackSources = vi.fn(async () => sourceList());
    const playbackMarkers = vi.fn();
    const preparePlayback = vi.fn(async () => ({ sourceRef: "source-ref", mode: "direct", protocol: "http", subtitleCount: 0, expiresAt: "2099-01-01T00:00:00Z" }));
    const onOpen = vi.fn();
    const onPlay = vi.fn();
    const client = {
      issuer: "https://example.test",
      playbackProgress: vi.fn(async () => null),
      library: vi.fn(async () => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      series,
      season,
      playbackSources,
      playbackMarkers,
      preparePlayback,
      resolveArtworkUrl: vi.fn(() => null),
    } as unknown as RivuneTvClient;

    await act(async () => root.render(<Detail
      client={client}
      item={continuation()}
      profileId="profile-1"
      timezone="UTC"
      accessibility={DEFAULT_ACCESSIBILITY_PREFERENCES}
      qualityPreset="automatic"
      devices={[]}
      onClose={vi.fn()}
      onOpen={onOpen}
      onPlay={onPlay}
      onSendToDevice={vi.fn()}
      onRemoteResult={vi.fn()}
    />));

    await vi.waitFor(() => expect(series).toHaveBeenCalledWith("series-1", "tvdb", undefined, "2"));
    await vi.waitFor(() => expect(season).toHaveBeenCalledWith(metadataSeasonId, "tvdb"));
    expect(season).not.toHaveBeenCalledWith(variantSeries.seasons[0]!.id, "tvdb");

    await act(async () => container.querySelector<HTMLButtonElement>(".tv-episode")?.click());
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({
      resourceId: "tvdb:10357450",
      mappingProvider: "tvdb",
      episodeOrderId: "2",
      metadataSeasonId,
      seasonId: "persisted-dvd-season-1",
    }));

    const play = Array.from(container.querySelectorAll<HTMLButtonElement>(".tv-actions button"))
      .find((button) => button.textContent === "Play");
    expect(play).toBeDefined();
    await act(async () => play?.click());
    await vi.waitFor(() => expect(playbackSources).toHaveBeenCalledWith("episode", "tvdb:10357450", expect.any(Object), undefined));
    await vi.waitFor(() => expect(onPlay).toHaveBeenCalledWith(expect.objectContaining({
      startSeconds: 480,
      item: expect.objectContaining({ resourceId: "tvdb:10357450", episodeOrderId: "2", metadataSeasonId }),
    })));
    expect(playbackMarkers).not.toHaveBeenCalled();
  });

  it("keeps canonical episode playback on IMDb series coordinates", async () => {
    const playbackSources = vi.fn(async () => sourceList());
    const onPlay = vi.fn();
    const client = {
      issuer: "https://example.test",
      playbackProgress: vi.fn(async () => null),
      library: vi.fn(async () => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      series: vi.fn(async () => canonicalSeries),
      season: vi.fn(async () => canonicalSeason),
      playbackSources,
      preparePlayback: vi.fn(async () => ({ sourceRef: "source-ref", mode: "direct", protocol: "http", subtitleCount: 0, expiresAt: "2099-01-01T00:00:00Z" })),
      resolveArtworkUrl: vi.fn(() => null),
    } as unknown as RivuneTvClient;

    await act(async () => root.render(<Detail
      client={client}
      item={continuation({
        titleId: "episode-1",
        seasonId: "season-1",
        episodeNumber: 1,
        mappingProvider: null,
        episodeOrderId: null,
        metadataSeasonId: null,
        resourceId: "tt9000:1:1",
        resourceProvider: "imdb",
        episodeTitle: "First Light",
      })}
      profileId="profile-1"
      timezone="UTC"
      accessibility={DEFAULT_ACCESSIBILITY_PREFERENCES}
      qualityPreset="automatic"
      devices={[]}
      onClose={vi.fn()}
      onOpen={vi.fn()}
      onPlay={onPlay}
      onSendToDevice={vi.fn()}
      onRemoteResult={vi.fn()}
    />));

    const play = await vi.waitFor(() => {
      const button = Array.from(container.querySelectorAll<HTMLButtonElement>(".tv-actions button"))
        .find((candidate) => candidate.textContent === "Play");
      expect(button).toBeDefined();
      return button!;
    });
    await act(async () => play.click());
    await vi.waitFor(() => expect(playbackSources).toHaveBeenCalledWith("episode", "tt9000:1:1", expect.any(Object), undefined));
    await vi.waitFor(() => expect(onPlay).toHaveBeenCalledWith(expect.objectContaining({
      item: expect.not.objectContaining({ episodeOrderId: expect.anything() }),
    })));
  });
});
