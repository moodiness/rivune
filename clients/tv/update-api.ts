export type TvUpdateStatus =
  | "idle"
  | "checking"
  | "up-to-date"
  | "available"
  | "downloading"
  | "ready"
  | "unavailable"
  | "error";

export type TvUpdateState = Readonly<{
  status: TvUpdateStatus;
  currentVersion: string;
  latestVersion?: string;
  notice?: boolean;
}>;

export interface RivuneTvUpdater {
  getState(): TvUpdateState;
  subscribe(listener: () => void): () => void;
  markHealthy(): Promise<void>;
  checkAutomatically(): Promise<void>;
  checkManually(): Promise<void>;
  download(): Promise<void>;
  dismissNotice(): void;
  restart(): Promise<void>;
}

export type RivuneRuntimePlatform = "webos" | "tizen";
