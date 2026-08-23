import type { RivuneRuntimePlatform } from "../update-api";

export const releaseEndpoint = "https://api.github.com/repos/moodiness/rivune/releases/latest";
export const runtimeFileName = "Rivune-TV-runtime.json";
export const maximumReleaseBytes = 512 * 1024;
export const maximumRuntimeBytes = 16 * 1024 * 1024;

const stableVersionPattern = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const stableTagPattern = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const sha256Pattern = /^sha256:([0-9a-f]{64})$/;
const publishedAtPattern = /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})$/;

const expectedAssetNames = [
  "Rivune-Android.apk",
  "Rivune-Tizen.wgt",
  "Rivune-TV-runtime.json",
  "Rivune-arm64.exe",
  "Rivune-iOS-unsigned.ipa",
  "Rivune-macOS.dmg",
  "Rivune-tvOS-unsigned.ipa",
  "Rivune-visionOS-unsigned.ipa",
  "Rivune-webOS.ipk",
  "Rivune-x64.exe",
  "rivune-update.json",
] as const;

export type RuntimeBundle = {
  schemaVersion: 1;
  version: string;
  application: {
    javascript: string;
    css: string;
  };
  platforms: Record<RivuneRuntimePlatform, { javascript: string }>;
};

export type ReleaseArtifact = {
  size: number;
  sha256: string;
  mirrorUrl: string;
};

export type AvailableRelease = {
  version: string;
  tagName: string;
  manifest: ReleaseArtifact;
  runtime: Omit<ReleaseArtifact, "mirrorUrl">;
};

export type RuntimeRelease = {
  version: string;
  tagName: string;
  size: number;
  sha256: string;
  mirrorUrl: string;
};

export type ReleaseCheck =
  | { updateAvailable: false; version: string }
  | { updateAvailable: true; release: AvailableRelease };

export type StoredRuntimeState = {
  schemaVersion: 1;
  active: RuntimeBundle | null;
  previous: RuntimeBundle | null;
  staged: RuntimeBundle | null;
  pendingVersion: string | null;
  lastSuccessfulCheckAt: number;
};

export type PreparedRuntime = {
  state: StoredRuntimeState;
  runtime: RuntimeBundle;
  source: "packaged" | "cached";
};

