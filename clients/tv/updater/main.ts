import type { RivuneRuntimePlatform, RivuneTvUpdater, TvUpdateState } from "../update-api";
import { decodeUtf8, sha256Hex } from "./bytes";
import {
  emptyStoredRuntimeState,
  maximumReleaseBytes,
  parseLatestRelease,
  parseUpdateManifest,
  parseRuntimeBundle,
  parseStoredRuntimeState,
  prepareRuntime,
  releaseEndpoint,
  rollbackRuntime,
  type RuntimeBundle,
  type RuntimeRelease,
} from "./contracts";
import { openRuntimeStore, type RuntimeStore } from "./storage";

const automaticCheckInterval = 24 * 60 * 60 * 1000;
const platform = window.RivuneRuntimePlatform;
const packagedValue = window.RivunePackagedRuntime;
delete window.RivunePackagedRuntime;

function fatal(message: string): void {
  const root = document.getElementById("root");
  if (root) root.innerHTML = `<div class="rivune-boot" role="alert">${message}</div>`;
}

function executeScript(source: string, name: string): void {
  const script = document.createElement("script");
  script.text = `${source}\n//# sourceURL=rivune-runtime://${name}`;
  document.head.appendChild(script);
  script.remove();
}

function executeRuntime(runtime: RuntimeBundle, selectedPlatform: RivuneRuntimePlatform): void {
  const loading = document.querySelector(".rivune-boot");
  loading?.remove();
  const style = document.createElement("style");
  style.dataset.rivuneRuntime = runtime.version;
  style.textContent = runtime.application.css;
  document.head.appendChild(style);
  executeScript(runtime.platforms[selectedPlatform].javascript, `${selectedPlatform}-platform.js`);
  executeScript(runtime.application.javascript, "application.js");
}

async function responseBytes(response: Response, maximumBytes: number): Promise<Uint8Array> {
  const declared = response.headers.get("Content-Length");
  if (declared !== null && (!/^(0|[1-9][0-9]*)$/.test(declared) || Number(declared) > maximumBytes)) {
    throw new Error("The update response is too large.");
  }
  const bytes = new Uint8Array(await response.arrayBuffer());
  if (bytes.length === 0 || bytes.length > maximumBytes) throw new Error("The update response size is invalid.");
  return bytes;
}

