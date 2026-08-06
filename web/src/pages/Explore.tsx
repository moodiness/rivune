import { ArrowLeft, ArrowRight, Bookmark, Check, Clapperboard, Compass, Film, Info, ListVideo, LoaderCircle, Play, Radio, RefreshCw, RotateCcw, Search, Shapes, Sparkles, Star, Trash2, Tv, WandSparkles, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { principalIdentity, useAuth } from "../auth";
import { ActionMenu, Button, EmptyState, HorizontalDragRow, MediaCard, Notice, SectionHeading, Skeleton, handleDirectionalFocus } from "../components";
import { translate as t } from "../i18n";
import { mediaFromLibraryItem, mediaIdentity, resolveMediaTitle } from "../mediaIdentity";
import { homeCollectionSignature, homeFolderCacheKey, readContinueCache, readHomeCache, writeContinueCache, writeHomeCache, writeHomeFolderCache, type CachedContinueItem } from "../homeCache";
import { notifyError, notifyErrorMessage, notifySuccess } from "../notifications";
import { mediaTypeLabel } from "../media";
import type { AddonCatalogDescriptor, Collection, CollectionListResponse, CurrentProgram, LibraryItem, LibraryPage, MediaItem, ResolvedFolder, ResourceBatch, ResourceResult, TVLibraryIdentity, TVLibraryMembershipResult } from "../types";
import type { ActionMenuAnchor } from "../components";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";

type OpenMedia = (item: MediaItem) => void;
type MediaPreferences = { profileID: string; hideUnreleased: boolean; animationsEnabled: boolean; maximumDirectTitles: number };
type LoadedLibrary = {
  pages: Record<number, LibraryItem[]>;
  page: number;
  totalPages: number;
  totalResults: number;
};
const initialLibraryRequests = new Map<string, Promise<LibraryPage>>();

function loadInitialLibraryPage(principalScope: string, filter: "" | "movie" | "series" | "tv", pageSize: number): Promise<LibraryPage> {
  const key = `${principalScope}:${api.metadataScope()}:${filter}:${pageSize}`;
  const pending = initialLibraryRequests.get(key);
  if (pending) return pending;

  const request = api.library(filter, 1, pageSize);
  initialLibraryRequests.set(key, request);
  const clear = () => {
    if (initialLibraryRequests.get(key) === request) initialLibraryRequests.delete(key);
  };
  void request.then(clear, clear);
  return request;
}

function libraryItems(library: LoadedLibrary | null): LibraryItem[] {
  const seen = new Set<string>();
  return Object.entries(library?.pages ?? {})
    .sort(([left], [right]) => Number(left) - Number(right))
    .flatMap(([, items]) => items.filter((item) => {
      if (seen.has(item.titleId)) return false;
      seen.add(item.titleId);
      return true;
    }));
}

function tvMembershipKey(sourceAddonId: string, resourceId: string): string {
  return `tv:${sourceAddonId.trim()}:${resourceId.trim()}`;
}

function tvMembershipLookupKey(sourceAddonId: string, resourceId: string): string {
  return `${sourceAddonId.trim().toLowerCase()}\u0000${resourceId.trim()}`;
}

function tvMembershipIdentities(items: MediaItem[]): TVLibraryIdentity[] {
  const identities = new Map<string, TVLibraryIdentity>();
  for (const item of items) {
    if (item.mediaType !== "tv") continue;
    const sourceAddonId = item.sourceAddonId?.trim() ?? "";
    const resourceId = (item.resourceId || item.id).trim();
    if (!sourceAddonId || !resourceId) continue;
    const key = tvMembershipLookupKey(sourceAddonId, resourceId);
    if (!identities.has(key)) identities.set(key, { sourceAddonId, resourceId });
  }
  return [...identities.values()];
}

type TVMembershipMaps = {
  checked: Set<string>;
  saved: Map<string, string>;
};

function tvMembershipMaps(identities: TVLibraryIdentity[], result: TVLibraryMembershipResult): TVMembershipMaps {
  const requestKeys = new Map(identities.map((identity) => [
    tvMembershipLookupKey(identity.sourceAddonId, identity.resourceId),
    tvMembershipKey(identity.sourceAddonId, identity.resourceId),
  ]));
  const checked = new Set(requestKeys.values());
  const saved = new Map(result.items.map((item) => [
    requestKeys.get(tvMembershipLookupKey(item.sourceAddonId, item.resourceId)) ?? tvMembershipKey(item.sourceAddonId, item.resourceId),
    item.titleId,
  ]));
  return { checked, saved };
}

function handleBrowseGridKeyDown(event: ReactKeyboardEvent<HTMLElement>) {
  if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
  const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(".media-card:not(:disabled), .source-folder-card:not(:disabled)"));
  const current = (event.target as HTMLElement).closest<HTMLElement>(".media-card, .source-folder-card");
  if (!current || !controls.includes(current)) return;
  const currentBounds = current.getBoundingClientRect();
  const currentX = currentBounds.left + currentBounds.width / 2;
  const currentY = currentBounds.top + currentBounds.height / 2;
  const horizontal = event.key === "ArrowLeft" || event.key === "ArrowRight";
  const direction = event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 1;
  const candidates = controls.flatMap((control) => {
    if (control === current) return [];
    const bounds = control.getBoundingClientRect();
    const deltaX = bounds.left + bounds.width / 2 - currentX;
    const deltaY = bounds.top + bounds.height / 2 - currentY;
    const primary = horizontal ? deltaX : deltaY;
    if (Math.sign(primary) !== direction) return [];
    const cross = horizontal ? deltaY : deltaX;
    return [{ control, score: Math.abs(primary) + Math.abs(cross) * 4 }];
  });
  const next = candidates.sort((left, right) => left.score - right.score)[0]?.control;
  if (!next) return;
  event.preventDefault();
  next.focus();
  next.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function record(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.fromEntries(Object.entries(value));
}

function stringValue(value: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  }
  return undefined;
}

function currentProgram(value: unknown): CurrentProgram | undefined {
  if (typeof value === "string" && value.trim()) return value.trim();
  const program = record(value);
  if (!program) return undefined;
  const normalized = {
    title: stringValue(program, "title"),
    name: stringValue(program, "name"),
    description: stringValue(program, "description", "overview"),
    start: stringValue(program, "start", "startsAt"),
    end: stringValue(program, "end", "endsAt"),
  };
  return Object.values(normalized).some(Boolean) ? normalized : undefined;
}

function currentProgramTitle(value: CurrentProgram | undefined): string | undefined {
  if (typeof value === "string") return value;
  return value?.title || value?.name;
}

function mediaFromResourceResult(result: ResourceResult): MediaItem[] {
  const output: MediaItem[] = [];
  const metas = result.payload.metas;
  if (!Array.isArray(metas)) return output;
  for (const candidate of metas) {
    const meta = record(candidate);
    if (!meta || typeof meta.id !== "string") continue;
    const mediaType = stringValue(meta, "type") || result.type;
    const addonScoped = mediaType === "tv" || !["movie", "series", "episode"].includes(mediaType);
    const title = stringValue(meta, "name", "title") || t("media.untitled");
    const resourceId = stringValue(meta, "resourceId") || meta.id;
    const sourceAddonId = stringValue(meta, "sourceAddonId") || result.addonId;
    const sourceCatalogId = stringValue(meta, "sourceCatalogId", "catalogId") || result.id;
    const sourceName = stringValue(meta, "sourceName", "source");
    const program = currentProgram(meta.currentProgram);
    const logo = stringValue(meta, "logo", "logoUrl");
    const poster = stringValue(meta, "poster", "posterUrl");
    const background = stringValue(meta, "background", "backgroundUrl", "backdrop");
    output.push({
      id: resourceId,
      resourceId,
      mediaType,
      title,
      posterUrl: mediaType === "tv" ? logo || poster || background : poster,
      backgroundUrl: mediaType === "tv" ? background || poster || logo : background,
      logoUrl: logo,
      description: stringValue(meta, "description", "overview"),
      releaseInfo: stringValue(meta, "releaseInfo"),
      released: stringValue(meta, "released"),
      voteAverage: typeof meta.imdbRating === "number" ? meta.imdbRating : undefined,
      externalIds: {},
      sources: [{ id: result.addonId, kind: "addon_catalog", title: sourceName || "", addonId: result.addonId }],
      sourceAddonId: addonScoped ? sourceAddonId : undefined,
      sourceCatalogId: addonScoped ? sourceCatalogId : undefined,
      sourceName: addonScoped ? sourceName : undefined,
      country: mediaType === "tv" ? stringValue(meta, "country", "countryCode") : undefined,
      language: mediaType === "tv" ? stringValue(meta, "language", "lang") : undefined,
      category: mediaType === "tv" ? stringValue(meta, "category", "genre") : undefined,
      available: mediaType === "tv" ? meta.available !== false : undefined,
      currentProgram: mediaType === "tv" ? program : undefined,
      raw: meta,
    });
  }
  return output;
}

function mediaFromBatch(batch: ResourceBatch): MediaItem[] {
  const output: MediaItem[] = [];
  for (const result of batch.results) {
    for (const item of mediaFromResourceResult(result)) output.push(item);
  }
  return output;
}

function mediaPageIsFull(batch: ResourceBatch, limit: number): boolean {
  return batch.results.some((result) => Array.isArray(result.payload.metas) && result.payload.metas.length >= limit);
}

function summarizeSearchOutcomes(outcomes: PromiseSettledResult<ResourceBatch>[], limit: number) {
  const resourceBatches: ResourceBatch[] = [];
  let httpFailureCount = 0;
  for (const outcome of outcomes) {
    if (outcome.status === "fulfilled") resourceBatches.push(outcome.value);
    else httpFailureCount += 1;
  }
  let internalFailureCount = 0;
  let successfulResourceResultCount = 0;
  const items: MediaItem[] = [];
  for (const batch of resourceBatches) {
    internalFailureCount += batch.errors.length;
    successfulResourceResultCount += batch.results.length;
    items.push(...mediaFromBatch(batch));
  }
  return {
    resourceBatches,
    httpFailureCount,
    internalFailureCount,
    successfulResourceResultCount,
    items,
    hasFullPage: resourceBatches.some((batch) => mediaPageIsFull(batch, limit)),
  };
}

const fallbackSearchTypes = ["movie", "series", "tv"];
const leadingSearchTypes = ["movie", "series", "anime", "other"];

function discoverSearchCatalogs(catalogs: AddonCatalogDescriptor[]): { descriptors: AddonCatalogDescriptor[]; types: string[] } {
  const descriptors = catalogs.filter((descriptor) => !descriptor.addonCatalog && descriptor.searchable && Boolean(descriptor.catalog.type.trim()));
  const seen = new Set<string>();
  const types: string[] = [];
  for (const descriptor of descriptors) {
    const { type } = descriptor.catalog;
    if (seen.has(type)) continue;
    seen.add(type);
    types.push(type);
  }
  return {
    descriptors,
    types: [
      ...leadingSearchTypes.filter((type) => seen.has(type)),
      ...types.filter((type) => !leadingSearchTypes.includes(type) && type !== "tv"),
      ...(seen.has("tv") ? ["tv"] : []),
    ],
  };
}

