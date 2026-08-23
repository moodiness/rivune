export type TvPlatform = "webos" | "tizen" | "browser";

export type TvPlaybackCapabilities = {
  streamingProtocols: string[];
  containers: string[];
  videoCodecs: string[];
  audioCodecs: string[];
  hdrFormats: string[];
  processingModes: Array<"direct" | "remux" | "transcode_audio" | "transcode">;
  maximumHeight: number;
  maximumAudioChannels: number;
  subtitleModes: Array<"embedded" | "external" | "burn">;
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

export function platformAdapter(): RivunePlatformAdapter {
  const adapter = window.RivunePlatformAdapter;
  if (!adapter) throw new Error("Rivune TV platform adapter is unavailable.");
  return adapter;
}
