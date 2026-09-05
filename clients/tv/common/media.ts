import type {
  AddonResourceBatch,
  AddonResourceResult,
  CollectionItem,
  ContinueWatchingItem,
  Episode,
  LibraryItem,
  MediaItem,
  Movie,
  Season,
  Series,
  TitleMediaType,
  TitleResolveInput,
} from "./types";
import { t } from "./i18n";

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function text(value: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  }
  return undefined;
}

export function resourceId(item: MediaItem): string {
  return item.resourceId?.trim() || item.id;
}

export function releaseDate(value?: string): string | undefined {
  const match = value?.trim().match(/^\d{4}-\d{2}-\d{2}/);
  return match?.[0];
}

export function mediaFromLibrary(item: LibraryItem): MediaItem {
  return {
    id: item.resourceId || item.externalId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.title || t("media.untitled"),
    posterUrl: item.posterUrl ?? undefined,
    backgroundUrl: item.backgroundUrl ?? undefined,
    releaseInfo: item.releaseInfo ?? undefined,
    externalIds: item.externalId && item.provider ? { [item.provider]: item.externalId } : undefined,
    resourceId: item.resourceId ?? undefined,
    sourceAddonId: item.sourceAddonId ?? undefined,
    sourceCatalogId: item.sourceCatalogId ?? undefined,
    sourceName: item.sourceName ?? undefined,
    country: item.country ?? undefined,
    language: item.language ?? undefined,
    category: item.category ?? undefined,
    available: item.available,
  };
}

export function mediaFromContinue(item: ContinueWatchingItem): MediaItem {
  const episode = item.mediaType === "episode";
  return {
    id: item.resourceId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    seasonNumber: item.seasonNumber ?? undefined,
    episodeNumber: item.episodeNumber ?? undefined,
    mappingProvider: item.mappingProvider ?? undefined,
    episodeOrderId: item.episodeOrderId ?? undefined,
    metadataSeasonId: item.metadataSeasonId ?? undefined,
    title: episode ? item.episodeTitle || `${t("media.episodes")} ${item.episodeNumber ?? ""}`.trim() : item.title || t("media.untitled"),
    posterUrl: item.episodeStillUrl || item.posterUrl || undefined,
    backgroundUrl: item.episodeStillUrl || item.backgroundUrl || item.posterUrl || undefined,
    releaseInfo: (episode ? item.episodeAirDate || item.releaseInfo : item.releaseInfo) || undefined,
    released: episode ? item.episodeAirDate ?? undefined : undefined,
    resourceId: item.resourceId ?? undefined,
    externalIds: item.resourceProvider && item.resourceId ? { [item.resourceProvider]: item.resourceId } : undefined,
    resumePositionSeconds: item.positionSeconds,
    durationSeconds: item.durationSeconds,
    progressVersion: item.version,
    seriesId: item.seriesId ?? undefined,
    seasonId: item.seasonId ?? undefined,
  };
}

function currentProgram(value: unknown): string | undefined {
  if (typeof value === "string" && value.trim()) return value.trim();
  const object = record(value);
  return object ? text(object, "title", "name") : undefined;
}

export function mediaFromResource(result: AddonResourceResult): MediaItem[] {
  const metas = result.payload.metas;
  if (!Array.isArray(metas)) return [];
  const output: MediaItem[] = [];
  for (const candidate of metas) {
    const meta = record(candidate);
    if (!meta || typeof meta.id !== "string" || !meta.id.trim()) continue;
    const mediaType = text(meta, "type") || result.type;
    const itemResource = text(meta, "resourceId") || meta.id;
    const logo = text(meta, "logo", "logoUrl");
    const poster = text(meta, "poster", "posterUrl");
    const background = text(meta, "background", "backgroundUrl", "backdrop");
    output.push({
      id: itemResource,
      resourceId: itemResource,
      mediaType,
      title: text(meta, "name", "title") || t("media.untitled"),
      posterUrl: mediaType === "tv" ? poster || background || logo : poster,
      backgroundUrl: mediaType === "tv" ? background || poster || logo : background,
      logoUrl: logo,
      description: text(meta, "description", "overview"),
      releaseInfo: text(meta, "releaseInfo"),
      released: text(meta, "released"),
      voteAverage: typeof meta.imdbRating === "number" ? meta.imdbRating : undefined,
      externalIds: {},
      sourceAddonId: text(meta, "sourceAddonId") || result.addonId,
      sourceCatalogId: text(meta, "sourceCatalogId", "catalogId") || result.id,
      sourceName: text(meta, "sourceName", "source"),
      country: mediaType === "tv" ? text(meta, "country", "countryCode") : undefined,
      language: mediaType === "tv" ? text(meta, "language", "lang") : undefined,
      category: mediaType === "tv" ? text(meta, "category", "genre") : undefined,
      available: meta.available !== false,
      currentProgram: mediaType === "tv" ? currentProgram(meta.currentProgram) : undefined,
      raw: meta,
    });
  }
  return output;
}

export function mediaFromResourceBatch(batch: AddonResourceBatch): MediaItem[] {
  const items: MediaItem[] = [];
  for (const result of batch.results) items.push(...mediaFromResource(result));
  return items;
}

