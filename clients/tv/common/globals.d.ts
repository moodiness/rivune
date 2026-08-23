import type { RivunePlatformAdapter } from "./platform";
import type { RuntimeBundle } from "../updater/contracts";
import type { RivuneRuntimePlatform, RivuneTvUpdater } from "../update-api";

declare global {
  interface Window {
    RivunePlatformAdapter?: RivunePlatformAdapter;
    RivuneUpdater?: RivuneTvUpdater;
    RivuneRuntimePlatform?: RivuneRuntimePlatform;
    RivunePackagedRuntime?: RuntimeBundle;
    webapis?: unknown;
    tizen?: unknown;
    PalmSystem?: { deviceInfo?: string; platformVersion?: string; [key: string]: unknown };
  }
}

export {};
