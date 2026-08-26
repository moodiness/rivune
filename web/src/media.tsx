import { ArrowLeft, AudioLines, Bookmark, Captions, Check, Clapperboard, ExternalLink, Eye, EyeOff, Gauge, Info, ListVideo, LoaderCircle, Maximize, Minimize, MonitorSmartphone, Pause, PictureInPicture, Play, RefreshCw, RotateCcw, RotateCw, ServerCrash, Settings2, SkipForward, Star, Volume2, VolumeX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from "react";
import { createPortal } from "react-dom";
import { activeProfileRequestID, api, APIError } from "./api";
import { Button, focusableElements, HorizontalDragRow, IconButton, Notice, Select } from "./components";
import { translate as t } from "./i18n";
import { mediaFromLibraryItem, mediaIdentity, mediaResourceID, resolveMediaTitle, titleReleaseDate } from "./mediaIdentity";
import { cachedMediaItem, cacheMediaItem, flushMetadataCache } from "./metadataCache";
import { notifyError, notifyErrorMessage, notifySuccess, notifyWarning } from "./notifications";
import { playbackDecisionOutcome, playbackDecisionReasons } from "./playbackDecision";
import { listenForPlaybackCommands, playbackItem, publishPlaybackCommandResult, publishPlaybackState } from "./playbackCoordination";
import { applyQualityLimits } from "./playbackQuality";
import { enqueueReadingQueue } from "./readingQueue";
import { TITLE_ID_PROVIDERS, titleProviderURL } from "./titleProviders";
import type { CastMember, CustomSeriesResolveResult, EpisodeMetadata, MediaItem, PlaybackCapabilities, PlaybackCommand, PlaybackDevice, PlaybackFailoverError, PlaybackFailoverState, PlaybackLoadMode, PlaybackMarker, PlaybackPreparation, PlaybackProgress, PlaybackSource, PlaybackSourceOption, PlaybackSubtitle, ResourceBatch, ResourceResult, SeasonMetadata, SeriesMetadata, TrailerMetadata } from "./types";
export type CanonicalRouteMetadata = {
  sourceID: string;
  sourceMediaType: string;
  titleID: string;
  titleMediaType: "movie" | "series";
  externalIds: Record<string, string>;
};


type ExternalTitleLink = {
  externalID: string;
  provider: (typeof TITLE_ID_PROVIDERS)[number];
  mediaType: string;
  episode: EpisodeMetadata | undefined;
};

function record(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.fromEntries(Object.entries(value));
}

function textValue(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return undefined;
}

function localizedArtworkURL(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim();
  return normalized.startsWith("/api/v1/artwork/") ? normalized : undefined;
}

function localizedArtworkValue(...values: unknown[]): string | undefined {
  for (const value of values) {
    const localized = localizedArtworkURL(value);
    if (localized) return localized;
  }
  return undefined;
}

function castCandidates(value: unknown): unknown[] {
  const envelope = record(value);
  return Array.isArray(value)
    ? value
    : typeof value === "string"
      ? value.split(",").map((name) => name.trim()).filter(Boolean)
      : [envelope?.cast, envelope?.actors, envelope?.items].find(Array.isArray) ?? [];
}

function stremioCastLinkCandidates(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((candidate) => {
    const link = record(candidate);
    const category = textValue(link?.category)?.toLowerCase();
    if (category !== "cast" && category !== "actor" && category !== "actors") return [];
    const name = textValue(link?.name);
    return name ? [name] : [];
  });
}

function castMembers(value: unknown, maximumCastMembers: number, rawAddon = false): CastMember[] {
  if (maximumCastMembers <= 0) return [];
  const candidates = castCandidates(value);
  const members: CastMember[] = [];
  const seenIDs = new Set<string>();
  const seenNames = new Set<string>();
  for (const candidate of candidates) {
    const person = record(candidate);
    const nestedPerson = record(person?.person);
    const name = typeof candidate === "string"
      ? candidate.trim()
      : textValue(person?.name, person?.actor, nestedPerson?.name);
    if (!name) continue;
    const explicitID = textValue(person?.id, nestedPerson?.id) ?? "";
    const normalizedName = name.toLowerCase();
    if (explicitID ? seenIDs.has(explicitID) : seenNames.has(normalizedName)) continue;
    const id = explicitID || `name:${normalizedName}`;
    const profileCandidates = [person?.profileUrl, person?.profile, person?.photo, person?.imageUrl, person?.image, nestedPerson?.profileUrl, nestedPerson?.profile, nestedPerson?.photo, nestedPerson?.imageUrl, nestedPerson?.image];
    const profileUrl = rawAddon
      ? localizedArtworkValue(...profileCandidates)
      : textValue(...profileCandidates);
    const character = textValue(person?.character, person?.role, person?.characterName);
    members.push({ id, name, ...(character ? { character } : {}), ...(profileUrl ? { profileUrl } : {}) });
    if (explicitID) seenIDs.add(explicitID);
    else seenNames.add(normalizedName);
    if (members.length >= maximumCastMembers) break;
  }
  return members;
}

function castInitials(name: string): string {
  return name.split(/\s+/).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
}

function CastMemberCard({ member }: { member: CastMember }) {
  return <article className="details-cast-member">
    <span className="details-cast-member__portrait">
      {member.profileUrl ? <img src={member.profileUrl} alt="" loading="lazy" /> : <span>{castInitials(member.name)}</span>}
    </span>
    <span className="details-cast-member__copy">
      <strong>{member.name}</strong>
      {member.character && <small>{member.character}</small>}
    </span>
  </article>;
}

function handleCastCarouselKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const carousel = event.currentTarget;
  if (event.key === "Home" || event.key === "End") {
    carousel.scrollTo({ left: event.key === "Home" ? 0 : carousel.scrollWidth, behavior: "smooth" });
    return;
  }
  const firstMember = carousel.querySelector<HTMLElement>(".details-cast-member");
  const gap = Number.parseFloat(getComputedStyle(carousel).columnGap) || 0;
  const distance = (firstMember?.offsetWidth ?? carousel.clientWidth) + gap;
  carousel.scrollBy({ left: event.key === "ArrowRight" ? distance : -distance, behavior: "smooth" });
}

function trailerLanguageBadge(trailer: TrailerMetadata): string {
  const language = trailer.language.trim().replaceAll("_", "-").toLowerCase();
  if (language === "fr" || language.startsWith("fr-")) return t("language.name.fr");
  if (language === "en" || language.startsWith("en-")) return t("language.name.en");
  return language.toUpperCase();
}

function episodeOrderLabel(order: SeriesMetadata["episodeOrders"][number]): string {
  switch (order.type.trim().toLowerCase()) {
    case "official": return t("media.episodeOrder.aired");
    case "dvd": return t("media.episodeOrder.dvd");
    case "absolute": return t("media.episodeOrder.absolute");
    default: return order.name;
  }
}

type SelectedPayloadRecord = { value: Record<string, unknown>; result: ResourceResult };

function payloadRecord(result: ResourceResult, key: string): Record<string, unknown> | undefined {
  const value = result.payload[key];
  if (Array.isArray(value)) return value.map(record).find((candidate) => candidate !== null) ?? undefined;
  return record(value) ?? undefined;
}

function firstPayloadRecord(batch: ResourceBatch, key: string, preferredAddonID?: string): SelectedPayloadRecord | undefined {
  if (preferredAddonID) {
    for (const result of batch.results) {
      if (result.addonId !== preferredAddonID) continue;
      const value = payloadRecord(result, key);
      if (value) return { value, result };
    }
  }
  for (const result of batch.results) {
    if (preferredAddonID && result.addonId === preferredAddonID) continue;
    const value = payloadRecord(result, key);
    if (value) return { value, result };
  }
  return undefined;
}

type CustomMetaVideo = {
  id: string;
  title: string;
  overview: string;
  released: string;
  thumbnail?: string;
  background?: string;
  season?: number;
  episode?: number;
  raw: Record<string, unknown>;
};

type CustomMetaPlayback = {
  id?: string;
  defaultVideoId?: string;
  videos: CustomMetaVideo[];
};

const customUnseasonedGroupKey = "unseasoned";
const emptyCustomVideos: CustomMetaVideo[] = [];

type CustomVideoSeasonGroup = {
  key: string;
  season?: number;
  videos: CustomMetaVideo[];
};

function customVideoSeasonKey(video: CustomMetaVideo): string {
  return video.season === undefined ? customUnseasonedGroupKey : `season:${video.season}`;
}

function groupCustomVideos(videos: CustomMetaVideo[]): CustomVideoSeasonGroup[] {
  const seasons = new Map<number, CustomMetaVideo[]>();
  const unseasoned: CustomMetaVideo[] = [];
  for (const video of videos) {
    if (video.season === undefined) {
      unseasoned.push(video);
      continue;
    }
    const group = seasons.get(video.season);
    if (group) group.push(video);
    else seasons.set(video.season, [video]);
  }
  const byEpisodeNumber = (left: CustomMetaVideo, right: CustomMetaVideo) => (left.episode ?? Number.MAX_SAFE_INTEGER) - (right.episode ?? Number.MAX_SAFE_INTEGER);
  const groups = Array.from(seasons, ([season, seasonVideos]) => ({
    key: `season:${season}`,
    season,
    videos: [...seasonVideos].sort(byEpisodeNumber),
  })).sort((left, right) => left.season - right.season);
  return unseasoned.length > 0
    ? [...groups, { key: customUnseasonedGroupKey, videos: [...unseasoned].sort(byEpisodeNumber) }]
    : groups;
}

function opaqueID(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function nonnegativeInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 2_147_483_647 ? value : undefined;
}

function customMetaPlayback(meta: Record<string, unknown>): CustomMetaPlayback {
  const behaviorHints = record(meta.behaviorHints);
  const seen = new Set<string>();
  const videos = Array.isArray(meta.videos) ? meta.videos.flatMap<CustomMetaVideo>((candidate) => {
    const video = record(candidate);
    const id = opaqueID(video?.id);
    if (!video || !id || seen.has(id)) return [];
    seen.add(id);
    return [{
      id,
      title: textValue(video.title, video.name) ?? "",
      overview: textValue(video.overview, video.description) ?? "",
      released: textValue(video.released, video.releaseInfo) ?? "",
      thumbnail: localizedArtworkValue(video.thumbnail, video.thumbnailUrl, video.poster),
      background: localizedArtworkValue(video.background, video.backgroundUrl),
      season: nonnegativeInteger(video.season),
      episode: nonnegativeInteger(video.episode),
      raw: video,
    }];
  }) : [];
  return {
    id: opaqueID(meta.id),
    defaultVideoId: opaqueID(behaviorHints?.defaultVideoId),
    videos,
  };
}

function customVideoReleaseInfo(video: CustomMetaVideo): string {
  return titleReleaseDate(video.released) ?? video.released;
}

function customVideoIsUpcoming(video: CustomMetaVideo): boolean {
  const released = titleReleaseDate(video.released);
  if (!released) return false;
  const releaseDate = new Date(`${released}T23:59:59Z`);
  return Number.isFinite(releaseDate.getTime()) && releaseDate.getTime() > Date.now();
}

function resolvableCustomVideos(videos: CustomMetaVideo[]): Array<CustomMetaVideo & { season: number; episode: number }> | undefined {
  const resolved: Array<CustomMetaVideo & { season: number; episode: number }> = [];
  const coordinates = new Set<string>();
  for (const video of videos) {
    if (video.season === undefined || video.episode === undefined) continue;
    const coordinate = `${video.season}:${video.episode}`;
    if (coordinates.has(coordinate)) return undefined;
    coordinates.add(coordinate);
    resolved.push({ ...video, season: video.season, episode: video.episode });
    if (resolved.length > 4096) return undefined;
  }
  return resolved;
}

function customVideoItem(video: CustomMetaVideo, fallback: MediaItem): MediaItem {
  const releaseInfo = customVideoReleaseInfo(video);
  return {
    ...fallback,
    id: video.id,
    titleId: undefined,
    title: video.title || fallback.title,
    posterUrl: fallback.posterUrl,
    backgroundUrl: video.background || video.thumbnail || fallback.backgroundUrl,
    description: video.overview || fallback.description,
    releaseInfo: releaseInfo || fallback.releaseInfo,
    released: video.released || fallback.released,
    resourceId: video.id,
    raw: { ...fallback.raw, ...video.raw },
  };
}

type ResolvedCustomVideo = CustomSeriesResolveResult["videos"][number];

function customEpisodePlayerItem(video: CustomMetaVideo, identity: ResolvedCustomVideo, fallback: MediaItem): MediaItem {
  const releaseInfo = customVideoReleaseInfo(video);
  return {
    id: video.id,
    titleId: identity.titleId,
    mediaType: "episode",
    seasonNumber: identity.seasonNumber,
    episodeNumber: identity.episodeNumber,
    title: video.title || fallback.title,
    posterUrl: fallback.posterUrl,
    backgroundUrl: video.background || video.thumbnail || fallback.backgroundUrl,
    description: video.overview || fallback.description,
    releaseInfo: releaseInfo || fallback.releaseInfo,
    released: video.released || fallback.released,
    resourceId: video.id,
  };
}

async function progressByTitleID(titleIDs: string[], signal?: AbortSignal): Promise<Record<string, PlaybackProgress | undefined>> {
  const progress: Record<string, PlaybackProgress | undefined> = {};
  for (let offset = 0; offset < titleIDs.length; offset += 100) {
    const response = await api.progressBatch(titleIDs.slice(offset, offset + 100), signal);
    for (const entry of response.items) progress[entry.titleId] = entry.progress ?? undefined;
  }
  return progress;
}


type SourceIdentity = Pick<PlaybackSourceOption, "addonId" | "manifestId" | "streamIndex">;

type StreamAddonCategory = { addonId: string; label: string };

function playbackAddonCategories(options: PlaybackSourceOption[]): StreamAddonCategory[] {
  const categories: Array<StreamAddonCategory & { manifestId: string }> = [];
  const seenAddonIDs = new Set<string>();
  for (const option of options) {
    if (seenAddonIDs.has(option.addonId)) continue;
    seenAddonIDs.add(option.addonId);
    const manifestId = option.manifestId.trim();
    categories.push({
      addonId: option.addonId,
      label: option.addonName?.trim() || manifestId || option.addonId,
      manifestId,
    });
  }
  const labelCounts = new Map<string, number>();
  for (const category of categories) labelCounts.set(category.label, (labelCounts.get(category.label) ?? 0) + 1);
  const candidateLabels = categories.map((category) => {
    if (labelCounts.get(category.label)! <= 1) return category.label;
    const discriminator = category.manifestId && category.manifestId !== category.label ? category.manifestId : category.addonId;
    return `${category.label} · ${discriminator}`;
  });
  const candidateCounts = new Map<string, number>();
  for (const label of candidateLabels) candidateCounts.set(label, (candidateCounts.get(label) ?? 0) + 1);
  return categories.map((category, index) => ({
    addonId: category.addonId,
    label: candidateCounts.get(candidateLabels[index]!)! > 1
      ? `${candidateLabels[index]} · ${category.addonId}`
      : candidateLabels[index]!,
  }));
}

