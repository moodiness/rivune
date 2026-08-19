import type { Collection, ContinueItem, ResolvedFolder } from "./types";

const homeStoragePrefix = "rivune.home-cache.v2";
const continueStoragePrefix = "rivune.home-continue-cache.v1";
const freshAgeMilliseconds = 5 * 60 * 1000;
const maximumAgeMilliseconds = 24 * 60 * 60 * 1000;
const maximumSerializedHomeLength = 2_000_000;
const maximumCachedItemsPerFolder = 5;

export type CachedContinueItem = ContinueItem;

export type HomeCacheSnapshot = {
  collections: Collection[];
  folders: Record<string, ResolvedFolder>;
  signature: string;
  updatedAt: number;
};

type StoredHomeCache = HomeCacheSnapshot & { version: 1 };
type StoredContinueCache = { version: 1; updatedAt: number; items: CachedContinueItem[] };
const demoHomeCaches = new Map<string, StoredHomeCache>();
const demoContinueCaches = new Map<string, StoredContinueCache>();

function cacheKey(prefix: string, profileID: string, scope: string): string {
  return `${prefix}.${encodeURIComponent(profileID)}.${encodeURIComponent(scope)}`;
}

export function homeFolderCacheKey(collectionID: string, folderID: string): string {
  return `${collectionID}:${folderID}`;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function validCollection(value: unknown): value is Collection {
  const candidate = record(value);
  return typeof candidate?.id === "string" && typeof candidate.title === "string" && Array.isArray(candidate.folders);
}

function validResolvedFolder(value: unknown): value is ResolvedFolder {
  const candidate = record(value);
  const folder = record(candidate?.folder);
  return typeof candidate?.collectionId === "string"
    && typeof folder?.title === "string"
    && Array.isArray(folder.sources)
    && Array.isArray(candidate.items)
    && candidate.items.every((item) => {
      const media = record(item);
      return typeof media?.id === "string" && typeof media.mediaType === "string" && typeof media.title === "string";
    })
    && typeof candidate.page === "number"
    && typeof candidate.hasMore === "boolean"
    && Array.isArray(candidate.errors);
}

function validContinueItem(value: unknown): value is CachedContinueItem {
  const candidate = record(value);
  return typeof candidate?.titleId === "string"
    && (candidate.mediaType === "movie" || candidate.mediaType === "episode")
    && typeof candidate.positionSeconds === "number"
    && typeof candidate.durationSeconds === "number"
    && typeof candidate.version === "number"
    && (candidate.reason === "resume" || candidate.reason === "next_episode")
    && typeof candidate.lastWatchedAt === "string";
}

function stableJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(",")}]`;
  const candidate = record(value);
  if (candidate) return `{${Object.keys(candidate).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(candidate[key])}`).join(",")}}`;
  return JSON.stringify(value) ?? "null";
}

export function homeCollectionSignature(collections: Collection[]): string {
  return stableJSON(collections);
}

function parseHomeCache(serialized: string | null): HomeCacheSnapshot | undefined {
  try {
    const parsed = record(JSON.parse(serialized ?? "null"));
    if (parsed?.version !== 1 || typeof parsed.updatedAt !== "number" || !Number.isFinite(parsed.updatedAt)) return undefined;
    if (Date.now() - parsed.updatedAt > maximumAgeMilliseconds || typeof parsed.signature !== "string" || !Array.isArray(parsed.collections) || !parsed.collections.every(validCollection)) return undefined;
    const rawFolders = record(parsed.folders);
    if (!rawFolders) return undefined;
    const folders = Object.fromEntries(Object.entries(rawFolders).filter((entry): entry is [string, ResolvedFolder] => validResolvedFolder(entry[1])));
    return {
      collections: parsed.collections,
      folders,
      signature: parsed.signature,
      updatedAt: parsed.updatedAt,
    };
  } catch {
    return undefined;
  }
}

export function readHomeCache(profileID: string, scope: string): HomeCacheSnapshot | undefined {
  const key = cacheKey(homeStoragePrefix, profileID, scope);
  if (profileID.startsWith("demo-")) {
    const cached = demoHomeCaches.get(key);
    if (!cached || Date.now() - cached.updatedAt > maximumAgeMilliseconds) {
      demoHomeCaches.delete(key);
      return undefined;
    }
    return cached;
  }
  try {
    const persisted = parseHomeCache(localStorage.getItem(key));
    if (persisted) return persisted;
    localStorage.removeItem(key);
  } catch {
    // Fall back to the tab cache when persistent storage is unavailable.
  }
  try {
    const serialized = sessionStorage.getItem(key);
    const cached = parseHomeCache(serialized);
    if (!cached) return undefined;
    try {
      localStorage.setItem(key, serialized!);
      sessionStorage.removeItem(key);
    } catch {
      // The valid tab cache remains usable when migration cannot persist.
    }
    return cached;
  } catch {
    return undefined;
  }
}