function searchTypeLabel(type: string): string {
  if (type === "movie") return t("media.filter.movies");
  if (type === "series") return t("media.filter.series");
  if (type === "tv") return t("media.type.liveTv");
  if (type.toLowerCase() === "anime") return mediaTypeLabel(type);
  const words = type.trim().replace(/([a-z0-9])([A-Z])/g, "$1 $2").replace(/[_-]+/g, " ").replace(/\s+/g, " ");
  return words ? words.charAt(0).toLocaleUpperCase() + words.slice(1) : type;
}

function mergeUniqueMedia(current: MediaItem[], incoming: MediaItem[]): MediaItem[] {
  const seen = new Set(current.map(mediaIdentity));
  const additions = incoming.filter((item) => {
    const key = mediaIdentity(item);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  return additions.length > 0 ? [...current, ...additions] : current;
}

type SearchSourceSection = {
  key: string;
  label: string;
  items: MediaItem[];
};

function searchSourceKey(addonId: string, type: string, catalogId: string): string {
  return `${addonId}\u0000${type}\u0000${catalogId}`;
}

function searchSourceLabel(descriptor: AddonCatalogDescriptor | undefined, result: ResourceResult): string {
  const addonName = descriptor?.addonName?.trim();
  const catalogName = descriptor?.catalog.name?.trim();
  if (addonName && catalogName && addonName !== catalogName) return `${addonName} · ${catalogName}`;
  return addonName || catalogName || descriptor?.catalog.id.trim() || result.id.trim() || descriptor?.addonId.trim() || result.addonId;
}

function orderSearchSourceSections(sections: SearchSourceSection[], descriptors: AddonCatalogDescriptor[]): SearchSourceSection[] {
  const descriptorOrder = new Map<string, number>();
  descriptors.forEach((descriptor, index) => {
    const key = searchSourceKey(descriptor.addonId, descriptor.catalog.type, descriptor.catalog.id);
    if (!descriptorOrder.has(key)) descriptorOrder.set(key, index);
  });
  return [...sections].sort((left, right) => {
    const leftOrder = descriptorOrder.get(left.key);
    const rightOrder = descriptorOrder.get(right.key);
    if (leftOrder === undefined && rightOrder === undefined) return 0;
    if (leftOrder === undefined) return 1;
    if (rightOrder === undefined) return -1;
    return leftOrder - rightOrder;
  });
}

function collectSearchSourcePage(resourceBatches: ResourceBatch[], descriptors: AddonCatalogDescriptor[], currentItems: MediaItem[] = []): { items: MediaItem[]; sections: SearchSourceSection[] } {
  const descriptorBySource = new Map(descriptors.map((descriptor) => [searchSourceKey(descriptor.addonId, descriptor.catalog.type, descriptor.catalog.id), descriptor]));
  const seen = new Set(currentItems.map(mediaIdentity));
  const items: MediaItem[] = [];
  const sectionsBySource = new Map<string, SearchSourceSection>();
  for (const batch of resourceBatches) {
    for (const result of batch.results) {
      const key = searchSourceKey(result.addonId, result.type, result.id);
      for (const item of mediaFromResourceResult(result)) {
        const identity = mediaIdentity(item);
        if (seen.has(identity)) continue;
        seen.add(identity);
        items.push(item);
        let section = sectionsBySource.get(key);
        if (!section) {
          section = { key, label: searchSourceLabel(descriptorBySource.get(key), result), items: [] };
          sectionsBySource.set(key, section);
        }
        section.items.push(item);
      }
    }
  }
  return { items, sections: orderSearchSourceSections([...sectionsBySource.values()], descriptors) };
}

function mergeSearchSourceSections(current: SearchSourceSection[], incoming: SearchSourceSection[], descriptors: AddonCatalogDescriptor[]): SearchSourceSection[] {
  const merged = new Map(current.map((section) => [section.key, { ...section, items: [...section.items] }]));
  for (const section of incoming) {
    const existing = merged.get(section.key);
    if (existing) existing.items.push(...section.items);
    else merged.set(section.key, section);
  }
  return orderSearchSourceSections([...merged.values()], descriptors);
}

function tvSubtitle(item: MediaItem): string {
  return currentProgramTitle(item.currentProgram)
    || [item.category, item.language, item.country, item.sourceName].filter(Boolean).join(" · ")
    || mediaTypeLabel(item.mediaType);
}

function tvMetadata(item: MediaItem): string {
  return [item.category, item.language, item.country, item.sourceName].filter(Boolean).join(" · ");
}

function tvTileSubtitle(item: MediaItem): string {
  return currentProgramTitle(item.currentProgram) || mediaTypeLabel("tv");
}

function isAvailable(item: MediaItem, hideUnreleased: boolean): boolean {
  if (!hideUnreleased || !item.released) return true;
  const releasedAt = Date.parse(item.released);
  return Number.isNaN(releasedAt) || releasedAt <= Date.now();
}

type EnrichedContinueItem = CachedContinueItem;

function remainingLabel(item: EnrichedContinueItem): string {
  const remainingSeconds = Math.max(0, item.durationSeconds - item.positionSeconds);
  if (remainingSeconds <= 0) return "";
  const minutes = Math.max(1, Math.ceil(remainingSeconds / 60));
  if (minutes < 60) return t("common.time.minutesLeft", { minutes });
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  return t("common.time.hoursLeft", { hours, minutes: remainder > 0 ? ` ${remainder}m` : "" });
}

function mediaFromContinue(item: EnrichedContinueItem): MediaItem {
  const episodeLabel = item.seasonNumber !== undefined && item.episodeNumber !== undefined
    ? `S${String(item.seasonNumber).padStart(2, "0")}E${String(item.episodeNumber).padStart(2, "0")}`
    : "";
  const episodeCardLabel = item.seasonNumber !== undefined && item.episodeNumber !== undefined
    ? `S${String(item.seasonNumber).padStart(2, "0")} · E${String(item.episodeNumber).padStart(2, "0")}`
    : "";
  const progress = item.durationSeconds > 0 ? Math.min(100, Math.round(item.positionSeconds / item.durationSeconds * 100)) : 0;
  const seriesTitle = item.title || t("media.type.series");
  return {
    id: item.resourceId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.mediaType === "episode" ? `${seriesTitle} · ${episodeLabel}${item.episodeTitle ? ` · ${item.episodeTitle}` : ""}` : item.title || t("media.untitled"),
    posterUrl: item.episodeStillUrl || item.posterUrl,
    backgroundUrl: item.episodeStillUrl || item.backgroundUrl || item.posterUrl,
    description: item.episodeOverview || (item.reason === "resume" ? t("home.continue.resumeFromPercent", { progress }) : t("home.continue.nextEpisodeReady")),
    releaseInfo: item.reason === "resume"
      ? episodeLabel ? t("home.continue.episodeProgress", { episodeCode: episodeLabel, progress }) : t("home.continue.percentWatched", { progress })
      : t("home.continue.episodeUpNext", { episodeCode: episodeLabel }),
    released: item.episodeAirDate,
    externalIds: item.resourceProvider && item.resourceId ? { [item.resourceProvider]: item.resourceId } : {},
    raw: {
      progress,
      continueReason: item.reason,
      continueSeriesId: item.seriesId,
      continueSeasonId: item.seasonId,
      continueSeasonNumber: item.seasonNumber,
      continueEpisodeNumber: item.episodeNumber,
      continueEpisodeId: item.titleId,
      continueCardTitle: seriesTitle,
      continueCardSubtitle: [episodeCardLabel, item.episodeTitle].filter(Boolean).join(" · ") || item.releaseInfo || "",
      continueCardBadge: item.reason === "resume"
        ? remainingLabel(item)
        : item.episodeNumber === 1 && (item.seasonNumber ?? 0) > 1 ? t("home.continue.newSeason") : t("home.continue.nextUp"),
    },
  };
}

async function loadContinueItems(signal?: AbortSignal, cachedItems: EnrichedContinueItem[] = []): Promise<EnrichedContinueItem[]> {
  const response = await api.continueWatching(signal);
  const cachedByTitleID = new Map(cachedItems.map((item) => [item.titleId, item]));
  const reusable = response.items.map((item) => {
    const cached = cachedByTitleID.get(item.titleId);
    return cached && cached.seasonId === item.seasonId && cached.seasonNumber === item.seasonNumber && cached.episodeNumber === item.episodeNumber ? cached : undefined;
  });
  const seasonIDs = Array.from(new Set(response.items.flatMap((item, index) => !reusable[index] && item.seasonId ? [item.seasonId] : [])));
  const seasons = new Map((await Promise.all(seasonIDs.map(async (seasonID) => [seasonID, await api.seasonDetails(seasonID, signal).catch(() => undefined)] as const))).filter((entry) => entry[1] !== undefined));
  return response.items.map((item, index) => {
    const cached = reusable[index];
    if (cached) return { ...item, episodeTitle: cached.episodeTitle, episodeOverview: cached.episodeOverview, episodeStillUrl: cached.episodeStillUrl, episodeAirDate: cached.episodeAirDate };
    const episode = item.seasonId ? seasons.get(item.seasonId)?.episodes.find((candidate) => candidate.id === item.titleId) : undefined;
    return episode ? { ...item, episodeTitle: episode.name, episodeOverview: episode.overview, episodeStillUrl: episode.stillUrl, episodeAirDate: episode.airDate } : item;
  });
}


type HomeRow = { collection: Collection; resolved: ResolvedFolder };
type OpenedHomeFolder = { row: HomeRow; refresh: Promise<HomeRow | undefined> };
type OpenedHomeCollection = { collection: Collection; refresh: Promise<HomeRow[]> };
type HeroSlide = { key: string; item: MediaItem; collection: Collection; folder: ResolvedFolder["folder"] };
const homeFolderConcurrency = 6;
const homeFolderTimeoutMilliseconds = 10_000;
const homeCacheWriteIntervalMilliseconds = 500;
const homeCollectionRequests = new WeakMap<AbortSignal, Promise<CollectionListResponse>>();

function loadHomeCollections(signal: AbortSignal): Promise<CollectionListResponse> {
  const existing = homeCollectionRequests.get(signal);
  if (existing) return existing;
  const request = api.collections(signal);
  homeCollectionRequests.set(signal, request);
  const forget = () => {
    if (homeCollectionRequests.get(signal) === request) homeCollectionRequests.delete(signal);
  };
  void request.then(forget, forget);
  return request;
}



export function HomePage({ onOpenMedia, mediaRevision, mediaPreferences }: { onOpenMedia: OpenMedia; mediaRevision: number; mediaPreferences: MediaPreferences }) {
  const [rows, setRows] = useState<HomeRow[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [partialWarning, setPartialWarning] = useState("");
  const [opened, setOpened] = useState<OpenedHomeFolder | null>(null);
  const [openedCollection, setOpenedCollection] = useState<OpenedHomeCollection | null>(null);
  const [continueItems, setContinueItems] = useState<EnrichedContinueItem[]>([]);
  const [continueAction, setContinueAction] = useState<{ item: MediaItem; anchor: ActionMenuAnchor }>();
  const [continueActionBusy, setContinueActionBusy] = useState(false);
  const { profileRequestSignal } = useAuth();
  const [pendingFolderKeys, setPendingFolderKeys] = useState<Set<string>>(new Set());
  const [activeHeroIndex, setActiveHeroIndex] = useState(0);
  const homeRequestGeneration = useRef(0);
  const continueRevisionRef = useRef(0);
  const folderRefreshes = useRef(new Map<string, Promise<HomeRow | undefined>>());

  useEffect(() => {
    if (!mediaPreferences.profileID || profileRequestSignal.aborted) return;
    const profileID = mediaPreferences.profileID;
    const cacheScope = api.metadataScope();
    const cachedHome = readHomeCache(profileID, cacheScope);
    const cachedContinue = readContinueCache(profileID, cacheScope);
    const generation = ++homeRequestGeneration.current;
    const controller = new AbortController();
    let active = true;
    let cacheWriteTimer: number | undefined;
    let publishFrame: number | undefined;
    const isCurrent = () => active && homeRequestGeneration.current === generation;
    const cancelRequest = () => {
      active = false;
      if (homeRequestGeneration.current === generation) homeRequestGeneration.current++;
      controller.abort();
    };
    profileRequestSignal.addEventListener("abort", cancelRequest, { once: true });
    setError("");
    setPartialWarning("");
    setContinueItems(cachedContinue?.items ?? []);
    setPendingFolderKeys(new Set());
    if (cachedHome) {
      const cachedRows = cachedHome.collections.flatMap((collection) => collection.folders.flatMap((folder) => {
        const resolved = cachedHome.folders[homeFolderCacheKey(collection.id, folder.id ?? "")];
        return resolved ? [{ collection, resolved }] : [];
      }));
      setCollections(cachedHome.collections);
      setRows(cachedRows);
      setPartialWarning(cachedRows.some((row) => row.resolved.errors.length > 0) ? t("home.browser.someTitlesLoadFailed") : "");
      setLoading(false);
    } else {
      setCollections([]);
      setRows([]);
      setLoading(true);
    }

    void loadContinueItems(controller.signal, cachedContinue?.fresh ? cachedContinue.items : []).then((items) => {
      if (!isCurrent()) return;
      setContinueItems(items);
      writeContinueCache(profileID, cacheScope, items);
    }).catch(() => undefined);

    void (async () => {
      try {
        const response = await loadHomeCollections(profileRequestSignal);
        if (!isCurrent()) return;
        const cacheMatches = cachedHome?.signature === homeCollectionSignature(response.collections);
        const targets = response.collections.flatMap((collection) => collection.folders.map((folder) => ({
          collection,
          folderID: folder.id ?? "",
          key: homeFolderCacheKey(collection.id, folder.id ?? ""),
        })));
        const results: Array<HomeRow | undefined> = targets.map((target) => {
          const resolved = cacheMatches ? cachedHome?.folders[target.key] : undefined;
          return resolved ? { collection: target.collection, resolved } : undefined;
        });
        let hasFolderFailure = results.some((row) => (row?.resolved.errors.length ?? 0) > 0);
        let lastCacheWriteAt = 0;
        const persistResolvedRows = () => {
          cacheWriteTimer = undefined;
          if (!isCurrent()) return;
          const resolvedRows = results.filter((row): row is HomeRow => row !== undefined);
          if (resolvedRows.length === 0) return;
          writeHomeCache(profileID, cacheScope, response.collections, resolvedRows);
          lastCacheWriteAt = Date.now();
        };
        const scheduleCacheWrite = () => {
          if (cacheWriteTimer !== undefined) return;
          const delay = Math.max(0, lastCacheWriteAt + homeCacheWriteIntervalMilliseconds - Date.now());
          if (delay === 0) {
            persistResolvedRows();
            return;
          }
          cacheWriteTimer = window.setTimeout(persistResolvedRows, delay);
        };
        const pending = targets.flatMap((target, index) => results[index] ? [] : [{ target, index }]);
        const pendingKeys = new Set(pending.map(({ target }) => target.key));
        const publishResolvedFolders = () => {
          publishFrame = undefined;
          if (!isCurrent()) return;
          setRows(results.filter((row): row is HomeRow => row !== undefined));
          setPendingFolderKeys(new Set(pendingKeys));
        };
        const scheduleResolvedFoldersPublish = () => {
          if (publishFrame === undefined) publishFrame = window.requestAnimationFrame(publishResolvedFolders);
        };
        setCollections(response.collections);
        setRows(results.filter((row): row is HomeRow => row !== undefined));
        setPartialWarning(hasFolderFailure ? t("home.browser.someTitlesLoadFailed") : "");
        setPendingFolderKeys(new Set(pendingKeys));
        setLoading(false);
        if (targets.length === 0) {
          writeHomeCache(profileID, cacheScope, response.collections, []);
          return;
        }
        if (pending.length === 0) return;

        let cursor = 0;
        const worker = async () => {
          while (isCurrent() && !controller.signal.aborted) {
            const pendingIndex = cursor++;
            if (pendingIndex >= pending.length) return;
            const { target, index } = pending[pendingIndex];
            const requestController = new AbortController();
            const abortRequest = () => requestController.abort();
            controller.signal.addEventListener("abort", abortRequest, { once: true });
            const timeout = window.setTimeout(abortRequest, homeFolderTimeoutMilliseconds);
            try {
              const resolved = await api.resolveFolder(target.collection.id, target.folderID, 1, requestController.signal);
              if (!isCurrent()) return;
              results[index] = { collection: target.collection, resolved };
              if (resolved.errors.length > 0) {
                hasFolderFailure = true;
                setPartialWarning(t("home.browser.someTitlesLoadFailed"));
              }
              scheduleResolvedFoldersPublish();
              scheduleCacheWrite();
            } catch {
              hasFolderFailure = true;
              setPartialWarning(t("home.browser.someTitlesLoadFailed"));
              // A missing folder must not prevent independent rows from rendering.
            } finally {
              window.clearTimeout(timeout);
              controller.signal.removeEventListener("abort", abortRequest);
              if (isCurrent()) {
                pendingKeys.delete(target.key);
                scheduleResolvedFoldersPublish();
              }
            }
          }
        };
        await Promise.all(Array.from({ length: Math.min(homeFolderConcurrency, pending.length) }, worker));
        if (!isCurrent()) return;
        if (publishFrame !== undefined) {
          window.cancelAnimationFrame(publishFrame);
          publishResolvedFolders();
        }
        if (cacheWriteTimer !== undefined) {
          window.clearTimeout(cacheWriteTimer);
          cacheWriteTimer = undefined;
        }
        const resolvedRows = results.filter((row): row is HomeRow => row !== undefined);
        setPartialWarning(hasFolderFailure ? t("home.browser.someTitlesLoadFailed") : "");
        if (resolvedRows.length === 0) {
          setError(notifyErrorMessage(t("home.error.sourcesUnavailable"), t("home.error.unavailableTitle")));
          setPartialWarning("");
          return;
        }
        writeHomeCache(profileID, cacheScope, response.collections, resolvedRows);
      } catch (cause) {
        if (isCurrent()) setError(notifyError(cause, t("home.error.loadFailed"), t("home.error.unavailableTitle")));
      } finally {
        if (isCurrent()) setLoading(false);
      }
    })();

    return () => {
      if (cacheWriteTimer !== undefined) window.clearTimeout(cacheWriteTimer);
      if (publishFrame !== undefined) window.cancelAnimationFrame(publishFrame);
      profileRequestSignal.removeEventListener("abort", cancelRequest);
      cancelRequest();
    };
  }, [mediaPreferences.profileID, profileRequestSignal]);

  useEffect(() => {
    if (mediaRevision === 0 || continueRevisionRef.current === mediaRevision || !mediaPreferences.profileID || profileRequestSignal.aborted) return;
    continueRevisionRef.current = mediaRevision;
    const profileID = mediaPreferences.profileID;
    const cacheScope = api.metadataScope();
    const cached = readContinueCache(profileID, cacheScope);
    let active = true;
    void loadContinueItems(profileRequestSignal, cached?.fresh ? cached.items : []).then((items) => {
      if (!active) return;
      setContinueItems(items);
      writeContinueCache(profileID, cacheScope, items);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [mediaPreferences.profileID, mediaRevision, profileRequestSignal]);


  const heroSlides = useMemo(() => {
    const seen = new Set<string>();
    const slides: HeroSlide[] = [];
    for (const row of rows) {
      if (!row.collection.heroEnabled) continue;
      for (const item of row.resolved.items) {
        const key = mediaIdentity(item);
        if (seen.has(key) || !isAvailable(item, mediaPreferences.hideUnreleased)) continue;
        seen.add(key);
        slides.push({ key, item, collection: row.collection, folder: row.resolved.folder });
        if (slides.length === 12) return slides;
      }
    }
    return slides;
  }, [rows, mediaPreferences.hideUnreleased]);

  useEffect(() => {
    setActiveHeroIndex(0);
  }, [mediaPreferences.profileID]);

  useEffect(() => {
    if (!mediaPreferences.animationsEnabled || heroSlides.length < 2 || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const timer = window.setInterval(() => setActiveHeroIndex((current) => (current + 1) % heroSlides.length), 8_000);
    return () => window.clearInterval(timer);
  }, [heroSlides.length, mediaPreferences.animationsEnabled]);

  useEffect(() => {
    setActiveHeroIndex((current) => heroSlides.length === 0 ? 0 : current % heroSlides.length);
  }, [heroSlides.length]);

  const heroSlide = heroSlides[activeHeroIndex];
  const hero = heroSlide?.item;
  const heroBackdrop = heroSlide && (hero.backgroundUrl || heroSlide.folder.heroBackdropUrl || heroSlide.collection.backdropImageUrl || hero.posterUrl);
  const heroTitleLogo = heroSlide && (heroSlide.folder.titleLogoUrl || hero?.logoUrl);
  const heroPending = collections.some((collection) => collection.heroEnabled && collection.folders.some((folder) => pendingFolderKeys.has(homeFolderCacheKey(collection.id, folder.id ?? ""))));
  const continueMedia = continueItems.map(mediaFromContinue);

  function refreshHomeRow(collection: Collection, folderID: string, persist = true): Promise<HomeRow | undefined> {
    if (!folderID || !mediaPreferences.profileID || profileRequestSignal.aborted) return Promise.resolve(undefined);
    const key = homeFolderCacheKey(collection.id, folderID);
    const inFlight = folderRefreshes.current.get(key);
    if (inFlight) return inFlight;
    const request = api.resolveFolder(collection.id, folderID, 1, profileRequestSignal)
      .then((resolved) => {
        if (profileRequestSignal.aborted) return undefined;
        const row = { collection, resolved };
        setRows((current) => {
          const index = current.findIndex((candidate) => homeFolderCacheKey(candidate.collection.id, candidate.resolved.folder.id ?? "") === key);
          if (index < 0) return [...current, row];
          const next = [...current];
          next[index] = row;
          return next;
        });
        if (persist) writeHomeFolderCache(mediaPreferences.profileID, api.metadataScope(), collections, resolved);
        return row;
      })
      .catch(() => undefined)
      .finally(() => {
        if (folderRefreshes.current.get(key) === request) folderRefreshes.current.delete(key);
      });
    folderRefreshes.current.set(key, request);
    return request;
  }

  async function refreshCollectionRows(collection: Collection): Promise<HomeRow[]> {
    const currentRows = new Map(rows.filter((row) => row.collection.id === collection.id).map((row) => [row.resolved.folder.id ?? "", row]));
    const refreshed: Array<HomeRow | undefined> = new Array(collection.folders.length);
    let cursor = 0;
    const worker = async () => {
      while (!profileRequestSignal.aborted) {
        const index = cursor++;
        if (index >= collection.folders.length) return;
        const folderID = collection.folders[index].id ?? "";
        refreshed[index] = await refreshHomeRow(collection, folderID, false) ?? currentRows.get(folderID);
      }
    };
    await Promise.all(Array.from({ length: Math.min(homeFolderConcurrency, collection.folders.length) }, worker));
    const resolvedRows = refreshed.filter((row): row is HomeRow => row !== undefined);
    if (!profileRequestSignal.aborted) {
      writeHomeCache(mediaPreferences.profileID, api.metadataScope(), collections, [...rows.filter((row) => row.collection.id !== collection.id), ...resolvedRows]);
    }
    return resolvedRows;
  }

  function openHomeFolder(row: HomeRow) {
    setOpened({ row, refresh: refreshHomeRow(row.collection, row.resolved.folder.id ?? "") });
  }

  function openHomeCollection(collection: Collection) {
    setOpenedCollection({ collection, refresh: refreshCollectionRows(collection) });
  }

  function rotateHero(direction: -1 | 1) {
    setActiveHeroIndex((current) => (current + direction + heroSlides.length) % heroSlides.length);
  }

  function continueActionTitle(item: MediaItem): string {
    return typeof item.raw?.continueCardTitle === "string" ? item.raw.continueCardTitle : item.title;
  }

  function continueActionEpisodeLabel(item: MediaItem): string {
    const seasonNumber = typeof item.raw?.continueSeasonNumber === "number" ? item.raw.continueSeasonNumber : undefined;
    const episodeNumber = typeof item.raw?.continueEpisodeNumber === "number" ? item.raw.continueEpisodeNumber : undefined;
    return seasonNumber !== undefined && episodeNumber !== undefined
      ? `S${String(seasonNumber).padStart(2, "0")} · E${String(episodeNumber).padStart(2, "0")}`
      : "";
  }

  function openContinueDetails(item: MediaItem) {
    setContinueAction(undefined);
    onOpenMedia(item);
  }

  function startContinueFromBeginning(item: MediaItem) {
    setContinueAction(undefined);
    onOpenMedia({
      ...item,
      raw: { ...item.raw, startFromBeginning: true },
    });
  }

  async function removeContinueItem(item: MediaItem) {
    if (!item.titleId) return;
    setContinueActionBusy(true);
    try {
      await api.dismissContinue(item.titleId);
      const remaining = continueItems.filter((candidate) => candidate.titleId !== item.titleId);
      setContinueItems(remaining);
      writeContinueCache(mediaPreferences.profileID, api.metadataScope(), remaining);
      setContinueAction(undefined);
      notifySuccess(
        t("home.continue.notifications.removedMessage", { title: continueActionTitle(item) }),
        t("home.continue.notifications.removedTitle"),
      );
    } catch (cause) {
      notifyError(cause, t("home.continue.errors.removeFailed"), t("home.continue.errors.removeFailedTitle"));
    } finally {
      setContinueActionBusy(false);
    }
  }


  if (loading) return <div className="home-page page-enter" aria-busy="true"><Skeleton className="hero-skeleton" /><div className="content-stack">{[0, 1, 2].map((row) => <div key={row}><Skeleton className="heading-skeleton" /><div className="skeleton-row">{[0, 1, 2, 3, 4, 5].map((card) => <Skeleton key={card} className="card-skeleton" />)}</div></div>)}</div></div>;

  if (opened) return <FolderBrowser key={`${opened.row.collection.id}-${opened.row.resolved.folder.id}`} row={opened.row} refresh={opened.refresh} hideUnreleased={mediaPreferences.hideUnreleased} onBack={() => setOpened(null)} onOpenMedia={onOpenMedia} />;
  if (openedCollection) return <CollectionBrowser key={openedCollection.collection.id} collection={openedCollection.collection} rows={rows.filter((row) => row.collection.id === openedCollection.collection.id)} refresh={openedCollection.refresh} hideUnreleased={mediaPreferences.hideUnreleased} onBack={() => setOpenedCollection(null)} onOpenMedia={onOpenMedia} />;

  return <div className="home-page page-enter">
    {hero && heroSlide ? <section key={heroSlide.key} className="hero hero--featured">
      {heroBackdrop && <div className="hero__backdrop" aria-hidden="true"><img src={heroBackdrop} alt="" loading="eager" fetchPriority="high" decoding="async" /></div>}
      <div className="hero__content">
        <span className="hero__eyebrow"><WandSparkles size={15} /> {t("home.hero.featuredCollection", { collection: heroSlide.collection.title })}</span>
        {heroTitleLogo ? <img src={heroTitleLogo} alt={hero.title} loading="eager" fetchPriority="high" decoding="async" /> : <h1>{hero.title}</h1>}
        <div className="hero__meta">{hero.releaseInfo && <span>{hero.releaseInfo}</span>}{hero.voteAverage !== undefined && <span><Star size={14} fill="currentColor" /> {hero.voteAverage.toFixed(1)}</span>}<span>{mediaTypeLabel(hero.mediaType)}</span></div>
        <p>{hero.description || t("home.hero.descriptionFallback")}</p>
        <div className="hero__actions"><Button onClick={() => onOpenMedia(hero)}><Play size={19} fill="currentColor" /> {t("home.hero.playNow")}</Button><Button variant="secondary" onClick={() => onOpenMedia(hero)}>{t("home.hero.moreInfo")} <ArrowRight size={18} /></Button></div>
      </div>
      {heroSlides.length > 1 && <div className="hero__navigation" aria-label={t("home.hero.carouselLabel")} onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "horizontal" }); }}>
        <button type="button" onClick={() => rotateHero(-1)} aria-label={t("home.hero.previousTitle")}><ArrowLeft size={18} /></button>
        <span>{heroSlides.map((slide, index) => <button key={slide.key} type="button" className={index === activeHeroIndex ? "is-active" : ""} onClick={() => setActiveHeroIndex(index)} aria-label={t("home.hero.showTitle", { title: slide.item.title })} aria-current={index === activeHeroIndex ? "true" : undefined} />)}</span>
        <button type="button" onClick={() => rotateHero(1)} aria-label={t("home.hero.nextTitle")}><ArrowRight size={18} /></button>
      </div>}
    </section> : heroPending ? <Skeleton className="hero-skeleton" /> : <section className="hero hero--empty"><div className="hero__content"><span className="hero__eyebrow"><Sparkles size={15} /> {t("home.emptyHero.eyebrow")}</span><h1>{t("home.emptyHero.title").split("\n").map((line, index) => <span key={line}>{index > 0 && <br />}{line}</span>)}</h1><p>{t("home.emptyHero.description")}</p></div></section>}
    <div className="content-stack">
      {error && <Notice>{error}</Notice>}
      {partialWarning && <Notice tone="warning">{partialWarning}</Notice>}
      {continueMedia.length > 0 && <section className="continue-section">
        <SectionHeading title={t("home.continue.title")} />
        <HorizontalDragRow className="media-row media-row--landscape media-row--continue">{continueMedia.map((item) => <MediaCard
          key={`${String(item.raw?.continueReason)}-${item.titleId}`}
          shape="landscape"
          title={typeof item.raw?.continueCardTitle === "string" ? item.raw.continueCardTitle : item.title}
          image={item.backgroundUrl || item.posterUrl}
          subtitle={typeof item.raw?.continueCardSubtitle === "string" ? item.raw.continueCardSubtitle : item.releaseInfo}
          badge={typeof item.raw?.continueCardBadge === "string" ? item.raw.continueCardBadge : undefined}
          progress={item.raw?.continueReason === "resume" && typeof item.raw.progress === "number" ? item.raw.progress : undefined}
          onClick={() => onOpenMedia(item)}
          onContextAction={(anchor) => setContinueAction({ item, anchor })}
        />)}</HorizontalDragRow>
      </section>}
      {collections.map((collection) => {
        const collectionRows = rows.filter((candidate) => candidate.collection.id === collection.id);
        const directItems = Array.from(new Map(collectionRows.flatMap((row) => row.resolved.items)
          .filter((item) => isAvailable(item, mediaPreferences.hideUnreleased))
          .map((item) => [mediaIdentity(item), item])).values());
        const showDirectly = collection.viewMode === "follow_layout";
        const displayedDirectItems = showDirectly ? directItems.slice(0, mediaPreferences.maximumDirectTitles) : directItems;
        const landscapeItems = displayedDirectItems.length > 0 && displayedDirectItems.every((item) => item.mediaType === "tv");
        const collectionPending = collection.folders.some((folder) => pendingFolderKeys.has(homeFolderCacheKey(collection.id, folder.id ?? "")));
        return <section className={`folder-collection-section folder-collection-section--${collection.folderCoverShape}`} key={collection.id}>
          <SectionHeading title={collection.title} action={<button type="button" className="text-button" onClick={() => openHomeCollection(collection)}>{t("common.actions.viewAll")} <ArrowRight size={16} /></button>} />
          {showDirectly ? displayedDirectItems.length > 0 ? <HorizontalDragRow className={landscapeItems ? "media-row media-row--landscape" : "media-row"}>{displayedDirectItems.map((item) => <MediaCard key={mediaIdentity(item)} shape={item.mediaType === "tv" ? "landscape" : "poster"} title={item.title} image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.mediaType === "tv" ? tvSubtitle(item) : item.releaseInfo} badge={item.mediaType === "tv" ? mediaTypeLabel("tv") : undefined} onClick={() => onOpenMedia(item)} />)}</HorizontalDragRow> : collectionPending ? <div className="skeleton-row">{[0, 1, 2, 3, 4, 5].map((card) => <Skeleton key={card} className="card-skeleton" />)}</div> : <EmptyState icon={<Clapperboard size={40} />} title={t("home.collection.emptyTitle")} description={t("home.collection.emptySourcesDescription")} /> : <HorizontalDragRow className={`folder-cover-row folder-cover-row--${collection.folderCoverShape}`}>{collection.folders.map((folder, index) => {
            const row = collectionRows.find((candidate) => candidate.resolved.folder.id === folder.id);
            const artwork = row?.resolved.folder.coverImageUrl || folder.coverImageUrl || row?.resolved.items.find((item) => isAvailable(item, mediaPreferences.hideUnreleased))?.posterUrl || collection.backdropImageUrl;
            return <button key={folder.id ?? index} className="folder-cover-card" disabled={!row} onClick={() => { if (row) openHomeFolder(row); }} aria-label={t("home.folder.openNamed", { name: folder.title })}>
              <span className="folder-cover-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span className="folder-cover-card__fallback">{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
              {!folder.hideTitle && <span className="folder-cover-card__copy"><strong>{folder.title}</strong></span>}
            </button>;
          })}</HorizontalDragRow>}
        </section>;
      })}
      {collections.length === 0 && <EmptyState icon={<Clapperboard size={46} />} title={t("home.empty.title")} description={t("home.empty.description")} />}
    </div>
    {continueAction && <ActionMenu
      label={t("home.continue.actions.menuLabel", { title: [continueActionTitle(continueAction.item), continueActionEpisodeLabel(continueAction.item)].filter(Boolean).join(" · ") })}
      eyebrow={t("home.continue.title")}
      title={continueActionTitle(continueAction.item)}
      subtitle={continueActionEpisodeLabel(continueAction.item) || undefined}
      anchor={continueAction.anchor}
      onClose={() => { if (!continueActionBusy) setContinueAction(undefined); }}
    >
      <button type="button" role="menuitem" onClick={() => openContinueDetails(continueAction.item)}>
        {continueAction.item.mediaType === "episode" ? <ListVideo size={19} /> : <Info size={19} />}
        <span>{t("home.continue.actions.details")}</span>
      </button>
      <button type="button" role="menuitem" onClick={() => startContinueFromBeginning(continueAction.item)}>
        <RotateCcw size={19} />
        <span>{t("home.continue.actions.startBeginning")}</span>
      </button>
      <button type="button" role="menuitem" className="action-menu__danger" disabled={continueActionBusy} onClick={() => void removeContinueItem(continueAction.item)}>
        {continueActionBusy ? <LoaderCircle className="spin" size={19} /> : <Trash2 size={19} />}
        <span>{t("home.continue.actions.remove")}</span>
      </button>
    </ActionMenu>}
  </div>;
}



type CollectionBrowserRow = {
  row: HomeRow;
  items: MediaItem[];
  page: number;
  hasMore: boolean;
};

function CollectionBrowser({ collection, rows, refresh, hideUnreleased, onBack, onOpenMedia }: { collection: Collection; rows: HomeRow[]; refresh: Promise<HomeRow[]>; hideUnreleased: boolean; onBack: () => void; onOpenMedia: OpenMedia }) {
  const [pages, setPages] = useState<CollectionBrowserRow[]>(() => rows.map((row) => ({
    row,
    items: row.resolved.items,
    page: row.resolved.page,
    hasMore: row.resolved.hasMore,
  })));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [partialWarning, setPartialWarning] = useState(() => rows.some((row) => row.resolved.errors.length > 0) ? t("home.browser.someTitlesLoadFailed") : "");
  const [openedFolderID, setOpenedFolderID] = useState("");
  const loadedMoreRef = useRef(false);
  useEffect(() => {
    let active = true;
    void refresh.then((refreshed) => {
      if (!active || loadedMoreRef.current || refreshed.length === 0) return;
      setPages(refreshed.map((row) => ({
        row,
        items: row.resolved.items,
        page: row.resolved.page,
        hasMore: row.resolved.hasMore,
      })));
      setPartialWarning(refreshed.some((row) => row.resolved.errors.length > 0) ? t("home.browser.someTitlesLoadFailed") : "");
    });
    return () => { active = false; };
  }, [refresh]);
  const showFolders = collection.viewMode !== "follow_layout";
  const cards = useMemo(() => {
    const seen = new Set<string>();
    return pages.flatMap((page) => page.items.flatMap((item) => {
      if (!isAvailable(item, hideUnreleased)) return [];
      const key = mediaIdentity(item);
      if (seen.has(key)) return [];
      seen.add(key);
      return [{ item, shape: page.row.resolved.folder.tileShape }];
    }));
  }, [hideUnreleased, pages]);
  const hasMore = pages.some((page) => page.hasMore);

  async function loadMore() {
    const targets = pages.filter((page) => page.hasMore && page.row.resolved.folder.id);
    if (targets.length === 0) return;
    setLoading(true);
    setError("");
    const outcomes = await Promise.allSettled(targets.map(async (target) => ({
      folderID: target.row.resolved.folder.id ?? "",
      resolved: await api.resolveFolder(collection.id, target.row.resolved.folder.id ?? "", target.page + 1),
    })));
    const loaded = new Map<string, ResolvedFolder>();
    let failed = false;
    for (const outcome of outcomes) {
      if (outcome.status === "fulfilled") {
        loaded.set(outcome.value.folderID, outcome.value.resolved);
        if (outcome.value.resolved.errors.length > 0) failed = true;
      } else failed = true;
    }
    if (loaded.size > 0) loadedMoreRef.current = true;
    setPages((current) => current.map((page) => {
      const folderID = page.row.resolved.folder.id ?? "";
      const next = loaded.get(folderID);
      if (!next) return page;
      const seen = new Set(page.items.map(mediaIdentity));
      const additions = next.items.filter((item) => {
        const key = mediaIdentity(item);
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
      return {
        ...page,
        items: [...page.items, ...additions],
        page: next.page,
        hasMore: next.hasMore && additions.length > 0,
      };
    }));
    if (failed) setError(notifyErrorMessage(t("home.browser.someTitlesLoadFailed"), t("common.error.loadingFailedTitle")));
    setLoading(false);
  }

  const openedPage = pages.find((page) => page.row.resolved.folder.id === openedFolderID);
  if (openedPage) {
    const row = {
      ...openedPage.row,
      resolved: {
        ...openedPage.row.resolved,
        items: openedPage.items,
        page: openedPage.page,
        hasMore: openedPage.hasMore,
      },
    };
    return <FolderBrowser key={openedFolderID} row={row} hideUnreleased={hideUnreleased} backLabel={t("common.actions.backToNamed", { name: collection.title })} onBack={() => setOpenedFolderID("")} onOpenMedia={onOpenMedia} />;
  }

  const description = `${showFolders
    ? t(collection.folders.length === 1 ? "home.collection.folderCount.one" : "home.collection.folderCount.many", { count: collection.folders.length })
    : t(cards.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: cards.length })}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={onBack}><ArrowLeft size={17} /> {t("common.actions.backToHome")}</button>
    <SectionHeading eyebrow={t("home.collection.eyebrow")} title={collection.title} description={description} />
    {error && <Notice>{error}</Notice>}
    {partialWarning && <Notice tone="warning">{partialWarning}</Notice>}
    {showFolders ? <div className={`source-folder-grid source-folder-grid--${collection.folderCoverShape}`} onKeyDown={handleBrowseGridKeyDown}>{collection.folders.map((folder, index) => { const page = pages.find((candidate) => candidate.row.resolved.folder.id === folder.id);
    const resolvedFolder = page?.row.resolved.folder ?? folder;
    const visibleItems = page?.items.filter((item) => isAvailable(item, hideUnreleased)) ?? [];
    const artwork = resolvedFolder.coverImageUrl || visibleItems[0]?.posterUrl || visibleItems[0]?.backgroundUrl || collection.backdropImageUrl;
    return <button key={folder.id ?? index} type="button" className="source-folder-card" disabled={!page} onClick={() => setOpenedFolderID(folder.id ?? "")} aria-label={t("home.folder.openNamed", { name: folder.title })}>
      <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" draggable={false} /> : <span>{folder.coverEmoji || folder.title.slice(0, 2).toUpperCase()}</span>}</span>
      {!folder.hideTitle && <span className="source-folder-card__copy"><strong>{folder.title}</strong><small>{t(visibleItems.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: visibleItems.length })}</small></span>}
    </button>; })}</div> : cards.length > 0 ? <div className="media-grid media-grid--adaptive" onKeyDown={handleBrowseGridKeyDown}>{cards.map(({ item, shape }) => <div className={item.mediaType === "tv" ? "tv-media-tile" : "media-tile"} key={mediaIdentity(item)}><MediaCard title={item.title} image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.mediaType === "tv" ? tvSubtitle(item) : item.releaseInfo} badge={item.mediaType === "tv" ? mediaTypeLabel("tv") : undefined} shape={item.mediaType === "tv" ? "landscape" : shape} onClick={() => onOpenMedia(item)} /></div>)}</div> : <EmptyState icon={<Clapperboard size={46} />} title={t("home.collection.emptyTitle")} description={hideUnreleased ? t("home.browser.noReleasedTitles") : t("home.collection.emptySourcesDescription")} />}
    {!showFolders && hasMore && <div className="load-more"><Button variant="secondary" loading={loading} aria-label={t("common.actions.loadMore")} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}

function FolderBrowser({ row, refresh, hideUnreleased, onBack, onOpenMedia, backLabel }: { row: HomeRow; refresh?: Promise<HomeRow | undefined>; hideUnreleased: boolean; onBack: () => void; onOpenMedia: OpenMedia; backLabel?: string }) {
  const [items, setItems] = useState(row.resolved.items);
  const [page, setPage] = useState(row.resolved.page);
  const [hasMore, setHasMore] = useState(row.resolved.hasMore);
  const [sourcePosterUrls, setSourcePosterUrls] = useState(row.resolved.sourcePosterUrls ?? {});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [partialWarning, setPartialWarning] = useState(row.resolved.errors.length > 0 ? t("home.browser.someTitlesLoadFailed") : "");
  const sources = useMemo(() => row.resolved.folder.sources.filter((source) => source.id), [row.resolved.folder.sources]);
  const sourceView = sources.length > 1 ? row.resolved.folder.sourceView ?? "merged" : "merged";
  const [activeSourceID, setActiveSourceID] = useState(sourceView === "categories" ? sources[0]?.id ?? "" : "");
  const [mediaFilter, setMediaFilter] = useState<"all" | "movie" | "series">("all");
  const loadedMoreRef = useRef(false);
  useEffect(() => {
    if (loadedMoreRef.current) return;
    setItems(row.resolved.items);
    setPage(row.resolved.page);
    setHasMore(row.resolved.hasMore);
    setSourcePosterUrls(row.resolved.sourcePosterUrls ?? {});
    setPartialWarning(row.resolved.errors.length > 0 ? t("home.browser.someTitlesLoadFailed") : "");
  }, [row]);

  useEffect(() => {
    let active = true;
    void refresh?.then((updated) => {
      if (!active || !updated) return;
      setSourcePosterUrls(updated.resolved.sourcePosterUrls ?? {});
      setPartialWarning(updated.resolved.errors.length > 0 ? t("home.browser.someTitlesLoadFailed") : "");
      if (loadedMoreRef.current) return;
      setItems(updated.resolved.items);
      setPage(updated.resolved.page);
      setHasMore(updated.resolved.hasMore);
    });
    return () => { active = false; };
  }, [refresh]);
  const availableItems = useMemo(() => items.filter((item) => isAvailable(item, hideUnreleased)), [hideUnreleased, items]);
  const itemsBySource = useMemo(() => {
    const grouped = new Map(sources.map((source) => [source.id ?? "", [] as MediaItem[]]));
    for (const item of availableItems) {
      for (const reference of item.sources ?? []) grouped.get(reference.id)?.push(item);
    }
    return grouped;
  }, [availableItems, sources]);
  const activeSource = sources.find((source) => source.id === activeSourceID);
  const scopedItems = activeSourceID ? itemsBySource.get(activeSourceID) ?? [] : availableItems;
  const supportsMediaFilter = (activeSource ? [activeSource] : sources).some((source) => source.tmdb?.mediaType === "both");
  const visibleItems = mediaFilter === "all" ? scopedItems : scopedItems.filter((item) => item.mediaType === mediaFilter);

  useEffect(() => {
    setMediaFilter("all");
  }, [activeSourceID]);

  async function loadMore() {
    const folderID = row.resolved.folder.id;
    if (!folderID) {
      setError(notifyErrorMessage(t("home.folder.missingIdentifier"), t("common.error.loadingFailedTitle")));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const next = await api.resolveFolder(row.collection.id, folderID, page + 1);
      const seen = new Set(items.map(mediaIdentity));
      const additions = next.items.filter((item) => {
        const key = mediaIdentity(item);
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
      loadedMoreRef.current = true;
      setItems((current) => [...current, ...additions]);
      setPage(next.page);
      setHasMore(next.hasMore && additions.length > 0);
      if (next.errors.length > 0) setPartialWarning(t("home.browser.someTitlesLoadFailed"));
    } catch (cause) {
      setError(notifyError(cause, t("home.browser.moreTitlesLoadFailed"), t("common.error.loadingFailedTitle")));
    } finally {
      setLoading(false);
    }
  }

  const browsingSourceFolders = sourceView === "folders" && !activeSource;
  const showMediaFilters = supportsMediaFilter && !browsingSourceFolders;
  const pageTitle = activeSource?.title ?? `${row.resolved.folder.coverEmoji ?? ""} ${row.resolved.folder.title}`.trim();
  const pageDescription = `${browsingSourceFolders
    ? t(sources.length === 1 ? "home.folder.sourceFolderCount.one" : "home.folder.sourceFolderCount.many", { count: sources.length })
    : activeSource
      ? t(visibleItems.length === 1 ? "home.folder.curatedTitleCount.fromSource.one" : "home.folder.curatedTitleCount.fromSource.many", { count: visibleItems.length })
      : t(visibleItems.length === 1 ? "home.folder.curatedTitleCount.one" : "home.folder.curatedTitleCount.many", { count: visibleItems.length })}.`;
  return <div className="standard-page folder-page page-enter">
    <button type="button" className="text-button folder-page__back" onClick={() => { if (activeSource && sourceView === "folders") setActiveSourceID(""); else onBack(); }}><ArrowLeft size={17} /> {activeSource && sourceView === "folders" ? t("common.actions.backToNamed", { name: row.resolved.folder.title }) : backLabel ?? t("common.actions.backToHome")}</button>
    <SectionHeading eyebrow={activeSource ? `${row.collection.title} · ${row.resolved.folder.title}` : row.collection.title} title={pageTitle} description={pageDescription} />
    {(sourceView === "categories" || showMediaFilters) && <div className="folder-filter-stack">
      {sourceView === "categories" && <div className="source-category-tabs" role="tablist" aria-label={t("home.folder.sourceCategoriesLabel")} onKeyDown={(event) => { if (handleDirectionalFocus(event, { orientation: "horizontal" })) (document.activeElement as HTMLButtonElement | null)?.click(); }}>{sources.map((source) => <button key={source.id} type="button" role="tab" tabIndex={activeSourceID === source.id ? 0 : -1} aria-selected={activeSourceID === source.id} aria-controls="folder-browser-results" className={activeSourceID === source.id ? "is-active" : ""} onClick={() => setActiveSourceID(source.id ?? "")}>{source.title}</button>)}</div>}
      {showMediaFilters && <div className="filter-pills folder-media-filters" role="group" aria-label={t("media.filter.groupLabel")} onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "horizontal" }); }}>{([["all", "media.filter.allTitles"], ["movie", "media.filter.movies"], ["series", "media.filter.series"]] as const).map(([value, labelKey]) => <button key={value} type="button" aria-pressed={mediaFilter === value} className={mediaFilter === value ? "is-active" : ""} onClick={() => setMediaFilter(value)}>{t(labelKey)}</button>)}</div>}
    </div>}
    {error && <Notice>{error}</Notice>}
    {partialWarning && <Notice tone="warning">{partialWarning}</Notice>}
    {browsingSourceFolders ? <div id="folder-browser-results" className="source-folder-grid" onKeyDown={handleBrowseGridKeyDown}>{sources.map((source) => {
      const sourceItems = itemsBySource.get(source.id ?? "") ?? [];
      const artwork = sourcePosterUrls[source.id ?? ""] || sourceItems[0]?.posterUrl || sourceItems[0]?.backgroundUrl;
      return <button key={source.id} type="button" className="source-folder-card" onClick={() => setActiveSourceID(source.id ?? "")} aria-label={t("home.folder.openNamed", { name: source.title })}>
        <span className="source-folder-card__visual">{artwork ? <img src={artwork} alt="" loading="lazy" /> : <span>{source.title.slice(0, 2).toUpperCase()}</span>}</span>
        <span className="source-folder-card__copy"><strong>{source.title}</strong><small>{t(sourceItems.length === 1 ? "home.collection.titleCount.one" : "home.collection.titleCount.many", { count: sourceItems.length })}</small></span>
      </button>;
    })}</div> : visibleItems.length > 0 ? <div id="folder-browser-results" className="media-grid media-grid--adaptive" role={sourceView === "categories" ? "tabpanel" : undefined} onKeyDown={handleBrowseGridKeyDown}>{visibleItems.map((item) => <div className={item.mediaType === "tv" ? "tv-media-tile" : "media-tile"} key={mediaIdentity(item)}><MediaCard title={item.title} image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.mediaType === "tv" ? tvSubtitle(item) : item.releaseInfo} badge={item.mediaType === "tv" ? mediaTypeLabel("tv") : undefined} shape={item.mediaType === "tv" ? "landscape" : row.resolved.folder.tileShape} onClick={() => onOpenMedia(item)} /></div>)}</div> : <div id="folder-browser-results" role={sourceView === "categories" ? "tabpanel" : undefined}><EmptyState icon={<Clapperboard size={46} />} title={t(activeSource ? "home.source.emptyTitle" : "home.folder.emptyTitle")} description={hideUnreleased ? t("home.browser.noReleasedTitles") : t("home.collection.emptySourcesDescription")} /></div>}
    {hasMore && <div className="load-more"><Button variant="secondary" loading={loading} aria-label={t("common.actions.loadMore")} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}

export function SearchPage({ onOpenMedia, mediaRevision, onLibraryMutation, mediaPreferences }: { onOpenMedia: OpenMedia; mediaRevision: number; onLibraryMutation: () => void; mediaPreferences: MediaPreferences }) {
  const pageSize = 24;
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<MediaItem[]>([]);
  const [sourceSections, setSourceSections] = useState<SearchSourceSection[]>([]);
  const [tvLibraryMembership, setTvLibraryMembership] = useState<Map<string, string>>(() => new Map());
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [warning, setWarning] = useState("");
  const [filter, setFilter] = useState<string>("all");
  const [catalogDiscovery, setCatalogDiscovery] = useState<{ profileID: string; status: "loading" | "success" | "failure"; types: string[]; descriptors: AddonCatalogDescriptor[] }>({ profileID: "", status: "loading", types: [], descriptors: [] });
  const [hasMore, setHasMore] = useState(false);
  const [nextSkip, setNextSkip] = useState(pageSize);
  const [retryVersion, setRetryVersion] = useState(0);
  const [savingIdentity, setSavingIdentity] = useState("");
  const [checkedTvIdentities, setCheckedTvIdentities] = useState<Set<string>>(() => new Set());
  const loadedProfileRef = useRef("");
  const paginationControllerRef = useRef<AbortController | null>(null);
  const membershipRefreshControllerRef = useRef<AbortController | null>(null);
  const searchRequestGenerationRef = useRef(0);
  const membershipRevisionRef = useRef(mediaRevision);
  const localMembershipRevisionPendingRef = useRef(false);
  const normalizedQuery = query.trim();
  const discoveryReady = catalogDiscovery.profileID === mediaPreferences.profileID && catalogDiscovery.status !== "loading";
  const discoveredSearchTypes = discoveryReady ? catalogDiscovery.types : undefined;
  const searchTypes = discoveredSearchTypes === undefined
    ? []
    : filter === "all"
      ? discoveredSearchTypes
      : discoveredSearchTypes.includes(filter) ? [filter] : [];
  const selectedSearchDescriptors = filter === "all" ? [] : catalogDiscovery.descriptors.filter((descriptor) => descriptor.catalog.type === filter);
  const allSearchGroups = useMemo(() => {
    if (filter !== "all") return [];
    const itemsByType = new Map<string, MediaItem[]>();
    for (const item of items) {
      const type = item.mediaType.trim().toLowerCase() || "other";
      const groupedItems = itemsByType.get(type);
      if (groupedItems) groupedItems.push(item);
      else itemsByType.set(type, [item]);
    }
    const orderedTypes: string[] = [];
    const includedTypes = new Set<string>();
    const appendType = (rawType: string) => {
      const type = rawType.trim().toLowerCase();
      if (!type || type === "tv" || includedTypes.has(type) || !itemsByType.has(type)) return;
      includedTypes.add(type);
      orderedTypes.push(type);
    };
    for (const type of ["movie", "series", "anime"]) appendType(type);
    for (const type of catalogDiscovery.types) appendType(type);
    for (const type of itemsByType.keys()) appendType(type);
    if (itemsByType.has("tv")) orderedTypes.push("tv");
    return orderedTypes.map((type) => ({ type, items: itemsByType.get(type)! }));
  }, [catalogDiscovery.types, filter, items]);

  useEffect(() => {
    if (!mediaPreferences.profileID || loadedProfileRef.current === mediaPreferences.profileID) return;
    loadedProfileRef.current = mediaPreferences.profileID;
    setQuery(sessionStorage.getItem(`rivune.search.${mediaPreferences.profileID}`) ?? "");
    setFilter("all");
    setItems([]);
    setSourceSections([]);
  }, [mediaPreferences.profileID]);

  useEffect(() => {
    const profileID = mediaPreferences.profileID;
    const controller = new AbortController();
    let active = true;
    setCatalogDiscovery({ profileID, status: "loading", types: [], descriptors: [] });
    if (!profileID) {
      setCatalogDiscovery({ profileID, status: "success", types: [], descriptors: [] });
      return () => controller.abort();
    }
    void api.addonCatalogs(controller.signal).then((response) => {
      if (!active || controller.signal.aborted) return;
      setCatalogDiscovery({ profileID, status: "success", ...discoverSearchCatalogs(response.catalogs) });
    }).catch(() => {
      if (!active || controller.signal.aborted) return;
      setCatalogDiscovery({ profileID, status: "failure", types: [...fallbackSearchTypes], descriptors: [] });
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [mediaPreferences.profileID]);


  useEffect(() => {
    const generation = ++searchRequestGenerationRef.current;
    paginationControllerRef.current?.abort();
    membershipRefreshControllerRef.current?.abort();
    setLoadingMore(false);
    setTvLibraryMembership(new Map());
    setCheckedTvIdentities(new Set());
    if (normalizedQuery.length < 2) {
      setItems([]);
      setSourceSections([]);
      setError("");
      setWarning("");
      setLoading(false);
      setHasMore(false);
      return;
    }
    if (!discoveryReady) {
      setItems([]);
      setSourceSections([]);
      setError("");
      setWarning("");
      setLoading(true);
      setHasMore(false);
      return;
    }
    if (searchTypes.length === 0) {
      setItems([]);
      setSourceSections([]);
      setError("");
      setWarning("");
      setLoading(false);
      setHasMore(false);
      return;
    }
    setItems([]);
    setSourceSections([]);
    setError("");
    setWarning("");
    setHasMore(false);
    setLoading(true);
    const controller = new AbortController();
    let active = true;
    const timer = window.setTimeout(() => {
      const run = async () => {
        const outcomes = await Promise.allSettled(searchTypes.map((type) => api.search(type, normalizedQuery, 0, pageSize, controller.signal)));
        if (!active || controller.signal.aborted || searchRequestGenerationRef.current !== generation) return;
        const summary = summarizeSearchOutcomes(outcomes, pageSize);
        const hasFailures = summary.httpFailureCount + summary.internalFailureCount > 0;
        const hasSuccessfulResource = summary.successfulResourceResultCount > 0;
        const sourcePage = filter === "all" ? undefined : collectSearchSourcePage(summary.resourceBatches, selectedSearchDescriptors);
        const displayedItems = hasFailures && !hasSuccessfulResource ? [] : sourcePage?.items ?? mergeUniqueMedia([], summary.items);
        const displayedSourceSections = hasFailures && !hasSuccessfulResource ? [] : sourcePage?.sections ?? [];
        const identities = tvMembershipIdentities(displayedItems).slice(0, 100);
        let membership = { checked: new Set<string>(), saved: new Map<string, string>() };
        if (identities.length > 0) {
          try {
            membership = tvMembershipMaps(identities, await api.tvLibraryMembership(identities, controller.signal));
          } catch {
            if (controller.signal.aborted) return;
          }
        }
        if (!active || controller.signal.aborted || searchRequestGenerationRef.current !== generation) return;
        setItems(displayedItems);
        setSourceSections(displayedSourceSections);
        setTvLibraryMembership(membership.saved);
        setCheckedTvIdentities(membership.checked);
        setNextSkip(pageSize);
        setHasMore(hasSuccessfulResource && summary.hasFullPage);
        if (!hasFailures) {
          setError("");
          setWarning("");
        } else if (hasSuccessfulResource) {
          setError("");
          setWarning(t("search.warning.sourcesUnavailable"));
        } else {
          setError(t("search.error.sourcesUnavailable"));
          setWarning("");
        }
      };
      void run().finally(() => {
        if (active && searchRequestGenerationRef.current === generation) setLoading(false);
      });
    }, 350);
    return () => {
      active = false;
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [catalogDiscovery, filter, mediaPreferences.profileID, normalizedQuery, retryVersion]);

  useEffect(() => {
    if (membershipRevisionRef.current === mediaRevision) return;
    membershipRevisionRef.current = mediaRevision;
    if (localMembershipRevisionPendingRef.current) {
      localMembershipRevisionPendingRef.current = false;
      return;
    }
    const identities = tvMembershipIdentities(items).slice(0, 100);
    if (identities.length === 0) return;
    membershipRefreshControllerRef.current?.abort();
    const controller = new AbortController();
    const generation = searchRequestGenerationRef.current;
    membershipRefreshControllerRef.current = controller;
    void api.tvLibraryMembership(identities, controller.signal).then((result) => {
      if (controller.signal.aborted || searchRequestGenerationRef.current !== generation) return;
      const membership = tvMembershipMaps(identities, result);
      setTvLibraryMembership((current) => {
        const updated = new Map(current);
        for (const identity of identities) updated.delete(tvMembershipKey(identity.sourceAddonId, identity.resourceId));
        for (const [identity, titleID] of membership.saved) updated.set(identity, titleID);
        return updated;
      });
      setCheckedTvIdentities((current) => new Set([...current, ...membership.checked]));
    }).catch(() => undefined);
    return () => controller.abort();
  }, [items, mediaRevision]);

  useEffect(() => () => {
    paginationControllerRef.current?.abort();
    membershipRefreshControllerRef.current?.abort();
  }, []);

  async function loadMore() {
    if (!discoveryReady || searchTypes.length === 0) return;
    paginationControllerRef.current?.abort();
    const controller = new AbortController();
    const generation = searchRequestGenerationRef.current;
    paginationControllerRef.current = controller;
    setLoadingMore(true);
    try {
      const outcomes = await Promise.allSettled(searchTypes.map((type) => api.search(type, normalizedQuery, nextSkip, pageSize, controller.signal)));
      if (controller.signal.aborted || searchRequestGenerationRef.current !== generation) return;
      const summary = summarizeSearchOutcomes(outcomes, pageSize);
      const hasFailures = summary.httpFailureCount + summary.internalFailureCount > 0;
      if (summary.successfulResourceResultCount > 0) {
        const sourcePage = filter === "all" ? undefined : collectSearchSourcePage(summary.resourceBatches, selectedSearchDescriptors, items);
        const additions = sourcePage?.items ?? mergeUniqueMedia([], summary.items);
        const identities = tvMembershipIdentities(additions).slice(0, 100);
        let membership: TVMembershipMaps | undefined;
        if (identities.length > 0) {
          try {
            membership = tvMembershipMaps(identities, await api.tvLibraryMembership(identities, controller.signal));
          } catch {
            if (controller.signal.aborted) return;
          }
        }
        if (controller.signal.aborted || searchRequestGenerationRef.current !== generation) return;
        if (membership) {
          setTvLibraryMembership((current) => new Map([...current, ...membership.saved]));
          setCheckedTvIdentities((current) => new Set([...current, ...membership.checked]));
        }
        setItems((current) => mergeUniqueMedia(current, additions));
        if (sourcePage) setSourceSections((current) => mergeSearchSourceSections(current, sourcePage.sections, selectedSearchDescriptors));
        setNextSkip((current) => current + pageSize);
        setHasMore(summary.hasFullPage);
      } else if (!hasFailures) {
        setHasMore(false);
      }
      setWarning(hasFailures ? t("search.warning.sourcesUnavailable") : "");
    } finally {
      if (!controller.signal.aborted && searchRequestGenerationRef.current === generation) setLoadingMore(false);
    }
  }

  async function toggleTvLibrary(item: MediaItem) {
    const identity = mediaIdentity(item);
    const savedTitleID = tvLibraryMembership.get(identity);
    setSavingIdentity(identity);
    try {
      const titleID = savedTitleID ?? await resolveMediaTitle(item);
      if (savedTitleID) {
        await api.removeLibrary(titleID);
        setTvLibraryMembership((current) => {
          const updated = new Map(current);
          updated.delete(identity);
          return updated;
        });
      } else {
        await api.addLibrary(titleID);
        setTvLibraryMembership((current) => new Map(current).set(identity, titleID));
      }
      notifySuccess(
        t(savedTitleID ? "library.notice.removed" : "library.notice.added", { title: item.title }),
        t(savedTitleID ? "library.notice.removedTitle" : "library.notice.addedTitle"),
      );
      localMembershipRevisionPendingRef.current = true;
      onLibraryMutation();
    } catch (cause) {
      setError(notifyError(cause, t("library.error.updateFailed"), t("library.error.notUpdatedTitle")));
    } finally {
      setSavingIdentity("");
    }
  }

  function updateQuery(value: string) {
    searchRequestGenerationRef.current++;
    paginationControllerRef.current?.abort();
    membershipRefreshControllerRef.current?.abort();
    setQuery(value);
    setItems([]);
    setSourceSections([]);
    setError("");
    setWarning("");
    setHasMore(false);
    setLoading(value.trim().length >= 2);
    setLoadingMore(false);
    if (mediaPreferences.profileID) sessionStorage.setItem(`rivune.search.${mediaPreferences.profileID}`, value);
  }

  function selectSearchFilter(value: typeof filter) {
    if (value === filter) return;
    searchRequestGenerationRef.current++;
    paginationControllerRef.current?.abort();
    membershipRefreshControllerRef.current?.abort();
    setFilter(value);
    setItems([]);
    setSourceSections([]);
    setError("");
    setWarning("");
    setHasMore(false);
    setLoading(normalizedQuery.length >= 2);
    setLoadingMore(false);
  }

  function renderSearchResult(item: MediaItem) {
    const identity = mediaIdentity(item);
    const saved = item.mediaType === "tv" && tvLibraryMembership.has(identity);
    const metadata = item.mediaType === "tv" ? tvMetadata(item) : "";
    return <div className={item.mediaType === "tv" ? "tv-media-tile" : "media-tile"} key={identity}>
      <MediaCard
        shape={item.mediaType === "tv" ? "landscape" : "poster"}
        title={item.title}
        image={item.mediaType === "tv" ? item.backgroundUrl || item.posterUrl || item.logoUrl : item.posterUrl}
        backdrop={item.backgroundUrl}
        subtitle={item.mediaType === "tv" ? tvTileSubtitle(item) : item.releaseInfo || mediaTypeLabel(item.mediaType)}
        accessibleLabel={item.sourceName ? `${t("media.open", { title: item.title })} · ${item.sourceName}` : undefined}
        badge={item.mediaType === "tv" ? mediaTypeLabel("tv") : undefined}
        onClick={() => onOpenMedia(item)}
      />
      {metadata && <small className="tv-media-tile__meta">{metadata}</small>}
      {item.mediaType === "tv" && <Button className="tv-media-tile__library" variant={saved ? "secondary" : "ghost"} loading={savingIdentity === identity} disabled={!checkedTvIdentities.has(identity)} aria-label={`${t(saved ? "library.actions.inLibrary" : "library.actions.add")}: ${item.title}`} onClick={() => void toggleTvLibrary(item)}>
        {saved ? <Check size={16} /> : <Bookmark size={16} />}
        {t(saved ? "library.actions.inLibrary" : "library.actions.add")}
      </Button>}
    </div>;
  }

  return <div className="standard-page search-page page-enter">
    <SectionHeading eyebrow={t("search.eyebrow")} title={t("search.title")} description={t("search.description")} />
    <div className="search-box"><Search size={23} /><input type="search" value={query} onChange={(event) => updateQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Escape" && query) { event.preventDefault(); updateQuery(""); } }} aria-label={t("nav.search")} aria-keyshortcuts="Escape" placeholder={t("search.placeholder")} autoFocus />{query && <button type="button" className="search-box__clear" aria-label={t("common.close")} title={t("common.close")} onClick={() => updateQuery("")}><X size={17} /></button>}{loading && <LoaderCircle className="spin" />}</div>
    <div className="browse-toolbar">
      <div className="filter-pills" role="group" aria-label={t("media.filter.groupLabel")} onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "horizontal" }); }}>
        <button type="button" className={filter === "all" ? "is-active" : ""} aria-pressed={filter === "all"} onClick={() => selectSearchFilter("all")}><Compass size={16} /> {t("media.filter.all")}</button>
        {discoveredSearchTypes?.map((type) => <button key={type} type="button" className={filter === type ? "is-active" : ""} aria-pressed={filter === type} onClick={() => selectSearchFilter(type)}>
          {type === "movie" ? <Film size={16} /> : type === "series" ? <Tv size={16} /> : type === "tv" ? <Radio size={16} /> : type.toLowerCase() === "other" ? <Shapes size={16} /> : <Clapperboard size={16} />} {searchTypeLabel(type)}
        </button>)}
      </div>
      {normalizedQuery.length >= 2 && !loading && <span className="browse-toolbar__count" role="status">{t(items.length === 1 ? "common.results.count.one" : "common.results.count.many", { count: items.length })}</span>}
    </div>
    {error && <Notice><span>{error}</span><Button variant="ghost" onClick={() => setRetryVersion((version) => version + 1)}><RefreshCw size={16} /> {t("common.actions.retry")}</Button></Notice>}
    {warning && <Notice tone="warning">{warning}</Notice>}
    {normalizedQuery.length < 2
      ? <div className="search-prompt"><span><Search /></span><h2>{t("search.prompt.title")}</h2><p>{t("search.prompt.description")}</p></div>
      : (loading || error) && items.length === 0
        ? <div className={`media-grid media-grid--adaptive browse-skeleton-grid${error ? " browse-skeleton-grid--replacement" : ""}`} aria-busy={loading || undefined} aria-hidden={error ? true : undefined}>{[0, 1, 2, 3, 4, 5].map((value) => <Skeleton key={value} className={filter === "tv" ? "card-skeleton card-skeleton--landscape" : "card-skeleton"} />)}</div>
        : items.length === 0
          ? <EmptyState icon={<Search size={42} />} title={t("search.empty.title")} description={t("search.empty.description")} />
          : filter === "all"
            ? <div className="search-result-groups" onKeyDown={handleBrowseGridKeyDown}>{allSearchGroups.map((group) => <section className="search-result-section" key={group.type}>
              <SectionHeading title={searchTypeLabel(group.type)} />
              <div className="media-grid media-grid--adaptive">{group.items.map(renderSearchResult)}</div>
            </section>)}</div>
            : <div className="search-result-groups" onKeyDown={handleBrowseGridKeyDown}>{sourceSections.map((section) => <section className="search-result-section" key={section.key}>
              <SectionHeading title={section.label} />
              <div className="media-grid media-grid--adaptive">{section.items.map(renderSearchResult)}</div>
            </section>)}</div>}
    {hasMore && items.length > 0 && <div className="load-more"><Button variant="secondary" loading={loadingMore} aria-label={t("common.actions.loadMore")} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}

export function LibraryPage({ onOpenMedia, mediaRevision }: { onOpenMedia: OpenMedia; mediaRevision: number }) {
  const pageSize = 100;
  const { account, activeProfile, discovery } = useAuth();
  const profileID = activeProfile?.id ?? "";
  const principalScope = principalIdentity(discovery, account, activeProfile);
  const [library, setLibrary] = useState<LoadedLibrary | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [filter, setFilter] = useState<"" | "movie" | "series" | "tv">("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<"added" | "title" | "released">("added");
  const [error, setError] = useState<{ message: string; retry: "initial" | "more" | "refresh" } | null>(null);
  const [retryVersion, setRetryVersion] = useState(0);
  const libraryRevisionRef = useRef(0);
  const libraryRequestGeneration = useRef(0);

  useEffect(() => {
    const generation = ++libraryRequestGeneration.current;
    let active = true;
    setLibrary(null);
    setLoading(true);
    setLoadingMore(false);
    setError(null);
    if (!profileID || principalScope === null) return () => { active = false; };
    void loadInitialLibraryPage(principalScope, filter, pageSize).then((value) => {
      if (!active || libraryRequestGeneration.current !== generation) return;
      setLibrary({
        pages: { [value.page]: value.items },
        page: value.page,
        totalPages: value.totalPages,
        totalResults: value.totalResults,
      });
    }).catch(() => {
      if (active && libraryRequestGeneration.current === generation) {
        setError({ message: t("library.error.loadFailed"), retry: "initial" });
      }
    }).finally(() => {
      if (active && libraryRequestGeneration.current === generation) setLoading(false);
    });
    return () => { active = false; };
  }, [filter, principalScope, profileID, retryVersion]);

  useEffect(() => {
    if (mediaRevision === 0 || libraryRevisionRef.current === mediaRevision || !library) return;
    libraryRevisionRef.current = mediaRevision;
    void refreshLoadedPages();
  }, [mediaRevision]);

  async function refreshLoadedPages() {
    if (!library) return;
    const generation = ++libraryRequestGeneration.current;
    setLoadingMore(false);
    const successful: LibraryPage[] = [];
    let failureCount = 0;
    for (let page = 1; page <= Math.max(1, library.page); page++) {
      try {
        successful.push(await api.library(filter, page, pageSize));
      } catch {
        failureCount++;
      }
      if (libraryRequestGeneration.current !== generation) return;
    }
    setLibrary((current) => {
      if (!current) return current;
      const pages = { ...current.pages };
      for (const value of successful) pages[value.page] = value.items;
      const metadata = successful.find((value) => value.page === 1) ?? successful[0];
      const totalPages = metadata?.totalPages ?? current.totalPages;
      for (const key of Object.keys(pages)) {
        if (Number(key) > totalPages) delete pages[Number(key)];
      }
      return {
        pages,
        page: Math.min(current.page, Math.max(1, totalPages)),
        totalPages,
        totalResults: metadata?.totalResults ?? current.totalResults,
      };
    });
    setError(failureCount === 0 ? null : { message: t("library.error.refreshFailed"), retry: "refresh" });
  }

  async function loadMore() {
    if (!library || loadingMore || library.page >= library.totalPages) return;
    const generation = libraryRequestGeneration.current;
    const nextPage = library.page + 1;
    setLoadingMore(true);
    setError(null);
    try {
      const value = await api.library(filter, nextPage, pageSize);
      if (libraryRequestGeneration.current !== generation) return;
      setLibrary((current) => current ? {
        pages: { ...current.pages, [value.page]: value.items },
        page: value.page,
        totalPages: value.totalPages,
        totalResults: value.totalResults,
      } : current);
    } catch {
      if (libraryRequestGeneration.current === generation) {
        setError({ message: t("library.error.loadFailed"), retry: "more" });
      }
    } finally {
      if (libraryRequestGeneration.current === generation) setLoadingMore(false);
    }
  }

  function selectLibraryFilter(value: typeof filter) {
    if (value === filter) return;
    libraryRequestGeneration.current++;
    setLibrary(null);
    setLoading(true);
    setLoadingMore(false);
    setError(null);
    setFilter(value);
  }

  function retryLibraryRequest() {
    if (error?.retry === "more") void loadMore();
    else if (error?.retry === "refresh") void refreshLoadedPages();
    else setRetryVersion((version) => version + 1);
  }

  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredItems = useMemo(() => {
    const items = libraryItems(library).filter((item) => !normalizedQuery || `${item.title ?? ""} ${item.releaseInfo ?? ""} ${item.sourceName ?? ""} ${item.country ?? ""} ${item.language ?? ""} ${item.category ?? ""}`.toLocaleLowerCase().includes(normalizedQuery));
    items.sort((left, right) => {
      if (sort === "title") return (left.title ?? "").localeCompare(right.title ?? "");
      const leftDate = Date.parse(sort === "released" ? left.released ?? "" : left.addedAt);
      const rightDate = Date.parse(sort === "released" ? right.released ?? "" : right.addedAt);
      return (Number.isNaN(rightDate) ? 0 : rightDate) - (Number.isNaN(leftDate) ? 0 : leftDate);
    });
    return items;
  }, [library, normalizedQuery, sort]);
  const media = filteredItems.map((item) => mediaFromLibraryItem(item, t("media.untitled")));
  const hasMore = Boolean(library && library.page < library.totalPages);

  return <div className="standard-page library-page page-enter">
    <SectionHeading eyebrow={t("library.eyebrow")} title={t("library.title")} description={t("library.description")} />
    <div className="library-controls">
      <div className="search-box search-box--compact"><Search size={19} /><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Escape" && query) { event.preventDefault(); setQuery(""); } }} aria-label={t("nav.search")} aria-keyshortcuts="Escape" placeholder={t("search.placeholder")} />{query && <button type="button" className="search-box__clear" aria-label={t("common.close")} title={t("common.close")} onClick={() => setQuery("")}><X size={16} /></button>}</div>
      <label className="library-sort"><span>{t("admin.collections.sources.sortBy")}</span><select value={sort} onChange={(event) => setSort(event.target.value as typeof sort)}><option value="added">{t("admin.collections.sort.added")}</option><option value="title">{t("admin.collections.sort.title")}</option><option value="released">{t("admin.collections.sort.released")}</option></select></label>
    </div>
    <div className="browse-toolbar">
      <div className="filter-pills" role="group" aria-label={t("media.filter.groupLabel")} onKeyDown={(event) => { handleDirectionalFocus(event, { orientation: "horizontal" }); }}>
        <button type="button" className={filter === "" ? "is-active" : ""} aria-pressed={filter === ""} onClick={() => selectLibraryFilter("")}><Bookmark size={16} /> {t("media.filter.allTitles")}</button>
        <button type="button" className={filter === "movie" ? "is-active" : ""} aria-pressed={filter === "movie"} onClick={() => selectLibraryFilter("movie")}><Film size={16} /> {t("media.filter.movies")}</button>
        <button type="button" className={filter === "series" ? "is-active" : ""} aria-pressed={filter === "series"} onClick={() => selectLibraryFilter("series")}><Tv size={16} /> {t("media.filter.series")}</button>
        <button type="button" className={filter === "tv" ? "is-active" : ""} aria-pressed={filter === "tv"} onClick={() => selectLibraryFilter("tv")}><Radio size={16} /> {t("media.type.liveTv")}</button>
      </div>
      {!loading && <span className="browse-toolbar__count" role="status">{t(media.length === 1 ? "common.results.count.one" : "common.results.count.many", { count: media.length })}</span>}
    </div>
    {error && <Notice><span>{error.message}</span><Button variant="ghost" onClick={retryLibraryRequest}><RefreshCw size={16} /> {t("common.actions.retry")}</Button></Notice>}
    {loading
      ? <div className="media-grid media-grid--adaptive browse-skeleton-grid" aria-busy="true">{[0, 1, 2, 3, 4, 5].map((value) => <Skeleton key={value} className="card-skeleton" />)}</div>
      : media.length > 0
        ? <div className="media-grid media-grid--adaptive" aria-busy={loadingMore || undefined} onKeyDown={handleBrowseGridKeyDown}>{media.map((item) => {
          const metadata = item.mediaType === "tv" ? tvMetadata(item) : "";
          return <div className="media-tile" key={item.titleId || mediaIdentity(item)}>
            <MediaCard shape="poster" title={item.title} image={item.mediaType === "tv" ? item.posterUrl || item.logoUrl || item.backgroundUrl : item.posterUrl} backdrop={item.backgroundUrl} subtitle={item.mediaType === "tv" ? item.available === false ? t("common.status.unavailable") : tvTileSubtitle(item) : item.releaseInfo || mediaTypeLabel(item.mediaType)} badge={item.mediaType === "tv" ? mediaTypeLabel("tv") : undefined} onClick={() => onOpenMedia(item)} />
            {metadata && <small className="tv-media-tile__meta">{metadata}</small>}
          </div>;
        })}</div>
        : query
          ? <EmptyState icon={<Search size={42} />} title={t("search.empty.title")} description={t("search.empty.description")} />
          : <EmptyState icon={<Bookmark size={46} />} title={t("library.empty.title")} description={t("library.empty.description")} />}
    {hasMore && !error && <div className="load-more"><Button variant="secondary" loading={loadingMore} aria-label={t("common.actions.loadMore")} onClick={() => void loadMore()}>{t("common.actions.loadMore")}</Button></div>}
  </div>;
}