function preparationLabel(preparation: PlaybackPreparation): string {
  const mode = preparation.mode === "direct" ? t("player.mode.direct")
    : preparation.mode === "remux" ? t("player.mode.remux")
      : preparation.mode === "transcode_audio" ? t("player.mode.audioConversion")
        : preparation.mode === "transcode" ? t("player.mode.videoConversion")
          : preparation.mode === "youtube" ? "YouTube"
            : t("player.mode.external");
  const video = preparation.media?.videoTracks[0];
  const resolution = video?.height ? `${video.height}p` : "";
  const codec = video?.codec ? video.codec.toUpperCase() : "";
  return [mode, resolution, codec].filter(Boolean).join(" · ");
}
function webPlaybackCapabilities(): PlaybackCapabilities {
  const video = document.createElement("video");
  const audio = document.createElement("audio");
  const containers: string[] = [];
  const videoCodecs: string[] = [];
  const audioCodecs: string[] = [];
  const mediaProfiles: NonNullable<PlaybackCapabilities["mediaProfiles"]> = [];
  const add = (target: string[], ...values: string[]) => {
    for (const value of values) if (!target.includes(value)) target.push(value);
  };
  if (video.canPlayType('video/mp4; codecs="avc1.42E01E"')) {
    add(containers, "mp4", "m4v", "mov");
    add(videoCodecs, "h264");
  }
  const supportsHEVCMain = Boolean(video.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') || video.canPlayType('video/mp4; codecs="hev1.1.6.L93.B0"'));
  const supportsHEVCMain10 = Boolean(video.canPlayType('video/mp4; codecs="hvc1.2.4.L153.B0"'));
  if (supportsHEVCMain || supportsHEVCMain10) {
    add(containers, "mp4", "m4v", "mov");
    add(videoCodecs, "h265");
  }
  if (video.canPlayType('video/mp4; codecs="av01.0.05M.08"')) {
    add(containers, "mp4", "m4v", "mov");
    add(videoCodecs, "av1");
  }
  if (video.canPlayType('video/webm; codecs="av01.0.05M.08"')) {
    add(containers, "webm");
    add(videoCodecs, "av1");
  }
  if (video.canPlayType('video/webm; codecs="vp9"')) {
    add(containers, "webm");
    add(videoCodecs, "vp9");
  }
  if (audio.canPlayType('audio/mp4; codecs="mp4a.40.2"')) add(audioCodecs, "aac");
  if (audio.canPlayType('audio/webm; codecs="opus"')) add(audioCodecs, "opus");
  if (audio.canPlayType('audio/webm; codecs="vorbis"')) add(audioCodecs, "vorbis");
  if (audio.canPlayType("audio/mpeg")) add(audioCodecs, "mp3");
  if (audio.canPlayType('audio/mp4; codecs="ac-3"')) add(audioCodecs, "ac3");
  if (audio.canPlayType('audio/mp4; codecs="ec-3"')) add(audioCodecs, "eac3");
  const addProfile = (container: string, videoCodec: string, maximumVideoBitDepth: number, audioCodec?: string) => {
    if (!mediaProfiles.some((profile) => profile.container === container && profile.videoCodec === videoCodec && profile.audioCodec === audioCodec && profile.maximumVideoBitDepth === maximumVideoBitDepth)) {
      mediaProfiles.push({ container, videoCodec, maximumVideoBitDepth, ...(audioCodec ? { audioCodec } : {}) });
    }
    add(containers, container, ...(container === "mp4" ? ["m4v", "mov"] : []));
    add(videoCodecs, videoCodec);
    if (audioCodec) add(audioCodecs, audioCodec);
  };
  const profileVideoCandidates = [
    { container: "mp4", videoCodec: "h264", identifiers: ["avc1.42E01E"], maximumVideoBitDepth: 8 },
    { container: "mp4", videoCodec: "h265", identifiers: ["hvc1.1.6.L93.B0", "hev1.1.6.L93.B0"], maximumVideoBitDepth: 8 },
    { container: "mp4", videoCodec: "h265", identifiers: ["hvc1.2.4.L153.B0"], maximumVideoBitDepth: 10 },
    { container: "mp4", videoCodec: "av1", identifiers: ["av01.0.05M.08"], maximumVideoBitDepth: 8 },
    { container: "webm", videoCodec: "vp9", identifiers: ["vp9", "vp09.00.10.08"], maximumVideoBitDepth: 8 },
    { container: "webm", videoCodec: "av1", identifiers: ["av01.0.05M.08"], maximumVideoBitDepth: 8 },
  ];
  const profileAudioCandidates = [
    { container: "mp4", audioCodec: "aac", identifier: "mp4a.40.2" },
    { container: "mp4", audioCodec: "ac3", identifier: "ac-3" },
    { container: "mp4", audioCodec: "eac3", identifier: "ec-3" },
    { container: "mp4", audioCodec: "mp3", identifier: "mp3" },
    { container: "mp4", audioCodec: "opus", identifier: "opus" },
    { container: "webm", audioCodec: "opus", identifier: "opus" },
    { container: "webm", audioCodec: "vorbis", identifier: "vorbis" },
  ];
  for (const videoProfile of profileVideoCandidates) {
    const videoMime = videoProfile.container === "webm" ? "video/webm" : "video/mp4";
    const audioMime = videoProfile.container === "webm" ? "audio/webm" : "audio/mp4";
    for (const videoIdentifier of videoProfile.identifiers) {
      if (!video.canPlayType(`${videoMime}; codecs="${videoIdentifier}"`)) continue;
      addProfile(videoProfile.container, videoProfile.videoCodec, videoProfile.maximumVideoBitDepth);
      for (const audioProfile of profileAudioCandidates) {
        if (audioProfile.container !== videoProfile.container || !audio.canPlayType(`${audioMime}; codecs="${audioProfile.identifier}"`)) continue;
        addProfile(videoProfile.container, videoProfile.videoCodec, videoProfile.maximumVideoBitDepth, audioProfile.audioCodec);
      }
    }
  }
  if (containers.length === 0) containers.push("none");
  if (videoCodecs.length === 0) videoCodecs.push("none");
  if (audioCodecs.length === 0) audioCodecs.push("none");
  const streamingProtocols = ["http", "youtube"];
  const hdrFormats = ["sdr"];
  const highDynamicRangeOutput = window.matchMedia?.("(dynamic-range: high)").matches ?? false;
  if (highDynamicRangeOutput && mediaProfiles.some((profile) => (profile.maximumVideoBitDepth ?? 0) >= 10)) {
    hdrFormats.push("hdr10", "hlg");
  }
  if (highDynamicRangeOutput && (video.canPlayType('video/mp4; codecs="dvh1.05.06"') || video.canPlayType('video/mp4; codecs="dvhe.05.06"'))) {
    hdrFormats.push("dolby_vision");
  }
  if (video.canPlayType("application/vnd.apple.mpegurl") || "MediaSource" in window) streamingProtocols.push("hls");
  return {
    streamingProtocols,
    containers,
    videoCodecs,
    audioCodecs,
    hdrFormats,
    processingModes: ["remux", "transcode_audio", "transcode"],
    subtitleModes: ["external", "burn"],
    maximumAudioChannels: 2,
    mediaProfiles,
    externalPlayers: ["system"],
  };
}



export function mediaTypeLabel(mediaType: string): string {
  if (mediaType === "tv") return t("media.type.liveTv");
  if (mediaType === "series") return t("media.type.series");
  if (mediaType === "episode") return t("media.type.episode");
  if (mediaType === "movie") return t("media.type.movie");
  if (mediaType.toLowerCase() === "anime") return "Anime";
  return mediaType;
}

function safeSourceLabel(source: NonNullable<MediaItem["sources"]>[number]): string | undefined {
  const label = source.title.trim();
  if (!label || /[\u0000-\u001f\u007f]/.test(label) || /^https?:\/\//i.test(label)) return undefined;
  const identifiers = [source.id, source.addonId, source.manifestId, source.catalogId]
    .map((value) => value?.trim())
    .filter((value): value is string => Boolean(value));
  if (identifiers.some((identifier) => identifier.localeCompare(label, undefined, { sensitivity: "accent" }) === 0)) return undefined;
  if (/^[0-9a-f]{32,}$/i.test(label) || /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(label)) return undefined;
  return label;
}

export function mediaSourceLabels(item: Pick<MediaItem, "sources">): string[] {
  const labels: string[] = [];
  const seenAddons = new Set<string>();
  for (const source of item.sources ?? []) {
    if (source.kind !== "addon_catalog") continue;
    const addonIdentity = (source.addonId || source.id).trim();
    const label = safeSourceLabel(source);
    if (!addonIdentity || !label || seenAddons.has(addonIdentity)) continue;
    seenAddons.add(addonIdentity);
    labels.push(label);
  }
  return labels;
}


function formatPlaybackTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const total = Math.floor(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const remainingSeconds = total % 60;
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(remainingSeconds).padStart(2, "0")}`
    : `${minutes}:${String(remainingSeconds).padStart(2, "0")}`;
}

function episodeResourceID(series: SeriesMetadata, episode: EpisodeMetadata, fallback: string): string {
  const seriesIMDB = series.externalIds.imdb;
  if (seriesIMDB) return `${seriesIMDB}:${episode.seasonNumber}:${episode.episodeNumber}`;
  if (episode.externalIds.imdb) return episode.externalIds.imdb;
  if (episode.externalIds.tvdb) return `tvdb:${episode.externalIds.tvdb}`;
  if (series.externalIds.tmdb) return `tmdb:${series.externalIds.tmdb}:${episode.seasonNumber}:${episode.episodeNumber}`;
  return `${fallback}:${episode.seasonNumber}:${episode.episodeNumber}`;
}

function episodeItem(series: SeriesMetadata, episode: EpisodeMetadata, fallback: MediaItem): MediaItem {
  return {
    id: episodeResourceID(series, episode, fallback.id),
    titleId: episode.id,
    mediaType: "episode",
    seasonNumber: episode.seasonNumber,
    episodeNumber: episode.episodeNumber,
    title: episode.name || t("media.episode.fallbackTitle", { number: episode.episodeNumber }),
    posterUrl: episode.stillUrl || fallback.posterUrl,
    backgroundUrl: episode.backdropUrl || episode.stillUrl || fallback.backgroundUrl,
    description: episode.overview,
    releaseInfo: episode.airDate,
    released: episode.airDate,
    externalIds: { ...episode.externalIds, ...(series.externalIds.imdb ? { imdb: series.externalIds.imdb } : {}) },
    raw: {
      ...fallback.raw,
      episodeSeriesName: series.name,
      continueSeriesId: series.id,
      continueSeasonId: episode.seasonId,
      continueSeasonNumber: episode.seasonNumber,
      continueEpisodeNumber: episode.episodeNumber,
      continueEpisodeId: episode.id,
    },
  };
}

function seriesItem(series: SeriesMetadata, fallback: MediaItem, episode: EpisodeMetadata | undefined): MediaItem {
  const canonicalSeriesResourceID = series.externalIds.imdb
    || (series.mappingProvider === "tvdb" && series.externalIds.tvdb ? `tvdb:${series.externalIds.tvdb}` : "")
    || (series.externalIds.tmdb ? `tmdb:${series.externalIds.tmdb}` : "")
    || (series.externalIds.tvdb ? `tvdb:${series.externalIds.tvdb}` : "")
    || series.id;
  const routeSeriesResourceID = typeof fallback.raw?.routeSeriesResourceId === "string"
    ? fallback.raw.routeSeriesResourceId
    : canonicalSeriesResourceID;
  return {
    id: routeSeriesResourceID,
    titleId: series.id,
    mediaType: "series",
    title: series.name,
    posterUrl: series.posterUrl,
    backgroundUrl: series.backdropUrl,
    logoUrl: series.logoUrl,
    description: series.overview,
    releaseInfo: series.firstAirDate,
    released: series.firstAirDate,
    externalIds: series.externalIds,
    raw: {
      ...fallback.raw,
      routeSeriesResourceId: routeSeriesResourceID,
      continueSeriesId: series.id,
      continueSeasonId: episode?.seasonId ?? fallback.raw?.continueSeasonId,
      continueSeasonNumber: episode?.seasonNumber ?? fallback.raw?.continueSeasonNumber,
      continueEpisodeNumber: undefined,
      continueEpisodeId: undefined,
      startFromBeginning: undefined,
    },
  };
}

function episodeIsUpcoming(episode: EpisodeMetadata): boolean {
  if (!episode.airDate) return false;
  const airDate = new Date(`${episode.airDate}T23:59:59Z`);
  return Number.isFinite(airDate.getTime()) && airDate.getTime() > Date.now();
}
function withoutEmptySeasons(series: SeriesMetadata): SeriesMetadata {
  const seasons = series.seasons.filter((season) => season.episodeCount > 0);
  return seasons.length === series.seasons.length ? series : { ...series, seasons };
}

export function MediaDetails({ item, maximumCastMembers, onCanonicalRoute, onClose, onNavigateContext, onOpenMedia, onOpenSeason, onLibraryMutation }: { item: MediaItem; maximumCastMembers: number; onCanonicalRoute?: (metadata: CanonicalRouteMetadata) => void; onClose: () => void; onNavigateContext?: (context: { seasonID: string; episodeID?: string; seasonNumber: number; episodeNumber?: number }) => void; onOpenMedia?: (item: MediaItem) => void; onOpenSeason?: (item: MediaItem) => void; onLibraryMutation?: () => void }) {
  const metadataLocale = api.metadataLocale();
  const customType = item.mediaType !== "movie" && item.mediaType !== "series" && item.mediaType !== "episode" && item.mediaType !== "tv";
  const preferredMetaAddonID = item.sourceAddonId ?? item.sources?.find((source) => source.addonId)?.addonId;
  const [details, setDetails] = useState(() => {
    const cached = cachedMediaItem(item, metadataLocale);
    return customType ? {
      ...cached,
      posterUrl: localizedArtworkURL(cached.posterUrl),
      backgroundUrl: localizedArtworkURL(cached.backgroundUrl),
      logoUrl: localizedArtworkURL(cached.logoUrl),
    } : cached;
  });
  const [playing, setPlaying] = useState(false);
  const [titleID, setTitleID] = useState(item.titleId);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [queueSaving, setQueueSaving] = useState(false);
  const [actionError, setActionError] = useState("");
  const [trailers, setTrailers] = useState<TrailerMetadata[]>([]);
  const [selectedTrailer, setSelectedTrailer] = useState<TrailerMetadata>();
  const [trailerLoading, setTrailerLoading] = useState(false);
  const [trailerMessage, setTrailerMessage] = useState("");
  const [trailerUnavailable, setTrailerUnavailable] = useState(false);
  const [trailerOwnerKey, setTrailerOwnerKey] = useState("");
  const [metaLoading, setMetaLoading] = useState(true);
  const [metaError, setMetaError] = useState("");
  const [metaResolved, setMetaResolved] = useState(false);
  const [resolvedCustomMeta, setResolvedCustomMeta] = useState<CustomMetaPlayback>();
  const [selectedCustomVideo, setSelectedCustomVideo] = useState<CustomMetaVideo>();
  const [customVideoChooserVisible, setCustomVideoChooserVisible] = useState(false);
  const [selectedCustomSeasonKey, setSelectedCustomSeasonKey] = useState("");
  const [resolvedCustomVideos, setResolvedCustomVideos] = useState<Map<string, ResolvedCustomVideo>>(() => new Map());
  const [customProgressLoading, setCustomProgressLoading] = useState(false);
  const [customProgressConfirmed, setCustomProgressConfirmed] = useState(false);
  const [availableStreams, setAvailableStreams] = useState<PlaybackSourceOption[]>([]);
  const [selectedStream, setSelectedStream] = useState<PlaybackSourceOption>();
  const [streamsLoading, setStreamsLoading] = useState(item.mediaType !== "tv");
  const [streamsError, setStreamsError] = useState("");
  const [streamRefreshVersion, setStreamRefreshVersion] = useState(0);
  const [streamAddonFilter, setStreamAddonFilter] = useState("");
  const [streamsRequested, setStreamsRequested] = useState(item.mediaType !== "tv");
  const [preparation, setPreparation] = useState<PlaybackPreparation>();
  const [preparationLoading, setPreparationLoading] = useState(false);
  const [preparationError, setPreparationError] = useState("");
  const [series, setSeries] = useState<SeriesMetadata>();
  const seriesContextEnabled = item.mediaType === "series" || item.mediaType === "episode";
  const [seriesLoading, setSeriesLoading] = useState(seriesContextEnabled);
  const [seriesError, setSeriesError] = useState("");
  const [episodeOrderLoading, setEpisodeOrderLoading] = useState(false);
  const [episodeOrderError, setEpisodeOrderError] = useState("");
  const [seasonID, setSeasonID] = useState("");
  const [season, setSeason] = useState<SeasonMetadata>();
  const [seasonLoading, setSeasonLoading] = useState(false);
  const [selectedEpisode, setSelectedEpisode] = useState<EpisodeMetadata>();
  const [episodeProgress, setEpisodeProgress] = useState<Record<string, PlaybackProgress | undefined>>({});
  const autoPlayNextRef = useRef(false);
  const autoStartRef = useRef(item.raw?.startFromBeginning === true);
  const sourceRefreshAttemptRef = useRef("");
  const playRequestedSourceRef = useRef("");
  const tvPlaybackPendingRef = useRef(false);
  const trailerRequestRef = useRef(0);
  const seasonCacheRef = useRef(new Map<string, SeasonMetadata>());
  const trailerItemRef = useRef("");
  const trailerWarningKeyRef = useRef("");
  const trailerStageRef = useRef<HTMLDivElement>(null);
  const trailerRevealPendingRef = useRef(false);
  const episodeListRef = useRef<HTMLDivElement>(null);
  const selectedEpisodeRowRef = useRef<HTMLDivElement>(null);
  const customChooserHeadingRef = useRef<HTMLHeadingElement>(null);
  const customStreamHeadingRef = useRef<HTMLElement>(null);
  const customPanelFocusTargetRef = useRef<"chooser" | "streams" | undefined>(undefined);
  const restorePlayerFocusRef = useRef(false);
  const [titleProgress, setTitleProgress] = useState<PlaybackProgress>();
  const [watchedBusy, setWatchedBusy] = useState("");
  const nextSourceRef = useRef<SourceIdentity | undefined>(undefined);
  const continueSeriesID = typeof item.raw?.continueSeriesId === "string" ? item.raw.continueSeriesId : "";
  const routeSeriesResourceID = typeof item.raw?.routeSeriesResourceId === "string" ? item.raw.routeSeriesResourceId : "";
  const continueSeasonID = typeof item.raw?.continueSeasonId === "string" ? item.raw.continueSeasonId : "";
  const continueEpisodeID = typeof item.raw?.continueEpisodeId === "string" ? item.raw.continueEpisodeId : "";
  const continueSeasonNumber = typeof item.raw?.continueSeasonNumber === "number" ? item.raw.continueSeasonNumber : undefined;
  const continueEpisodeNumber = typeof item.raw?.continueEpisodeNumber === "number" ? item.raw.continueEpisodeNumber : undefined;
  const libraryTitleID = item.mediaType === "episode" ? series?.id || continueSeriesID || undefined : titleID ?? item.titleId;
  const trailerSeriesContext = item.mediaType === "series" || item.mediaType === "episode";
  const trailerTitleID = item.mediaType === "episode"
    ? series?.id ?? continueSeriesID
    : titleID ?? item.titleId;
  const selectedTrailerSeason = trailerSeriesContext ? series?.seasons.find((candidate) => candidate.id === seasonID) : undefined;
  const trailersAvailableForContext = item.mediaType === "movie"
    || item.mediaType === "series"
    || item.mediaType === "episode" && Boolean(trailerTitleID && selectedTrailerSeason);
  const trailerItemKey = `${trailerSeriesContext ? "series" : item.mediaType}:${item.id}:${selectedTrailerSeason ? `season:${selectedTrailerSeason.seasonNumber}` : "title"}`;
  trailerItemRef.current = trailerItemKey;
  const activeTrailers = trailerOwnerKey === trailerItemKey ? trailers : [];
  const activeTrailer = trailerOwnerKey === trailerItemKey ? selectedTrailer : undefined;
  const activeTrailerLoading = trailerOwnerKey === trailerItemKey && trailerLoading;
  const activeTrailerMessage = trailerOwnerKey === trailerItemKey ? trailerMessage : "";
  const activeTrailerUnavailable = trailerOwnerKey === trailerItemKey && trailerUnavailable;
  const trailerStageVisible = Boolean(activeTrailer || activeTrailerMessage);
  const customVideos = resolvedCustomMeta?.videos ?? emptyCustomVideos;
  const defaultCustomVideo = resolvedCustomMeta?.defaultVideoId
    ? customVideos.find((video) => video.id === resolvedCustomMeta.defaultVideoId)
    : undefined;
  const activeCustomVideo = selectedCustomVideo ?? defaultCustomVideo;
  const activeCustomIdentity = activeCustomVideo ? resolvedCustomVideos.get(activeCustomVideo.id) : undefined;
  const customVideoGroups = useMemo(() => groupCustomVideos(customVideos), [customVideos]);
  const preferredCustomSeasonKey = activeCustomVideo ? customVideoSeasonKey(activeCustomVideo) : undefined;
  const activeCustomVideoGroup = customVideoGroups.find((group) => group.key === selectedCustomSeasonKey)
    ?? customVideoGroups.find((group) => group.key === preferredCustomSeasonKey)
    ?? customVideoGroups.find((group) => group.season !== undefined && group.season > 0)
    ?? customVideoGroups[0];
  const activeCustomSeasonKey = activeCustomVideoGroup?.key ?? "";
  const visibleCustomVideos = activeCustomVideoGroup?.videos ?? emptyCustomVideos;
  const orderedCustomVideos = useMemo(() => customVideoGroups.flatMap((group) => group.videos), [customVideoGroups]);
  const customSeasonBusyKey = activeCustomSeasonKey ? `custom-season:${activeCustomSeasonKey}` : "";
  const customPlaybackResourceID = selectedCustomVideo?.id
    ?? resolvedCustomMeta?.defaultVideoId
    ?? (customVideos.length === 0 ? resolvedCustomMeta?.id : undefined);
  const streamResourceID = customType
    ? customPlaybackResourceID ?? ""
    : selectedEpisode && series ? episodeResourceID(series, selectedEpisode, item.id) : mediaResourceID(item);
  const playbackMediaType = selectedEpisode || item.mediaType === "episode" ? "episode" : item.mediaType;
  const startFromBeginning = item.raw?.startFromBeginning === true;
  const selectedProgress = selectedEpisode
    ? episodeProgress[selectedEpisode.id]
    : activeCustomIdentity ? episodeProgress[activeCustomIdentity.titleId] : titleProgress;
  const preparationStartSeconds = startFromBeginning ? 0 : selectedProgress?.completed ? 0 : Math.max(0, Math.floor(selectedProgress?.positionSeconds ?? 0));
  const fromContinue = item.raw?.continueReason === "resume" || item.raw?.continueReason === "next_episode";
  const autoplayNextEpisode = document.documentElement.dataset.autoplayNextEpisode !== "false";
  const awaitingRestartEpisode = startFromBeginning && item.mediaType === "episode" && Boolean(continueSeriesID) && !selectedEpisode && !seriesError;
  const playbackIdentityResolved = !customType || metaResolved && Boolean(customPlaybackResourceID);
  const canSelectStream = item.mediaType !== "series" && !awaitingRestartEpisode && playbackIdentityResolved;
  const streamAddonCategories = useMemo(() => playbackAddonCategories(availableStreams), [availableStreams]);
  const streamAddonLabels = useMemo(() => new Map(streamAddonCategories.map((category) => [category.addonId, category.label])), [streamAddonCategories]);
  const filteredStreams = useMemo(() => streamAddonFilter
    ? availableStreams.filter((option) => option.addonId === streamAddonFilter)
    : availableStreams, [availableStreams, streamAddonFilter]);

  useEffect(() => {
    setStreamAddonFilter("");
  }, [playbackMediaType, streamResourceID]);

  useEffect(() => {
    if (!trailerStageVisible || !trailerRevealPendingRef.current) return;
    trailerRevealPendingRef.current = false;
    const frame = window.requestAnimationFrame(() => {
      trailerStageRef.current?.querySelector<HTMLButtonElement>("button")?.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [trailerStageVisible]);
  const showCustomVideoChooser = customType && (!metaResolved || customVideoChooserVisible);

  useEffect(() => {
    const target = customPanelFocusTargetRef.current;
    if (!target || (target === "chooser") !== showCustomVideoChooser) return;
    customPanelFocusTargetRef.current = undefined;
    const frame = window.requestAnimationFrame(() => {
      (target === "chooser" ? customChooserHeadingRef.current : customStreamHeadingRef.current)?.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [showCustomVideoChooser]);

  useEffect(() => {
    const expectedEpisodeID = item.titleId ?? continueEpisodeID;
    if (item.mediaType === "episode" && selectedEpisode && expectedEpisodeID && selectedEpisode.id !== expectedEpisodeID) return;
    cacheMediaItem(item, details, metadataLocale, titleID);
  }, [continueEpisodeID, details, item, metadataLocale, selectedEpisode, titleID]);

  useEffect(() => () => flushMetadataCache(), []);

  useEffect(() => {
    if (playing) return;
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose, playing]);

  useEffect(() => {
    trailerRequestRef.current += 1;
    setTrailers([]);
    setSelectedTrailer(undefined);
    setTrailerLoading(false);
    setTrailerMessage("");
    setTrailerUnavailable(false);
    setTrailerOwnerKey("");
    return () => { trailerRequestRef.current += 1; };
  }, [trailerItemKey]);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setMetaLoading(true);
    setMetaResolved(false);
    setMetaError("");
    setResolvedCustomMeta(undefined);
    setSelectedCustomVideo(undefined);
    setSelectedCustomSeasonKey("");
    setCustomVideoChooserVisible(false);
    setResolvedCustomVideos(new Map());
    setCustomProgressLoading(false);
    if (customType) setEpisodeProgress({});
    setCustomProgressConfirmed(false);
    void api.resources("meta", item.mediaType === "episode" ? "series" : item.mediaType, item.id).then(async (batch) => {
      const selected = firstPayloadRecord(batch, "meta", customType ? preferredMetaAddonID : undefined);
      if (!active || !selected) return;
      const { value: meta, result } = selected;
      const playback = customType ? customMetaPlayback(meta) : undefined;
      if (playback) {
        setResolvedCustomMeta(playback);
        setCustomVideoChooserVisible(playback.videos.length > 0 && !playback.defaultVideoId);
      }
      const metaTitle = textValue(meta.name, meta.title);
      const metaDescription = textValue(meta.description, meta.overview);
      const metaPoster = localizedArtworkValue(meta.poster, meta.posterUrl);
      const metaBackground = localizedArtworkValue(meta.background, meta.backgroundUrl, meta.backdrop, meta.backdropUrl);
      const metaLogo = localizedArtworkValue(meta.logo, meta.logoUrl);
      const metaReleaseInfo = textValue(meta.releaseInfo, meta.year);
      setDetails((current) => ({
        ...current,
        title: item.mediaType === "episode" ? current.title : customType ? metaTitle || current.title : current.title || metaTitle || "",
        description: item.mediaType === "episode" ? current.description : customType ? metaDescription || current.description : current.description || metaDescription,
        posterUrl: item.mediaType === "episode"
          ? current.posterUrl
          : customType ? metaPoster || localizedArtworkURL(current.posterUrl) : current.posterUrl || textValue(meta.poster, meta.posterUrl),
        backgroundUrl: customType
          ? metaBackground || localizedArtworkURL(current.backgroundUrl)
          : current.backgroundUrl || textValue(meta.background, meta.backgroundUrl, meta.backdrop, meta.backdropUrl),
        logoUrl: customType ? metaLogo || localizedArtworkURL(current.logoUrl) : current.logoUrl || textValue(meta.logo, meta.logoUrl),
        releaseInfo: item.mediaType === "episode" ? current.releaseInfo : customType ? metaReleaseInfo || current.releaseInfo : current.releaseInfo || metaReleaseInfo,
        raw: { ...current.raw, ...meta },
      }));
      if (!playback) return;
      const eligibleVideos = resolvableCustomVideos(playback.videos);
      if (!eligibleVideos) {
        if (active) setActionError(t("media.watch.error.episodeUpdateFailed"));
        return;
      }
      try {
        const resolved = await api.resolveCustomSeries({
          sourceAddonId: result.addonId,
          sourceType: result.type,
          series: {
            resourceId: playback.id ?? item.id,
            title: metaTitle ?? item.title,
            ...(metaPoster ? { posterUrl: metaPoster } : {}),
            ...(metaBackground ? { backgroundUrl: metaBackground } : {}),
            ...(metaReleaseInfo ? { releaseInfo: metaReleaseInfo } : {}),
          },
          videos: eligibleVideos.map((video) => ({
            resourceId: video.id,
            ...(video.title ? { title: video.title } : {}),
            seasonNumber: video.season,
            episodeNumber: video.episode,
            ...(video.thumbnail ? { thumbnailUrl: video.thumbnail } : {}),
            ...(video.background ? { backgroundUrl: video.background } : {}),
            ...(video.released ? { releaseInfo: video.released } : {}),
            ...(titleReleaseDate(video.released) ? { released: titleReleaseDate(video.released) } : {}),
          })),
        }, controller.signal);
        if (!active) return;
        setMetaResolved(true);
        setMetaLoading(false);
        setTitleID(resolved.series.titleId);
        setResolvedCustomVideos(new Map(resolved.videos.map((video) => [video.resourceId, video])));
        if (resolved.videos.length === 0) {
          setCustomProgressConfirmed(true);
          return;
        }
        setCustomProgressLoading(true);
        try {
          const progress = await progressByTitleID(resolved.videos.map((video) => video.titleId), controller.signal);
          if (active) {
            setEpisodeProgress(progress);
            setCustomProgressConfirmed(true);
          }
        } catch (cause) {
          if (cause instanceof DOMException && cause.name === "AbortError") return;
          if (active) setActionError(t("media.watch.error.episodeUpdateFailed"));
        } finally {
          if (active) setCustomProgressLoading(false);
        }
      } catch (cause) {
        if (cause instanceof DOMException && cause.name === "AbortError") return;
        if (active) setActionError(cause instanceof APIError ? cause.message : t("media.watch.error.episodeUpdateFailed"));
      }
    }).catch((cause) => {
      if (active) setMetaError(cause instanceof APIError ? cause.message : t("media.details.error.additionalDetailsLoadFailed"));
    }).finally(() => {
      if (!active) return;
      setMetaResolved(true);
      setMetaLoading(false);
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [customType, item.id, item.mediaType, item.title, item.titleId, preferredMetaAddonID]);

  useEffect(() => {
    let active = true;
    if (item.mediaType !== "tv" && !libraryTitleID) {
      setSaved(false);
      return;
    }
    void api.library(item.mediaType === "tv" ? "tv" : "").then((library) => {
      if (!active) return;
      if (item.mediaType !== "tv") {
        setSaved(library.items.some((entry) => entry.titleId === libraryTitleID));
        return;
      }
      const identity = mediaIdentity(item);
      const entry = library.items.find((candidate) => mediaIdentity(mediaFromLibraryItem(candidate, t("media.untitled"))) === identity);
      setSaved(Boolean(entry));
      if (!entry) return;
      setTitleID(entry.titleId);
      const libraryItem = mediaFromLibraryItem(entry, t("media.untitled"));
      setDetails((current) => ({
        ...current,
        ...libraryItem,
        title: current.title === t("media.untitled") ? libraryItem.title : current.title,
        raw: current.raw,
      }));
    }).catch(() => undefined);
    return () => { active = false; };
  }, [item, libraryTitleID]);

  useEffect(() => {
    let active = true;
    if (item.mediaType !== "movie" && item.mediaType !== "episode") {
      setTitleProgress(undefined);
      return;
    }
    void (async () => {
      const resolvedTitleID = item.titleId ?? await resolveMediaTitle(item);
      const [progress, movie] = await Promise.all([
        api.progress(resolvedTitleID).catch(() => undefined),
        item.mediaType === "movie" ? api.movieDetails(resolvedTitleID).catch(() => undefined) : Promise.resolve(undefined),
      ]);
      if (!active) return;
      setTitleID(resolvedTitleID);
      setTitleProgress(progress);
      if (movie) {
        onCanonicalRoute?.({
          sourceID: item.id,
          sourceMediaType: item.mediaType,
          titleID: resolvedTitleID,
          titleMediaType: "movie",
          externalIds: movie.externalIds,
        });
        setDetails((current) => ({
          ...current,
          title: movie.title || current.title,
          description: movie.overview || current.description,
          voteAverage: movie.voteAverage,
          posterUrl: movie.posterUrl || current.posterUrl,
          backgroundUrl: movie.backdropUrl || current.backgroundUrl,
          logoUrl: movie.logoUrl || current.logoUrl,
          releaseInfo: movie.releaseDate || current.releaseInfo,
          released: movie.releaseDate || current.released,
          voteCount: movie.voteCount,
          externalIds: { ...current.externalIds, ...movie.externalIds },
          raw: { ...current.raw, ...movie },
        }));
      }
    })().catch(() => undefined);
    return () => { active = false; };
  }, [item.id, item.mediaType, item.titleId, onCanonicalRoute]);

  useEffect(() => {
    let active = true;
    if (!seriesContextEnabled && !(item.mediaType === "episode" && continueSeriesID)) {
      setSeriesLoading(false);
      return;
    }
    setSeriesLoading(true);
    setSeriesError("");
    setEpisodeOrderError("");
    setSeasonID("");
    setSeason(undefined);
    setSelectedEpisode(undefined);
    void (async () => {
      const resolvedTitleID = item.mediaType === "episode" && continueSeriesID
        ? continueSeriesID
        : item.mediaType === "episode" && routeSeriesResourceID
          ? await resolveMediaTitle({ ...item, id: routeSeriesResourceID, titleId: undefined, mediaType: "series", externalIds: undefined })
          : await resolveMediaTitle(item);
      const resolved = withoutEmptySeasons(await api.seriesDetails(resolvedTitleID));
      if (!active) return;
      onCanonicalRoute?.({
        sourceID: item.id,
        sourceMediaType: item.mediaType,
        titleID: resolvedTitleID,
        titleMediaType: "series",
        externalIds: resolved.externalIds,
      });
      if (item.mediaType === "series") setTitleID(resolvedTitleID);
      setSeries(resolved);
      seasonCacheRef.current.clear();
      if (item.mediaType === "series" || item.mediaType === "episode") setDetails((current) => ({
        ...current,
        title: item.mediaType === "series" ? resolved.name || current.title : current.title,
        description: item.mediaType === "series" ? resolved.overview || current.description : current.description,
        voteAverage: resolved.voteAverage,
        posterUrl: resolved.posterUrl || current.posterUrl,
        backgroundUrl: item.mediaType === "series" ? resolved.backdropUrl || current.backgroundUrl : current.backgroundUrl || resolved.backdropUrl,
        logoUrl: resolved.logoUrl || current.logoUrl,
        releaseInfo: item.mediaType === "series" ? resolved.firstAirDate || current.releaseInfo : current.releaseInfo,
        released: item.mediaType === "series" ? resolved.firstAirDate || current.released : current.released,
        voteCount: resolved.voteCount,
        externalIds: { ...resolved.externalIds, ...current.externalIds },
        raw: { ...current.raw, ...resolved },
      }));
      const seasons = [...resolved.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber);
      const requestedSeason = seasons.find((candidate) => candidate.id === continueSeasonID)
        ?? (continueSeasonNumber !== undefined ? seasons.find((candidate) => candidate.seasonNumber === continueSeasonNumber) : undefined);
      let initial = requestedSeason
        ?? seasons.find((candidate) => candidate.seasonNumber > 0)
        ?? seasons[0];
      if (resolved.mappingProvider === "tvdb" && continueEpisodeID) {
        const episodeAirDate = item.released ?? item.releaseInfo;
        const episodeAirTime = episodeAirDate ? Date.parse(episodeAirDate) : Number.NaN;
        const candidates = [...seasons].sort((left, right) => {
          if (!Number.isFinite(episodeAirTime)) return 0;
          const distance = (airDate?: string) => {
            const seasonAirTime = airDate ? Date.parse(airDate) : Number.NaN;
            return Number.isFinite(seasonAirTime) && seasonAirTime <= episodeAirTime
              ? episodeAirTime - seasonAirTime
              : Number.POSITIVE_INFINITY;
          };
          const leftDistance = distance(left.airDate);
          const rightDistance = distance(right.airDate);
          return leftDistance === rightDistance ? 0 : leftDistance < rightDistance ? -1 : 1;
        });
        for (const candidate of candidates) {
          let mappedSeason = seasonCacheRef.current.get(candidate.id);
          if (!mappedSeason) {
            try {
              mappedSeason = await api.seasonDetails(candidate.id, undefined, resolved.mappingProvider);
            } catch {
              continue;
            }
            if (!active) return;
            seasonCacheRef.current.set(candidate.id, mappedSeason);
          }
          if (mappedSeason.episodes.some((episode) => episode.id === continueEpisodeID)) {
            initial = candidate;
            break;
          }
        }
      }
      if (active) setSeasonID(initial?.id ?? "");
    })().catch((cause) => {
      if (active) setSeriesError(notifyError(cause, t("media.series.error.loadFailed"), t("media.series.error.unavailableTitle")));
    }).finally(() => { if (active) setSeriesLoading(false); });
    return () => { active = false; };
  }, [continueEpisodeID, continueSeasonID, continueSeasonNumber, continueSeriesID, item.id, item.mediaType, item.releaseInfo, item.released, item.titleId, onCanonicalRoute, routeSeriesResourceID, seriesContextEnabled]);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    if (!seasonID) {
      setSeason(undefined);
      return;
    }
    setSeasonLoading(true);
    setSelectedEpisode(undefined);
    setEpisodeProgress({});
    void (seasonCacheRef.current.has(seasonID) ? Promise.resolve(seasonCacheRef.current.get(seasonID)!) : api.seasonDetails(seasonID, controller.signal, series?.mappingProvider)).then(async (resolved) => {
      if (!active) return;
      setSeason(resolved);
      const resolvedPoster = resolved.posterUrl
        || series?.seasons.find((candidate) => candidate.id === seasonID)?.posterUrl
        || series?.posterUrl;
      if (resolvedPoster) setDetails((current) => current.posterUrl === resolvedPoster ? current : { ...current, posterUrl: resolvedPoster });
      if (autoPlayNextRef.current) {
        const first = resolved.episodes.find((episode) => !episodeIsUpcoming(episode));
        if (first) setSelectedEpisode(first);
        else autoPlayNextRef.current = false;
      } else if (item.mediaType === "episode" || continueEpisodeID) {
        const requested = continueEpisodeID
          ? resolved.episodes.find((episode) => episode.id === continueEpisodeID)
          : undefined;
        const exactMappedEpisodeRequired = series?.mappingProvider === "tvdb" && continueEpisodeID !== "";
        const requestedByNumber = continueEpisodeNumber !== undefined && !exactMappedEpisodeRequired
          ? resolved.episodes.find((episode) => episode.episodeNumber === continueEpisodeNumber)
          : undefined;
        const fallbackEpisode = item.mediaType === "episode" || exactMappedEpisodeRequired ? undefined : resolved.episodes[0];
        setSelectedEpisode(requested ?? requestedByNumber ?? fallbackEpisode);
      }
      if (resolved.episodes.length === 0) {
        setEpisodeProgress({});
        return;
      }
      const progressBatch = await api.progressBatch(resolved.episodes.map((episode) => episode.id), controller.signal);
      if (!active) return;
      setEpisodeProgress(Object.fromEntries(progressBatch.items.map((entry) => [entry.titleId, entry.progress ?? undefined])));
    }).catch((cause) => {
      if (active && !controller.signal.aborted) setSeriesError(notifyError(cause, t("media.season.error.episodesLoadFailed"), t("media.season.error.unavailableTitle")));
    }).finally(() => { if (active) setSeasonLoading(false); });
    return () => { active = false; controller.abort(); };
  }, [continueEpisodeID, continueEpisodeNumber, item.mediaType, seasonID, series?.mappingProvider]);
  useEffect(() => {
    if (item.mediaType !== "episode" || !selectedEpisode) return;
    const episodeCode = `S${String(selectedEpisode.seasonNumber).padStart(2, "0")}E${String(selectedEpisode.episodeNumber).padStart(2, "0")}`;
    setDetails((current) => ({
      ...current,
      title: [series?.name, episodeCode, selectedEpisode.name].filter(Boolean).join(" · "),
      description: selectedEpisode.overview || current.description,
      backgroundUrl: selectedEpisode.backdropUrl || selectedEpisode.stillUrl || current.backgroundUrl,
      releaseInfo: selectedEpisode.airDate || current.releaseInfo,
      released: selectedEpisode.airDate || current.released,
      voteAverage: selectedEpisode.voteAverage,
      voteCount: selectedEpisode.voteCount,
      externalIds: { ...series?.externalIds, ...selectedEpisode.externalIds },
    }));
  }, [item.mediaType, selectedEpisode, series]);
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    if (!canSelectStream || item.mediaType === "tv" && (!streamsRequested || item.available === false)) {
      setAvailableStreams([]);
      setSelectedStream(undefined);
      setStreamsError("");
      setStreamsLoading(false);
      return () => controller.abort();
    }
    setStreamsLoading(true);
    setStreamsError("");
    setSelectedStream(undefined);
    playRequestedSourceRef.current = "";
    setPreparation(undefined);
    setPreparationError("");
    void api.playbackSources({
      mediaType: playbackMediaType,
      resourceId: streamResourceID,
      addonId: item.mediaType === "tv" ? item.sourceAddonId : undefined,
      capabilities: applyQualityLimits(webPlaybackCapabilities()),
    }, controller.signal).then((response) => {
      if (!active) return;
      const options = response.sources;
      setAvailableStreams(options);
      setStreamAddonFilter((current) => current && !options.some((option) => option.addonId === current) ? "" : current);
      if (item.mediaType === "tv" && tvPlaybackPendingRef.current) {
        tvPlaybackPendingRef.current = false;
        const next = options[0];
        if (next) {
          playRequestedSourceRef.current = next.sourceRef;
          setSelectedStream(next);
        }
      } else if (autoStartRef.current) {
        const next = options[0];
        if (next) setSelectedStream(next);
        else autoStartRef.current = false;
      } else if (autoPlayNextRef.current) {
        const preferred = nextSourceRef.current;
        const next = options.find((option) => preferred &&
          option.addonId === preferred.addonId &&
          option.manifestId === preferred.manifestId &&
          option.streamIndex === preferred.streamIndex) ?? options[0];
        nextSourceRef.current = undefined;
        if (next) setSelectedStream(next);
        else autoPlayNextRef.current = false;
      }
    }).catch((cause) => {
      if (!active || cause instanceof DOMException && cause.name === "AbortError") return;
      autoPlayNextRef.current = false;
      tvPlaybackPendingRef.current = false;
      setStreamsError(notifyError(cause, t("media.sources.error.loadFailed"), t("media.sources.error.unavailableTitle")));
    }).finally(() => { if (active) setStreamsLoading(false); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [canSelectStream, item.available, item.mediaType, item.sourceAddonId, playbackMediaType, selectedEpisode, streamRefreshVersion, streamResourceID, streamsRequested]);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setPreparation(undefined);
    setPreparationError("");
    if (!selectedStream) {
      setPreparationLoading(false);
      return () => controller.abort();
    }
    setPreparationLoading(true);
    void api.preparePlayback({ sourceRef: selectedStream.sourceRef, startSeconds: preparationStartSeconds }, controller.signal).then((prepared) => {
      if (!active) return;
      sourceRefreshAttemptRef.current = "";
      setPreparation(prepared);
      const playRequested = playRequestedSourceRef.current === selectedStream.sourceRef;
      if (autoStartRef.current || autoPlayNextRef.current || playRequested) {
        autoStartRef.current = false;
        autoPlayNextRef.current = false;
        playRequestedSourceRef.current = "";
        setPlaying(true);
      }
    }).catch((cause) => {
      if (!active || cause instanceof DOMException && cause.name === "AbortError") return;
      if (cause instanceof APIError && cause.code === "playback_source_expired" && sourceRefreshAttemptRef.current !== selectedStream.sourceRef) {
        sourceRefreshAttemptRef.current = selectedStream.sourceRef;
        setStreamRefreshVersion((version) => version + 1);
        return;
      }
      autoStartRef.current = false;
      autoPlayNextRef.current = false;
      if (playRequestedSourceRef.current === selectedStream.sourceRef) playRequestedSourceRef.current = "";
      if (cause instanceof APIError && cause.code === "playback_source_unsupported") {
        setPreparationError(t("media.sources.error.conversionUnsupported"));
        return;
      }
      const policyMessage = playbackPolicyErrorMessage(cause);
      if (policyMessage) {
        setPreparationError(policyMessage);
        return;
      }
      setPreparationError(notifyError(cause, t("media.sources.error.prepareFailed"), t("media.sources.error.streamUnavailableTitle")));
    }).finally(() => { if (active) setPreparationLoading(false); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [preparationStartSeconds, selectedStream]);

  async function toggleLibrary() {
    if ((item.mediaType === "episode" || customType) && !libraryTitleID) return;
    setSaving(true);
    setActionError("");
    const removing = saved;
    try {
      const resolvedTitleID = libraryTitleID ?? await resolveMediaTitle(details);
      if (!customType && item.mediaType !== "episode" && !titleID) setTitleID(resolvedTitleID);
      if (saved) await api.removeLibrary(resolvedTitleID);
      else await api.addLibrary(resolvedTitleID);
      setSaved(!removing);
      notifySuccess(
        t(removing ? "library.notice.removed" : "library.notice.added", { title: item.mediaType === "episode" ? series?.name ?? details.title : details.title }),
        t(removing ? "library.notice.removedTitle" : "library.notice.addedTitle"),
      );
      onLibraryMutation?.();
    } catch (cause) {
      setActionError(notifyError(cause, t("library.error.updateFailed"), t("library.error.notUpdatedTitle")));
    } finally {
      setSaving(false);
    }
  }

  async function addToReadingQueue() {
    const profileId = activeProfileRequestID();
    if (customType || !profileId || !streamResourceID) return;
    setQueueSaving(true);
    setActionError("");
    try {
      await enqueueReadingQueue(profileId, {
        mediaType: playbackMediaType as "movie" | "series" | "episode" | "tv",
        resourceId: streamResourceID,
        ...(details.sourceAddonId ? { sourceAddonId: details.sourceAddonId } : {}),
        ...(details.titleId ? { titleId: details.titleId } : {}),
        title: details.title,
        ...(details.posterUrl ? { posterUrl: details.posterUrl } : {}),
      });
      notifySuccess(t("queue.notice.added", { title: details.title }));
    } catch (cause) {
      setActionError(notifyError(cause, t("queue.error.mutation")));
    } finally {
      setQueueSaving(false);
    }
  }

  async function showTrailer() {
    if (activeTrailers.length > 0 || activeTrailerLoading || activeTrailerUnavailable) return;
    trailerRevealPendingRef.current = true;
    const requestID = ++trailerRequestRef.current;
    const requestedItemKey = trailerItemRef.current;
    const requestedSeasonNumber = selectedTrailerSeason?.seasonNumber;
    const requestIsCurrent = () => trailerRequestRef.current === requestID && trailerItemRef.current === requestedItemKey;
    let trailerRequested = false;
    const warnUnavailable = () => {
      const message = requestedSeasonNumber === undefined
        ? t("media.trailers.noneForTitle")
        : t("media.trailers.noneForSeason", { season: requestedSeasonNumber === 0 ? t("media.season.specials") : t("media.season.number", { number: requestedSeasonNumber }) });
      trailerRevealPendingRef.current = false;
      setTrailerUnavailable(true);
      setTrailerMessage("");
      if (trailerWarningKeyRef.current === requestedItemKey) return;
      trailerWarningKeyRef.current = requestedItemKey;
      notifyWarning(message, t("media.trailers.error.unavailableTitle"));
    };
    setTrailers([]);
    setSelectedTrailer(undefined);
    setTrailerOwnerKey(requestedItemKey);
    setTrailerLoading(true);
    setTrailerMessage("");
    setTrailerUnavailable(false);
    try {
      const resolvedTitleID = trailerTitleID || await resolveMediaTitle(item);
      if (!requestIsCurrent()) return;
      if (item.mediaType !== "episode") setTitleID(resolvedTitleID);
      trailerRequested = true;
      const metadata = await api.trailers(resolvedTitleID, requestedSeasonNumber);
      if (!requestIsCurrent()) return;
      const nextTrailers = Array.isArray(metadata.trailers) ? metadata.trailers : [];
      if (nextTrailers.length === 0) {
        warnUnavailable();
        return;
      }
      setTrailers(nextTrailers);
      setSelectedTrailer(nextTrailers[0]);
    } catch (cause) {
      if (!requestIsCurrent()) return;
      if (trailerRequested && cause instanceof APIError && cause.status === 404) {
        warnUnavailable();
      } else {
        setTrailerMessage(notifyError(cause, t("media.trailers.error.loadFailed"), t("media.trailers.error.unavailableTitle")));
      }
    } finally {
      if (requestIsCurrent()) setTrailerLoading(false);
    }
  }

  function dismissTrailer() {
    trailerRequestRef.current += 1;
    trailerRevealPendingRef.current = false;
    setTrailers([]);
    setSelectedTrailer(undefined);
    setTrailerLoading(false);
    setTrailerMessage("");
    setTrailerUnavailable(false);
    setTrailerOwnerKey("");
  }

  function selectPlaybackStream(option: PlaybackSourceOption) {
    autoPlayNextRef.current = false;
    playRequestedSourceRef.current = "";
    setSelectedStream(option);
  }

  function changeStreamAddonFilter(addonId: string) {
    setStreamAddonFilter(addonId);
    if (!selectedStream || !addonId || selectedStream.addonId === addonId) return;
    autoStartRef.current = false;
    autoPlayNextRef.current = false;
    playRequestedSourceRef.current = "";
    sourceRefreshAttemptRef.current = "";
    setSelectedStream(undefined);
    setPreparation(undefined);
    setPreparationLoading(false);
    setPreparationError("");
  }

  function handleStreamOptionKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
    const group = event.currentTarget.closest<HTMLElement>('[role="radiogroup"]');
    const options = group ? Array.from(group.querySelectorAll<HTMLButtonElement>('[role="radio"]')) : [];
    const currentIndex = options.indexOf(event.currentTarget);
    if (currentIndex < 0 || options.length === 0) return;
    event.preventDefault();
    const nextIndex = event.key === "Home" ? 0
      : event.key === "End" ? options.length - 1
        : Math.max(0, Math.min(options.length - 1, currentIndex + (event.key === "ArrowDown" ? 1 : -1)));
    const next = options[nextIndex];
    next?.focus({ preventScroll: true });
    next?.click();
    next?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  function playPlaybackStream(option: PlaybackSourceOption) {
    autoPlayNextRef.current = false;
    restorePlayerFocusRef.current = false;
    if (selectedStream?.sourceRef === option.sourceRef && preparation) {
      playRequestedSourceRef.current = "";
      setPlaying(true);
      return;
    }
    playRequestedSourceRef.current = option.sourceRef;
    setSelectedStream(option);
  }
  function watchLive() {
    if (item.mediaType !== "tv" || item.available === false || streamsLoading) return;
    const first = availableStreams[0];
    if (first) {
      playPlaybackStream(first);
      return;
    }
    tvPlaybackPendingRef.current = true;
    setStreamsRequested(true);
    if (streamsRequested) setStreamRefreshVersion((version) => version + 1);
  }

  function handleTrailerOptionKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>, index: number) {
    if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const nextIndex = event.key === "Home" ? 0
      : event.key === "End" ? activeTrailers.length - 1
        : (index + (event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : -1) + activeTrailers.length) % activeTrailers.length;
    setSelectedTrailer(activeTrailers[nextIndex]);
    event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[nextIndex]?.focus();
  }

  function closeDetails() {
    dismissTrailer();
    onClose();
  }

  async function toggleTitleWatched() {
    if (watchedBusy) return;
    setWatchedBusy(titleID ?? item.titleId ?? "resolving");
    setActionError("");
    try {
      const resolvedTitleID = titleID ?? item.titleId ?? await resolveMediaTitle(details);
      const watched = !titleProgress?.completed;
      const progress = await api.setWatched(resolvedTitleID, watched, titleProgress?.version ?? 0);
      setTitleID(resolvedTitleID);
      setTitleProgress(progress);
      if (item.mediaType === "episode") setEpisodeProgress((values) => ({ ...values, [resolvedTitleID]: progress }));
      notifySuccess(
        t(watched ? "media.watch.markedWatched" : "media.watch.markedUnwatched", { title: details.title }),
        t(watched ? "media.watch.markedWatchedTitle" : "media.watch.markedUnwatchedTitle"),
      );
    } catch (cause) {
      setActionError(notifyError(cause, t("media.watch.error.updateFailed"), t("media.watch.error.notUpdatedTitle")));
    } finally {
      setWatchedBusy("");
    }
  }

  async function toggleEpisodeWatched(episode: EpisodeMetadata) {
    const current = episodeProgress[episode.id];
    const watched = !current?.completed;
    setWatchedBusy(episode.id);
    setActionError("");
    try {
      const progress = await api.setWatched(episode.id, watched, current?.version ?? 0);
      setEpisodeProgress((values) => ({ ...values, [episode.id]: progress }));
    } catch (cause) {
      setActionError(notifyError(cause, t("media.watch.error.episodeUpdateFailed"), t("media.watch.error.notUpdatedTitle")));
    } finally {
      setWatchedBusy("");
    }
  }

  async function toggleCustomVideoWatched(video: CustomMetaVideo) {
    const identity = resolvedCustomVideos.get(video.id);
    if (!identity || watchedBusy || customVideoIsUpcoming(video)) return;
    const current = episodeProgress[identity.titleId];
    const watched = !current?.completed;
    setWatchedBusy(identity.titleId);
    setActionError("");
    try {
      const progress = await api.setWatched(identity.titleId, watched, current?.version ?? 0);
      setEpisodeProgress((values) => ({ ...values, [identity.titleId]: progress }));
      notifySuccess(
        t(watched ? "media.watch.markedWatched" : "media.watch.markedUnwatched", { title: video.title || details.title }),
        t(watched ? "media.watch.markedWatchedTitle" : "media.watch.markedUnwatchedTitle"),
      );
    } catch (cause) {
      const refreshed = await progressByTitleID([identity.titleId]).catch(() => undefined);
      if (refreshed) setEpisodeProgress((values) => ({ ...values, ...refreshed }));
      setActionError(notifyError(cause, t("media.watch.error.episodeUpdateFailed"), t("media.watch.error.notUpdatedTitle")));
    } finally {
      setWatchedBusy("");
    }
  }

  async function changeEpisodeOrder(episodeOrderID: string) {
    if (!series || episodeOrderLoading || episodeOrderID === (series.selectedEpisodeOrderId ?? "")) return;
    setEpisodeOrderLoading(true);
    setEpisodeOrderError("");
    try {
      const resolved = withoutEmptySeasons(await api.seriesDetails(series.id, episodeOrderID
        ? { mappingProvider: "tvdb", episodeOrderId: episodeOrderID }
        : undefined));
      const seasons = [...resolved.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber);
      const initial = seasons.find((candidate) => candidate.seasonNumber > 0) ?? seasons[0];
      autoPlayNextRef.current = false;
      seasonCacheRef.current.clear();
      setSeries(resolved);
      setSeason(undefined);
      setSeasonID(initial?.id ?? "");
      setSelectedEpisode(undefined);
      setEpisodeProgress({});
      if (initial) onNavigateContext?.({ seasonID: initial.id, seasonNumber: initial.seasonNumber });
    } catch (cause) {
      setEpisodeOrderError(notifyError(cause, t("media.episodeOrder.error.loadFailed"), t("media.episodeOrder.error.unavailableTitle")));
    } finally {
      setEpisodeOrderLoading(false);
    }
  }

  async function toggleSeasonWatched() {
    if (item.mediaType !== "series" || !seasonID || season?.id !== seasonID || seriesLoading || seasonLoading || watchedBusy) return;
    const episodes = (season?.episodes ?? []).filter((episode) => !episodeIsUpcoming(episode));
    if (episodes.length === 0) return;
    const watched = !episodes.every((episode) => episodeProgress[episode.id]?.completed);
    const changed = episodes.filter((episode) => Boolean(episodeProgress[episode.id]?.completed) !== watched);
    setWatchedBusy(seasonID);
    setActionError("");
    try {
      const results = await api.setWatchedBatch(changed.map((episode) => ({
        titleId: episode.id,
        completed: watched,
        expectedVersion: episodeProgress[episode.id]?.version ?? 0,
      })));
      setEpisodeProgress((values) => ({
        ...values,
        ...Object.fromEntries(results.items.map((entry) => [entry.titleId, entry.progress] as const)),
      }));
      notifySuccess(
        t(watched ? "media.season.watch.allMarkedWatched" : "media.season.watch.allMarkedUnwatched"),
        t(watched ? "media.season.watch.watchedTitle" : "media.season.watch.unwatchedTitle"),
      );
    } catch (cause) {
      const refreshed = await api.progressBatch(episodes.map((episode) => episode.id)).catch(() => undefined);
      if (refreshed) {
        setEpisodeProgress(Object.fromEntries(refreshed.items.map((entry) => [entry.titleId, entry.progress ?? undefined])));
      }
      setActionError(notifyError(cause, t("media.season.watch.error.partialUpdate"), t("media.season.watch.error.partialUpdateTitle")));
    } finally {
      setWatchedBusy("");
    }
  }

  async function toggleCustomSeasonWatched() {
    if (item.mediaType !== "anime" || !showCustomVideoChooser || activeCustomVideoGroup?.season === undefined) return;
    const videos = visibleCustomVideos.flatMap((video) => {
      const identity = resolvedCustomVideos.get(video.id);
      return identity && !customVideoIsUpcoming(video) ? [{ video, identity }] : [];
    });
    if (!customProgressConfirmed || videos.length === 0 || watchedBusy) return;
    const watched = !videos.every(({ identity }) => episodeProgress[identity.titleId]?.completed);
    const changed = videos.filter(({ identity }) => Boolean(episodeProgress[identity.titleId]?.completed) !== watched);
    setWatchedBusy(customSeasonBusyKey);
    setActionError("");
    try {
      const progressUpdates: Record<string, PlaybackProgress> = {};
      for (let offset = 0; offset < changed.length; offset += 100) {
        const results = await api.setWatchedBatch(changed.slice(offset, offset + 100).map(({ identity }) => ({
          titleId: identity.titleId,
          completed: watched,
          expectedVersion: episodeProgress[identity.titleId]?.version ?? 0,
        })));
        for (const entry of results.items) progressUpdates[entry.titleId] = entry.progress;
      }
      setEpisodeProgress((values) => ({ ...values, ...progressUpdates }));
      notifySuccess(
        t(watched ? "media.season.watch.allMarkedWatched" : "media.season.watch.allMarkedUnwatched"),
        t(watched ? "media.season.watch.watchedTitle" : "media.season.watch.unwatchedTitle"),
      );
    } catch (cause) {
      const refreshed = await progressByTitleID(videos.map(({ identity }) => identity.titleId)).catch(() => undefined);
      if (refreshed) setEpisodeProgress((values) => ({ ...values, ...refreshed }));
      setActionError(notifyError(cause, t("media.season.watch.error.partialUpdate"), t("media.season.watch.error.partialUpdateTitle")));
    } finally {
      setWatchedBusy("");
    }
  }

  function handleEpisodeEnded() {
    if (!series || !season || !selectedEpisode || !selectedStream) {
      setPlaying(false);
      return;
    }
    const currentIndex = orderedEpisodes.findIndex((episode) => episode.id === selectedEpisode.id);
    const nextEpisode = orderedEpisodes.slice(currentIndex + 1).find((episode) => !episodeIsUpcoming(episode));
    nextSourceRef.current = selectedStream;
    setPlaying(false);
    autoPlayNextRef.current = true;
    if (nextEpisode) {
      setSelectedEpisode(nextEpisode);
      return;
    }
    const seasons = [...series.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber);
    const currentSeasonIndex = seasons.findIndex((candidate) => candidate.id === season.id);
    const nextSeason = seasons.slice(currentSeasonIndex + 1).find((candidate) => candidate.episodeCount > 0);
    if (nextSeason) {
      setSeasonID(nextSeason.id);
      return;
    }
    nextSourceRef.current = undefined;
    autoPlayNextRef.current = false;
  }

  function handleCustomVideoEnded() {
    if (!activeCustomVideo || !selectedStream) {
      setPlaying(false);
      return;
    }
    const currentIndex = orderedCustomVideos.findIndex((video) => video.id === activeCustomVideo.id);
    const nextVideo = orderedCustomVideos.slice(currentIndex + 1).find((video) => !customVideoIsUpcoming(video));
    setPlaying(false);
    if (!nextVideo) {
      nextSourceRef.current = undefined;
      autoPlayNextRef.current = false;
      return;
    }
    nextSourceRef.current = selectedStream;
    autoPlayNextRef.current = true;
    setSelectedCustomSeasonKey(customVideoSeasonKey(nextVideo));
    setSelectedCustomVideo(nextVideo);
    setCustomVideoChooserVisible(false);
  }


  const orderedEpisodes = useMemo(
    () => [...(season?.episodes ?? [])].sort((left, right) => left.seasonNumber - right.seasonNumber || left.episodeNumber - right.episodeNumber),
    [season],
  );
  useEffect(() => {
    const list = episodeListRef.current;
    const row = selectedEpisodeRowRef.current;
    if (!list || !row) return;
    const listRect = list.getBoundingClientRect();
    const rowRect = row.getBoundingClientRect();
    if (rowRect.top < listRect.top) list.scrollTop -= listRect.top - rowRect.top;
    else if (rowRect.bottom > listRect.bottom) list.scrollTop += rowRect.bottom - listRect.bottom;
  }, [activeCustomVideo?.id, orderedEpisodes, selectedEpisode?.id, showCustomVideoChooser, visibleCustomVideos]);
  const availableSeasonEpisodes = orderedEpisodes.filter((episode) => !episodeIsUpcoming(episode));
  const watchedEpisodeCount = availableSeasonEpisodes.filter((episode) => episodeProgress[episode.id]?.completed).length;
  const allSeasonWatched = availableSeasonEpisodes.length > 0 && watchedEpisodeCount === availableSeasonEpisodes.length;
  const watchableCustomSeasonVideos = visibleCustomVideos.filter((video) => video.season !== undefined && video.episode !== undefined && !customVideoIsUpcoming(video));
  const availableCustomSeasonVideos = watchableCustomSeasonVideos.flatMap((video) => {
    const identity = resolvedCustomVideos.get(video.id);
    return identity ? [{ video, identity }] : [];
  });
  const watchedCustomVideoCount = availableCustomSeasonVideos.filter(({ identity }) => episodeProgress[identity.titleId]?.completed).length;
  const allCustomSeasonWatched = watchableCustomSeasonVideos.length > 0
    && availableCustomSeasonVideos.length === watchableCustomSeasonVideos.length
    && watchedCustomVideoCount === watchableCustomSeasonVideos.length;
  const customVideoContext = customType && !showCustomVideoChooser && Boolean(activeCustomVideo);
  const customEpisodeContext = customVideoContext && activeCustomVideo?.season !== undefined && activeCustomVideo.episode !== undefined;
  const customWatchPending = Boolean(customEpisodeContext && (!metaResolved || customProgressLoading));
  const customSeasonPending = !metaResolved || customProgressLoading;
  const standardSeasonPending = seriesLoading || seasonLoading || season?.id !== seasonID;
  const showStandardSeasonWatch = item.mediaType === "series" && Boolean(seasonID);
  const showCustomSeasonWatch = item.mediaType === "anime" && showCustomVideoChooser && activeCustomVideoGroup?.season !== undefined;
  const customDisplayItem = customVideoContext && activeCustomVideo ? customVideoItem(activeCustomVideo, details) : details;
  const activePlayerItem = selectedEpisode && series
    ? episodeItem(series, selectedEpisode, details)
    : activeCustomVideo && activeCustomIdentity
      ? customEpisodePlayerItem(activeCustomVideo, activeCustomIdentity, details)
      : customType ? customDisplayItem : { ...details, titleId: titleID };

  const genres = Array.isArray(details.raw?.genres) ? details.raw.genres.map((genre) => {
    if (typeof genre === "string") return genre;
    const value = record(genre);
    return typeof value?.name === "string" ? value.name : "";
  }).filter(Boolean).slice(0, 4) : [];
  const rawCredits = record(details.raw?.credits);
  const rawCast = details.raw?.cast ?? details.raw?.actors ?? rawCredits?.cast;
  const rawCastCandidates = [
    ...castCandidates(rawCast),
    ...stremioCastLinkCandidates(details.raw?.links),
  ];
  const seriesCast = series?.cast ?? [];
  const castSource = (item.mediaType === "episode" || item.mediaType === "series") && seriesCast.length > 0
    ? seriesCast
    : rawCastCandidates;
  const cast = castMembers(castSource, maximumCastMembers, customType);
  const selectedSeasonSummary = series?.seasons.find((candidate) => candidate.id === seasonID);
  const loadedSelectedSeason = season && (
    season.id === seasonID
    || selectedSeasonSummary?.seasonNumber === season.seasonNumber
  ) ? season : undefined;
  const selectedSeasonPoster = loadedSelectedSeason?.posterUrl || selectedSeasonSummary?.posterUrl;
  const selectedSeasonBackdrop = loadedSelectedSeason?.backdropUrl || selectedSeasonSummary?.backdropUrl;
  const selectedSeasonEpisode = selectedEpisode && (
    selectedEpisode.seasonId === loadedSelectedSeason?.id
    || selectedEpisode.seasonId === seasonID
  ) ? selectedEpisode : undefined;
  const cachedSeriesContextPoster = item.mediaType === "series" || details.posterUrl !== item.posterUrl
    ? details.posterUrl
    : undefined;
  const seriesContextPoster = selectedSeasonPoster || cachedSeriesContextPoster || series?.posterUrl;
  const seriesContextBackdrop = selectedSeasonEpisode?.backdropUrl || selectedSeasonEpisode?.stillUrl || selectedSeasonBackdrop || series?.backdropUrl;
  const backdrop = seriesContextEnabled
    ? seriesContextBackdrop
    : customDisplayItem.backgroundUrl || customDisplayItem.posterUrl;
  const heroArtwork = seriesContextEnabled
    ? seriesContextPoster
    : customDisplayItem.posterUrl || customDisplayItem.backgroundUrl;
  const trailerBackdropSources = [...new Set([
    item.mediaType === "movie" ? details.backgroundUrl : series?.backdropUrl,
    details.backgroundUrl,
    details.posterUrl,
  ].filter((source): source is string => Boolean(source)))];
  const trailerBackdropStyle: CSSProperties | undefined = trailerBackdropSources.length > 0
    ? { backgroundImage: trailerBackdropSources.map((source) => `url(${JSON.stringify(source)})`).join(", ") }
    : undefined;
  const trailerURL = activeTrailer ? (() => {
    const params = new URLSearchParams({ autoplay: "1", vq: "highres" });
    if (activeTrailer.captionPreference) {
      params.set("cc_lang_pref", activeTrailer.captionPreference);
      params.set("cc_load_policy", "1");
    }
    return `https://www.youtube-nocookie.com/embed/${encodeURIComponent(activeTrailer.youtubeId)}?${params.toString()}`;
  })() : "";
  const activeTrailerBadge = activeTrailer ? trailerLanguageBadge(activeTrailer) : "";
  const trailerAvailability = activeTrailer?.captionPreference
    ? t("media.trailers.preferredSubtitlesRequested")
    : activeTrailer?.isFallback ? t("media.trailers.fallbackLanguage") : t("media.trailers.preferredLanguage");

  if (playing && selectedStream) {
    const closePlayer = () => {
      restorePlayerFocusRef.current = true;
      setPlaying(false);
    };
    const candidateSourceRefs = [selectedStream.sourceRef, ...availableStreams.map((option) => option.sourceRef).filter((candidate) => candidate !== selectedStream.sourceRef)].slice(0, 8);
    return <Player item={activePlayerItem} sourceRef={selectedStream.sourceRef} candidateSourceRefs={candidateSourceRefs} startSeconds={preparationStartSeconds} autoplayNextEpisode={autoplayNextEpisode} onClose={closePlayer} onSourceExpired={() => { closePlayer(); setStreamRefreshVersion((version) => version + 1); }} onEnded={selectedEpisode ? handleEpisodeEnded : activeCustomVideo ? handleCustomVideoEnded : undefined} />;
  }

  const isEpisodeContext = item.mediaType === "episode" || Boolean(customEpisodeContext);
  const typeLabel = mediaTypeLabel(isEpisodeContext ? "episode" : details.mediaType);
  const liveProgramTitle = typeof details.currentProgram === "string"
    ? details.currentProgram
    : details.currentProgram?.title || details.currentProgram?.name;
  const episodeSeriesName = item.mediaType === "episode"
    ? series?.name ?? (typeof item.raw?.episodeSeriesName === "string" ? item.raw.episodeSeriesName : "")
    : customEpisodeContext ? details.title : "";
  const episodeTitle = selectedEpisode?.name || (customEpisodeContext ? activeCustomVideo?.title : undefined) || details.title || t("media.episode.fallbackTitle", { number: item.episodeNumber ?? "" });
  const episodeSeasonNumber = selectedEpisode?.seasonNumber ?? item.seasonNumber ?? (customEpisodeContext ? activeCustomVideo?.season : undefined);
  const episodeNumber = selectedEpisode?.episodeNumber ?? item.episodeNumber ?? (customEpisodeContext ? activeCustomVideo?.episode : undefined);
  const episodeRuntimeMinutes = selectedEpisode?.runtimeMinutes;
  const externalTitleLinks: ExternalTitleLink[] = (item.mediaType === "movie" || item.mediaType === "series" || item.mediaType === "episode")
    ? TITLE_ID_PROVIDERS.flatMap<ExternalTitleLink>((provider) => {
      if (item.mediaType === "episode" || selectedEpisode) {
        if (provider.key === "tmdb") {
          const externalID = (series?.externalIds.tmdb ?? details.externalIds?.tmdb)?.trim();
          return externalID ? [{ externalID, provider, mediaType: selectedEpisode ? "episode" : "series", episode: selectedEpisode }] : [];
        }
        const episodeExternalID = (selectedEpisode?.externalIds[provider.key] ?? (item.mediaType === "episode" ? item.externalIds?.[provider.key] : undefined))?.trim();
        const seriesExternalID = (series?.externalIds[provider.key] ?? details.externalIds?.[provider.key])?.trim();
        const externalID = episodeExternalID || seriesExternalID;
        return externalID ? [{ externalID, provider, mediaType: episodeExternalID ? "episode" : "series", episode: undefined }] : [];
      }
      const externalID = details.externalIds?.[provider.key]?.trim();
      return externalID ? [{ externalID, provider, mediaType: item.mediaType, episode: undefined }] : [];
    })
    : [];
  const sourceLabels = mediaSourceLabels(item);

  return (
    <article className="details-page details-page--immersive page-enter" aria-labelledby="media-details-title">
      <button type="button" className="details-page__back" onClick={closeDetails} autoFocus>
        <ArrowLeft size={18} />
        <span>{t("media.details.backToBrowse")}</span>
      </button>

      <section className={`details-hero${isEpisodeContext ? " details-hero--episode" : ""}`} style={backdrop ? { backgroundImage: `url(${backdrop})` } : undefined}>
        <div className="details-hero__shade" aria-hidden="true" />
        <div className="details-hero__glow" aria-hidden="true" />
        <div className="details-hero__inner">
          <div className="details-left">
          <div className="details-primary">
            <aside className="details-artwork" aria-hidden="true">
              {heroArtwork ? <img src={heroArtwork} alt="" loading="eager" fetchPriority="high" /> : <span>{customDisplayItem.title.slice(0, 2).toUpperCase()}</span>}
              {isEpisodeContext && episodeSeasonNumber !== undefined && episodeNumber !== undefined && <small className="details-artwork__episode-code">S{String(episodeSeasonNumber).padStart(2, "0")} · E{String(episodeNumber).padStart(2, "0")}</small>}
            </aside>

            <div className="details-overview">
              {isEpisodeContext
                ? <>
                  {episodeSeriesName && <span className="details-series-name">{episodeSeriesName}</span>}
                  <h1 id="media-details-title">{episodeTitle}</h1>
                </>
                : customDisplayItem.logoUrl
                  ? <><img className="details-logo" src={customDisplayItem.logoUrl} alt="" /><h1 id="media-details-title" className="visually-hidden">{customDisplayItem.title}</h1></>
                  : <h1 id="media-details-title">{customDisplayItem.title}</h1>}

              <div className="details-meta">
                {episodeSeasonNumber !== undefined && episodeNumber !== undefined && <span>{t("media.episode.seasonEpisode", { season: episodeSeasonNumber, episode: episodeNumber })}</span>}
                {customDisplayItem.releaseInfo && customDisplayItem.releaseInfo !== typeLabel && <span>{customDisplayItem.releaseInfo}</span>}
                {episodeRuntimeMinutes !== undefined && <span>{t("common.time.minutesShort", { minutes: episodeRuntimeMinutes })}</span>}
                {customDisplayItem.voteAverage !== undefined && <span className="rating"><Star size={14} fill="currentColor" /> {customDisplayItem.voteAverage.toFixed(1)}</span>}
                <span className={item.mediaType === "tv" ? "details-meta__live" : undefined}>{typeLabel}</span>
                {item.mediaType === "tv" && details.sourceName && <span>{details.sourceName}</span>}
                {item.mediaType === "tv" && details.country && <span>{details.country}</span>}
                {item.mediaType === "tv" && details.language && <span>{details.language}</span>}
                {item.mediaType === "tv" && details.category && <span>{details.category}</span>}
                {item.mediaType === "tv" && liveProgramTitle && <span>{liveProgramTitle}</span>}
                {genres.map((genre) => <span key={genre}>{genre}</span>)}
              </div>

              {(externalTitleLinks.length > 0 || sourceLabels.length > 0) && <div className="details-title-links">
                <div className="details-provider-badges">
                  {externalTitleLinks.length > 0 && <span className="details-provider-badges__external" role="group" aria-label={t("media.details.externalPagesLabel")}>
                    {externalTitleLinks.map(({ externalID, provider, mediaType, episode }) => {
                      const label = t("media.details.openExternalPage", { provider: provider.label, id: externalID });
                      return <a key={provider.key} className={`details-provider-badge details-provider-badge--${provider.key}`} href={titleProviderURL(provider.key, externalID, mediaType, episode)} target="_blank" rel="noreferrer" aria-label={label} title={label}>
                        <span className="details-provider-badge__brand">{provider.label}</span>
                        <ExternalLink size={11} aria-hidden="true" />
                      </a>;
                    })}
                  </span>}
                  {externalTitleLinks.length > 0 && sourceLabels.length > 0 && <span className="details-provider-badges__separator" aria-hidden="true" />}
                  {sourceLabels.map((label, index) => <span className="details-provider-badge details-provider-badge--source" key={`${label}:${index}`}>
                    <span className="details-provider-badge__brand">{label}</span>
                  </span>)}
                </div>
              </div>}


              {metaLoading && !customDisplayItem.description
                ? <div className="details-loading" role="status"><LoaderCircle className="spin" size={18} /> {t("media.details.loading")}</div>
                : <p className="details-description">{customDisplayItem.description || t("media.details.noSynopsis")}</p>}
              {metaError && <Notice tone="info">{metaError} {t("media.details.partialInformationShown")}</Notice>}

              <div className="details-actions">
                <Button variant="secondary" loading={saving} disabled={item.mediaType === "episode" && !libraryTitleID} onClick={() => void toggleLibrary()}>
                  {saved ? <Check size={19} /> : <Bookmark size={19} />}
                  {t(saved ? "library.actions.inLibrary" : "library.actions.add")}
                </Button>
                {item.mediaType === "tv" && <Button loading={streamsLoading} disabled={details.available === false} onClick={watchLive}>
                  <Play size={19} fill="currentColor" />
                  {t("common.play")} · {t("media.type.liveTv")}
                </Button>}
                {trailersAvailableForContext && <Button type="button" variant="secondary" disabled={Boolean(activeTrailer) || activeTrailerUnavailable} loading={activeTrailerLoading} aria-label={t(activeTrailerLoading ? "media.trailers.loading" : "media.trailers.title")} aria-busy={activeTrailerLoading} aria-controls={activeTrailer ? "details-trailer" : undefined} aria-expanded={Boolean(activeTrailer)} onClick={() => void showTrailer()}>
                  <Clapperboard size={19} />
                  {t("media.trailers.title")}
                </Button>}
                {showStandardSeasonWatch && <Button type="button" variant="secondary" disabled={standardSeasonPending || availableSeasonEpisodes.length === 0 || Boolean(watchedBusy)} loading={standardSeasonPending || watchedBusy === seasonID} aria-busy={standardSeasonPending || watchedBusy === seasonID} aria-label={t(allSeasonWatched ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} onClick={() => void toggleSeasonWatched()}>
                  {allSeasonWatched ? <EyeOff size={19} /> : <Eye size={19} />}
                  {t(allSeasonWatched ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
                </Button>}
                {showCustomSeasonWatch && <Button type="button" variant="secondary" disabled={!customProgressConfirmed || availableCustomSeasonVideos.length !== watchableCustomSeasonVideos.length || watchableCustomSeasonVideos.length === 0 || Boolean(watchedBusy)} loading={customSeasonPending || watchedBusy === customSeasonBusyKey} aria-busy={customSeasonPending || watchedBusy === customSeasonBusyKey} aria-label={t(allCustomSeasonWatched ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} onClick={() => void toggleCustomSeasonWatched()}>
                  {allCustomSeasonWatched ? <EyeOff size={19} /> : <Eye size={19} />}
                  {t(allCustomSeasonWatched ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
                </Button>}
                {(item.mediaType === "movie" || item.mediaType === "episode") && <Button type="button" variant="secondary" loading={Boolean(watchedBusy)} aria-busy={Boolean(watchedBusy)} aria-label={t(titleProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} onClick={() => void toggleTitleWatched()}>
                  {titleProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}
                  {t(titleProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
                </Button>}
                {!customType && <Button variant="secondary" loading={queueSaving} disabled={!streamResourceID} onClick={() => void addToReadingQueue()}>
                  <ListVideo size={19} /> {t("queue.actions.add")}
                </Button>}
                {customEpisodeContext && activeCustomVideo && <Button type="button" variant="secondary" disabled={customVideoIsUpcoming(activeCustomVideo) || !activeCustomIdentity || !customProgressConfirmed} loading={customWatchPending || watchedBusy === activeCustomIdentity?.titleId} aria-busy={customWatchPending || watchedBusy === activeCustomIdentity?.titleId} aria-label={t(selectedProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} onClick={() => void toggleCustomVideoWatched(activeCustomVideo)}>
                  {selectedProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}
                  {t(selectedProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
                </Button>}
              </div>

              {fromContinue && item.mediaType === "episode" && seriesLoading && <div className="details-context-actions">
                <span className="details-context-loading" role="status"><LoaderCircle className="spin" size={15} /> {t("media.season.loading")}</span>
              </div>}
              {actionError && <Notice>{actionError}</Notice>}
          </div>
          </div>
            {cast.length > 0 && <section className="details-cast" aria-labelledby="details-cast-title">
              <header className="details-cast__header">
                <h2 id="details-cast-title">{t("media.cast.title")}</h2>
              </header>
              <HorizontalDragRow className="details-cast__list" role="group" aria-labelledby="details-cast-title" tabIndex={0} onKeyDown={handleCastCarouselKeyDown}>
                {cast.map((member) => <CastMemberCard member={member} key={member.id} />)}
              </HorizontalDragRow>
            </section>}
          </div>

          <aside className="details-context-panel" role="region" aria-label={item.mediaType === "series" || showCustomVideoChooser ? t("media.series.episodesTitle") : t("media.sources.sectionLabel")}>
            {item.mediaType === "series"
              ? <div className="series-browser">
                <header className="details-context-panel__header">
                  <span className="details-section-heading__icon"><ListVideo size={20} /></span>
                  <div>
                    <span>{t("media.series.guideEyebrow")}</span>
                    <h2 id="details-episodes-title">{t("media.series.episodesTitle")}</h2>
                  </div>
                  {series && series.episodeOrders.length > 0 && <label className="series-order">
                    <span>{t("media.episodeOrder.label")}</span>
                    <span className="series-order__field">
                      <Select className={episodeOrderLoading ? "is-loading" : ""} aria-label={t("media.episodeOrder.accessibleLabel")} value={series.selectedEpisodeOrderId ?? ""} disabled={episodeOrderLoading} onChange={(value) => void changeEpisodeOrder(value)} options={[
                        { value: "", label: t("media.episodeOrder.profileDefault") },
                        ...series.episodeOrders.map((order) => ({ value: order.id, label: episodeOrderLabel(order) })),
                      ]} />
                      {episodeOrderLoading && <LoaderCircle className="spin" size={15} aria-hidden="true" />}
                    </span>
                  </label>}
                </header>
                {episodeOrderError && <Notice>{episodeOrderError}</Notice>}

                {seriesLoading
                  ? <div className="series-browser__loading"><LoaderCircle className="spin" size={18} /> {t("media.season.loading")}</div>
                  : seriesError && !series
                    ? <Notice>{seriesError}</Notice>
                    : series && <>
                      <HorizontalDragRow className="season-tabs" role="tablist" aria-label={t("media.season.tabsLabel")}>
                        {[...series.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber).map((candidate) => (
                          <button key={candidate.id} type="button" role="tab" aria-selected={seasonID === candidate.id} className={seasonID === candidate.id ? "is-active" : ""} onClick={() => {
                            autoPlayNextRef.current = false;
                            setSeasonID(candidate.id);
                            onNavigateContext?.({ seasonID: candidate.id, seasonNumber: candidate.seasonNumber });
                          }}>
                            <span>{candidate.seasonNumber === 0 ? t("media.season.specials") : t("media.season.number", { number: candidate.seasonNumber })}</span>
                            <small>{t(candidate.episodeCount === 1 ? "media.episode.count.one" : "media.episode.count.many", { count: candidate.episodeCount })}</small>
                          </button>
                        ))}
                      </HorizontalDragRow>

                      {seasonLoading
                        ? <div className="series-browser__loading"><LoaderCircle className="spin" size={18} /> {t("media.episode.loading")}</div>
                        : <>
                          <div className="season-watch-state">
                            <span>{t("media.season.watchedCount", { watched: watchedEpisodeCount, total: availableSeasonEpisodes.length })}</span>
                          </div>

                          <div ref={episodeListRef} className="episode-list">
                            {orderedEpisodes.map((episode) => {
                              const progress = episodeProgress[episode.id];
                              const upcoming = episodeIsUpcoming(episode);
                              const progressPercent = progress && progress.durationSeconds > 0 ? Math.min(100, progress.positionSeconds / progress.durationSeconds * 100) : 0;
                              return <div ref={selectedEpisode?.id === episode.id ? selectedEpisodeRowRef : undefined} key={episode.id} className={selectedEpisode?.id === episode.id ? "is-selected" : ""}>
                                <button type="button" className={upcoming ? "episode-main is-upcoming" : "episode-main"} aria-current={selectedEpisode?.id === episode.id ? "true" : undefined} onClick={() => {
                                  autoPlayNextRef.current = false;
                                  onOpenMedia?.(episodeItem(series, episode, details));
                                }}>
                                  <span className="episode-number">{String(episode.episodeNumber).padStart(2, "0")}</span>
                                  <span className="episode-visual">
                                    {episode.stillUrl ? <img src={episode.stillUrl} alt="" loading="lazy" /> : <span className="episode-placeholder"><Play size={20} /></span>}
                                    {progressPercent > 0 && <i className="episode-progress"><span style={{ width: `${progressPercent}%` }} /></i>}
                                  </span>
                                  <span className="episode-copy">
                                    <strong>{episode.name || t("media.episode.fallbackTitle", { number: episode.episodeNumber })}</strong>
                                    <small>{episode.runtimeMinutes ? t("common.time.minutesShort", { minutes: episode.runtimeMinutes }) : ""}{episode.airDate ? ` · ${episode.airDate}` : ""}{upcoming ? ` · ${t("media.episode.upcoming")}` : ""}</small>
                                    <p>{episode.overview || t("media.episode.noSynopsis")}</p>
                                  </span>
                                  <span className="episode-play" aria-hidden="true"><Play size={16} fill="currentColor" /></span>
                                </button>
                                <button type="button" className={progress?.completed ? "episode-watched is-watched" : "episode-watched"} aria-label={t(progress?.completed ? "media.watch.actions.markNamedUnwatched" : "media.watch.actions.markNamedWatched", { title: episode.name || t("media.episode.fallbackTitle", { number: episode.episodeNumber }) })} title={t(progress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} disabled={upcoming || watchedBusy === episode.id || watchedBusy === seasonID} onClick={() => void toggleEpisodeWatched(episode)}>
                                  {watchedBusy === episode.id ? <LoaderCircle className="spin" size={17} /> : progress?.completed ? <Check size={17} /> : <Eye size={17} />}
                                </button>
                              </div>;
                            })}
                          </div>
                        </>}
                    </>}
              </div>
              : showCustomVideoChooser
                ? <div className="series-browser">
                  <header className="details-context-panel__header">
                    <span className="details-section-heading__icon"><ListVideo size={20} /></span>
                    <div>
                      <span>{t("media.series.guideEyebrow")}</span>
                      <h2 ref={customChooserHeadingRef} tabIndex={-1}>{t("media.series.episodesTitle")}</h2>
                    </div>
                  </header>
                  {metaLoading && !resolvedCustomMeta
                    ? <div className="series-browser__loading"><LoaderCircle className="spin" size={18} /> {t("media.episode.loading")}</div>
                    : <>
                      <HorizontalDragRow className="season-tabs" role="tablist" aria-label={t("media.season.tabsLabel")}>
                        {customVideoGroups.map((group) => {
                          const active = group.key === activeCustomSeasonKey;
                          return <button
                            key={group.key}
                            type="button"
                            role="tab"
                            aria-selected={active}
                            className={active ? "is-active" : ""}
                            onClick={() => {
                              autoPlayNextRef.current = false;
                              setSelectedCustomSeasonKey(group.key);
                            }}
                          >
                            <span>{group.season === undefined ? t("common.fallback.unknown") : group.season === 0 ? t("media.season.specials") : t("media.season.number", { number: group.season })}</span>
                            <small>{t(group.videos.length === 1 ? "media.episode.count.one" : "media.episode.count.many", { count: group.videos.length })}</small>
                          </button>;
                        })}
                      </HorizontalDragRow>
                      <div className="season-watch-state">
                        <span>{t("media.season.watchedCount", { watched: watchedCustomVideoCount, total: watchableCustomSeasonVideos.length })}</span>
                      </div>
                      <div ref={episodeListRef} className="episode-list episode-list--custom">
                        {visibleCustomVideos.map((video, index) => {
                          const number = video.episode ?? index + 1;
                          const title = video.title || t("media.episode.fallbackTitle", { number });
                          const episodeContext = video.season !== undefined && video.episode !== undefined
                            ? t("media.episode.seasonEpisode", { season: video.season, episode: video.episode })
                            : "";
                          const active = activeCustomVideo?.id === video.id;
                          const identity = resolvedCustomVideos.get(video.id);
                          const progress = identity ? episodeProgress[identity.titleId] : undefined;
                          const progressPercent = progress && progress.durationSeconds > 0 ? Math.min(100, progress.positionSeconds / progress.durationSeconds * 100) : 0;
                          const upcoming = customVideoIsUpcoming(video);
                          const watchCoordinates = video.season !== undefined && video.episode !== undefined;
                          const watchPending = watchCoordinates && (!metaResolved || customProgressLoading);
                          const rowClassName = [active ? "is-selected" : "", watchCoordinates ? "" : "has-no-watch-action"].filter(Boolean).join(" ");
                          return <div ref={active ? selectedEpisodeRowRef : undefined} key={video.id} className={rowClassName}>
                            <button type="button" className={upcoming ? "episode-main is-upcoming" : "episode-main"} aria-current={active ? "true" : undefined} onClick={() => {
                              autoPlayNextRef.current = false;
                              customPanelFocusTargetRef.current = "streams";
                              setSelectedCustomVideo(video);
                              setCustomVideoChooserVisible(false);
                            }}>
                              <span className="episode-number">{String(number).padStart(2, "0")}</span>
                              <span className="episode-visual">
                                {video.thumbnail ? <img src={video.thumbnail} alt="" loading="lazy" /> : <span className="episode-placeholder"><Play size={20} /></span>}
                                {progressPercent > 0 && <i className="episode-progress"><span style={{ width: `${progressPercent}%` }} /></i>}
                              </span>
                              <span className="episode-copy">
                                <strong>{title}</strong>
                                <small>{[episodeContext, customVideoReleaseInfo(video), upcoming ? t("media.episode.upcoming") : ""].filter(Boolean).join(" · ")}</small>
                                <p>{video.overview || t("media.episode.noSynopsis")}</p>
                              </span>
                              <span className="episode-play" aria-hidden="true"><Play size={16} fill="currentColor" /></span>
                            </button>
                            {watchCoordinates && <button type="button" className={progress?.completed ? "episode-watched is-watched" : "episode-watched"} aria-busy={watchPending || watchedBusy === identity?.titleId} aria-label={t(progress?.completed ? "media.watch.actions.markNamedUnwatched" : "media.watch.actions.markNamedWatched", { title })} title={t(progress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")} disabled={upcoming || !identity || !customProgressConfirmed || watchedBusy === identity.titleId || watchedBusy === customSeasonBusyKey} onClick={() => void toggleCustomVideoWatched(video)}>
                              {watchPending || watchedBusy === identity?.titleId ? <LoaderCircle className="spin" size={17} /> : progress?.completed ? <Check size={17} /> : <Eye size={17} />}
                            </button>}
                          </div>;
                        })}
                      </div>
                    </>}
                </div>
              : <section className="details-stream-selector" aria-labelledby="details-streams-title">
                <header className="details-context-panel__header details-context-panel__header--streams">
                  <div>
                    <span>{episodeSeasonNumber !== undefined && episodeNumber !== undefined ? t("media.episode.seasonEpisode", { season: episodeSeasonNumber, episode: episodeNumber }) : t("media.sources.sectionLabel")}</span>
                    <strong ref={customStreamHeadingRef} id="details-streams-title" tabIndex={customType ? -1 : undefined}>{item.mediaType === "episode" ? episodeTitle : customDisplayItem.title}</strong>
                  </div>
                  {item.mediaType === "episode" && series && onOpenSeason && <button type="button" className="details-context-panel__back" onClick={() => onOpenSeason(seriesItem(series, details, selectedEpisode))}>
                    <ArrowLeft size={15} />
                    <span>{t("common.back")} · {t("media.series.episodesTitle")}</span>
                  </button>}
                  {customType && customVideos.length > 0 && <button type="button" className="details-context-panel__back" onClick={() => {
                    customPanelFocusTargetRef.current = "chooser";
                    setCustomVideoChooserVisible(true);
                  }}>
                    <ArrowLeft size={15} />
                    <span>{t("common.back")} · {t("media.series.episodesTitle")}</span>
                  </button>}
                </header>

                {(item.mediaType !== "tv" || streamsRequested) && <div className="details-stream-toolbar">
                  {streamAddonCategories.length >= 1 && <div className="details-stream-filter">
                    <Select
                      aria-label={t("media.sources.addonFilterLabel")}
                      fitContent
                      value={streamAddonFilter}
                      onChange={changeStreamAddonFilter}
                      options={[
                        { value: "", label: t("media.filter.all") },
                        ...streamAddonCategories.map((category) => ({ value: category.addonId, label: category.label })),
                      ]}
                    />
                  </div>}
                  <div className="details-stream-toolbar__status">
                    <span aria-live="polite">{streamsLoading ? t("common.status.loading") : t(filteredStreams.length === 1 ? "media.sources.availableCount.one" : "media.sources.availableCount.many", { count: filteredStreams.length })}</span>
                    <IconButton label={t("media.sources.refresh")} disabled={streamsLoading} onClick={() => {
                      autoPlayNextRef.current = false;
                      setStreamRefreshVersion((version) => version + 1);
                    }}>
                      <RefreshCw size={17} className={streamsLoading ? "spin" : ""} />
                    </IconButton>
                  </div>
                </div>}

                <div className="details-context-panel__scroll">
                  {item.mediaType === "tv" && details.available === false
                    ? <div className="details-stream-feedback"><Notice tone="warning">{t("common.status.unavailable")}</Notice></div>
                    : item.mediaType === "tv" && !streamsRequested
                      ? <div className="details-stream-feedback">
                        <Button onClick={watchLive}><Play size={18} fill="currentColor" /> {t("common.play")} · {t("media.type.liveTv")}</Button>
                      </div>
                      : streamsLoading
                        ? <div className="details-stream-skeletons" role="status" aria-label={t("media.sources.loading")}>
                          <span /><span /><span />
                          <i className="visually-hidden">{t("media.sources.loading")}</i>
                        </div>
                        : streamsError && availableStreams.length === 0
                          ? <div className="details-stream-feedback">
                            <Notice>{streamsError}</Notice>
                            <Button variant="ghost" onClick={item.mediaType === "tv" ? watchLive : () => setStreamRefreshVersion((version) => version + 1)}><RefreshCw size={16} /> {t("common.actions.retry")}</Button>
                          </div>
                      : filteredStreams.length > 0
                        ? <div className="details-stream-list" role="radiogroup" aria-orientation="vertical" aria-label={t("media.sources.availableLabel")}>
                          {filteredStreams.map((option, index) => {
                            const selected = selectedStream?.sourceRef === option.sourceRef;
                            const playDisabled = preparationLoading || Boolean(preparationError);
                            const addonLabel = streamAddonLabels.get(option.addonId) ?? option.addonName?.trim() ?? option.manifestId;
                            const technicalLabel = [option.protocol, option.container].filter(Boolean).map((value) => value!.toUpperCase()).join(" · ");
                            return <div key={option.sourceRef} className={selected ? "is-selected" : ""}>
                              <button type="button" className="details-stream-list__option" role="radio" aria-checked={selected} tabIndex={selected ? 0 : selectedStream ? -1 : index === 0 ? 0 : -1} onKeyDown={handleStreamOptionKeyDown} onClick={() => selectPlaybackStream(option)}>
                                <span>
                                  <strong>{option.name}</strong>
                                  {option.description && <small>{option.description}</small>}
                                  {!option.description && option.filename && <small>{option.filename}</small>}
                                  <span className="details-stream-list__metadata">
                                    <small className="details-stream-list__addon">{addonLabel}</small>
                                    <small className="details-stream-list__technical">{technicalLabel}</small>
                                  </span>
                                </span>
                                {selected && <span className="details-stream-list__state">
                                  {preparationLoading
                                    ? <LoaderCircle className="spin" size={17} />
                                    : preparation
                                      ? <><Check size={17} /><small>{preparationLabel(preparation)}</small></>
                                      : preparationError
                                        ? <small>{t("common.status.unavailable")}</small>
                                        : <small>{t("common.status.selected")}</small>}
                                </span>}
                              </button>
                              {selected && <button ref={(node) => {
                                if (!node) return;
                                if (!restorePlayerFocusRef.current) return;
                                restorePlayerFocusRef.current = false;
                                window.requestAnimationFrame(() => node.focus({ preventScroll: true }));
                              }} type="button" className="episode-play" data-media-action="play-selected-stream" aria-label={`${item.mediaType === "episode" ? t("media.details.playEpisode") : t("media.details.playSelectedStream")}: ${option.name}`} disabled={playDisabled} onClick={() => playPlaybackStream(option)}>
                                <Play size={16} fill="currentColor" />
                              </button>}
                            </div>;
                          })}
                        </div>
                        : <Notice>{t("media.sources.empty")}</Notice>}
                  {preparationError && <Notice>{preparationError}</Notice>}
                </div>
              </section>}
          </aside>
        </div>

        {trailerStageVisible && <div ref={trailerStageRef} className="details-trailer-stage">
          <div className="details-trailer-stage__backdrop" style={trailerBackdropStyle} aria-hidden="true" />
          {activeTrailer && <section id="details-trailer" className="details-trailer" aria-label={t("media.trailers.forTitle", { title: details.title })}>
            <header className="details-trailer__header">
              <span className="details-trailer__heading"><Clapperboard size={17} /><span><strong>{t("media.trailers.title")}</strong><small>{t(activeTrailers.length > 1 ? "media.trailers.chooseVersion" : "media.trailers.nowPlaying")}</small></span></span>
              <IconButton label={t("media.trailers.dismiss")} onClick={dismissTrailer}><X size={17} /></IconButton>
            </header>
            <div className="details-trailer__frame"><iframe key={`${activeTrailer.youtubeId}:${activeTrailer.captionPreference ?? ""}`} src={trailerURL} title={t("media.trailers.frameTitle", { trailer: activeTrailer.name || t("media.trailers.fallbackName"), title: details.title })} allow="autoplay; encrypted-media; picture-in-picture" referrerPolicy="strict-origin-when-cross-origin" allowFullScreen /></div>
            <div className="details-trailer__active">
              <span className={activeTrailer.isFallback ? "details-trailer__badge" : "details-trailer__badge is-preferred"}>{activeTrailerBadge}</span>
              <span><strong title={activeTrailer.name || t("media.trailers.fallbackName")}>{activeTrailer.name || t("media.trailers.fallbackName")}</strong><small>{trailerAvailability}</small></span>
            </div>
            {activeTrailers.length > 1 && <div className="details-trailer__chooser">
              <div className="details-trailer__chooser-heading"><strong>{t("media.trailers.choose")}</strong><span>{t(activeTrailers.length === 1 ? "common.results.count.one" : "common.results.count.many", { count: activeTrailers.length })}</span></div>
              <div className="details-trailer__options" role="radiogroup" aria-label={t("media.trailers.availableForTitle", { title: details.title })}>
                {activeTrailers.map((option, index) => {
                  const selected = option.youtubeId === activeTrailer.youtubeId;
                  const badge = trailerLanguageBadge(option);
                  return <button key={option.youtubeId} type="button" role="radio" aria-checked={selected} tabIndex={selected ? 0 : -1} aria-label={t("media.trailers.optionLabel", { name: option.name || t("media.trailers.fallbackName"), language: badge, preferredSuffix: option.isFallback ? "" : `, ${t("media.trailers.preferredLanguage")}` })} className={`${selected ? "is-selected " : ""}${option.isFallback ? "" : "is-preferred"}`.trim()} onClick={() => setSelectedTrailer(option)} onKeyDown={(event) => handleTrailerOptionKeyDown(event, index)}>
                    <span className="details-trailer__radio" aria-hidden="true" />
                    <strong title={option.name || t("media.trailers.fallbackName")}>{option.name || t("media.trailers.fallbackName")}</strong>
                    <span className="details-trailer__badge">{badge}</span>
                  </button>;
                })}
              </div>
            </div>}
          </section>}
          {activeTrailerMessage && <div className="details-trailer-feedback"><Notice>{activeTrailerMessage}</Notice></div>}
        </div>}
      </section>
    </article>
  );
}

type PlayerPhase = "preparing" | "ready" | "playing" | "paused" | "buffering" | "recovering" | "failed" | "ended";
type PlayerPanel = "sources" | "audio" | "subtitles" | "speed" | "stats" | "remote" | null;
type PlayerPreferences = { volume: number; muted: boolean; rate: number };
type PlayerStats = { bufferedAhead: number; droppedFrames: number; totalFrames: number; width: number; height: number };
type ProgressWrite = { titleID: string; positionSeconds: number; durationSeconds: number; completed: boolean; expectedVersion: number };

function coalesceProgressWrite(current: ProgressWrite, next: ProgressWrite): ProgressWrite {
  return { ...next, completed: current.completed || next.completed };
}

function playbackSubtitleAvailable(subtitle: PlaybackSubtitle): boolean {
  return subtitle.delivery !== "external" || Boolean(subtitle.url?.trim());
}
type PlayerFullscreenKind = "none" | "standard" | "webkit";
type WebKitFullscreenVideo = HTMLVideoElement & {
  webkitDisplayingFullscreen?: boolean;
  webkitEnterFullscreen?: () => void;
  webkitExitFullscreen?: () => void;
};
type PlayerScreenOrientation = {
  lock?: (orientation: "landscape") => Promise<void>;
  unlock?: () => void;
};

const playerPreferencesKey = "rivune.player.preferences";
const playbackRates = [0.5, 0.75, 1, 1.25, 1.5, 2];

function loadPlayerPreferences(): PlayerPreferences {
  try {
    const stored = JSON.parse(localStorage.getItem(playerPreferencesKey) || "{}") as Partial<PlayerPreferences>;
    const volume = typeof stored.volume === "number" ? Math.min(1, Math.max(0, stored.volume)) : 1;
    const rate = typeof stored.rate === "number" && playbackRates.includes(stored.rate) ? stored.rate : 1;
    return { volume, muted: Boolean(stored.muted), rate };
  } catch {
    return { volume: 1, muted: false, rate: 1 };
  }
}

function playerModeLabel(mode?: string, toneMapped = false): string {
  if (mode === "direct") return t("player.mode.direct");
  if (mode === "remux") return t("player.mode.remux");
  if (mode === "transcode_audio") return t("player.mode.audioConversion");
  if (mode === "transcode") return t(toneMapped ? "player.mode.hdrConversion" : "player.mode.videoConversion");
  if (mode === "youtube") return "YouTube";
  if (mode === "external") return t("player.mode.external");
  return t("player.mode.playback");
}
function playbackPolicyErrorMessage(cause: unknown): string | undefined {
  if (!(cause instanceof APIError) || cause.status !== 422) return undefined;
  if (cause.code === "playback_transcoding_disabled") return t("player.error.transcodingDisabled");
  if (cause.code === "playback_client_capability_missing") return t("player.error.clientCapabilityMissing");
  return undefined;
}


function playerSourceAvailable(source: PlaybackSource): boolean {
  if (!source.compatible) return false;
  if (source.mode === "external") return Boolean(source.url || source.infoHash);
  return Boolean(source.url || source.ytId);
}

function playerPhaseLabel(phase: PlayerPhase): string {
  if (phase === "preparing") return t("player.phase.preparing");
  if (phase === "ready") return t("player.phase.ready");
  if (phase === "playing") return t("player.phase.playing");
  if (phase === "paused") return t("player.phase.paused");
  if (phase === "buffering") return t("player.phase.buffering");
  if (phase === "recovering") return t("player.phase.recovering");
  if (phase === "failed") return t("player.phase.failed");
  return t("player.phase.ended");
}

function playerTrackLabel(track: { codec: string; channels?: number }): string {
  const channelLabel = track.channels ? track.channels === 2 ? "2.0" : t(track.channels === 1 ? "player.audio.channelCount.one" : "player.audio.channelCount.many", { count: track.channels }) : "";
  return `${track.codec.toUpperCase()}${channelLabel ? ` · ${channelLabel}` : ""}`;
}
function validPlaybackMarker(marker: PlaybackMarker, duration: number): boolean {
  return (marker.type === "intro" || marker.type === "recap" || marker.type === "outro") &&
    Number.isFinite(marker.startSeconds) && Number.isFinite(marker.endSeconds) &&
    marker.startSeconds >= 0 && marker.endSeconds > marker.startSeconds &&
    (duration <= 0 || marker.endSeconds <= duration);
}

function skipMarkerLabel(type: PlaybackMarker["type"]): string {
  if (type === "recap") return t("player.skipRecap");
  if (type === "outro") return t("player.skipOutro");
  return t("player.skipIntro");
}


export function Player(
  { item, sourceRef, candidateSourceRefs, startSeconds, autoplayNextEpisode, onClose, onSourceExpired, onEnded }:
  { item: MediaItem; sourceRef: string; candidateSourceRefs: string[]; startSeconds: number; autoplayNextEpisode: boolean; onClose: () => void; onSourceExpired: () => void; onEnded?: () => void },
) {
  const initialPreferences = useRef(loadPlayerPreferences()).current;
  const playerRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const panelRef = useRef<HTMLElement>(null);
  const panelTriggerRef = useRef<HTMLElement | null>(null);
  const previousPanelRef = useRef<PlayerPanel>(null);
  const controlsTimerRef = useRef<number | undefined>(undefined);
  const clickTimerRef = useRef<number | undefined>(undefined);
  const lastTouchRef = useRef({ time: 0, x: 0 });
  const suppressNextClickRef = useRef(false);
  const fullscreenKindRef = useRef<PlayerFullscreenKind>("none");
  const [fullscreenKind, setFullscreenKind] = useState<PlayerFullscreenKind>("none");
  const [fullscreenSupported, setFullscreenSupported] = useState(() => typeof document.documentElement.requestFullscreen === "function" || "webkitEnterFullscreen" in HTMLVideoElement.prototype);
  const [streams, setStreams] = useState<PlaybackSource[]>([]);
  const [subtitles, setSubtitles] = useState<PlaybackSubtitle[]>([]);
  const [markers, setMarkers] = useState<PlaybackMarker[]>([]);
  const [dismissedMarkers, setDismissedMarkers] = useState<Set<PlaybackMarker["type"]>>(() => new Set());
  const [selected, setSelected] = useState(0);
  const [loading, setLoading] = useState(true);
  const [progressReady, setProgressReady] = useState(false);
  const [phase, setPhase] = useState<PlayerPhase>("preparing");
  const [error, setError] = useState("");
  const [panel, setPanel] = useState<PlayerPanel>(null);
  const [controlsVisible, setControlsVisible] = useState(true);
  const [currentTime, setCurrentTime] = useState(0);
  const [videoDuration, setVideoDuration] = useState(0);
  const [playbackStart, setPlaybackStart] = useState<number>();
  const [playbackGeneration, setPlaybackGeneration] = useState(0);
  const [subtitleResolveGeneration, setSubtitleResolveGeneration] = useState(0);
  const [retryVersion, setRetryVersion] = useState(0);
  const [activeSourceRef, setActiveSourceRef] = useState(sourceRef);
  const [failoverReady, setFailoverReady] = useState(candidateSourceRefs.length < 2);
  const [failoverState, setFailoverState] = useState<PlaybackFailoverState>();
  const [failoverNotice, setFailoverNotice] = useState("");
  const failoverRef = useRef<PlaybackFailoverState | undefined>(undefined);
  const failoverPendingRef = useRef(false);
  const [seekPreview, setSeekPreview] = useState<number | null>(null);
  const [seekFeedback, setSeekFeedback] = useState<{ seconds: number; id: number }>();
  const [paused, setPaused] = useState(true);
  const [playbackBlocked, setPlaybackBlocked] = useState(false);
  const [muted, setMuted] = useState(initialPreferences.muted);
  const [volume, setVolume] = useState(initialPreferences.volume);
  const [playbackRate, setPlaybackRate] = useState(initialPreferences.rate);
  const [preferredAudioTrack, setPreferredAudioTrack] = useState<number>();
  const [selectedAudioTrack, setSelectedAudioTrack] = useState<number>();
  const [selectedSubtitleID, setSelectedSubtitleID] = useState("none");
  const [stats, setStats] = useState<PlayerStats>({ bufferedAhead: 0, droppedFrames: 0, totalFrames: 0, width: 0, height: 0 });
  const titleIDRef = useRef(item.titleId);
  const [coordinationDevices, setCoordinationDevices] = useState<PlaybackDevice[]>([]);
  const [coordinationTarget, setCoordinationTarget] = useState("");
  const [coordinationMode, setCoordinationMode] = useState<PlaybackLoadMode>("play-copy");
  const [coordinationPending, setCoordinationPending] = useState(false);
  const [coordinationError, setCoordinationError] = useState("");
  const progressVersionRef = useRef(0);
  const resumePositionRef = useRef(startSeconds);
  const lastSavedPositionRef = useRef(0);
  const progressRequestRef = useRef<ProgressWrite | undefined>(undefined);
  const pendingProgressRef = useRef<Map<string, ProgressWrite>>(new Map());
  const sessionIDRef = useRef("");
  const playbackDurationRef = useRef(0);
  const streamProtocolRef = useRef("");
  const playbackOffsetRef = useRef(0);
  const seekTransportRef = useRef<((position: number) => void) | undefined>(undefined);
  const pausedAtRef = useRef(0);
  const subtitlePreferenceRef = useRef<string | undefined>(undefined);
  const coordinationOperationRef = useRef(typeof item.raw?.coordinationOperationId === "string" ? item.raw.coordinationOperationId : "");
  const coordinationReportedRef = useRef(false);
  const subtitleHandoffRef = useRef(false);

  useEffect(() => {
    const candidates = [...new Set(candidateSourceRefs.map((candidate) => candidate.trim()).filter(Boolean))].slice(0, 8);
    if (candidates.length < 2 || candidates[0] !== sourceRef) {
      failoverRef.current = undefined;
      setFailoverState(undefined);
      setFailoverReady(true);
      return;
    }
    let active = true;
    setFailoverReady(false);
    const storageKey = `rivune.playback-failover.v1:${item.titleId || item.id}:${sourceRef}`;
    void (async () => {
      const existingID = sessionStorage.getItem(storageKey);
      if (existingID) {
        try {
          const existing = await api.playbackFailover(existingID);
          if (existing.status === "active") {
            if (!active) return;
            failoverRef.current = existing;
            setFailoverState(existing);
            setActiveSourceRef(existing.currentSourceRef);
            return;
          }
        } catch {
          sessionStorage.removeItem(storageKey);
        }
      }
      const created = await api.createPlaybackFailover({ candidateSourceRefs: candidates, selectedSourceRef: sourceRef });
      if (!active) return;
      failoverRef.current = created;
      setFailoverState(created);
      sessionStorage.setItem(storageKey, created.id);
    })().catch(() => {
      if (!active) return;
      failoverRef.current = undefined;
      setFailoverState(undefined);
    }).finally(() => { if (active) setFailoverReady(true); });
    return () => { active = false; };
  }, [item.id, item.titleId, sourceRef]);

  useEffect(() => {
    setProgressReady(false);
    let active = true;
    if (item.mediaType !== "movie" && item.mediaType !== "episode") {
      setProgressReady(true);
      return () => { active = false; };
    }
    void (item.titleId ? Promise.resolve(item.titleId) : resolveMediaTitle(item)).then(async (titleID) => {
      if (!active) return;
      progressVersionRef.current = 0;
      titleIDRef.current = titleID;
      resumePositionRef.current = startSeconds;
      lastSavedPositionRef.current = 0;
      const progress = await api.progress(titleID);
      if (!active || !progress) return;
      progressVersionRef.current = progress.version;
      lastSavedPositionRef.current = progress.positionSeconds;
      const video = videoRef.current;
      if (video && video.readyState >= HTMLMediaElement.HAVE_METADATA) resumePlayback(video);
    }).catch(() => undefined).finally(() => { if (active) setProgressReady(true); });
    return () => { active = false; };
  }, [item, startSeconds]);

  useEffect(() => {
    const imdbID = item.externalIds?.imdb?.trim() ?? "";
    const season = item.seasonNumber ?? 0;
    const episode = item.episodeNumber ?? 0;
    const controller = new AbortController();
    let active = true;
    setMarkers([]);
    setDismissedMarkers(new Set());
    if (item.mediaType === "episode" && imdbID && Number.isInteger(season) && season > 0 && Number.isInteger(episode) && episode > 0) {
      void api.playbackMarkers(imdbID, season, episode, controller.signal)
        .then((response) => {
          if (!active || !Array.isArray(response.markers)) return;
          setMarkers(response.markers.filter((marker) => validPlaybackMarker(marker, 0)));
        })
        .catch(() => undefined);
    }
    return () => {
      active = false;
      controller.abort();
    };
  }, [item.episodeNumber, item.externalIds?.imdb, item.id, item.mediaType, item.seasonNumber]);

  useEffect(() => {
    if (!progressReady || !failoverReady) return;
    let active = true;
    setLoading(true);
    setPhase(subtitleHandoffRef.current || sessionIDRef.current ? "recovering" : "preparing");
    setError("");
    setPlaybackBlocked(false);
    setPanel(null);
    setSelected(0);
    setCurrentTime(0);
    setVideoDuration(0);
    setPlaybackStart(undefined);
    setSeekPreview(null);
    const captionPreference = document.documentElement.dataset.captions;
    void api.resolvePlayback({
      sourceRef: activeSourceRef,
      startSeconds: Math.max(0, Math.floor(resumePositionRef.current)),
      titleId: item.titleId,
      preferredAudioTrack,
      preferredSubtitleId: captionPreference === "off" ? "none" : subtitlePreferenceRef.current,
    }).then((session) => {
      if (!active) {
        void api.stopPlayback(session.id).catch(() => undefined);
        return;
      }
      const previousSessionID = sessionIDRef.current;
      sessionIDRef.current = session.id;
      if (previousSessionID) void api.stopPlayback(previousSessionID).catch(() => undefined);
      const resolvedSources = session.sources ?? [];
      const resolvedSubtitles = session.subtitles ?? [];
      setStreams(resolvedSources);
      setSubtitles(resolvedSubtitles);
      setSelectedAudioTrack(session.selectedAudioTrack);
      const requestedSubtitleID = document.documentElement.dataset.captions === "off" ? "none" : session.selectedSubtitleId || "none";
      const automaticSubtitle = document.documentElement.dataset.captions === "on" && requestedSubtitleID === "none"
        ? resolvedSubtitles.find((subtitle) => playbackSubtitleAvailable(subtitle) && subtitle.delivery !== "burn" && subtitle.default)
          ?? resolvedSubtitles.find((subtitle) => playbackSubtitleAvailable(subtitle) && subtitle.delivery !== "burn")
        : undefined;
      const resolvedSubtitleID = automaticSubtitle?.id ?? (resolvedSubtitles.some((subtitle) => subtitle.id === requestedSubtitleID && playbackSubtitleAvailable(subtitle)) ? requestedSubtitleID : "none");
      subtitlePreferenceRef.current = resolvedSubtitleID;
      setSelectedSubtitleID(resolvedSubtitleID);
      const compatible = resolvedSources.filter(playerSourceAvailable);
      const selectedIndex = compatible.findIndex((source) => source.id === session.selectedSourceId);
      setSelected(selectedIndex < 0 ? 0 : selectedIndex);
      setPhase("ready");
      if (coordinationOperationRef.current && !coordinationReportedRef.current) {
        coordinationReportedRef.current = true;
        publishPlaybackCommandResult({ operationId: coordinationOperationRef.current, status: "applied", code: "applied" });
      }
    }).catch((cause) => {
      if (!active) return;
      stopCurrentSession();
      if (coordinationOperationRef.current && !coordinationReportedRef.current) {
        coordinationReportedRef.current = true;
        publishPlaybackCommandResult({ operationId: coordinationOperationRef.current, status: "failed", code: "execution_failed" });
      }
      if (cause instanceof APIError && cause.code === "playback_source_expired") {
        onSourceExpired();
        return;
      }
      if (cause instanceof APIError && cause.code === "playback_source_unsupported") {
        setError(t("player.error.conversionUnsupported"));
        setPhase("failed");
        return;
      }
      const policyMessage = playbackPolicyErrorMessage(cause);
      if (policyMessage) {
        setError(policyMessage);
        setPhase("failed");
        return;
      }
      setError(notifyError(cause, t("player.error.sourcesUnavailable"), t("player.error.unavailableTitle")));
      setPhase("failed");
    }).finally(() => {
      if (!active) return;
      subtitleHandoffRef.current = false;
      setLoading(false);
    });
    return () => { active = false; };
  }, [activeSourceRef, failoverReady, item.id, item.titleId, onSourceExpired, preferredAudioTrack, progressReady, retryVersion, subtitleResolveGeneration]);
  useEffect(() => {
    const stopListening = listenForPlaybackCommands((command: PlaybackCommand) => {
      const video = videoRef.current;
      if (!video) return "invalid_state";
      if (command.command === "play") {
        void video.play();
        return "applied";
      }
      if (command.command === "pause") {
        video.pause();
        return "applied";
      }
      if (command.command === "seek" && command.positionMilliseconds !== undefined) {
        commitSeek(command.positionMilliseconds / 1_000, true);
        return "applied";
      }
      if (command.command === "stop") {
        closePlayer();
        return "applied";
      }
      return "unsupported";
    });
    return stopListening;
  });
  useEffect(() => {
    if (panel !== "remote") return;
    let active = true;
    const refresh = () => void api.playbackDevices().then(({ devices }) => {
      if (!active) return;
      const targets = devices.filter((device) => !device.current && device.capabilities.includes("remote-control"));
      setCoordinationDevices(targets);
      setCoordinationTarget((current) => targets.some((device) => device.sessionId === current) ? current : targets[0]?.sessionId ?? "");
    }).catch(() => { if (active) setCoordinationError(t("player.remote.loadFailed")); });
    refresh();
    const timer = window.setInterval(refresh, 5_000);
    return () => { active = false; window.clearInterval(timer); };
  }, [panel]);

  async function sendRemoteCommand(command: PlaybackCommand["command"], positionMilliseconds?: number) {
    const target = coordinationDevices.find((device) => device.sessionId === coordinationTarget);
    if (!target || coordinationPending) return;
    setCoordinationPending(true);
    setCoordinationError("");
    try {
      const operationId = crypto.randomUUID();
      const itemState = playbackItem(item);
      const sent = await api.sendPlaybackCommand(target.sessionId, { operationId, command, targetRevision: target.revision, ...(positionMilliseconds !== undefined ? { positionMilliseconds } : {}), ...(command === "load" ? { mode: coordinationMode, ...(itemState ? { item: itemState } : {}) } : {}) });
      if (command !== "load") return;
      let result = sent;
      while (result.status === "pending" && Date.now() < Date.parse(result.expiresAt)) {
        await new Promise<void>((resolve) => window.setTimeout(resolve, 1_000));
        result = await api.outgoingPlaybackCommand(operationId);
      }
      if (result.status !== "applied") throw new Error(result.resultCode ?? "expired");
      if (coordinationMode === "handoff") closePlayer();
    } catch {
      setCoordinationError(t("player.remote.commandFailed"));
    } finally { setCoordinationPending(false); }
  }

  useEffect(() => {
    const itemState = playbackItem(item) ?? undefined;
    publishPlaybackState({ status: phase === "playing" ? "playing" : phase === "ended" ? "ended" : phase === "failed" ? "idle" : "paused", item: itemState, positionMilliseconds: Math.max(0, Math.round(currentTime * 1_000)), durationMilliseconds: Math.max(0, Math.round(playbackDurationRef.current * 1_000)) });
    return () => publishPlaybackState({ status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 });
  }, [currentTime, item, phase]);

  const playable = useMemo(() => streams.filter(playerSourceAvailable), [streams]);
  const stream = playable[selected];
  const inspectedDuration = stream?.media?.durationSeconds ?? 0;
  const playbackDuration = inspectedDuration > 0 ? inspectedDuration : videoDuration;
  playbackDurationRef.current = playbackDuration;
  streamProtocolRef.current = stream?.protocol ?? "";
  const audioTracks = stream?.media?.audioTracks ?? [];
  const selectableSubtitles = subtitles.filter(playbackSubtitleAvailable);
  const selectedSubtitle = selectableSubtitles.find((subtitle) => subtitle.id === selectedSubtitleID);
  const selectedExternalSubtitleURL = selectedSubtitle?.delivery === "external" ? selectedSubtitle.url!.trim() : "";
  const transportTime = seekPreview ?? currentTime;
  const progressPercent = playbackDuration > 0 ? Math.min(100, Math.max(0, transportTime / playbackDuration * 100)) : 0;
  const remainingSeconds = playbackDuration > 0 ? Math.max(0, playbackDuration - currentTime) : Number.POSITIVE_INFINITY;
  const showNextEpisode = Boolean(onEnded && (phase === "ended" || remainingSeconds <= 30));
  const customTransport = Boolean(stream?.url && stream.mode !== "external");
  const toneMapped = Boolean(stream?.media?.hdrFormat && stream.media.hdrFormat !== "sdr" && stream.mode === "transcode");
  const modeLabel = playerModeLabel(stream?.mode, toneMapped);
  const externalPlaybackURL = stream?.mode === "external"
    ? stream.infoHash ? `magnet:?xt=urn:btih:${encodeURIComponent(stream.infoHash)}` : stream.url ?? ""
    : "";
  const activeMarker = customTransport && playbackDuration > 0
    ? markers.find((marker) => validPlaybackMarker(marker, playbackDuration) && !dismissedMarkers.has(marker.type) && currentTime >= marker.startSeconds && currentTime < marker.endSeconds)
    : undefined;


  useEffect(() => {
    const ended = markers.filter((marker) => currentTime >= marker.endSeconds && !dismissedMarkers.has(marker.type));
    if (ended.length === 0) return;
    setDismissedMarkers((current) => {
      const next = new Set(current);
      for (const marker of ended) next.add(marker.type);
      return next;
    });
  }, [currentTime, dismissedMarkers, markers]);

  useEffect(() => {
    const player = playerRef.current;
    const video = videoRef.current as WebKitFullscreenVideo | null;
    const standardSupported = document.fullscreenEnabled !== false && typeof player?.requestFullscreen === "function" && typeof document.exitFullscreen === "function";
    const webkitSupported = typeof video?.webkitEnterFullscreen === "function";
    setFullscreenSupported(standardSupported || webkitSupported);

    function handleStandardFullscreenChange() {
      const nextKind: PlayerFullscreenKind = document.fullscreenElement === player ? "standard" : video?.webkitDisplayingFullscreen ? "webkit" : "none";
      fullscreenKindRef.current = nextKind;
      setFullscreenKind(nextKind);
      if (nextKind === "none") unlockPlayerOrientation();
    }

    function handleStandardFullscreenError() {
      if (document.fullscreenElement === player) return;
      fullscreenKindRef.current = "none";
      setFullscreenKind("none");
      unlockPlayerOrientation();
    }

    function handleWebKitFullscreenBegin() {
      fullscreenKindRef.current = "webkit";
      setFullscreenKind("webkit");
    }

    function handleWebKitFullscreenEnd() {
      fullscreenKindRef.current = "none";
      setFullscreenKind("none");
      unlockPlayerOrientation();
    }

    const initialKind: PlayerFullscreenKind = document.fullscreenElement === player ? "standard" : video?.webkitDisplayingFullscreen ? "webkit" : "none";
    fullscreenKindRef.current = initialKind;
    setFullscreenKind(initialKind);
    document.addEventListener("fullscreenchange", handleStandardFullscreenChange);
    document.addEventListener("fullscreenerror", handleStandardFullscreenError);
    video?.addEventListener("webkitbeginfullscreen", handleWebKitFullscreenBegin);
    video?.addEventListener("webkitendfullscreen", handleWebKitFullscreenEnd);
    return () => {
      document.removeEventListener("fullscreenchange", handleStandardFullscreenChange);
      document.removeEventListener("fullscreenerror", handleStandardFullscreenError);
      video?.removeEventListener("webkitbeginfullscreen", handleWebKitFullscreenBegin);
      video?.removeEventListener("webkitendfullscreen", handleWebKitFullscreenEnd);
    };
  }, [playbackGeneration, stream?.id, stream?.url]);

  useEffect(() => {
    const video = videoRef.current;
    if (!progressReady || !video || !stream?.url || stream.mode === "external") return;
    const playbackURL = new URL(stream.url, window.location.origin);
    const processed = stream.mode !== "direct";
    const requestedTimestamp = Math.max(0, Math.floor(playbackStart ?? resumePositionRef.current));
    const mediaPosition = processed && stream.mediaTimeline === "absolute" ? requestedTimestamp : 0;
    const playbackOffset = processed && stream.mediaTimeline !== "absolute" ? requestedTimestamp : 0;
    playbackOffsetRef.current = playbackOffset;
    setCurrentTime(requestedTimestamp);
    setSeekPreview(null);
    setPlaybackBlocked(false);
    setPhase("preparing");
    pausedAtRef.current = 0;
    if (processed && requestedTimestamp > 0) playbackURL.searchParams.set("start", String(requestedTimestamp));
    const sourceURL = processed ? `${playbackURL.pathname}${playbackURL.search}` : stream.url;
    let disposed = false;
    let destroyHLS = () => {};
    let seekTransport: ((position: number) => void) | undefined;
    const isHLS = stream.protocol === "hls";
    const startPlayback = () => {
      void video.play().catch((cause: unknown) => {
        if (disposed || cause instanceof DOMException && cause.name === "AbortError") return;
        setPaused(true);
        if (cause instanceof DOMException && cause.name === "NotAllowedError") {
          setPlaybackBlocked(true);
          setPhase("paused");
          return;
        }
        setError(notifyError(cause, t("player.error.browserStartFailed"), t("player.error.unavailableTitle")));
        setPhase("failed");
      });
    };
    const failPlayback = (message: string, failure?: PlaybackFailoverError) => {
      if (failure && failoverRef.current?.status === "active") {
        void requestSourceFailover(failure, message);
        return;
      }
      setPaused(true);
      setError(notifyErrorMessage(message, t("player.error.unavailableTitle")));
      setPhase("failed");
    };
    const handleMediaError = (event: Event) => {
      if (event.target instanceof HTMLTrackElement) return;
      failPlayback(t("player.error.sourcePlayFailed"), "source_failed");
    };

    video.volume = volume;
    video.muted = muted;
    video.playbackRate = playbackRate;
    if (!isHLS) {
      if (processed) {
        failPlayback(t("player.error.hlsUnsupported"));
      } else {
        video.addEventListener("error", handleMediaError);
        video.src = sourceURL;
        startPlayback();
      }
    } else {
      void import("hls.js").then(({ default: Hls }) => {
        if (disposed) return;
        if (!Hls.isSupported()) {
          if (video.canPlayType("application/vnd.apple.mpegurl")) {
            video.addEventListener("error", handleMediaError);
            video.src = sourceURL;
            startPlayback();
          } else {
            failPlayback(t("player.error.hlsUnsupported"));
          }
          return;
        }
        const hls = new Hls({
          enableWorker: true,
          autoStartLoad: false,
          startPosition: mediaPosition,
          maxBufferLength: 30,
          backBufferLength: 30,
        });
        destroyHLS = () => hls.destroy();
        let mediaRecoveries = 0;
        seekTransport = (position: number) => {
          hls.stopLoad();
          mediaRecoveries = 0;
          video.currentTime = position;
          hls.startLoad(position);
        };
        seekTransportRef.current = seekTransport;
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
          setPhase("ready");
          hls.startLoad(mediaPosition);
          startPlayback();
        });
        hls.on(Hls.Events.FRAG_BUFFERED, () => {
          if (!video.paused) setPhase("playing");
        });
        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (disposed) return;
          if (!data.fatal) {
            if (String(data.details).toLowerCase().includes("stall")) setPhase("buffering");
            return;
          }
          setPhase("recovering");
          if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRecoveries < 2) {
            mediaRecoveries += 1;
            hls.recoverMediaError();
            return;
          }
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
            failPlayback(t("player.error.hlsStopped"), String(data.details).toLowerCase().includes("timeout") ? "source_timeout" : "source_failed");
            return;
          }
          failPlayback(t("player.error.hlsStopped"));
        });
        hls.loadSource(sourceURL);
        hls.attachMedia(video);
      }).catch((cause) => {
        if (!disposed) {
          setError(notifyError(cause, t("player.error.hlsPlayerLoadFailed"), t("player.error.unavailableTitle")));
          setPhase("failed");
        }
      });
    }

    return () => {
      disposed = true;
      video.removeEventListener("error", handleMediaError);
      destroyHLS();
      if (seekTransportRef.current === seekTransport) seekTransportRef.current = undefined;
      video.removeAttribute("src");
      video.load();
    };
  }, [playbackGeneration, progressReady, stream]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.volume = volume;
    video.muted = muted;
    video.playbackRate = playbackRate;
    try {
      localStorage.setItem(playerPreferencesKey, JSON.stringify({ volume, muted, rate: playbackRate }));
    } catch {
      // Playback preferences remain session-local when storage is unavailable.
    }
  }, [muted, playbackRate, stream, volume]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    Array.from(video.textTracks).forEach((track) => {
      track.mode = "showing";
    });
  }, [selectedSubtitleID, stream, subtitles]);

  useEffect(() => {
    const appRoot = document.getElementById("root");
    const player = playerRef.current;
    if (!appRoot || !player) return;
    const wasInert = appRoot.inert;
    const previousAriaHidden = appRoot.getAttribute("aria-hidden");
    const previousBodyOverflow = document.body.style.overflow;
    const previousRootOverflow = document.documentElement.style.overflow;
    player.focus({ preventScroll: true });
    appRoot.inert = true;
    appRoot.setAttribute("aria-hidden", "true");
    document.body.style.overflow = "hidden";
    document.documentElement.style.overflow = "hidden";
    return () => {
      appRoot.inert = wasInert;
      if (previousAriaHidden === null) appRoot.removeAttribute("aria-hidden");
      else appRoot.setAttribute("aria-hidden", previousAriaHidden);
      document.body.style.overflow = previousBodyOverflow;
      document.documentElement.style.overflow = previousRootOverflow;
    };
  }, []);

  useEffect(() => {
    const player = playerRef.current;
    if (!player) return;
    const containFocus = (event: FocusEvent) => {
      const scope: HTMLElement = panelRef.current ?? player;
      if (event.target instanceof Node && scope.contains(event.target)) return;
      (focusableElements(scope)[0] ?? player).focus({ preventScroll: true });
    };
    document.addEventListener("focusin", containFocus, true);
    return () => document.removeEventListener("focusin", containFocus, true);
  }, [panel]);

  useEffect(() => {
    if (!stream) return;
    const frame = window.requestAnimationFrame(() => {
      const player = playerRef.current;
      if (!player || panelRef.current) return;
      const primaryControl = player.querySelector<HTMLElement>(".player__external-action")
        ?? player.querySelector<HTMLElement>(".player__control-primary")
        ?? player.querySelector<HTMLElement>("[data-player-action='close']");
      primaryControl?.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [stream?.id, stream?.infoHash, stream?.url]);

  useEffect(() => {
    const previousPanel = previousPanelRef.current;
    previousPanelRef.current = panel;
    const frame = window.requestAnimationFrame(() => {
      if (panel) {
        const panelElement = panelRef.current;
        const selectedOption = panelElement?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]');
        const firstControl = panelElement?.querySelector<HTMLElement>("[data-player-control], button:not(:disabled), [role='combobox']");
        (selectedOption ?? firstControl)?.focus({ preventScroll: true });
        return;
      }
      if (previousPanel && panelTriggerRef.current?.isConnected) panelTriggerRef.current.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [panel]);

  useEffect(() => {
    if (phase !== "failed") return;
    const frame = window.requestAnimationFrame(() => playerRef.current?.querySelector<HTMLElement>(".player__failure [data-player-control]")?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, [phase]);

  useEffect(() => {
    window.clearTimeout(controlsTimerRef.current);
    setControlsVisible(true);
    if (phase === "playing" && panel === null) {
      controlsTimerRef.current = window.setTimeout(() => setControlsVisible(false), 3200);
    }
    return () => window.clearTimeout(controlsTimerRef.current);
  }, [panel, phase]);

  useEffect(() => {
    if (phase !== "buffering" && phase !== "recovering") return;
    const timeout = window.setTimeout(() => {
      videoRef.current?.pause();
      setError(notifyErrorMessage(t("player.error.recoveryTimeout"), t("player.error.recoveryTimeoutTitle")));
      setPhase("failed");
    }, 30_000);
    return () => window.clearTimeout(timeout);
  }, [phase]);

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      const target = event.target;
      const interactive = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || (target instanceof HTMLElement && target.getAttribute("role") === "combobox");
      if (event.key === "Escape" || event.key === "BrowserBack" || event.key === "GoBack") {
        event.preventDefault();
        if (panel) closePanel();
        else if (fullscreenKind !== "none" || document.fullscreenElement === playerRef.current) void exitPlayerFullscreen();
        else closePlayer();
        return;
      }
      if (event.key === "Tab") {
        const player = playerRef.current;
        if (!player) return;
        const scope = panelRef.current ?? player;
        const candidates = focusableElements(scope);
        const activeIndex = document.activeElement instanceof HTMLElement ? candidates.indexOf(document.activeElement) : -1;
        const nextIndex = event.shiftKey ? candidates.length - 1 : 0;
        if (candidates.length === 0) {
          event.preventDefault();
          player.focus({ preventScroll: true });
        } else if (activeIndex < 0 || !event.shiftKey && activeIndex === candidates.length - 1 || event.shiftKey && activeIndex === 0) {
          event.preventDefault();
          candidates[nextIndex]?.focus({ preventScroll: true });
        }
        return;
      }
      if (interactive) return;
      if (target instanceof HTMLElement && target.hasAttribute("data-player-control") && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        movePlayerFocus(event.key);
        return;
      }
      switch (event.key.toLowerCase()) {
        case " ":
        case "k":
        case "mediaplaypause":
          event.preventDefault();
          togglePlayback();
          break;
        case "arrowleft":
        case "j":
        case "mediarewind":
          event.preventDefault();
          seekBy(-10);
          break;
        case "arrowright":
        case "l":
        case "mediafastforward":
          event.preventDefault();
          seekBy(10);
          break;
        case "arrowup":
          event.preventDefault();
          changeVolume(volume + 0.05);
          break;
        case "arrowdown":
          event.preventDefault();
          changeVolume(volume - 0.05);
          break;
        case "m":
          event.preventDefault();
          toggleMute();
          break;
        case "f":
          event.preventDefault();
          void toggleFullscreen();
          break;
        case "p":
          event.preventDefault();
          void togglePictureInPicture();
          break;
        case "i":
          event.preventDefault();
          togglePanel("stats");
          break;
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [controlsVisible, fullscreenKind, panel, paused, phase, playbackDuration, stream, volume]);

  useEffect(() => () => {
    window.clearTimeout(controlsTimerRef.current);
    window.clearTimeout(clickTimerRef.current);
    void exitPlayerFullscreen(false, false);
    unlockPlayerOrientation();
    stopCurrentSession();
  }, []);

  function persistProgress(completed = false, positionOverride?: number) {
    const video = videoRef.current;
    const titleID = titleIDRef.current;
    if (!video || !titleID) return;
    const durationSeconds = playbackDurationRef.current > 0 ? playbackDurationRef.current : streamProtocolRef.current !== "hls" ? video.duration : Number.NaN;
    if (!Number.isFinite(durationSeconds) || durationSeconds <= 0) return;
    const positionSeconds = completed ? Math.floor(durationSeconds) : Math.floor(positionOverride ?? playbackOffsetRef.current + video.currentTime);
    if (!completed && positionSeconds <= 0) return;
    const next = { titleID, positionSeconds, durationSeconds: Math.floor(durationSeconds), completed, expectedVersion: progressVersionRef.current };
    const pending = pendingProgressRef.current.get(titleID);
    if (pending) {
      pendingProgressRef.current.set(titleID, coalesceProgressWrite(pending, next));
    } else if (progressRequestRef.current?.titleID === titleID) {
      pendingProgressRef.current.set(titleID, coalesceProgressWrite(progressRequestRef.current, next));
    } else {
      pendingProgressRef.current.set(titleID, next);
    }
    if (!progressRequestRef.current) void drainProgressWrites();
  }

  async function drainProgressWrites() {
    while (pendingProgressRef.current.size > 0) {
      const [pendingTitleID, pending] = pendingProgressRef.current.entries().next().value!;
      pendingProgressRef.current.delete(pendingTitleID);
      let next = pending;
      progressRequestRef.current = next;
      try {
        let progress: PlaybackProgress;
        try {
          progress = await api.updateProgress(next.titleID, {
            positionSeconds: next.positionSeconds,
            durationSeconds: next.durationSeconds,
            completed: next.completed,
            expectedVersion: next.expectedVersion,
          });
        } catch (cause) {
          if (!(cause instanceof APIError) || cause.status !== 409) throw cause;
          let current: PlaybackProgress | undefined;
          try {
            current = await api.progress(next.titleID);
            next.expectedVersion = current?.version ?? 0;
          } catch (refreshCause) {
            notifyError(refreshCause, t("player.progress.syncFailed"), t("player.progress.notSavedTitle"));
            continue;
          }
          if (current) {
            next = coalesceProgressWrite({
              titleID: next.titleID,
              positionSeconds: current.positionSeconds,
              durationSeconds: current.durationSeconds,
              completed: current.completed,
              expectedVersion: current.version,
            }, next);
          }
          const pending = pendingProgressRef.current.get(next.titleID);
          if (pending) {
            next = coalesceProgressWrite(next, pending);
            pendingProgressRef.current.delete(next.titleID);
            next.expectedVersion = current?.version ?? 0;
          }
          progressRequestRef.current = next;
          progress = await api.updateProgress(next.titleID, {
            positionSeconds: next.positionSeconds,
            durationSeconds: next.durationSeconds,
            completed: next.completed,
            expectedVersion: next.expectedVersion,
          });
        }
        if (titleIDRef.current === next.titleID) {
          progressVersionRef.current = progress.version;
          lastSavedPositionRef.current = next.positionSeconds;
        }
        const pending = pendingProgressRef.current.get(next.titleID);
        if (pending) pending.expectedVersion = progress.version;
      } catch (cause) {
        notifyError(cause, t("player.progress.saveFailed"), t("player.progress.notSavedTitle"));
      } finally {
        progressRequestRef.current = undefined;
      }
    }
  }

  function releaseCurrentSession(): Promise<void> {
    const sessionID = sessionIDRef.current;
    sessionIDRef.current = "";
    return sessionID ? api.stopPlayback(sessionID) : Promise.resolve();
  }

  function stopCurrentSession() {
    void releaseCurrentSession().catch(() => undefined);
  }

  async function requestSourceFailover(failure: PlaybackFailoverError, fallbackMessage: string) {
    const current = failoverRef.current;
    if (!current || current.status !== "active" || failoverPendingRef.current) {
      setPaused(true);
      setError(notifyErrorMessage(fallbackMessage, t("player.error.unavailableTitle")));
      setPhase("failed");
      return;
    }
    failoverPendingRef.current = true;
    const video = videoRef.current;
    const position = Math.max(0, Math.floor(video ? playbackOffsetRef.current + video.currentTime : currentTime));
    resumePositionRef.current = position;
    setPaused(true);
    setPhase("recovering");
    setFailoverNotice("");
    try {
      const next = await api.advancePlaybackFailover(current.id, { error: failure, positionSeconds: position, expectedRevision: current.revision });
      failoverRef.current = next;
      setFailoverState(next);
      if (next.status !== "active" || !next.currentSourceRef || next.currentSourceRef === activeSourceRef) {
        setError(notifyErrorMessage(t("player.failover.exhausted"), t("player.error.unavailableTitle")));
        setPhase("failed");
        return;
      }
      await releaseCurrentSession().catch(() => undefined);
      setError("");
      setPlaybackStart(position);
      setFailoverNotice(t("player.failover.switched", { position: formatPlaybackTime(position), source: next.currentPosition + 1 }));
      setActiveSourceRef(next.currentSourceRef);
    } catch (cause) {
      setError(notifyError(cause, fallbackMessage, t("player.error.unavailableTitle")));
      setPhase("failed");
    } finally {
      failoverPendingRef.current = false;
    }
  }

  async function cancelSourceFailover() {
    const current = failoverRef.current;
    if (!current || current.status !== "active") return;
    try {
      await api.cancelPlaybackFailover(current.id);
      const cancelled = { ...current, status: "cancelled" as const, revision: current.revision + 1 };
      failoverRef.current = cancelled;
      setFailoverState(cancelled);
      setFailoverNotice(t("player.failover.cancelled"));
    } catch (cause) {
      notifyError(cause, t("player.failover.cancelFailed"), t("player.error.unavailableTitle"));
    }
  }

  function closePlayer() {
    void persistProgress();
    void exitPlayerFullscreen(false, false);
    unlockPlayerOrientation();
    stopCurrentSession();
    onClose();
  }

  function resumePlayback(video: HTMLVideoElement) {
    video.volume = volume;
    video.muted = muted;
    video.playbackRate = playbackRate;
    if (Number.isFinite(video.duration)) setVideoDuration(video.duration);
    const position = resumePositionRef.current;
    const duration = playbackDurationRef.current;
    if (playbackOffsetRef.current > 0) return;
    if (position <= 0 || duration > 0 && position >= duration - 10) return;
    video.currentTime = position;
    void persistProgress(false, position);
  }

  function updatePlayerStats(video: HTMLVideoElement) {
    const absolutePosition = playbackOffsetRef.current + video.currentTime;
    let bufferedAhead = 0;
    for (let index = 0; index < video.buffered.length; index += 1) {
      const start = playbackOffsetRef.current + video.buffered.start(index);
      const end = playbackOffsetRef.current + video.buffered.end(index);
      if (absolutePosition >= start && absolutePosition <= end) bufferedAhead = Math.max(0, end - absolutePosition);
    }
    const quality = typeof video.getVideoPlaybackQuality === "function" ? video.getVideoPlaybackQuality() : undefined;
    setStats({
      bufferedAhead,
      droppedFrames: quality?.droppedVideoFrames ?? 0,
      totalFrames: quality?.totalVideoFrames ?? 0,
      width: video.videoWidth,
      height: video.videoHeight,
    });
  }

  function trackPlayback(video: HTMLVideoElement) {
    const position = playbackOffsetRef.current + video.currentTime;
    if (seekPreview === null) setCurrentTime(position);
    if (panel === "stats") updatePlayerStats(video);
    if (Math.floor(position) - lastSavedPositionRef.current >= 15) void persistProgress(false, position);
  }

  function revealControls() {
    window.clearTimeout(controlsTimerRef.current);
    setControlsVisible(true);
    if (phase === "playing" && panel === null) {
      controlsTimerRef.current = window.setTimeout(() => setControlsVisible(false), 3200);
    }
  }

  function closePanel() {
    setPanel(null);
  }

  function togglePanel(nextPanel: Exclude<PlayerPanel, null>) {
    revealControls();
    if (panel === nextPanel) {
      closePanel();
      return;
    }
    const active = document.activeElement;
    panelTriggerRef.current = active instanceof HTMLElement && playerRef.current?.contains(active) ? active : null;
    setPanel(nextPanel);
    if (nextPanel === "stats" && videoRef.current) updatePlayerStats(videoRef.current);
  }

  function togglePlayback() {
    const video = videoRef.current;
    if (!video || phase === "failed" || phase === "preparing") return;
    revealControls();
    if (!video.paused) {
      video.pause();
      return;
    }
    setPlaybackBlocked(false);
    void video.play().catch((cause: unknown) => {
      if (cause instanceof DOMException && cause.name === "AbortError") return;
      setPaused(true);
      if (cause instanceof DOMException && cause.name === "NotAllowedError") {
        setPlaybackBlocked(true);
        setPhase("paused");
        return;
      }
      setError(notifyError(cause, t("player.error.browserStartFailed"), t("player.error.unavailableTitle")));
      setPhase("failed");
    });
  }

  function commitSeek(rawPosition: number, preserveFraction = false) {
    const duration = playbackDurationRef.current;
    const normalized = preserveFraction ? rawPosition : Math.floor(rawPosition);
    const target = duration > 0 ? Math.min(duration, Math.max(0, normalized)) : Math.max(0, normalized);
    const video = videoRef.current;
    resumePositionRef.current = target;
    setSeekPreview(null);
    setCurrentTime(target);
    if (video && (stream?.mode === "direct" || stream?.mediaTimeline === "absolute")) {
      playbackOffsetRef.current = 0;
      if (stream?.mediaTimeline === "absolute" && seekTransportRef.current) seekTransportRef.current(target);
      else video.currentTime = target;
    } else {
      playbackOffsetRef.current = target;
      setPlaybackStart(target);
      setPlaybackGeneration((generation) => generation + 1);
      setPhase("recovering");
    }
    void persistProgress(false, target);
  }

  function seekBy(seconds: number) {
    const video = videoRef.current;
    const position = video ? playbackOffsetRef.current + video.currentTime : currentTime;
    commitSeek(position + seconds);
    setSeekFeedback({ seconds, id: Date.now() });
    revealControls();
  }

  function skipMarker(marker: PlaybackMarker) {
    if (!validPlaybackMarker(marker, playbackDurationRef.current) || dismissedMarkers.has(marker.type)) return;
    setDismissedMarkers((current) => new Set(current).add(marker.type));
    commitSeek(stream?.mode === "direct" ? marker.endSeconds : Math.ceil(marker.endSeconds), stream?.mode === "direct");
    revealControls();
  }

  function changeVolume(nextVolume: number) {
    const normalized = Math.min(1, Math.max(0, nextVolume));
    const video = videoRef.current;
    if (video) {
      video.volume = normalized;
      video.muted = normalized === 0;
    }
    setVolume(normalized);
    setMuted(normalized === 0);
    revealControls();
  }

  function toggleMute() {
    const nextMuted = !muted;
    const video = videoRef.current;
    if (video) video.muted = nextMuted;
    setMuted(nextMuted);
    revealControls();
  }

  function changePlaybackRate(rate: number) {
    const video = videoRef.current;
    if (video) video.playbackRate = rate;
    setPlaybackRate(rate);
    closePanel();
    revealControls();
  }

  function updateFullscreenKind(kind: PlayerFullscreenKind) {
    fullscreenKindRef.current = kind;
    setFullscreenKind(kind);
  }

  function unlockPlayerOrientation() {
    try {
      const orientation = (screen as Screen & { orientation?: PlayerScreenOrientation }).orientation;
      orientation?.unlock?.();
    } catch {
      // Orientation cleanup is best effort across browser implementations.
    }
  }

  async function exitPlayerFullscreen(reportFailure = true, synchronizeState = true) {
    const player = playerRef.current;
    const video = videoRef.current as WebKitFullscreenVideo | null;
    let exitCompleted = false;
    try {
      if (document.fullscreenElement && player?.contains(document.fullscreenElement) && typeof document.exitFullscreen === "function") {
        await document.exitFullscreen();
        exitCompleted = true;
      } else if ((fullscreenKindRef.current === "webkit" || video?.webkitDisplayingFullscreen) && typeof video?.webkitExitFullscreen === "function") {
        video.webkitExitFullscreen();
        exitCompleted = true;
      } else {
        exitCompleted = true;
      }
    } catch (cause) {
      if (reportFailure) notifyError(cause, t("player.fullscreen.closeFailed"), t("player.fullscreen.unavailableTitle"));
    } finally {
      if (!synchronizeState) {
        unlockPlayerOrientation();
        return;
      }
      const standardActive = Boolean(document.fullscreenElement && player?.contains(document.fullscreenElement));
      const webkitActive = Boolean(video?.webkitDisplayingFullscreen || !exitCompleted && fullscreenKindRef.current === "webkit");
      if (!standardActive && !webkitActive) {
        updateFullscreenKind("none");
        unlockPlayerOrientation();
      }
    }
  }

  async function toggleFullscreen() {
    const player = playerRef.current;
    const video = videoRef.current as WebKitFullscreenVideo | null;
    if (fullscreenKindRef.current !== "none" || document.fullscreenElement === player || video?.webkitDisplayingFullscreen) {
      await exitPlayerFullscreen();
      return;
    }

    const standardSupported = document.fullscreenEnabled !== false && typeof player?.requestFullscreen === "function" && typeof document.exitFullscreen === "function";
    if (standardSupported && player) {
      try {
        await player.requestFullscreen();
        updateFullscreenKind("standard");
        const orientation = (screen as Screen & { orientation?: PlayerScreenOrientation }).orientation;
        if (typeof orientation?.lock === "function") {
          try {
            await orientation.lock("landscape");
            if (document.fullscreenElement !== player) unlockPlayerOrientation();
          } catch {
            // Fullscreen remains active when orientation locking is unavailable or denied.
          }
        }
        return;
      } catch (cause) {
        unlockPlayerOrientation();
        notifyError(cause, t("player.fullscreen.openFailed"), t("player.fullscreen.unavailableTitle"));
        return;
      }
    }

    if (typeof video?.webkitEnterFullscreen === "function") {
      try {
        video.webkitEnterFullscreen();
        updateFullscreenKind("webkit");
        return;
      } catch (cause) {
        notifyError(cause, t("player.fullscreen.openFailed"), t("player.fullscreen.unavailableTitle"));
        return;
      }
    }

    unlockPlayerOrientation();
    notifyErrorMessage(t("player.fullscreen.unsupported"), t("player.fullscreen.unavailableTitle"));
  }

  async function togglePictureInPicture() {
    const video = videoRef.current;
    if (!video || !document.pictureInPictureEnabled) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await video.requestPictureInPicture();
    } catch (cause) {
      notifyError(cause, t("player.pictureInPicture.openFailed"), t("player.pictureInPicture.unavailableTitle"));
    }
  }

  function retryPlayback() {
    const video = videoRef.current;
    const position = video ? Math.floor(playbackOffsetRef.current + video.currentTime) : Math.floor(currentTime);
    resumePositionRef.current = position;
    stopCurrentSession();
    setError("");
    setPanel(null);
    setPhase("preparing");
    setLoading(true);
    setRetryVersion((version) => version + 1);
  }

  function selectSource(index: number) {
    const video = videoRef.current;
    const position = video ? Math.floor(playbackOffsetRef.current + video.currentTime) : Math.floor(currentTime);
    resumePositionRef.current = position;
    setPlaybackStart(position);
    setSelected(index);
    closePanel();
    setPhase("recovering");
    void persistProgress(false, position);
  }

  function selectSubtitle(subtitleID: string) {
    if (subtitleHandoffRef.current || subtitleID === selectedSubtitleID) {
      closePanel();
      return;
    }
    const nextSubtitle = selectableSubtitles.find((subtitle) => subtitle.id === subtitleID);
    if (subtitleID !== "none" && !nextSubtitle) {
      closePanel();
      return;
    }
    const changesBurnDelivery = selectedSubtitle?.delivery === "burn" || nextSubtitle?.delivery === "burn";
    subtitlePreferenceRef.current = subtitleID;
    closePanel();
    if (!changesBurnDelivery) {
      setSelectedSubtitleID(subtitleID);
      return;
    }
    const video = videoRef.current;
    const position = Math.floor(video ? playbackOffsetRef.current + video.currentTime : currentTime);
    resumePositionRef.current = position;
    setPlaybackStart(position);
    subtitleHandoffRef.current = true;
    setLoading(true);
    setError("");
    setPhase("recovering");
    void releaseCurrentSession().then(() => {
      setSubtitleResolveGeneration((generation) => generation + 1);
    }).catch((cause) => {
      subtitleHandoffRef.current = false;
      setLoading(false);
      setError(notifyError(cause, t("admin.activity.errors.stop"), t("admin.activity.errors.stopTitle")));
      setPhase("failed");
    });
  }

  function handleExternalSubtitleError(subtitleID: string) {
    if (subtitlePreferenceRef.current !== subtitleID) return;
    subtitlePreferenceRef.current = "none";
    setSelectedSubtitleID("none");
    notifyErrorMessage(t("common.status.unavailable"), t("player.panel.subtitles"));
  }

  function handlePlaybackReady(video: HTMLVideoElement) {
    if (video.paused) {
      setPaused(true);
      setPhase((current) => current === "failed" ? current : "ready");
    } else {
      setPhase("playing");
    }
  }

  function handlePlaybackStarted(video: HTMLVideoElement) {
    const pausedAt = pausedAtRef.current;
    pausedAtRef.current = 0;
    setPaused(false);
    setPlaybackBlocked(false);
    setPhase("playing");
    if (stream?.protocol !== "hls" || stream.mode === "direct" || pausedAt === 0 || Date.now() - pausedAt < 30_000) return;
    const position = Math.floor(playbackOffsetRef.current + video.currentTime);
    resumePositionRef.current = position;
    playbackOffsetRef.current = position;
    setPlaybackStart(position);
    setPlaybackGeneration((generation) => generation + 1);
    setPhase("recovering");
  }

  function handlePlaybackPaused(video: HTMLVideoElement) {
    if (video.ended) return;
    pausedAtRef.current = Date.now();
    setPaused(true);
    setPhase((current) => current === "failed" || current === "recovering" ? current : "paused");
    revealControls();
    void persistProgress();
  }

  function handlePlaybackEnded(video: HTMLVideoElement) {
    const position = playbackOffsetRef.current + video.currentTime;
    const duration = playbackDurationRef.current;
    if (duration > 0 && position < duration - 10) {
      void requestSourceFailover("ended_early", t("player.error.sourceEndedEarly"));
      return;
    }
    setPhase("ended");
    void persistProgress(true);
    if (autoplayNextEpisode) onEnded?.();
  }

  function playNextEpisode() {
    if (!onEnded) return;
    setPhase("ended");
    void persistProgress(true);
    stopCurrentSession();
    onEnded();
  }

  function handleSurfaceClick(event: ReactMouseEvent<HTMLDivElement>) {
    if (suppressNextClickRef.current) {
      suppressNextClickRef.current = false;
      return;
    }
    if (event.detail > 1) return;
    window.clearTimeout(clickTimerRef.current);
    clickTimerRef.current = window.setTimeout(togglePlayback, 220);
  }

  function handleSurfaceDoubleClick(event: ReactMouseEvent<HTMLDivElement>) {
    window.clearTimeout(clickTimerRef.current);
    const bounds = event.currentTarget.getBoundingClientRect();
    const position = (event.clientX - bounds.left) / bounds.width;
    if (position < 0.4) seekBy(-10);
    else if (position > 0.6) seekBy(10);
    else void toggleFullscreen();
  }

  function handleSurfacePointerUp(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.pointerType !== "touch") return;
    const now = Date.now();
    const previous = lastTouchRef.current;
    if (now - previous.time < 320 && Math.abs(event.clientX - previous.x) < 100) {
      suppressNextClickRef.current = true;
      window.clearTimeout(clickTimerRef.current);
      const bounds = event.currentTarget.getBoundingClientRect();
      const position = (event.clientX - bounds.left) / bounds.width;
      if (position < 0.5) seekBy(-10);
      else seekBy(10);
      lastTouchRef.current = { time: 0, x: 0 };
      return;
    }
    lastTouchRef.current = { time: now, x: event.clientX };
  }

  function movePlayerFocus(key: string) {
    const root = playerRef.current;
    if (!root) return;
    const candidates = Array.from(root.querySelectorAll<HTMLElement>("[data-player-control]"))
      .filter((candidate) => !candidate.hasAttribute("disabled") && candidate.offsetParent !== null);
    if (candidates.length === 0) return;
    const active = document.activeElement instanceof HTMLElement && root.contains(document.activeElement) ? document.activeElement : undefined;
    if (!active || !candidates.includes(active)) {
      candidates[0].focus();
      return;
    }

    const optionGroup = active.closest<HTMLElement>('[role="radiogroup"]');
    if (optionGroup) {
      const options = Array.from(optionGroup.querySelectorAll<HTMLElement>('[role="radio"]'))
        .filter((option) => !option.hasAttribute("disabled") && option.offsetParent !== null);
      const index = options.indexOf(active);
      if (index >= 0 && options.length > 0) {
        if (key === "Home" || key === "End") {
          options[key === "Home" ? 0 : options.length - 1]?.focus();
          return;
        }
        const grid = optionGroup.dataset.playerLayout === "grid";
        const columnCount = grid ? Number(optionGroup.dataset.playerColumns) || 1 : 1;
        const delta = grid
          ? key === "ArrowLeft" ? -1 : key === "ArrowRight" ? 1 : key === "ArrowUp" ? -columnCount : columnCount
          : key === "ArrowLeft" || key === "ArrowUp" ? -1 : 1;
        options[Math.min(options.length - 1, Math.max(0, index + delta))]?.focus();
        return;
      }
    }

    if (key === "Home" || key === "End") {
      candidates[key === "Home" ? 0 : candidates.length - 1]?.focus();
      return;
    }
    const origin = active.getBoundingClientRect();
    const originX = origin.left + origin.width / 2;
    const originY = origin.top + origin.height / 2;
    const vertical = key === "ArrowUp" || key === "ArrowDown";
    const direction = key === "ArrowLeft" || key === "ArrowUp" ? -1 : 1;
    const next = candidates.map((candidate) => {
      const bounds = candidate.getBoundingClientRect();
      const dx = bounds.left + bounds.width / 2 - originX;
      const dy = bounds.top + bounds.height / 2 - originY;
      const primary = vertical ? dy : dx;
      const cross = vertical ? dx : dy;
      return { candidate, primary, score: Math.abs(primary) + Math.abs(cross) * 2.5 };
    }).filter(({ primary }) => primary * direction > 4).sort((left, right) => left.score - right.score)[0]?.candidate;
    next?.focus();
  }

  const timelineStyle = { "--player-progress": `${progressPercent}%` } as CSSProperties;
  const panelTitle = panel === "sources" ? t("player.panel.sources")
    : panel === "audio" ? t("player.panel.audio")
      : panel === "subtitles" ? t("player.panel.subtitles")
        : panel === "speed" ? t("player.panel.speed")
          : panel === "stats" ? t("player.panel.diagnostics")
        : panel === "remote" ? t("player.panel.remote")
          : "";

  return createPortal(<div ref={playerRef} className={`player player--${phase}${controlsVisible ? " has-controls" : " controls-hidden"}`} role="dialog" aria-modal="true" aria-label={t("player.playingTitle", { title: item.title })} aria-busy={loading || phase === "preparing" || phase === "buffering" || phase === "recovering"} data-player-state={phase} data-controls-visible={controlsVisible} tabIndex={-1} onPointerMove={revealControls} onPointerDown={revealControls} onFocusCapture={revealControls}>
    <div className="player__surface" onClick={handleSurfaceClick} onDoubleClick={handleSurfaceDoubleClick} onPointerUp={handleSurfacePointerUp}>
      {stream?.ytId ? <iframe className="player__video" src={`https://www.youtube-nocookie.com/embed/${encodeURIComponent(stream.ytId)}?autoplay=1`} allow="autoplay; encrypted-media; picture-in-picture" allowFullScreen title={item.title} /> :
        stream?.mode !== "external" && stream?.url ? <video key={`${stream.id}:${stream.url}:${playbackStart ?? 0}:${playbackGeneration}`} ref={videoRef} className="player__video" controls={false} playsInline crossOrigin="anonymous"
          onCanPlay={(event) => handlePlaybackReady(event.currentTarget)}
          onLoadedMetadata={(event) => resumePlayback(event.currentTarget)}
          onDurationChange={(event) => { if (Number.isFinite(event.currentTarget.duration)) setVideoDuration(event.currentTarget.duration); }}
          onTimeUpdate={(event) => trackPlayback(event.currentTarget)}
          onPlay={(event) => handlePlaybackStarted(event.currentTarget)}
          onPlaying={(event) => { setPaused(false); setPhase("playing"); updatePlayerStats(event.currentTarget); }}
          onPause={(event) => handlePlaybackPaused(event.currentTarget)}
          onWaiting={() => setPhase((current) => current === "paused" ? current : "buffering")}
          onStalled={() => setPhase((current) => current === "paused" ? current : "buffering")}
          onEnded={(event) => handlePlaybackEnded(event.currentTarget)}>
          {selectedSubtitle && selectedExternalSubtitleURL && <track key={selectedSubtitle.id} src={selectedExternalSubtitleURL} srcLang={selectedSubtitle.language || "und"} label={(selectedSubtitle.language || t("common.fallback.unknown")).toUpperCase()} default onError={() => handleExternalSubtitleError(selectedSubtitle.id)} />}
        </video> : null}
    </div>

    <header className={`player__header${controlsVisible ? "" : " is-hidden"}`}>
      <div><small>{playerPhaseLabel(phase)} · {modeLabel}</small><strong>{item.title}</strong></div>
      <IconButton label={t("player.actions.close")} onClick={closePlayer} data-player-action="close" data-player-control><X /></IconButton>
    </header>

    {(loading || phase === "preparing") && <div className="player__loading" role="status" aria-live="polite" aria-busy="true"><span className="player__pulse"><LoaderCircle className="spin" /></span><strong>{t("player.loading.title")}</strong><p>{t("player.loading.description")}</p></div>}
    {(phase === "buffering" || phase === "recovering") && <div className="player__buffering" role="status" aria-live="polite" aria-busy="true"><LoaderCircle className="spin" /><span>{t(phase === "recovering" ? "player.status.recovering" : "player.status.buffering")}</span></div>}
    {failoverNotice && <div className="player__failover" role="status" aria-live="polite"><span>{failoverNotice}</span>{failoverState?.status === "active" && <Button variant="secondary" onClick={() => void cancelSourceFailover()} data-player-control>{t("common.cancel")}</Button>}</div>}
    {seekFeedback && <div key={seekFeedback.id} className={`player__seek-feedback ${seekFeedback.seconds < 0 ? "is-backward" : "is-forward"}`}>{seekFeedback.seconds < 0 ? <RotateCcw /> : <RotateCw />}<span>{t("player.seek.feedbackSeconds", { sign: seekFeedback.seconds > 0 ? "+" : "", seconds: seekFeedback.seconds })}</span></div>}
    {playbackBlocked && phase !== "failed" && <button type="button" className="player__start" onClick={togglePlayback} data-player-control><Play size={30} fill="currentColor" /><span>{t("player.actions.play")}</span></button>}
    {phase === "failed" && <div className="player__failure" role="alert"><ServerCrash size={34} /><strong>{t("player.error.unavailableTitle")}</strong><p>{error || t("player.error.streamPlayFailed")}</p><div><Button onClick={retryPlayback} data-player-control><RefreshCw size={17} /> {t("common.actions.retry")}</Button><Button variant="secondary" onClick={closePlayer} data-player-control>{t("common.actions.goBack")}</Button></div></div>}
    {!loading && playable.length === 0 && phase !== "failed" && <div className="player__failure" role="alert"><ServerCrash size={34} /><strong>{t("player.empty.title")}</strong><p>{error || t("player.empty.description")}</p><Button variant="secondary" onClick={closePlayer} data-player-control>{t("common.actions.goBack")}</Button></div>}
    {!loading && stream?.mode === "external" && externalPlaybackURL && <div className="player__external">
      <ExternalLink size={36} />
      <small>{t("player.external.eyebrow")}</small>
      <strong>{t("player.external.title")}</strong>
      <p>{t("player.external.description")}</p>
      <a className="player__external-action" href={externalPlaybackURL} target={externalPlaybackURL.startsWith("http") ? "_blank" : undefined} rel="noreferrer" data-player-control><ExternalLink size={18} /> {t("player.external.open")}</a>
      <button type="button" onClick={closePlayer} data-player-control>{t("player.external.chooseAnother")}</button>
    </div>}
    {showNextEpisode && <button type="button" className="player__next" onClick={playNextEpisode} data-player-control><span>{t("player.next.eyebrow")}</span><strong>{t("player.next.title")}</strong><small>{autoplayNextEpisode && phase !== "ended" ? t("player.next.startsIn", { seconds: Math.ceil(remainingSeconds) }) : t("player.next.play")}</small><SkipForward size={20} fill="currentColor" /></button>}
    {activeMarker && <button type="button" className={`player__skip-marker is-${activeMarker.type}`} onClick={() => skipMarker(activeMarker)} data-player-control><SkipForward size={20} /><span>{skipMarkerLabel(activeMarker.type)}</span></button>}

    {customTransport && <div className={`player__chrome${controlsVisible ? "" : " is-hidden"}`}>
      <div className="player__timeline-row">
        <span>{formatPlaybackTime(transportTime)}</span>
        <div className="player__timeline-wrap">
          {seekPreview !== null && <output style={{ left: `${progressPercent}%` }}>{formatPlaybackTime(seekPreview)}</output>}
          <input className="player__timeline" style={timelineStyle} type="range" aria-label={t("player.timeline.positionLabel")} min={0} max={Math.max(1, Math.floor(playbackDuration))} step={1} value={Math.min(playbackDuration || 1, Math.max(0, transportTime))}
            onChange={(event) => setSeekPreview(Number(event.target.value))}
            onPointerUp={(event) => commitSeek(Number(event.currentTarget.value))}
            onKeyUp={(event) => { if (["ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(event.key)) commitSeek(Number(event.currentTarget.value)); }}
            data-player-control />
        </div>
        <span>{formatPlaybackTime(playbackDuration)}</span>
      </div>
      <div className="player__controls">
        <div className="player__controls-group">
          <button type="button" className="player__control-primary" aria-label={t(paused ? "player.actions.play" : "player.actions.pause")} aria-pressed={!paused} onClick={togglePlayback} data-player-action="playback" data-player-control>{paused ? <Play size={20} fill="currentColor" /> : <Pause size={20} fill="currentColor" />}</button>
          <button type="button" aria-label={t("player.seek.back10")} onClick={() => seekBy(-10)} data-player-action="seek-backward" data-player-control><RotateCcw size={19} /><small>10</small></button>
          <button type="button" aria-label={t("player.seek.forward10")} onClick={() => seekBy(10)} data-player-action="seek-forward" data-player-control><RotateCw size={19} /><small>10</small></button>
          <button type="button" aria-label={t(muted ? "player.volume.unmute" : "player.volume.mute")} aria-pressed={muted} onClick={toggleMute} data-player-action="mute" data-player-control>{muted ? <VolumeX size={19} /> : <Volume2 size={19} />}</button>
          <input className="player__volume" type="range" aria-label={t("player.volume.label")} min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={(event) => changeVolume(Number(event.target.value))} data-player-action="volume" data-player-control />
        </div>
        <div className="player__mode"><span>{modeLabel}</span><small>{stream?.media?.videoTracks[0]?.height ? `${stream.media.videoTracks[0].height}p` : stream?.protocol?.toUpperCase()}</small></div>
        <div className="player__controls-group player__controls-group--right">
          {playable.length > 1 && <button type="button" aria-label={t("player.panel.sources")} aria-controls="player-panel-sources" aria-expanded={panel === "sources"} aria-haspopup="dialog" className={panel === "sources" ? "is-active" : ""} onClick={() => togglePanel("sources")} data-player-action="sources" data-player-control><Settings2 size={19} /></button>}
          {audioTracks.length > 1 && <button type="button" aria-label={t("player.panel.audio")} aria-controls="player-panel-audio" aria-expanded={panel === "audio"} aria-haspopup="dialog" className={panel === "audio" ? "is-active" : ""} onClick={() => togglePanel("audio")} data-player-action="audio" data-player-control><AudioLines size={19} /></button>}
          {selectableSubtitles.length > 0 && <button type="button" aria-label={t("player.panel.subtitles")} aria-controls="player-panel-subtitles" aria-expanded={panel === "subtitles"} aria-haspopup="dialog" className={panel === "subtitles" ? "is-active" : ""} onClick={() => togglePanel("subtitles")} data-player-action="subtitles" data-player-control><Captions size={19} /></button>}
          <button type="button" aria-label={t("player.speed.currentLabel", { rate: playbackRate })} aria-controls="player-panel-speed" aria-expanded={panel === "speed"} aria-haspopup="dialog" className={panel === "speed" ? "is-active" : ""} onClick={() => togglePanel("speed")} data-player-action="speed" data-player-control><Gauge size={19} /><small>{playbackRate}×</small></button>
          <button type="button" aria-label={t("player.panel.diagnostics")} aria-controls="player-panel-stats" aria-expanded={panel === "stats"} aria-haspopup="dialog" className={panel === "stats" ? "is-active" : ""} onClick={() => togglePanel("stats")} data-player-action="diagnostics" data-player-control><Info size={19} /></button>
          {document.pictureInPictureEnabled && <button type="button" aria-label={t("player.pictureInPicture.label")} onClick={() => void togglePictureInPicture()} data-player-action="picture-in-picture" data-player-control><PictureInPicture size={19} /></button>}
          {fullscreenSupported && <button type="button" className="player__fullscreen" aria-label={t(fullscreenKind === "none" ? "player.fullscreen.enter" : "player.fullscreen.exit")} aria-pressed={fullscreenKind !== "none"} onClick={() => void toggleFullscreen()} data-player-action="fullscreen" data-player-control>{fullscreenKind === "none" ? <Maximize size={19} /> : <Minimize size={19} />}</button>}
          <button type="button" aria-label={t("player.panel.remote")} aria-controls="player-panel-remote" aria-expanded={panel === "remote"} aria-haspopup="dialog" className={panel === "remote" ? "is-active" : ""} onClick={() => togglePanel("remote")} data-player-action="remote" data-player-control><MonitorSmartphone size={19} /></button>
        </div>
      </div>
    </div>}

    {panel && <section ref={panelRef} id={`player-panel-${panel}`} className="player__panel" role="dialog" aria-label={panelTitle}>
      <header><div><small>{t("player.settings.eyebrow")}</small><strong>{panelTitle}</strong></div><button type="button" aria-label={t("player.settings.close")} onClick={closePanel} data-player-action="close-panel" data-player-control><X size={17} /></button></header>
      {panel === "sources" && <div className="player__option-list" role="radiogroup" aria-label={panelTitle} data-player-layout="vertical">{playable.map((candidate, index) => {
        const video = candidate.media?.videoTracks[0];
        const candidateMode = playerModeLabel(candidate.mode, Boolean(candidate.media?.hdrFormat && candidate.media.hdrFormat !== "sdr" && candidate.mode === "transcode"));
        return <button key={candidate.id} type="button" role="radio" aria-checked={selected === index} className={selected === index ? "is-active" : ""} onClick={() => selectSource(index)} data-player-control><span><strong>{candidate.name || candidate.title || t("player.sources.fallbackName", { number: index + 1 })}</strong><small>{candidateMode} · {video?.height ? `${video.height}p` : candidate.protocol.toUpperCase()} {video?.codec ? `· ${video.codec.toUpperCase()}` : ""}</small></span>{selected === index && <Check size={17} />}</button>;
      })}</div>}
      {panel === "audio" && <div className="player__option-list" role="radiogroup" aria-label={panelTitle} data-player-layout="vertical">{audioTracks.map((track) => <button key={track.index} type="button" role="radio" aria-checked={selectedAudioTrack === track.index} className={selectedAudioTrack === track.index ? "is-active" : ""} onClick={() => {
        const video = videoRef.current;
        if (video) resumePositionRef.current = Math.floor(playbackOffsetRef.current + video.currentTime);
        setSelectedAudioTrack(track.index);
        setPreferredAudioTrack(track.index);
        closePanel();
        setPhase("recovering");
      }} data-player-control><span><strong>{track.title || track.language?.toUpperCase() || t("player.audio.fallbackTrack", { number: track.index + 1 })}</strong><small>{playerTrackLabel(track)}</small></span>{selectedAudioTrack === track.index && <Check size={17} />}</button>)}</div>}
      {panel === "subtitles" && <div className="player__option-list" role="radiogroup" aria-label={panelTitle} data-player-layout="vertical">
        <button type="button" role="radio" aria-checked={selectedSubtitleID === "none"} className={selectedSubtitleID === "none" ? "is-active" : ""} onClick={() => selectSubtitle("none")} data-player-control><span><strong>{t("player.subtitles.off")}</strong><small>{t("player.subtitles.none")}</small></span>{selectedSubtitleID === "none" && <Check size={17} />}</button>
        {selectableSubtitles.map((subtitle) => <button key={subtitle.id} type="button" role="radio" aria-checked={selectedSubtitleID === subtitle.id} className={selectedSubtitleID === subtitle.id ? "is-active" : ""} onClick={() => selectSubtitle(subtitle.id)} data-player-control><span><strong>{(subtitle.language || t("common.fallback.unknown")).toUpperCase()}</strong><small>{t(subtitle.default ? "player.subtitles.defaultTrack" : "player.subtitles.track")}</small></span>{selectedSubtitleID === subtitle.id && <Check size={17} />}</button>)}
      </div>}
      {panel === "speed" && <div className="player__speed-grid" role="radiogroup" aria-label={panelTitle} data-player-layout="grid" data-player-columns="3">{playbackRates.map((rate) => <button key={rate} type="button" role="radio" aria-checked={playbackRate === rate} className={playbackRate === rate ? "is-active" : ""} onClick={() => changePlaybackRate(rate)} data-player-control>{rate}×</button>)}</div>}
      {panel === "remote" && <div className="player__remote">
        {coordinationError && <Notice>{coordinationError}</Notice>}
        <label><span>{t("player.remote.target")}</span><Select value={coordinationTarget} disabled={coordinationPending} onChange={setCoordinationTarget} options={coordinationDevices.length ? coordinationDevices.map((device) => ({ value: device.sessionId, label: `${device.name} · ${device.platform}` })) : [{ value: "", label: t("player.remote.empty"), disabled: true }]} /></label>
        <label><span>{t("player.remote.mode")}</span><Select value={coordinationMode} disabled={coordinationPending} onChange={(value) => setCoordinationMode(value as PlaybackLoadMode)} options={[{ value: "play-copy", label: t("player.remote.playCopy") }, { value: "handoff", label: t("player.remote.handoff") }]} /></label>
        <div className="player__remote-actions"><Button loading={coordinationPending} disabled={!coordinationTarget} onClick={() => void sendRemoteCommand("load", Math.round(currentTime * 1_000))}>{t("player.remote.send")}</Button><Button variant="secondary" disabled={!coordinationTarget || coordinationPending} onClick={() => void sendRemoteCommand("play")}>{t("player.actions.play")}</Button><Button variant="secondary" disabled={!coordinationTarget || coordinationPending} onClick={() => void sendRemoteCommand("pause")}>{t("player.actions.pause")}</Button><Button variant="secondary" disabled={!coordinationTarget || coordinationPending} onClick={() => void sendRemoteCommand("seek", Math.round(currentTime * 1_000))}>{t("player.remote.syncPosition")}</Button></div>
      </div>}
      {panel === "stats" && <dl className="player__stats">
        <div><dt>{t("player.diagnostics.status")}</dt><dd>{playerPhaseLabel(phase)}</dd></div>
        <div><dt>{t("player.diagnostics.mode")}</dt><dd>{modeLabel}</dd></div>
        <div><dt>{t("player.diagnostics.protocol")}</dt><dd>{stream?.protocol?.toUpperCase() || "—"}{stream?.container ? ` / ${stream.container.toUpperCase()}` : ""}</dd></div>
        <div><dt>{t("player.diagnostics.video")}</dt><dd>{stats.width && stats.height ? `${stats.width}×${stats.height}` : "—"}{stream?.media?.videoTracks[0]?.codec ? ` · ${stream.media.videoTracks[0].codec.toUpperCase()}` : ""}</dd></div>
        <div><dt>{t("player.diagnostics.audio")}</dt><dd>{audioTracks.find((track) => track.index === selectedAudioTrack)?.codec.toUpperCase() || audioTracks[0]?.codec.toUpperCase() || "—"}</dd></div>
        <div><dt>{t("player.diagnostics.hdr")}</dt><dd>{stream?.media?.hdrFormat?.toUpperCase() || "SDR"}</dd></div>
        <div><dt>{t("player.diagnostics.buffer")}</dt><dd>{stats.bufferedAhead.toFixed(1)}s</dd></div>
        <div><dt>{t("player.diagnostics.droppedFrames")}</dt><dd>{stats.droppedFrames} / {stats.totalFrames}</dd></div>
        {stream?.decision && <><div><dt>{t("player.diagnostics.outcome")}</dt><dd>{playbackDecisionOutcome(stream.decision)}</dd></div>{playbackDecisionReasons(stream.decision).map((reason) => <div key={reason}><dt>{t("player.diagnostics.reason")}</dt><dd>{reason}</dd></div>)}</>}
      </dl>}
    </section>}
  </div>, document.body);
}
