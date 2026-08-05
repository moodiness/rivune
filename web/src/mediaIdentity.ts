import { api } from "./api";
import type { LibraryItem, MediaItem } from "./types";

export type MediaIdentityInput = Pick<MediaItem, "id" | "mediaType" | "resourceId" | "sourceAddonId" | "titleId">;

export function mediaResourceID(item: Pick<MediaItem, "id" | "resourceId">): string {
  return item.resourceId?.trim() || item.id;
}

export function mediaIdentity(item: MediaIdentityInput): string {
  if (item.mediaType === "tv") return `tv:${item.sourceAddonId?.trim() ?? ""}:${mediaResourceID(item)}`;
  return `${item.mediaType}:${item.titleId?.trim() || item.id}`;
}


export function titleReleaseDate(value: string | undefined): string | undefined {
  const normalized = value?.trim();
  if (!normalized) return undefined;
  const match = normalized.match(/^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2})(?:\.\d+)?)?(?:(Z)|([+-])(\d{2}):(\d{2}))?)?$/);
  if (!match) return undefined;
  const [, yearValue, monthValue, dayValue, hourValue, minuteValue, secondValue, utcValue, offsetSign, offsetHourValue, offsetMinuteValue] = match;
  const year = Number(yearValue);
  const month = Number(monthValue);
  const day = Number(dayValue);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const monthDays = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > monthDays[month - 1]) return undefined;
  if (hourValue === undefined) return normalized;
  const hour = Number(hourValue);
  const minute = Number(minuteValue);
  const second = secondValue === undefined ? 0 : Number(secondValue);
  if (hour > 23 || minute > 59 || second > 59) return undefined;
  if (!utcValue && offsetSign) {
    const offsetHour = Number(offsetHourValue);
    const offsetMinute = Number(offsetMinuteValue);
    if (offsetHour > 14 || offsetMinute > 59 || offsetHour === 14 && offsetMinute !== 0) return undefined;
  }
  return `${yearValue}-${monthValue}-${dayValue}`;
}

export async function resolveMediaTitle(item: MediaItem): Promise<string> {
  if (item.titleId) return item.titleId;
  const resourceId = mediaResourceID(item);
  const preferred = ["tmdb", "imdb", "tvdb", "trakt"].find((provider) => item.externalIds?.[provider]);
  const namespaced = item.id.match(/^([a-z0-9._-]+):(.+)$/i);
  const provider = item.mediaType === "tv"
    ? "addon"
    : preferred ?? (namespaced ? namespaced[1].toLowerCase() : /^tt\d+$/i.test(item.id) ? "imdb" : "addon");
  const externalId = item.mediaType === "tv"
    ? resourceId
    : preferred ? item.externalIds?.[preferred] ?? item.id : namespaced?.[2] ?? item.id;
  const resolved = await api.resolveTitle({
    mediaType: item.mediaType,
    provider,
    externalId,
    resourceId,
    title: item.title,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: titleReleaseDate(item.released),
    sourceAddonId: item.sourceAddonId,
    sourceCatalogId: item.sourceCatalogId,
    sourceName: item.sourceName,
    country: item.country,
    language: item.language,
    category: item.category,
  });
  return resolved.titleId;
}

export function mediaFromLibraryItem(item: LibraryItem, untitled: string): MediaItem {
  return {
    id: item.resourceId || item.externalId || item.titleId,
    titleId: item.titleId,
    mediaType: item.mediaType,
    title: item.title || untitled,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: item.released,
    externalIds: item.externalId && item.provider ? { [item.provider]: item.externalId } : undefined,
    resourceId: item.resourceId,
    sourceAddonId: item.sourceAddonId,
    sourceCatalogId: item.sourceCatalogId,
    sourceName: item.sourceName,
    country: item.country,
    language: item.language,
    category: item.category,
    available: item.available,
    currentProgram: item.currentProgram,
  };
}
