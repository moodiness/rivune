export type TvPlatform = "webos" | "tizen" | "browser";
export type TvNetworkClass = "local" | "remote_wifi" | "mobile";
export type TvQualityPreset = "automatic" | "economy" | "balanced" | "maximum";

export type TvPlaybackCapabilities = {
  streamingProtocols: string[];
  containers: string[];
  videoCodecs: string[];
  audioCodecs: string[];
  hdrFormats: string[];
  processingModes: Array<"direct" | "remux" | "transcode_audio" | "transcode">;
  maximumHeight: number;
  maximumVideoBitrateKbps: number;
  maximumAudioChannels: number;
  subtitleModes: Array<"embedded" | "external" | "burn">;
};

export type TvLocalPlaybackPolicy = {
  networkClass: TvNetworkClass;
  qualityPreset: TvQualityPreset;
  maximumHeight: number;
  maximumVideoBitrateKbps: number;
  offlineMedia: false;
  capabilities: TvPlaybackCapabilities;
};

export type TvSubtitle = {
  id: string;
  url: string;
  label: string;
  language?: string;
  forced: boolean;
  selected: boolean;
};

export type TvPlaybackRequest = {
  url: string;
  title: string;
  protocol: string;
  container?: string;
  startSeconds: number;
  subtitles: TvSubtitle[];
};

export type TvPlayerTrack = {
  id: string;
  index: number;
  kind: "audio" | "subtitle";
  label: string;
  language?: string;
  selected: boolean;
};

export type TvPlayerEvents = {
  onReady(durationSeconds: number): void;
  onTime(positionSeconds: number, durationSeconds: number): void;
  onState(state: "buffering" | "playing" | "paused"): void;
  onTracks(tracks: TvPlayerTrack[]): void;
  onEnded(): void;
  onError(message: string): void;
};

export interface TvPlayer {
  load(request: TvPlaybackRequest, events: TvPlayerEvents): Promise<void>;
  play(): Promise<void>;
  pause(): Promise<void>;
  seek(positionSeconds: number): Promise<void>;
  selectAudio(index: number): Promise<void>;
  selectSubtitle(id: string | null): Promise<void>;
  stop(): Promise<void>;
  destroy(): void;
}

export interface RivunePlatformAdapter {
  readonly platform: TvPlatform;
  deviceName(): Promise<string>;
  capabilities(): TvPlaybackCapabilities;
  createPlayer(host: HTMLElement): TvPlayer;
  exitApp(): void;
}

function localHost(hostname: string): boolean {
  const value = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (value === "localhost" || value === "::1" || (value.startsWith("fd") || value.startsWith("fc")) && value.includes(":")) return true;
  const parts = value.split(".").map(Number);
  return parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255) && (parts[0] === 10 || parts[0] === 127 ||
    parts[0] === 192 && parts[1] === 168 || parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31);
}

export function classifyTvNetwork(serverUrl: string): TvNetworkClass {
  const connection = (navigator as Navigator & { connection?: { type?: string; effectiveType?: string } }).connection;
  if (connection?.type === "cellular" || ["slow-2g", "2g", "3g"].includes(connection?.effectiveType ?? "")) return "mobile";
  try { if (localHost(new URL(serverUrl).hostname)) return "local"; } catch { /* the API client already validates its issuer */ }
  return "remote_wifi";
}

export function tvPlaybackPolicy(adapter: RivunePlatformAdapter, serverUrl: string, qualityPreset: TvQualityPreset): TvLocalPlaybackPolicy {
  const capacity = adapter.capabilities();
  const networkClass = classifyTvNetwork(serverUrl);
  const limit = qualityPreset === "economy" ? { height: 480, bitrate: 2_000 }
    : qualityPreset === "balanced" ? { height: 1080, bitrate: 8_000 }
      : qualityPreset === "automatic" && networkClass === "mobile" ? { height: 720, bitrate: 5_000 }
        : null;
  const maximumHeight = limit ? Math.min(capacity.maximumHeight, limit.height) : capacity.maximumHeight;
  const maximumVideoBitrateKbps = limit ? Math.min(capacity.maximumVideoBitrateKbps, limit.bitrate) : capacity.maximumVideoBitrateKbps;
  return {
    networkClass, qualityPreset, maximumHeight, maximumVideoBitrateKbps, offlineMedia: false,
    capabilities: { ...capacity, maximumHeight, maximumVideoBitrateKbps },
  };
}

export function platformAdapter(): RivunePlatformAdapter {
  const adapter = window.RivunePlatformAdapter;
  if (!adapter) throw new Error("Rivune TV platform adapter is unavailable.");
  return adapter;
}