function record(value: unknown, context: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${context} must be an object.`);
  }
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, expected: readonly string[], context: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) {
    throw new Error(`${context} has an unsupported shape.`);
  }
}

function requiredString(value: Record<string, unknown>, key: string, context: string): string {
  const text = value[key];
  if (typeof text !== "string" || text.length === 0) throw new Error(`${context}.${key} is invalid.`);
  return text;
}

function requiredSafeInteger(value: Record<string, unknown>, key: string, context: string): number {
  const number = value[key];
  if (typeof number !== "number" || !Number.isSafeInteger(number) || number <= 0) {
    throw new Error(`${context}.${key} is invalid.`);
  }
  return number;
}

function compareDecimal(left: string, right: string): number {
  if (left.length !== right.length) return left.length < right.length ? -1 : 1;
  return left === right ? 0 : left < right ? -1 : 1;
}

export function isStableVersion(value: string): boolean {
  return stableVersionPattern.test(value);
}

export function compareStableVersions(left: string, right: string): number {
  if (!isStableVersion(left) || !isStableVersion(right)) throw new Error("A stable semantic version is invalid.");
  const leftParts = left.split(".");
  const rightParts = right.split(".");
  for (let index = 0; index < 3; index += 1) {
    const result = compareDecimal(leftParts[index], rightParts[index]);
    if (result !== 0) return result;
  }
  return 0;
}

export function parseLatestRelease(value: unknown, currentVersion: string, platform: RivuneRuntimePlatform): ReleaseCheck {
  if (!isStableVersion(currentVersion)) throw new Error("The current TV runtime version is invalid.");
  const release = record(value, "GitHub release");
  const tagName = requiredString(release, "tag_name", "GitHub release");
  if (!stableTagPattern.test(tagName)) throw new Error("The GitHub release tag is invalid.");
  const version = tagName.slice(1);
  if (release.name !== tagName || release.draft !== false || release.prerelease !== false) {
    throw new Error("The GitHub release is not a published stable release.");
  }
  if (release.html_url !== `https://github.com/moodiness/rivune/releases/tag/${tagName}`) {
    throw new Error("The GitHub release URL is invalid.");
  }
  const publishedAt = release.published_at;
  if (typeof publishedAt !== "string" || !publishedAtPattern.test(publishedAt) || Number.isNaN(Date.parse(publishedAt))) {
    throw new Error("The GitHub release publication date is invalid.");
  }
  if (compareStableVersions(version, currentVersion) <= 0) return { updateAvailable: false, version };

  if (!Array.isArray(release.assets) || release.assets.length !== expectedAssetNames.length) {
    throw new Error("The GitHub release asset set is incomplete.");
  }
  const assets = new Map<string, Record<string, unknown>>();
  for (const candidate of release.assets) {
    const asset = record(candidate, "GitHub release asset");
    const name = requiredString(asset, "name", "GitHub release asset");
    if (!(expectedAssetNames as readonly string[]).includes(name) || assets.has(name)) {
      throw new Error("The GitHub release asset set is invalid.");
    }
    const id = requiredSafeInteger(asset, "id", `GitHub release asset ${name}`);
    const size = requiredSafeInteger(asset, "size", `GitHub release asset ${name}`);
    if (size > 2_147_483_647 || asset.state !== "uploaded") throw new Error(`GitHub release asset ${name} is invalid.`);
    if (asset.url !== `https://api.github.com/repos/moodiness/rivune/releases/assets/${id}`) {
      throw new Error(`GitHub release asset ${name} API URL is invalid.`);
    }
    if (asset.browser_download_url !== `https://github.com/moodiness/rivune/releases/download/${tagName}/${name}`) {
      throw new Error(`GitHub release asset ${name} download URL is invalid.`);
    }
    if (typeof asset.digest !== "string" || !sha256Pattern.test(asset.digest)) {
      throw new Error(`GitHub release asset ${name} digest is invalid.`);
    }
    assets.set(name, asset);
  }
  const platformAsset = platform === "webos" ? "Rivune-webOS.ipk" : "Rivune-Tizen.wgt";
  if (!assets.has(platformAsset)) throw new Error(`The GitHub release has no ${platform} package.`);
  const runtime = assets.get(runtimeFileName);
  const manifest = assets.get("rivune-update.json");
  if (!runtime || !manifest) throw new Error("The GitHub release has no TV update manifest or runtime.");
  const runtimeSize = runtime.size as number;
  const manifestSize = manifest.size as number;
  if (runtimeSize > maximumRuntimeBytes || manifestSize > 256 * 1024) throw new Error("The GitHub TV update metadata is too large.");
  return {
    updateAvailable: true,
    release: {
      version,
      tagName,
      manifest: {
        size: manifestSize,
        sha256: (manifest.digest as string).slice("sha256:".length),
        mirrorUrl: `https://moodiness.github.io/rivune/tv-runtime/${tagName}/rivune-update.json`,
      },
      runtime: {
        size: runtimeSize,
        sha256: (runtime.digest as string).slice("sha256:".length),
      },
    },
  };
}

function exactStringArray(value: unknown, expected: readonly string[], context: string): void {
  if (!Array.isArray(value) || value.length !== expected.length || value.some((entry, index) => entry !== expected[index])) {
    throw new Error(`${context} is invalid.`);
  }
}

