import type { MediaItem } from "./types";

const storageKey = "rivune.metadata-cache.v1";
const maximumSnapshots = 256;
const maximumAgeMilliseconds = 30 * 24 * 60 * 60 * 1000;

type MetadataSnapshot = {
  title?: string;
  posterUrl?: string;
  backgroundUrl?: string;
  logoUrl?: string;
  description?: string;
  releaseInfo?: string;
  released?: string;
  voteAverage?: number;
  voteCount?: number;
  externalIds?: Record<string, string>;
  genres?: unknown[];
  cast?: unknown[];
};

type CacheEntry = {
  updatedAt: number;
  value: MetadataSnapshot;
};

type CacheDocument = {
  version: 1;
  snapshots: Record<string, CacheEntry>;
  aliases: Record<string, string>;
};

let loadedDocument: CacheDocument | undefined;

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function cacheDocument(): CacheDocument {
  if (loadedDocument) return loadedDocument;
  const empty: CacheDocument = { version: 1, snapshots: {}, aliases: {} };
  try {
    const parsed = record(JSON.parse(localStorage.getItem(storageKey) ?? "null"));
    if (parsed?.version !== 1) return loadedDocument = empty;
    const snapshots = record(parsed.snapshots);
    const aliases = record(parsed.aliases);
    if (!snapshots || !aliases) return loadedDocument = empty;
    for (const [key, rawEntry] of Object.entries(snapshots)) {
      const entry = record(rawEntry);
      const value = record(entry?.value);
      if (typeof entry?.updatedAt === "number" && Number.isFinite(entry.updatedAt) && value) {
        empty.snapshots[key] = { updatedAt: entry.updatedAt, value: value as MetadataSnapshot };
      }
    }
    for (const [key, target] of Object.entries(aliases)) {
      if (typeof target === "string" && empty.snapshots[target]) empty.aliases[key] = target;
    }
  } catch {
    // A disabled, full, or corrupted browser store must not block media navigation.
  }
  loadedDocument = empty;
  return empty;
}

function normalized(value: string | undefined): string {
  return value?.trim().slice(0, 1024) ?? "";
}

function identityKeys(item: MediaItem, locale: string, titleID?: string): string[] {
  const prefix = `${normalized(locale).toLowerCase() || "und"}|${normalized(item.mediaType).toLowerCase()}|`;
  const identities: string[] = [];
  const canonicalTitleID = normalized(titleID);
  const itemTitleID = normalized(item.titleId);
  if (canonicalTitleID) identities.push(`title:${canonicalTitleID}`);
  if (itemTitleID) identities.push(`title:${itemTitleID}`);
  const resourceID = normalized(item.resourceId || item.id);
  const resourceIdentities: string[] = [];
  if (resourceID) {
    const tvSourceIdentity = item.mediaType === "tv" ? normalized(item.sourceAddonId) : "";
    if (tvSourceIdentity) resourceIdentities.push(`resource:${tvSourceIdentity}:${resourceID}`);
    for (const source of item.sources ?? []) {
      const sourceIdentity = normalized(source.addonId || source.id);
      if (sourceIdentity && !resourceIdentities.includes(`resource:${sourceIdentity}:${resourceID}`)) resourceIdentities.push(`resource:${sourceIdentity}:${resourceID}`);
    }
    if (resourceIdentities.length === 0) resourceIdentities.push(`resource:${resourceID}`);
  }
  if (item.mediaType === "episode") identities.push(...resourceIdentities);
  for (const [provider, externalID] of Object.entries(item.externalIds ?? {}).sort(([left], [right]) => left.localeCompare(right))) {
    const normalizedProvider = normalized(provider).toLowerCase();
    const normalizedExternalID = normalized(externalID);
    if (normalizedProvider && normalizedExternalID) identities.push(`external:${normalizedProvider}:${normalizedExternalID}`);
  }
  if (item.mediaType !== "episode") identities.push(...resourceIdentities);
  return Array.from(new Set(identities.map((identity) => prefix + identity)));
}