function cacheProjection(resolved: ResolvedFolder): ResolvedFolder {
  if (resolved.items.length <= maximumCachedItemsPerFolder) return resolved;
  return {
    ...resolved,
    items: resolved.items.slice(0, maximumCachedItemsPerFolder),
    page: 0,
    hasMore: true,
  };
}

export function writeHomeCache(profileID: string, scope: string, collections: Collection[], rows: Array<{ resolved: ResolvedFolder }>, updatedAt = Date.now()) {
  const directCollectionIDs = new Set(collections.filter((collection) => collection.viewMode === "follow_layout").map((collection) => collection.id));
  const folders = Object.fromEntries(rows.flatMap(({ resolved }) => resolved.folder.id ? [[homeFolderCacheKey(resolved.collectionId, resolved.folder.id), directCollectionIDs.has(resolved.collectionId) ? resolved : cacheProjection(resolved)] as const] : []));
  const document: StoredHomeCache = {
    version: 1,
    collections,
    folders,
    signature: homeCollectionSignature(collections),
    updatedAt,
  };
  if (profileID.startsWith("demo-")) {
    demoHomeCaches.set(cacheKey(homeStoragePrefix, profileID, scope), document);
    return;
  }
  let serialized = JSON.stringify(document);
  const folderIDs = Object.keys(document.folders);
  while (serialized.length > maximumSerializedHomeLength && folderIDs.length > 0) {
    delete document.folders[folderIDs.pop()!];
    serialized = JSON.stringify(document);
  }
  const key = cacheKey(homeStoragePrefix, profileID, scope);
  try {
    localStorage.setItem(key, serialized);
    sessionStorage.removeItem(key);
  } catch {
    try {
      sessionStorage.setItem(key, serialized);
    } catch {
      // A disabled or full browser store must not block Home.
    }
  }
}

export function writeHomeFolderCache(profileID: string, scope: string, collections: Collection[], resolved: ResolvedFolder) {
  const existing = readHomeCache(profileID, scope);
  const signature = homeCollectionSignature(collections);
  const folders = existing?.signature === signature ? { ...existing.folders } : {};
  if (resolved.folder.id) folders[homeFolderCacheKey(resolved.collectionId, resolved.folder.id)] = resolved;
  writeHomeCache(profileID, scope, collections, Object.values(folders).map((folder) => ({ resolved: folder })));
}

export function readContinueCache(profileID: string, scope: string): { items: CachedContinueItem[]; fresh: boolean } | undefined {
  if (profileID.startsWith("demo-")) {
    const cached = demoContinueCaches.get(cacheKey(continueStoragePrefix, profileID, scope));
    if (!cached || Date.now() - cached.updatedAt > maximumAgeMilliseconds) return undefined;
    return { items: cached.items, fresh: Date.now() - cached.updatedAt <= freshAgeMilliseconds };
  }
  try {
    const parsed = record(JSON.parse(sessionStorage.getItem(cacheKey(continueStoragePrefix, profileID, scope)) ?? "null"));
    if (parsed?.version !== 1 || typeof parsed.updatedAt !== "number" || !Number.isFinite(parsed.updatedAt) || !Array.isArray(parsed.items) || !parsed.items.every(validContinueItem)) return undefined;
    if (Date.now() - parsed.updatedAt > maximumAgeMilliseconds) return undefined;
    return { items: parsed.items, fresh: Date.now() - parsed.updatedAt <= freshAgeMilliseconds };
  } catch {
    return undefined;
  }
}

export function writeContinueCache(profileID: string, scope: string, items: CachedContinueItem[]) {
  const document: StoredContinueCache = { version: 1, updatedAt: Date.now(), items };
  if (profileID.startsWith("demo-")) {
    demoContinueCaches.set(cacheKey(continueStoragePrefix, profileID, scope), document);
    return;
  }
  try {
    sessionStorage.setItem(cacheKey(continueStoragePrefix, profileID, scope), JSON.stringify(document));
  } catch {
    // A disabled or full session store must not block Continue Watching.
  }
}

const transientMediaPrefixes = [
  homeStoragePrefix,
  continueStoragePrefix,
  "rivune.search.",
  "rivune.notifications.",
  "rivune.metadata-cache.",
] as const;

function clearMatchingStorage(storage: Storage): void {
  try {
    const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index))
      .filter((key): key is string => key !== null && transientMediaPrefixes.some((prefix) => key.startsWith(prefix)));
    keys.forEach((key) => storage.removeItem(key));
  } catch {
    // Storage isolation must still proceed when a browser blocks a storage area.
  }
}

export function clearMediaCaches(): void {
  clearMatchingStorage(localStorage);
  demoHomeCaches.clear();
  demoContinueCaches.clear();
  clearMatchingStorage(sessionStorage);
}