export function parseUpdateManifest(value: unknown, release: AvailableRelease, platform: RivuneRuntimePlatform): RuntimeRelease {
  const manifest = record(value, "update manifest");
  exactKeys(manifest, ["schemaVersion", "channel", "version", "tagName", "publishedAt", "releaseUrl", "packages"], "update manifest");
  if (manifest.schemaVersion !== 2 || manifest.channel !== "stable" || manifest.version !== release.version || manifest.tagName !== release.tagName) {
    throw new Error("The update manifest release identity is invalid.");
  }
  const publishedAt = requiredString(manifest, "publishedAt", "update manifest");
  if (!publishedAtPattern.test(publishedAt) || Number.isNaN(Date.parse(publishedAt))) throw new Error("The update manifest publication date is invalid.");
  if (manifest.releaseUrl !== `https://github.com/moodiness/rivune/releases/tag/${release.tagName}`) {
    throw new Error("The update manifest release URL is invalid.");
  }
  const packages = record(manifest.packages, "update manifest packages");
  exactKeys(packages, ["android", "ios", "tvos", "visionos", "macos", "webos", "tizen", "tvRuntime", "windowsX64", "windowsArm64"], "update manifest packages");

  const platformPackage = record(packages[platform], `update manifest ${platform} package`);
  exactKeys(platformPackage, ["format", "architectures", "minimumOsVersion", "applicationId", "signature", "fileName", "url", "size", "sha256"], `update manifest ${platform} package`);
  const expectedPlatform = platform === "webos"
    ? { format: "ipk", minimumOsVersion: "4.0", applicationId: "io.rivune.app.webos", fileName: "Rivune-webOS.ipk" }
    : { format: "wgt", minimumOsVersion: "5.5", applicationId: "RivuneTV01.Rivune", fileName: "Rivune-Tizen.wgt" };
  if (
    platformPackage.format !== expectedPlatform.format ||
    platformPackage.minimumOsVersion !== expectedPlatform.minimumOsVersion ||
    platformPackage.applicationId !== expectedPlatform.applicationId ||
    platformPackage.signature !== "unsigned" ||
    platformPackage.fileName !== expectedPlatform.fileName
  ) throw new Error(`The update manifest ${platform} package is invalid.`);
  exactStringArray(platformPackage.architectures, ["universal"], `update manifest ${platform} architectures`);
  if (platformPackage.url !== `https://github.com/moodiness/rivune/releases/download/${release.tagName}/${expectedPlatform.fileName}`) {
    throw new Error(`The update manifest ${platform} package URL is invalid.`);
  }
  requiredSafeInteger(platformPackage, "size", `update manifest ${platform} package`);
  if (typeof platformPackage.sha256 !== "string" || !/^[0-9a-f]{64}$/.test(platformPackage.sha256)) {
    throw new Error(`The update manifest ${platform} package digest is invalid.`);
  }

  const runtime = record(packages.tvRuntime, "update manifest TV runtime");
  exactKeys(runtime, ["format", "platforms", "fileName", "url", "size", "sha256"], "update manifest TV runtime");
  if (runtime.format !== "json" || runtime.fileName !== runtimeFileName) throw new Error("The update manifest TV runtime is invalid.");
  exactStringArray(runtime.platforms, ["webos", "tizen"], "update manifest TV runtime platforms");
  if (runtime.url !== `https://github.com/moodiness/rivune/releases/download/${release.tagName}/${runtimeFileName}`) {
    throw new Error("The update manifest TV runtime URL is invalid.");
  }
  const size = requiredSafeInteger(runtime, "size", "update manifest TV runtime");
  const digest = requiredString(runtime, "sha256", "update manifest TV runtime");
  if (size > maximumRuntimeBytes || size !== release.runtime.size || !/^[0-9a-f]{64}$/.test(digest) || digest !== release.runtime.sha256) {
    throw new Error("The update manifest TV runtime does not match the GitHub release asset.");
  }
  return {
    version: release.version,
    tagName: release.tagName,
    size,
    sha256: digest,
    mirrorUrl: `https://moodiness.github.io/rivune/tv-runtime/${release.tagName}/${runtimeFileName}`,
  };
}