function cachedEntry(document: CacheDocument, keys: string[]): { key: string; entry: CacheEntry } | undefined {
  const now = Date.now();
  for (const key of keys) {
    const target = document.aliases[key] ?? key;
    const entry = document.snapshots[target];
    if (entry && now - entry.updatedAt <= maximumAgeMilliseconds) return { key: target, entry };
  }
  return undefined;
}

function populatedString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

export function cachedMediaItem(item: MediaItem, locale: string): MediaItem {
  const selected = cachedEntry(cacheDocument(), identityKeys(item, locale));
  if (!selected) return item;
  const cached = selected.entry.value;
  const result: MediaItem = { ...item };
  for (const key of ["title", "posterUrl", "backgroundUrl", "logoUrl", "description", "releaseInfo", "released"] as const) {
    const value = populatedString(cached[key]);
    if (value) result[key] = value;
  }
  if (typeof cached.voteAverage === "number" && Number.isFinite(cached.voteAverage)) result.voteAverage = cached.voteAverage;
  if (typeof cached.voteCount === "number" && Number.isFinite(cached.voteCount)) result.voteCount = cached.voteCount;
  const externalIDs = record(cached.externalIds);
  if (externalIDs) {
    result.externalIds = { ...item.externalIds };
    for (const [provider, externalID] of Object.entries(externalIDs)) {
      if (typeof externalID === "string" && externalID.trim()) result.externalIds[provider] = externalID;
    }
  }
  if (Array.isArray(cached.genres) || Array.isArray(cached.cast)) {
    result.raw = {
      ...item.raw,
      ...(Array.isArray(cached.genres) ? { genres: cached.genres } : {}),
      ...(Array.isArray(cached.cast) ? { cast: cached.cast } : {}),
    };
  }
  return result;
}

function snapshot(details: MediaItem): MetadataSnapshot {
  const value: MetadataSnapshot = {};
  for (const key of ["title", "posterUrl", "backgroundUrl", "logoUrl", "description", "releaseInfo", "released"] as const) {
    const field = populatedString(details[key]);
    if (field) value[key] = field;
  }
  if (typeof details.voteAverage === "number" && Number.isFinite(details.voteAverage)) value.voteAverage = details.voteAverage;
  if (typeof details.voteCount === "number" && Number.isFinite(details.voteCount)) value.voteCount = details.voteCount;
  if (details.externalIds && Object.keys(details.externalIds).length > 0) value.externalIds = { ...details.externalIds };
  if (Array.isArray(details.raw?.genres)) value.genres = details.raw.genres;
  if (Array.isArray(details.raw?.cast)) value.cast = details.raw.cast;
  return value;
}

function persist(document: CacheDocument) {
  const retained = Object.entries(document.snapshots)
    .filter(([, entry]) => Date.now() - entry.updatedAt <= maximumAgeMilliseconds)
    .sort(([, left], [, right]) => right.updatedAt - left.updatedAt)
    .slice(0, maximumSnapshots);
  document.snapshots = Object.fromEntries(retained);
  const targets = new Set(retained.map(([key]) => key));
  document.aliases = Object.fromEntries(Object.entries(document.aliases).filter(([, target]) => targets.has(target)));
  try {
    localStorage.setItem(storageKey, JSON.stringify(document));
  } catch {
    // The in-memory cache remains useful when persistent browser storage is unavailable.
  }
}

export function cacheMediaItem(item: MediaItem, details: MediaItem, locale: string, titleID?: string) {
  const keys = identityKeys(item, locale, titleID);
  if (keys.length === 0) return;
  const document = cacheDocument();
  const previous = cachedEntry(document, keys);
  const target = keys[0];
  document.snapshots[target] = {
    updatedAt: Date.now(),
    value: { ...previous?.entry.value, ...snapshot(details) },
  };
  if (previous && previous.key !== target) delete document.snapshots[previous.key];
  for (const [alias, existingTarget] of Object.entries(document.aliases)) {
    if (previous && existingTarget === previous.key) document.aliases[alias] = target;
  }
  for (const key of keys) document.aliases[key] = target;
  persist(document);
}

export function clearMetadataCache(): void {
  loadedDocument = undefined;
  try {
    localStorage.removeItem(storageKey);
  } catch {
    // The next cache read still starts from a fresh in-memory document.
  }
}