export function mediaFromCollection(item: CollectionItem): MediaItem {
  const source = item.sources.find((candidate) => candidate.addonId) ?? item.sources[0];
  return {
    id: item.id,
    resourceId: item.id,
    mediaType: item.mediaType,
    title: item.title,
    posterUrl: item.posterUrl ?? undefined,
    backgroundUrl: item.backgroundUrl ?? undefined,
    logoUrl: item.logoUrl ?? undefined,
    description: item.description ?? undefined,
    releaseInfo: item.releaseInfo ?? undefined,
    released: item.released ?? undefined,
    voteAverage: item.voteAverage ?? undefined,
    voteCount: item.voteCount ?? undefined,
    popularity: item.popularity ?? undefined,
    externalIds: item.externalIds,
    sourceAddonId: source?.addonId ?? undefined,
    sourceCatalogId: source?.catalogId ?? undefined,
    sourceName: source?.title,
  };
}

export function titleResolveInput(item: MediaItem): TitleResolveInput {
  const id = resourceId(item);
  const preferred = ["tmdb", "imdb", "tvdb", "trakt"].find((provider) => item.externalIds?.[provider]);
  const namespaced = item.id.match(/^([a-z0-9._-]+):(.+)$/i);
  const provider = item.mediaType === "tv" ? "addon" : preferred ?? namespaced?.[1]?.toLowerCase() ?? (/^tt\d+$/i.test(item.id) ? "imdb" : "addon");
  const externalId = item.mediaType === "tv" ? id : preferred ? item.externalIds?.[preferred] ?? item.id : namespaced?.[2] ?? item.id;
  const mediaType: TitleMediaType = item.mediaType === "movie" || item.mediaType === "tv" ? item.mediaType : "series";
  return {
    mediaType,
    provider,
    externalId,
    resourceId: id,
    title: item.title,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: releaseDate(item.released),
    sourceAddonId: item.sourceAddonId,
    sourceCatalogId: item.sourceCatalogId,
    sourceName: item.sourceName,
    country: item.country,
    language: item.language,
    category: item.category,
  };
}

export function mediaFromMovie(movie: Movie, fallback: MediaItem): MediaItem {
  return { ...fallback, id: movie.id, titleId: movie.id, mediaType: "movie", title: movie.title, posterUrl: movie.posterUrl || fallback.posterUrl, backgroundUrl: movie.backdropUrl || fallback.backgroundUrl, logoUrl: movie.logoUrl || fallback.logoUrl, description: movie.overview || fallback.description, released: movie.releaseDate ?? undefined, voteAverage: movie.voteAverage, externalIds: movie.externalIds };
}

export function mediaFromSeries(series: Series, fallback: MediaItem): MediaItem {
  return { ...fallback, id: series.id, titleId: series.id, mediaType: "series", title: series.name, posterUrl: series.posterUrl || fallback.posterUrl, backgroundUrl: series.backdropUrl || fallback.backgroundUrl, logoUrl: series.logoUrl || fallback.logoUrl, description: series.overview || fallback.description, released: series.firstAirDate ?? undefined, voteAverage: series.voteAverage, externalIds: series.externalIds };
}

export function mediaFromEpisode(episode: Episode, series: Series, season: Season, fallback: MediaItem): MediaItem {
  const selectedOrderId = series.selectedEpisodeOrderId?.trim();
  const selectedOrder = selectedOrderId
    ? series.episodeOrders.find((order) => order.id === selectedOrderId)
    : undefined;
  const selectedNonOfficialOrder = Boolean(selectedOrderId && selectedOrder?.type.trim().toLowerCase() !== "official");
  const variant = Boolean(fallback.episodeOrderId) || selectedNonOfficialOrder;
  const episodeOrderId = fallback.episodeOrderId || (selectedNonOfficialOrder ? selectedOrderId : undefined);
  const metadataSeasonId = fallback.metadataSeasonId === season.id
    ? fallback.metadataSeasonId
    : variant ? season.id : undefined;
  const persistedSeasonId = fallback.metadataSeasonId === season.id && fallback.seasonId
    ? fallback.seasonId
    : season.id;
  const resource = variant && episode.externalIds.tvdb
    ? `tvdb:${episode.externalIds.tvdb}`
    : series.externalIds.imdb
      ? `${series.externalIds.imdb}:${episode.seasonNumber}:${episode.episodeNumber}`
      : episode.externalIds.imdb || episode.externalIds.tvdb && `tvdb:${episode.externalIds.tvdb}` || episode.id;
  return {
    id: resource,
    resourceId: resource,
    titleId: episode.id,
    mediaType: "episode",
    seasonNumber: episode.seasonNumber,
    episodeNumber: episode.episodeNumber,
    mappingProvider: variant ? "tvdb" : undefined,
    episodeOrderId,
    metadataSeasonId,
    resumePositionSeconds: fallback.titleId === episode.id ? fallback.resumePositionSeconds : undefined,
    durationSeconds: fallback.titleId === episode.id ? fallback.durationSeconds : undefined,
    progressVersion: fallback.titleId === episode.id ? fallback.progressVersion : undefined,
    title: episode.name || `${t("media.episodes")} ${episode.episodeNumber}`,
    posterUrl: episode.stillUrl || season.backdropUrl || fallback.posterUrl,
    backgroundUrl: episode.backdropUrl || episode.stillUrl || series.backdropUrl || fallback.backgroundUrl,
    description: episode.overview,
    released: episode.airDate ?? undefined,
    voteAverage: episode.voteAverage,
    externalIds: episode.externalIds,
    seriesId: series.id,
    seasonId: persistedSeasonId,
  };
}

export function mediaSubtitle(item: MediaItem): string {
  if (typeof item.currentProgram === "string") return item.currentProgram;
  if (item.currentProgram && typeof item.currentProgram === "object") return item.currentProgram.title || "";
  return [item.releaseInfo, item.category, item.sourceName].filter(Boolean).join(" · ") || item.mediaType;
}
