import { safeLocalStorage } from "./browserStorage";
import type { NetworkClass, PlaybackCapabilities, QualityPreset, QualityPreferences } from "./types";

const preferencesKey = "rivune.playback-quality.v1";
const presets: Record<QualityPreset, true> = { automatic: true, economy: true, balanced: true, maximum: true };
const defaults: QualityPreferences = { local: "automatic", remote_wifi: "automatic", mobile: "automatic" };

function privateHost(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  return host === "localhost" || host === "::1" || host.endsWith(".local") || /^127\./.test(host) || /^10\./.test(host) || /^192\.168\./.test(host) || /^172\.(1[6-9]|2\d|3[01])\./.test(host);
}

export function classifyNetwork(): NetworkClass {
  const connection = (navigator as Navigator & { connection?: { type?: string; effectiveType?: string } }).connection;
  if (connection?.type === "cellular" || connection?.effectiveType === "slow-2g" || connection?.effectiveType === "2g" || connection?.effectiveType === "3g") return "mobile";
  if (privateHost(window.location.hostname)) return "local";
  return connection?.type === "wifi" || connection?.effectiveType === "4g" ? "remote_wifi" : "mobile";
}

export function loadQualityPreferences(): QualityPreferences {
  try {
    const value = JSON.parse(safeLocalStorage.getItem(preferencesKey) ?? "null") as Partial<QualityPreferences> | null;
    if (!value) return { ...defaults };
    return Object.fromEntries((Object.keys(defaults) as NetworkClass[]).map((network) => [network, presets[value[network] as QualityPreset] ? value[network] : defaults[network]])) as QualityPreferences;
  } catch {
    return { ...defaults };
  }
}

export function saveQualityPreference(network: NetworkClass, preset: QualityPreset): QualityPreferences {
  const next = { ...loadQualityPreferences(), [network]: preset };
  safeLocalStorage.setItem(preferencesKey, JSON.stringify(next));
  return next;
}

export function qualityLimits(network: NetworkClass, preset: QualityPreset): { maximumHeight?: number; maximumVideoBitrateKbps?: number } {
  if (preset === "economy") return { maximumHeight: 480, maximumVideoBitrateKbps: 2_000 };
  if (preset === "balanced") return { maximumHeight: 1080, maximumVideoBitrateKbps: 8_000 };
  if (preset === "automatic" && network === "mobile") return { maximumHeight: 720, maximumVideoBitrateKbps: 5_000 };
  return {};
}

function minimumPositive(capacity: number | undefined, limit: number | undefined): number | undefined {
  if (!capacity || capacity <= 0) return limit;
  if (!limit || limit <= 0) return capacity;
  return Math.min(capacity, limit);
}

export function applyQualityLimits(capabilities: PlaybackCapabilities, network = classifyNetwork(), preferences = loadQualityPreferences()): PlaybackCapabilities {
  const limit = qualityLimits(network, preferences[network]);
  return {
    ...capabilities,
    maximumHeight: minimumPositive(capabilities.maximumHeight, limit.maximumHeight),
    maximumVideoBitrateKbps: minimumPositive(capabilities.maximumVideoBitrateKbps, limit.maximumVideoBitrateKbps),
  };
}