async function bootstrap(): Promise<void> {
  if (platform !== "webos" && platform !== "tizen") throw new Error("The packaged TV platform is invalid.");
  const packaged = parseRuntimeBundle(packagedValue);
  const selectedPlatform: RivuneRuntimePlatform = platform;
  let store: RuntimeStore | null = null;
  let persisted = emptyStoredRuntimeState();
  try {
    store = await openRuntimeStore();
    persisted = parseStoredRuntimeState(await store.load());
  } catch {
    store = null;
  }
  let prepared = prepareRuntime(packaged, persisted);
  persisted = prepared.state;
  if (store) {
    try {
      await store.save(persisted);
    } catch {
      store = null;
      persisted = emptyStoredRuntimeState();
      prepared = { state: persisted, runtime: packaged, source: "packaged" };
    }
  }

  let publicState: TvUpdateState = Object.freeze({
    status: store ? "idle" : "unavailable",
    currentVersion: prepared.runtime.version,
  });
  const listeners = new Set<() => void>();
  let availableRelease: RuntimeRelease | null = null;
  let operationRunning = false;
  let bootHealthy = false;
  let bootTimer = 0;
  let recovering = false;

  const publish = (state: TvUpdateState) => {
    publicState = Object.freeze(state);
    listeners.forEach((listener) => listener());
  };
  const save = async () => {
    if (!store) throw new Error("TV runtime storage is unavailable.");
    await store.save(persisted);
  };
  const recover = async () => {
    if (recovering || prepared.source !== "cached") return;
    recovering = true;
    try {
      persisted = rollbackRuntime(persisted);
      await save();
      window.location.reload();
    } catch {
      fatal("Rivune could not restore the packaged TV runtime.");
    }
  };
  const bootError = () => { if (!bootHealthy) void recover(); };
  window.addEventListener("error", bootError);
  window.addEventListener("unhandledrejection", bootError);
  bootTimer = window.setTimeout(() => {
    if (bootHealthy) return;
    if (prepared.source === "cached") void recover();
    else fatal("Rivune could not start the packaged TV runtime.");
  }, 15_000);

  async function download(release: RuntimeRelease, automatic: boolean): Promise<void> {
    publish({ status: "downloading", currentVersion: prepared.runtime.version, latestVersion: release.version });
    try {
      const response = await fetch(release.mirrorUrl, {
        method: "GET",
        headers: { Accept: "application/json" },
        cache: "no-store",
        credentials: "omit",
        redirect: "error",
      });
      if (!response.ok || response.url !== release.mirrorUrl) throw new Error("The TV runtime mirror returned an invalid response.");
      const bytes = await responseBytes(response, release.size);
      if (bytes.length !== release.size || sha256Hex(bytes) !== release.sha256) {
        throw new Error("The downloaded TV runtime does not match the GitHub release digest.");
      }
      const runtime = parseRuntimeBundle(JSON.parse(decodeUtf8(bytes)), release.version);
      persisted = { ...persisted, staged: runtime, lastSuccessfulCheckAt: Date.now() };
      await save();
      availableRelease = null;
      publish({ status: "ready", currentVersion: prepared.runtime.version, latestVersion: release.version });
    } catch {
      publish({ status: automatic ? "idle" : "error", currentVersion: prepared.runtime.version, latestVersion: release.version });
    }
  }

  async function check(manual: boolean): Promise<void> {
    if (!store || operationRunning) return;
    if (persisted.staged) {
      publish({ status: "ready", currentVersion: prepared.runtime.version, latestVersion: persisted.staged.version });
      return;
    }
    const now = Date.now();
    if (!manual && persisted.lastSuccessfulCheckAt > 0 && now >= persisted.lastSuccessfulCheckAt && now - persisted.lastSuccessfulCheckAt < automaticCheckInterval) {
      return;
    }
    operationRunning = true;
    publish({ status: "checking", currentVersion: prepared.runtime.version });
    try {
      const response = await fetch(releaseEndpoint, {
        method: "GET",
        headers: { Accept: "application/vnd.github+json" },
        cache: "no-store",
        credentials: "omit",
        redirect: "error",
      });
      if (!response.ok || response.url !== releaseEndpoint) throw new Error("GitHub returned an invalid release response.");
      const bytes = await responseBytes(response, maximumReleaseBytes);
      const result = parseLatestRelease(JSON.parse(decodeUtf8(bytes)), prepared.runtime.version, selectedPlatform);
      if (!result.updateAvailable) {
        persisted = { ...persisted, lastSuccessfulCheckAt: now };
        await save();
        availableRelease = null;
        publish({ status: "up-to-date", currentVersion: prepared.runtime.version, latestVersion: result.version });
      } else {
        const manifestResponse = await fetch(result.release.manifest.mirrorUrl, {
          method: "GET",
          headers: { Accept: "application/json" },
          cache: "no-store",
          credentials: "omit",
          redirect: "error",
        });
        if (!manifestResponse.ok || manifestResponse.url !== result.release.manifest.mirrorUrl) {
          throw new Error("The TV update manifest mirror returned an invalid response.");
        }
        const manifestBytes = await responseBytes(manifestResponse, result.release.manifest.size);
        if (manifestBytes.length !== result.release.manifest.size || sha256Hex(manifestBytes) !== result.release.manifest.sha256) {
          throw new Error("The TV update manifest does not match the GitHub release digest.");
        }
        const runtimeRelease = parseUpdateManifest(JSON.parse(decodeUtf8(manifestBytes)), result.release, selectedPlatform);
        availableRelease = runtimeRelease;
        if (manual) {
          publish({ status: "available", currentVersion: prepared.runtime.version, latestVersion: runtimeRelease.version });
        } else {
          await download(runtimeRelease, true);
        }
      }
    } catch {
      publish({ status: manual ? "error" : "idle", currentVersion: prepared.runtime.version });
    } finally {
      operationRunning = false;
    }
  }

  const updater: RivuneTvUpdater = Object.freeze({
    getState: () => publicState,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    async markHealthy() {
      if (bootHealthy) return;
      bootHealthy = true;
      window.clearTimeout(bootTimer);
      window.removeEventListener("error", bootError);
      window.removeEventListener("unhandledrejection", bootError);
      if (persisted.pendingVersion === prepared.runtime.version) {
        persisted = { ...persisted, previous: null, pendingVersion: null };
        try { await save(); } catch { /* A failed health write safely rolls back on the next launch. */ }
      }
    },
    checkAutomatically: () => check(false),
    checkManually: () => check(true),
    async download() {
      if (!availableRelease || operationRunning) return;
      operationRunning = true;
      try { await download(availableRelease, false); }
      finally { operationRunning = false; }
    },
    async restart() {
      if (!store || !persisted.staged) return;
      window.location.reload();
    },
  });
  window.RivuneUpdater = updater;
  executeRuntime(prepared.runtime, selectedPlatform);
}

void bootstrap().catch(() => fatal("Rivune could not load its packaged TV runtime."));