export function parseRuntimeBundle(value: unknown, expectedVersion?: string): RuntimeBundle {
  const runtime = record(value, "TV runtime");
  exactKeys(runtime, ["schemaVersion", "version", "application", "platforms"], "TV runtime");
  if (runtime.schemaVersion !== 1) throw new Error("The TV runtime schema is unsupported.");
  const version = requiredString(runtime, "version", "TV runtime");
  if (!isStableVersion(version) || (expectedVersion !== undefined && version !== expectedVersion)) {
    throw new Error("The TV runtime version is invalid.");
  }
  const application = record(runtime.application, "TV runtime application");
  exactKeys(application, ["javascript", "css"], "TV runtime application");
  const javascript = requiredString(application, "javascript", "TV runtime application");
  const css = requiredString(application, "css", "TV runtime application");
  if (javascript.length > 8 * 1024 * 1024 || css.length > 2 * 1024 * 1024) {
    throw new Error("The TV runtime application is too large.");
  }
  const platforms = record(runtime.platforms, "TV runtime platforms");
  exactKeys(platforms, ["webos", "tizen"], "TV runtime platforms");
  const parsedPlatforms = {} as RuntimeBundle["platforms"];
  for (const platform of ["webos", "tizen"] as const) {
    const entry = record(platforms[platform], `TV runtime platform ${platform}`);
    exactKeys(entry, ["javascript"], `TV runtime platform ${platform}`);
    const platformJavascript = requiredString(entry, "javascript", `TV runtime platform ${platform}`);
    if (platformJavascript.length > 2 * 1024 * 1024) throw new Error(`The ${platform} runtime adapter is too large.`);
    parsedPlatforms[platform] = { javascript: platformJavascript };
  }
  return {
    schemaVersion: 1,
    version,
    application: { javascript, css },
    platforms: parsedPlatforms,
  };
}

export function emptyStoredRuntimeState(): StoredRuntimeState {
  return {
    schemaVersion: 1,
    active: null,
    previous: null,
    staged: null,
    pendingVersion: null,
    lastSuccessfulCheckAt: 0,
  };
}

export function parseStoredRuntimeState(value: unknown): StoredRuntimeState {
  try {
    const state = record(value, "stored TV runtime state");
    exactKeys(state, ["schemaVersion", "active", "previous", "staged", "pendingVersion", "lastSuccessfulCheckAt"], "stored TV runtime state");
    if (state.schemaVersion !== 1) throw new Error("Unsupported stored state.");
    const parseOptionalRuntime = (candidate: unknown) => candidate === null ? null : parseRuntimeBundle(candidate);
    const pendingVersion = state.pendingVersion;
    if (pendingVersion !== null && (typeof pendingVersion !== "string" || !isStableVersion(pendingVersion))) {
      throw new Error("Invalid pending version.");
    }
    const checkedAt = state.lastSuccessfulCheckAt;
    if (typeof checkedAt !== "number" || !Number.isSafeInteger(checkedAt) || checkedAt < 0) {
      throw new Error("Invalid update check time.");
    }
    return {
      schemaVersion: 1,
      active: parseOptionalRuntime(state.active),
      previous: parseOptionalRuntime(state.previous),
      staged: parseOptionalRuntime(state.staged),
      pendingVersion,
      lastSuccessfulCheckAt: checkedAt,
    };
  } catch {
    return emptyStoredRuntimeState();
  }
}

export function prepareRuntime(packaged: RuntimeBundle, stored: StoredRuntimeState): PreparedRuntime {
  const state: StoredRuntimeState = { ...stored };
  if (state.pendingVersion !== null) {
    state.active = state.previous;
    state.previous = null;
    state.staged = null;
    state.pendingVersion = null;
  }
  if (state.active !== null && compareStableVersions(state.active.version, packaged.version) <= 0) {
    state.active = null;
    state.previous = null;
  }
  const current = state.active ?? packaged;
  if (state.staged !== null && compareStableVersions(state.staged.version, current.version) > 0) {
    state.previous = state.active;
    state.active = state.staged;
    state.pendingVersion = state.staged.version;
  }
  state.staged = null;
  const runtime = state.active ?? packaged;
  return { state, runtime, source: state.active === null ? "packaged" : "cached" };
}

export function rollbackRuntime(state: StoredRuntimeState): StoredRuntimeState {
  return {
    ...state,
    active: state.previous,
    previous: null,
    staged: null,
    pendingVersion: null,
  };
}
