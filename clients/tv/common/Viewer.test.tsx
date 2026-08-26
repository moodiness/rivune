import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RivuneTvClient } from "./api";
import type { ViewerSearchRequest, ViewerSearchResult } from "./ViewerSearch";
import { performViewerSearch } from "./ViewerSearch";
import type { Account, Discovery, Profile } from "./types";
import { Viewer } from "./Viewer";

vi.mock("./updates", () => ({
  checkForTvUpdate: vi.fn(),
  downloadTvUpdate: vi.fn(),
  restartForTvUpdate: vi.fn(),
  useTvUpdateState: () => ({ status: "unavailable", currentVersion: "test" }),
}));

vi.mock("./ViewerSearch", async () => {
  const actual = await vi.importActual("./ViewerSearch");
  return { ...actual, performViewerSearch: vi.fn() };
});

const emptyResult: ViewerSearchResult = {
  items: [],
  intents: [],
  mediaTypes: [],
  page: 1,
  hasMore: false,
  partial: false,
};

const discovery = {
  name: "Test server",
  serverVersion: "1",
  protocolVersion: 22,
  apiBaseUrl: "https://example.test",
  setupRequired: false,
  timezone: "UTC",
  interfaceLanguage: "en",
  capabilities: ["semantic-search"],
} as Discovery;

const profile = {
  id: "profile-1",
  name: "Viewer",
  categoryId: "category-1",
  category: { id: "category-1", name: "Default", color: null, icon: null },
  isChild: false,
  hasPin: false,
  canManage: false,
  enabled: true,
  availableFrom: null,
  availableUntil: null,
  accessStartTime: null,
  accessEndTime: null,
  accessTimezone: "UTC",
  accessible: true,
  avatar: { kind: "preset", url: "" },
} as Profile;

const account = {
  user: { id: "user-1", username: "viewer", role: "user" },
  session: { id: "session-1", deviceId: "device-1", activeProfile: null, authorizationScope: "global_admin", category: null },
  profiles: [profile],
  maintenance: { enabled: false, message: null },
} as Account;

