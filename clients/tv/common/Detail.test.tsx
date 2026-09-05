import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_ACCESSIBILITY_PREFERENCES } from "./accessibility";
import type { RivuneTvClient } from "./api";
import { Detail, type PlayerRequest } from "./Detail";
import { mediaFromContinue } from "./media";
import { Player } from "./Player";
import type { ContinueWatchingItem, Episode, Season, Series } from "./types";
import type { TvPlayerEvents } from "./platform";

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

const variantSiblingEpisode = {
  ...variantEpisode,
  id: "dvd-episode-3",
  name: "Disc Finale",
  episodeNumber: 3,
  externalIds: { tvdb: "10357451" },
} satisfies Episode;

const variantSeason = {
  id: metadataSeasonId,
  mediaType: "season",
  seriesId: "series-1",
  name: "DVD Season 1",
  overview: "The disc order.",
  seasonNumber: 1,
  voteAverage: 8.4,
  episodes: [variantEpisode, variantSiblingEpisode],
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

  it("does not carry continuation progress into a sibling DVD episode", async () => {
    const onOpen = vi.fn();
    const onPlay = vi.fn();
    const updatePlaybackProgress = vi.fn(async (_titleId: string, input: {
      positionSeconds: number;
      durationSeconds: number;
      completed: boolean;
      expectedVersion: number;
    }) => ({ ...input, version: 1, updatedAt: "2026-09-04T12:01:00Z" }));
    const client = {
      issuer: "https://example.test",
      playbackProgress: vi.fn(async () => null),
      library: vi.fn(async () => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      series: vi.fn(async () => variantSeries),
      season: vi.fn(async () => variantSeason),
      playbackSources: vi.fn(async () => sourceList()),
      preparePlayback: vi.fn(async () => ({ sourceRef: "source-ref", mode: "direct", protocol: "http", subtitleCount: 0, expiresAt: "2099-01-01T00:00:00Z" })),
      resolveArtworkUrl: vi.fn(() => null),
      resolvePlayback: vi.fn(async () => ({
        id: "session-1",
        selectedSourceId: "source",
        subtitles: [],
        providerErrors: [],
        expiresAt: "2099-01-01T00:00:00Z",
        sources: [{ id: "source", addonId: "addon", manifestId: "manifest", mode: "direct", protocol: "http", url: "/stream", compatible: true }],
      })),
      resolveResourceUrl: vi.fn(() => "https://example.test/stream"),
      updatePlaybackProgress,
      stopPlayback: vi.fn(async () => undefined),
    } as unknown as RivuneTvClient;
    const detailProps = {
      client,
      profileId: "profile-1",
      timezone: "UTC",
      accessibility: DEFAULT_ACCESSIBILITY_PREFERENCES,
      qualityPreset: "automatic" as const,
      devices: [],
      onClose: vi.fn(),
      onOpen,
      onPlay,
      onSendToDevice: vi.fn(),
      onRemoteResult: vi.fn(),
    };

    await act(async () => root.render(<Detail {...detailProps} item={continuation()} />));
    await vi.waitFor(() => expect(container.querySelectorAll(".tv-episode")).toHaveLength(2));
    await act(async () => container.querySelectorAll<HTMLButtonElement>(".tv-episode")[1]?.click());
    const sibling = onOpen.mock.calls[0]?.[0];
    expect(sibling).toMatchObject({ titleId: "dvd-episode-3", resourceId: "tvdb:10357451" });

    await act(async () => root.render(<Detail {...detailProps} item={sibling} />));
    const play = await vi.waitFor(() => {
      const button = Array.from(container.querySelectorAll<HTMLButtonElement>(".tv-actions button"))
        .find((candidate) => candidate.textContent === "Play");
      expect(button).toBeDefined();
      return button!;
    });
    await act(async () => play.click());
    const request = await vi.waitFor(() => {
      const value = onPlay.mock.calls[0]?.[0] as PlayerRequest | undefined;
      expect(value).toBeDefined();
      return value!;
    });
    expect(request.startSeconds).toBe(0);
    expect(request.item.resumePositionSeconds).toBeUndefined();
    expect(request.item.durationSeconds).toBeUndefined();
    expect(request.item.progressVersion).toBeUndefined();

    let events: TvPlayerEvents | undefined;
    const nativePlayer = {
      load: vi.fn(async (_request: unknown, nextEvents: TvPlayerEvents) => {
        events = nextEvents;
        nextEvents.onReady(120);
      }),
      play: vi.fn(async () => undefined),
      pause: vi.fn(async () => undefined),
      seek: vi.fn(async () => undefined),
      selectAudio: vi.fn(async () => undefined),
      selectSubtitle: vi.fn(async () => undefined),
      stop: vi.fn(async () => undefined),
      destroy: vi.fn(),
    };
    window.RivunePlatformAdapter!.createPlayer = () => nativePlayer;
    await act(async () => root.render(<Player
      client={client}
      {...request}
      devices={[]}
      qualityPreset="automatic"
      onSendToDevice={vi.fn()}
      onControlDevice={vi.fn()}
      onController={vi.fn()}
      onPlaybackState={vi.fn()}
      onRemoteResult={vi.fn()}
      onClose={vi.fn()}
      setBackHandler={vi.fn()}
    />));
    await vi.waitFor(() => expect(nativePlayer.load).toHaveBeenCalled());
    await act(async () => events?.onTime(10, 120));
    const stop = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find((button) => button.textContent === "Stop");
    expect(stop).toBeDefined();
    await act(async () => stop?.click());
    await vi.waitFor(() => expect(updatePlaybackProgress).toHaveBeenCalledWith("dvd-episode-3", {
      positionSeconds: 10,
      durationSeconds: 120,
      completed: false,
      expectedVersion: 0,
    }));
  });

  it("keeps a canonical continuation on the aired hierarchy when the platform default is DVD", async () => {
    const playbackSources = vi.fn(async () => sourceList());
    const onPlay = vi.fn();
    const series = vi.fn(async (_id: string, mappingProvider = "tvdb") =>
      mappingProvider === "tmdb" ? canonicalSeries : variantSeries);
    const season = vi.fn(async (id: string) =>
      id === canonicalSeason.id ? canonicalSeason : variantSeason);
    const client = {
      issuer: "https://example.test",
      playbackProgress: vi.fn(async () => null),
      library: vi.fn(async () => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      series,
      season,
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
    await vi.waitFor(() => expect(container.querySelector(".tv-episode")?.textContent).toContain("First Light"));

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