describe("TV Viewer progressive search", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
    container = document.createElement("div");
    window.RivunePlatformAdapter = {
      platform: "webos",
      deviceName: async () => "Living room TV",
      capabilities: () => ({
        streamingProtocols: ["http", "hls"], containers: ["mp4"], videoCodecs: ["h264"], audioCodecs: ["aac"], hdrFormats: [],
        processingModes: ["direct", "remux", "transcode_audio", "transcode"], maximumHeight: 2160, maximumVideoBitrateKbps: 20_000,
        maximumAudioChannels: 6, subtitleModes: ["external", "burn"],
      }),
      createPlayer: vi.fn(),
      exitApp: vi.fn(),
    };
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps activity beside results and preserves the focused card node across append-only updates", async () => {
    let publish: ViewerSearchRequest["onUpdate"];
    let finish: ((result: ViewerSearchResult) => void) | undefined;
    vi.mocked(performViewerSearch).mockImplementation((_client, request) => {
      publish = request.onUpdate;
      return new Promise<ViewerSearchResult>((resolve) => { finish = resolve; });
    });
    const client = {
      effectiveProfileSettings: vi.fn(async () => ({ schemaVersion: 1, settings: {}, sources: {} })),
      accessibilityPreferences: vi.fn(async () => ({ revision: 0, reducedMotion: "system", highContrast: "system", textScale: 100, captions: "system", audioDescription: false, focusIndicators: "standard" })),
      collections: vi.fn(async () => []),
      continueWatching: vi.fn(async () => ({ items: [] })),
      resolveArtworkUrl: vi.fn(() => null),
    } as unknown as RivuneTvClient;

    await act(async () => {
      root.render(<Viewer client={client} discovery={discovery} account={account} profile={profile} setBackHandler={vi.fn()} onChangeProfile={vi.fn()} onDisconnect={vi.fn()} />);
    });
    const searchNav = Array.from(container.querySelectorAll<HTMLButtonElement>(".tv-nav__button"))
      .find((button) => button.textContent === "Search");
    expect(searchNav).toBeDefined();
    await act(async () => searchNav?.click());

    const input = container.querySelector<HTMLInputElement>(".tv-searchbar input");
    expect(input).not.toBeNull();
    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
      valueSetter?.call(input, "Dune");
      input?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => container.querySelector<HTMLButtonElement>(".tv-searchbar button")?.click());

    const first = { id: "tmdb:42", mediaType: "movie", title: "Dune", externalIds: { tmdb: "42" } };
    await act(async () => publish?.({ ...emptyResult, items: [first] }));
    const focusedCard = container.querySelector<HTMLButtonElement>(".tv-card");
    expect(focusedCard).not.toBeNull();
    focusedCard?.focus();
    expect(document.activeElement).toBe(focusedCard);
    expect(container.querySelector('[role="status"]')).not.toBeNull();
    expect(container.querySelector(".tv-grid")).not.toBeNull();

    const updatedFirst = { ...first, title: "Dune enriched", description: "New metadata" };
    const second = { id: "tmdb:84", mediaType: "movie", title: "Arrival", externalIds: { tmdb: "84" } };
    await act(async () => publish?.({ ...emptyResult, items: [updatedFirst, second] }));
    expect(container.querySelector<HTMLButtonElement>(".tv-card")).toBe(focusedCard);
    expect(document.activeElement).toBe(focusedCard);
    expect(container.querySelectorAll(".tv-card")).toHaveLength(2);
    expect(container.querySelector('[role="status"]')).not.toBeNull();

    await act(async () => finish?.({ ...emptyResult, items: [updatedFirst, second] }));
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("advertises a real TV target and resolves a failed remote load through the Detail pipeline", async () => {
    const operationId = "44444444-4444-4444-8444-444444444444";
    const reportPlaybackCommandResult = vi.fn(async () => ({ status: "failed" }));
    const client = {
      issuer: "https://example.test",
      effectiveProfileSettings: vi.fn(async () => ({ schemaVersion: 1, settings: {}, sources: {} })),
      accessibilityPreferences: vi.fn(async () => ({ revision: 0, reducedMotion: "system", highContrast: "system", textScale: 100, captions: "system", audioDescription: false, focusIndicators: "standard" })),
      collections: vi.fn(async () => []),
      continueWatching: vi.fn(async () => ({ items: [] })),
      resolveArtworkUrl: vi.fn(() => null),
      updatePlaybackDevice: vi.fn(async (input) => ({ sessionId: "self", current: true, revision: 1, ...input })),
      playbackDevices: vi.fn(async () => ({ devices: [] })),
      playbackCommands: vi.fn(async () => ({ commands: [{
        operationId, command: "load", mode: "play-copy", item: { titleId: "title-1", mediaType: "tv", resourceId: "channel-1", title: "Remote news" },
        senderDeviceName: "Phone", status: "pending", createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 60_000).toISOString(),
      }] })),
      playbackProgress: vi.fn(async () => null),
      library: vi.fn(async () => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      playbackSources: vi.fn(async () => ({ sources: [], providerErrors: [] })),
      reportPlaybackCommandResult,
    } as unknown as RivuneTvClient;

    await act(async () => {
      root.render(<Viewer client={client} discovery={{ ...discovery, capabilities: ["playback-command-results"] }} account={account} profile={profile} setBackHandler={vi.fn()} onChangeProfile={vi.fn()} onDisconnect={vi.fn()} />);
    });

    await vi.waitFor(() => expect(reportPlaybackCommandResult).toHaveBeenCalledWith(operationId, { status: "failed", code: "execution_failed" }));
    expect(client.updatePlaybackDevice).toHaveBeenCalledWith(expect.objectContaining({ capabilities: expect.arrayContaining(["remote-control", "load-target", "playback-command-results"]) }));
    expect(container.textContent).toContain("Remote news");
  });
});
