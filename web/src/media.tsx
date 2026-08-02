import { ArrowLeft, AudioLines, Bookmark, Captions, Check, Clapperboard, ExternalLink, Eye, EyeOff, Gauge, Info, ListVideo, LoaderCircle, Maximize, Minimize, Pause, PictureInPicture, Play, RefreshCw, RotateCcw, RotateCw, ServerCrash, Settings2, SkipForward, Star, Volume2, VolumeX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from "react";
import { createPortal } from "react-dom";
import { api, APIError } from "./api";
import { Button, HorizontalDragRow, IconButton, Notice } from "./components";
import { translate as t } from "./i18n";
import { notifyError, notifyErrorMessage, notifySuccess } from "./notifications";
import { TITLE_ID_PROVIDERS, titleProviderURL } from "./titleProviders";
import type { EpisodeMetadata, MediaItem, PlaybackCapabilities, PlaybackMarker, PlaybackPreparation, PlaybackProgress, PlaybackSource, PlaybackSourceOption, PlaybackSubtitle, ResourceBatch, SeasonMetadata, SeriesMetadata, TrailerMetadata } from "./types";

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

function payloadRecords(batch: ResourceBatch, key: string): Record<string, unknown>[] {
  return batch.results.flatMap((result) => {
    const value = result.payload[key];
    if (!Array.isArray(value)) return [];
    return value.map(record).filter((entry): entry is Record<string, unknown> => entry !== null);
  });
}


type SourceIdentity = Pick<PlaybackSourceOption, "addonId" | "manifestId" | "streamIndex">;

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
  if (video.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') || video.canPlayType('video/mp4; codecs="hev1.1.6.L93.B0"')) {
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
  const addProfile = (container: string, videoCodec: string, codecString: string, audioCodec?: string) => {
    const mime = container === "webm" ? "video/webm" : "video/mp4";
    if (!video.canPlayType(`${mime}; codecs="${codecString}"`)) return;
    if (!mediaProfiles.some((profile) => profile.container === container && profile.videoCodec === videoCodec && profile.audioCodec === audioCodec)) {
      mediaProfiles.push({ container, videoCodec, ...(audioCodec ? { audioCodec } : {}) });
    }
    add(containers, container, ...(container === "mp4" ? ["m4v", "mov"] : []));
    add(videoCodecs, videoCodec);
    if (audioCodec) add(audioCodecs, audioCodec);
  };
  const profileVideoCandidates = [
    { container: "mp4", videoCodec: "h264", identifiers: ["avc1.42E01E"] },
    { container: "mp4", videoCodec: "h265", identifiers: ["hvc1.1.6.L93.B0", "hev1.1.6.L93.B0"] },
    { container: "mp4", videoCodec: "av1", identifiers: ["av01.0.05M.08"] },
    { container: "webm", videoCodec: "vp9", identifiers: ["vp9", "vp09.00.10.08"] },
    { container: "webm", videoCodec: "av1", identifiers: ["av01.0.05M.08"] },
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
    for (const videoIdentifier of videoProfile.identifiers) {
      addProfile(videoProfile.container, videoProfile.videoCodec, videoIdentifier);
      for (const audioProfile of profileAudioCandidates) {
        if (audioProfile.container !== videoProfile.container) continue;
        addProfile(videoProfile.container, videoProfile.videoCodec, `${videoIdentifier}, ${audioProfile.identifier}`, audioProfile.audioCodec);
      }
    }
  }
  if (containers.length === 0) containers.push("none");
  if (videoCodecs.length === 0) videoCodecs.push("none");
  if (audioCodecs.length === 0) audioCodecs.push("none");
  const streamingProtocols = ["http", "youtube"];
  if (video.canPlayType("application/vnd.apple.mpegurl") || "MediaSource" in window) streamingProtocols.push("hls");
  return { streamingProtocols, containers, videoCodecs, audioCodecs, hdrFormats: ["sdr"], processingModes: ["remux"], mediaProfiles, externalPlayers: ["system"] };
}

function titleReleaseDate(value: string | undefined): string | undefined {
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

async function resolveMediaTitle(item: MediaItem): Promise<string> {
  if (item.titleId) return item.titleId;
  const preferred = ["tmdb", "imdb", "tvdb", "trakt"].find((provider) => item.externalIds?.[provider]);
  const provider = preferred ?? (/^tt\d+$/i.test(item.id) ? "imdb" : "addon");
  const externalId = preferred ? item.externalIds?.[preferred] ?? item.id : item.id;
  const resolved = await api.resolveTitle({
    mediaType: item.mediaType,
    provider,
    externalId,
    resourceId: item.id,
    title: item.title,
    posterUrl: item.posterUrl,
    backgroundUrl: item.backgroundUrl,
    releaseInfo: item.releaseInfo,
    released: titleReleaseDate(item.released),
  });
  return resolved.titleId;
}


export function mediaTypeLabel(mediaType: string): string {
  if (mediaType === "tv") return t("media.type.liveTv");
  if (mediaType === "series") return t("media.type.series");
  if (mediaType === "episode") return t("media.type.episode");
  return t("media.type.movie");
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
    backgroundUrl: episode.stillUrl || fallback.backgroundUrl,
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
      openSeriesBrowser: false,
    },
  };
}

function episodeIsUpcoming(episode: EpisodeMetadata): boolean {
  if (!episode.airDate) return false;
  const airDate = new Date(`${episode.airDate}T23:59:59Z`);
  return Number.isFinite(airDate.getTime()) && airDate.getTime() > Date.now();
}

export function MediaDetails({ item, onClose, onNavigateContext, onOpenEpisode }: { item: MediaItem; onClose: () => void; onNavigateContext?: (context: { seasonID: string; episodeID?: string; seasonNumber: number; episodeNumber?: number }) => void; onOpenEpisode?: (item: MediaItem) => void }) {
  const [details, setDetails] = useState(item);
  const [playing, setPlaying] = useState(false);
  const [titleID, setTitleID] = useState(item.titleId);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [actionError, setActionError] = useState("");
  const [trailers, setTrailers] = useState<TrailerMetadata[]>([]);
  const [selectedTrailer, setSelectedTrailer] = useState<TrailerMetadata>();
  const [trailerLoading, setTrailerLoading] = useState(false);
  const [trailerMessage, setTrailerMessage] = useState("");
  const [trailerUnavailable, setTrailerUnavailable] = useState(false);
  const [trailerOwnerKey, setTrailerOwnerKey] = useState("");
  const [metaLoading, setMetaLoading] = useState(true);
  const [metaError, setMetaError] = useState("");
  const [availableStreams, setAvailableStreams] = useState<PlaybackSourceOption[]>([]);
  const [selectedStream, setSelectedStream] = useState<PlaybackSourceOption>();
  const [streamsLoading, setStreamsLoading] = useState(true);
  const [streamsError, setStreamsError] = useState("");
  const [streamRefreshVersion, setStreamRefreshVersion] = useState(0);
  const [preparation, setPreparation] = useState<PlaybackPreparation>();
  const [preparationLoading, setPreparationLoading] = useState(false);
  const [preparationError, setPreparationError] = useState("");
  const [series, setSeries] = useState<SeriesMetadata>();
  const [seriesVisible, setSeriesVisible] = useState(item.mediaType === "series" || item.raw?.openSeriesBrowser === true);
  const [seriesLoading, setSeriesLoading] = useState(item.mediaType === "series" || item.raw?.openSeriesBrowser === true);
  const [seriesError, setSeriesError] = useState("");
  const [episodeOrderLoading, setEpisodeOrderLoading] = useState(false);
  const [episodeOrderError, setEpisodeOrderError] = useState("");
  const [seasonID, setSeasonID] = useState("");
  const [season, setSeason] = useState<SeasonMetadata>();
  const [seasonLoading, setSeasonLoading] = useState(false);
  const [selectedEpisode, setSelectedEpisode] = useState<EpisodeMetadata>();
  const [episodeProgress, setEpisodeProgress] = useState<Record<string, PlaybackProgress | undefined>>({});
  const autoPlayNextRef = useRef(false);
  const sourceRefreshAttemptRef = useRef("");
  const trailerRequestRef = useRef(0);
  const seasonCacheRef = useRef(new Map<string, SeasonMetadata>());
  const trailerItemRef = useRef("");
  const episodeListRef = useRef<HTMLDivElement>(null);
  const selectedEpisodeRowRef = useRef<HTMLDivElement>(null);
  const [titleProgress, setTitleProgress] = useState<PlaybackProgress>();
  const [watchedBusy, setWatchedBusy] = useState("");
  const nextSourceRef = useRef<SourceIdentity | undefined>(undefined);
  const continueSeriesID = typeof item.raw?.continueSeriesId === "string" ? item.raw.continueSeriesId : "";
  const continueSeasonID = typeof item.raw?.continueSeasonId === "string" ? item.raw.continueSeasonId : "";
  const continueEpisodeID = typeof item.raw?.continueEpisodeId === "string" ? item.raw.continueEpisodeId : "";
  const continueSeasonNumber = typeof item.raw?.continueSeasonNumber === "number" ? item.raw.continueSeasonNumber : undefined;
  const continueEpisodeNumber = typeof item.raw?.continueEpisodeNumber === "number" ? item.raw.continueEpisodeNumber : undefined;
  const trailerSeriesContext = item.mediaType === "series" || (item.mediaType === "episode" && seriesVisible);
  const trailerTitleID = item.mediaType === "episode" && seriesVisible ? series?.id ?? continueSeriesID : item.titleId ?? item.id;
  const selectedTrailerSeason = trailerSeriesContext ? series?.seasons.find((candidate) => candidate.id === seasonID) : undefined;
  const trailersAvailableForContext = item.mediaType === "movie" || item.mediaType === "series" || (item.mediaType === "episode" && seriesVisible && Boolean(series && selectedTrailerSeason));
  const trailerItemKey = `${trailerSeriesContext ? "series" : item.mediaType}:${trailerTitleID}:${selectedTrailerSeason ? `season:${selectedTrailerSeason.seasonNumber}` : "title"}`;
  trailerItemRef.current = trailerItemKey;
  const activeTrailers = trailerOwnerKey === trailerItemKey ? trailers : [];
  const activeTrailer = trailerOwnerKey === trailerItemKey ? selectedTrailer : undefined;
  const activeTrailerLoading = trailerOwnerKey === trailerItemKey && trailerLoading;
  const streamResourceID = selectedEpisode && series ? episodeResourceID(series, selectedEpisode, item.id) : item.id;
  const playbackMediaType = selectedEpisode || item.mediaType === "episode" ? "episode" : item.mediaType;
  const selectedProgress = selectedEpisode ? episodeProgress[selectedEpisode.id] : titleProgress;
  const preparationStartSeconds = selectedProgress?.completed ? 0 : Math.max(0, Math.floor(selectedProgress?.positionSeconds ?? 0));
  const fromContinue = item.raw?.continueReason === "resume" || item.raw?.continueReason === "next_episode";
  const autoplayNextEpisode = document.documentElement.dataset.autoplayNextEpisode !== "false";
  const canSelectStream = item.mediaType !== "series" && !(fromContinue && seriesVisible);

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
    let active = true;
    setMetaError("");
    void api.resources("meta", item.mediaType === "episode" ? "series" : item.mediaType, item.id).then((batch) => {
      const metas = payloadRecords(batch, "meta");
      const fallback = batch.results.map((result) => record(result.payload.meta)).find((value) => value !== null);
      const meta = metas[0] ?? fallback;
      if (!active || !meta) return;
      setDetails((current) => ({
        ...current,
        title: item.mediaType === "episode" ? current.title : String(meta.name ?? meta.title ?? current.title),
        description: item.mediaType === "episode" ? current.description : String(meta.description ?? current.description ?? ""),
        posterUrl: item.mediaType === "episode" ? current.posterUrl : String(meta.poster ?? current.posterUrl ?? ""),
        backgroundUrl: String(current.backgroundUrl ?? meta.background ?? meta.backgroundUrl ?? ""),
        logoUrl: String(meta.logo ?? current.logoUrl ?? ""),
        releaseInfo: item.mediaType === "episode" ? current.releaseInfo : String(meta.releaseInfo ?? meta.year ?? current.releaseInfo ?? ""),
        raw: { ...current.raw, ...meta },
      }));
    }).catch((cause) => {
      if (active) setMetaError(cause instanceof APIError ? cause.message : t("media.details.error.additionalDetailsLoadFailed"));
    }).finally(() => { if (active) setMetaLoading(false); });
    if (item.titleId) {
      void api.library().then((library) => { if (active) setSaved(library.items.some((entry) => entry.titleId === item.titleId)); }).catch(() => undefined);
    }
    return () => { active = false; };
  }, [item.id, item.mediaType, item.titleId]);

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
      if (movie) setDetails((current) => ({
        ...current,
        voteAverage: movie.voteAverage,
        posterUrl: movie.posterUrl || current.posterUrl,
        backgroundUrl: current.backgroundUrl || movie.backdropUrl,
        logoUrl: current.logoUrl || movie.logoUrl,
        voteCount: movie.voteCount,
        externalIds: { ...current.externalIds, ...movie.externalIds },
      }));
    })().catch(() => undefined);
    return () => { active = false; };
  }, [item.id, item.mediaType, item.titleId]);

  useEffect(() => {
    let active = true;
    if (!seriesVisible && !(item.mediaType === "episode" && continueSeriesID)) {
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
        : await resolveMediaTitle(item);
      const resolved = await api.seriesDetails(resolvedTitleID);
      if (!active) return;
      if (item.mediaType === "series") setTitleID(resolvedTitleID);
      setSeries(resolved);
      seasonCacheRef.current.clear();
      if (item.mediaType === "series" || item.mediaType === "episode") setDetails((current) => ({
        ...current,
        voteAverage: resolved.voteAverage,
        posterUrl: resolved.posterUrl || current.posterUrl,
        backgroundUrl: current.backgroundUrl || resolved.backdropUrl,
        logoUrl: current.logoUrl || resolved.logoUrl,
        voteCount: resolved.voteCount,
        externalIds: { ...resolved.externalIds, ...current.externalIds },
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
  }, [continueEpisodeID, continueSeasonID, continueSeasonNumber, continueSeriesID, item.id, item.mediaType, item.releaseInfo, item.released, item.titleId, seriesVisible]);

  useEffect(() => {
    let active = true;
    if (!seasonID) {
      setSeason(undefined);
      return;
    }
    setSeasonLoading(true);
    setSelectedEpisode(undefined);
    setEpisodeProgress({});
    void (seasonCacheRef.current.has(seasonID) ? Promise.resolve(seasonCacheRef.current.get(seasonID)!) : api.seasonDetails(seasonID, undefined, series?.mappingProvider)).then(async (resolved) => {
      if (!active) return;
      setSeason(resolved);
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
        setSelectedEpisode(requested ?? requestedByNumber ?? (exactMappedEpisodeRequired ? undefined : resolved.episodes[0]));
      }
      const progressEntries = await Promise.all(resolved.episodes.map(async (episode) => [episode.id, await api.progress(episode.id).catch(() => undefined)] as const));
      if (!active) return;
      setEpisodeProgress(Object.fromEntries(progressEntries));
    }).catch((cause) => {
      if (active) setSeriesError(notifyError(cause, t("media.season.error.episodesLoadFailed"), t("media.season.error.unavailableTitle")));
    }).finally(() => { if (active) setSeasonLoading(false); });
    return () => { active = false; };
  }, [continueEpisodeID, continueEpisodeNumber, item.mediaType, seasonID, series?.mappingProvider]);
  useEffect(() => {
    if (item.mediaType !== "episode" || !selectedEpisode) return;
    const episodeCode = `S${String(selectedEpisode.seasonNumber).padStart(2, "0")}E${String(selectedEpisode.episodeNumber).padStart(2, "0")}`;
    setDetails((current) => ({
      ...current,
      title: [series?.name, episodeCode, selectedEpisode.name].filter(Boolean).join(" · "),
      description: selectedEpisode.overview || current.description,
      backgroundUrl: selectedEpisode.stillUrl || current.backgroundUrl,
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
    if (!canSelectStream) {
      setAvailableStreams([]);
      setSelectedStream(undefined);
      setStreamsError("");
      setStreamsLoading(false);
      return () => controller.abort();
    }
    setStreamsLoading(true);
    setStreamsError("");
    setSelectedStream(undefined);
    setPreparation(undefined);
    setPreparationError("");
    void api.playbackSources({
      mediaType: playbackMediaType,
      resourceId: streamResourceID,
      capabilities: webPlaybackCapabilities(),
    }, controller.signal).then((response) => {
      if (!active) return;
      const options = response.sources;
      setAvailableStreams(options);
      if (autoPlayNextRef.current) {
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
      setStreamsError(notifyError(cause, t("media.sources.error.loadFailed"), t("media.sources.error.unavailableTitle")));
    }).finally(() => { if (active) setStreamsLoading(false); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [canSelectStream, playbackMediaType, selectedEpisode, streamRefreshVersion, streamResourceID]);

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
      if (prepared.mode === "transcode" || prepared.mode === "transcode_audio") {
        autoPlayNextRef.current = false;
        setPreparationError(t("media.sources.error.encodingRequired"));
        return;
      }
      setPreparation(prepared);
      if (autoPlayNextRef.current) {
        autoPlayNextRef.current = false;
        setPlaying(true);
      }
    }).catch((cause) => {
      if (!active || cause instanceof DOMException && cause.name === "AbortError") return;
      autoPlayNextRef.current = false;
      if (cause instanceof APIError && cause.code === "playback_source_expired" && sourceRefreshAttemptRef.current !== selectedStream.sourceRef) {
        sourceRefreshAttemptRef.current = selectedStream.sourceRef;
        setStreamRefreshVersion((version) => version + 1);
        return;
      }
      if (cause instanceof APIError && cause.code === "playback_source_unsupported") {
        setPreparationError(t("media.sources.error.conversionUnsupported"));
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
    setSaving(true);
    setActionError("");
    const removing = saved;
    try {
      const resolvedTitleID = titleID ?? await resolveMediaTitle(details);
      if (!titleID) setTitleID(resolvedTitleID);
      if (saved) await api.removeLibrary(resolvedTitleID);
      else await api.addLibrary(resolvedTitleID);
      setSaved(!removing);
      notifySuccess(
        t(removing ? "library.notice.removed" : "library.notice.added", { title: details.title }),
        t(removing ? "library.notice.removedTitle" : "library.notice.addedTitle"),
      );
    } catch (cause) {
      setActionError(notifyError(cause, t("library.error.updateFailed"), t("library.error.notUpdatedTitle")));
    } finally {
      setSaving(false);
    }
  }

  async function showTrailer() {
    if (activeTrailers.length > 0 || activeTrailerLoading) return;
    const requestID = ++trailerRequestRef.current;
    const requestedItemKey = trailerItemRef.current;
    const requestedSeasonNumber = selectedTrailerSeason?.seasonNumber;
    const requestIsCurrent = () => trailerRequestRef.current === requestID && trailerItemRef.current === requestedItemKey;
    let trailerRequested = false;
    setTrailers([]);
    setSelectedTrailer(undefined);
    setTrailerOwnerKey(requestedItemKey);
    setTrailerLoading(true);
    setTrailerMessage("");
    setTrailerUnavailable(false);
    try {
      const resolvedTitleID = item.mediaType === "episode" && seriesVisible && trailerTitleID ? trailerTitleID : await resolveMediaTitle(item);
      if (!requestIsCurrent()) return;
      setTitleID(resolvedTitleID);
      trailerRequested = true;
      const metadata = await api.trailers(resolvedTitleID, requestedSeasonNumber);
      if (!requestIsCurrent()) return;
      const nextTrailers = Array.isArray(metadata.trailers) ? metadata.trailers : [];
      if (nextTrailers.length === 0) {
        setTrailerUnavailable(true);
        setTrailerMessage(requestedSeasonNumber === undefined
          ? t("media.trailers.noneForTitle")
          : t("media.trailers.noneForSeason", { season: requestedSeasonNumber === 0 ? t("media.season.specials") : t("media.season.number", { number: requestedSeasonNumber }) }));
        return;
      }
      setTrailers(nextTrailers);
      setSelectedTrailer(nextTrailers[0]);
    } catch (cause) {
      if (!requestIsCurrent()) return;
      if (trailerRequested && cause instanceof APIError && cause.status === 404) {
        setTrailerUnavailable(true);
        setTrailerMessage(requestedSeasonNumber === undefined
          ? t("media.trailers.noneForTitle")
          : t("media.trailers.noneForSeason", { season: requestedSeasonNumber === 0 ? t("media.season.specials") : t("media.season.number", { number: requestedSeasonNumber }) }));
      } else {
        setTrailerMessage(notifyError(cause, t("media.trailers.error.loadFailed"), t("media.trailers.error.unavailableTitle")));
      }
    } finally {
      if (requestIsCurrent()) setTrailerLoading(false);
    }
  }

  function dismissTrailer() {
    trailerRequestRef.current += 1;
    setTrailers([]);
    setSelectedTrailer(undefined);
    setTrailerLoading(false);
    setTrailerMessage("");
    setTrailerUnavailable(false);
    setTrailerOwnerKey("");
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
    const resolvedTitleID = titleID ?? item.titleId ?? await resolveMediaTitle(details);
    const watched = !titleProgress?.completed;
    setWatchedBusy(resolvedTitleID);
    setActionError("");
    try {
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

  async function changeEpisodeOrder(episodeOrderID: string) {
    if (!series || episodeOrderLoading || episodeOrderID === (series.selectedEpisodeOrderId ?? "")) return;
    setEpisodeOrderLoading(true);
    setEpisodeOrderError("");
    try {
      const resolved = await api.seriesDetails(series.id, episodeOrderID
        ? { mappingProvider: "tvdb", episodeOrderId: episodeOrderID }
        : undefined);
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
    const episodes = (season?.episodes ?? []).filter((episode) => !episodeIsUpcoming(episode));
    if (episodes.length === 0) return;
    const watched = !episodes.every((episode) => episodeProgress[episode.id]?.completed);
    const changed = episodes.filter((episode) => Boolean(episodeProgress[episode.id]?.completed) !== watched);
    setWatchedBusy(seasonID);
    setActionError("");
    try {
      const results = await Promise.all(changed.map(async (episode) => [episode.id, await api.setWatched(episode.id, watched, episodeProgress[episode.id]?.version ?? 0)] as const));
      setEpisodeProgress((values) => ({ ...values, ...Object.fromEntries(results) }));
      notifySuccess(
        t(watched ? "media.season.watch.allMarkedWatched" : "media.season.watch.allMarkedUnwatched"),
        t(watched ? "media.season.watch.watchedTitle" : "media.season.watch.unwatchedTitle"),
      );
    } catch (cause) {
      const refreshed = await Promise.all(episodes.map(async (episode) => [episode.id, await api.progress(episode.id).catch(() => undefined)] as const));
      setEpisodeProgress(Object.fromEntries(refreshed));
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
  }, [orderedEpisodes, selectedEpisode?.id]);
  const availableSeasonEpisodes = orderedEpisodes.filter((episode) => !episodeIsUpcoming(episode));
  const watchedEpisodeCount = availableSeasonEpisodes.filter((episode) => episodeProgress[episode.id]?.completed).length;
  const allSeasonWatched = availableSeasonEpisodes.length > 0 && watchedEpisodeCount === availableSeasonEpisodes.length;
  const activePlayerItem = selectedEpisode && series ? episodeItem(series, selectedEpisode, details) : { ...details, titleId: titleID };

  const genres = Array.isArray(details.raw?.genres) ? details.raw.genres.map((genre) => {
    if (typeof genre === "string") return genre;
    const value = record(genre);
    return typeof value?.name === "string" ? value.name : "";
  }).filter(Boolean).slice(0, 4) : [];
  const backdrop = details.backgroundUrl || details.posterUrl;
  const trailerURL = activeTrailer ? (() => {
    const params = new URLSearchParams({ autoplay: "1" });
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
    return <Player item={activePlayerItem} sourceRef={selectedStream.sourceRef} startSeconds={preparationStartSeconds} autoplayNextEpisode={autoplayNextEpisode} onClose={() => setPlaying(false)} onSourceExpired={() => { setPlaying(false); setStreamRefreshVersion((version) => version + 1); }} onEnded={selectedEpisode ? handleEpisodeEnded : undefined} />;
  }

  const typeLabel = mediaTypeLabel(details.mediaType);
  const episodeSeriesName = item.mediaType === "episode"
    ? series?.name ?? (typeof item.raw?.episodeSeriesName === "string" ? item.raw.episodeSeriesName : "")
    : "";
  const episodeTitle = selectedEpisode?.name || details.title || t("media.episode.fallbackTitle", { number: item.episodeNumber ?? "" });
  const episodeSeasonNumber = selectedEpisode?.seasonNumber ?? item.seasonNumber;
  const episodeNumber = selectedEpisode?.episodeNumber ?? item.episodeNumber;
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

  return (
    <article className="details-page page-enter" aria-labelledby="media-details-title">
      <button type="button" className="details-page__back" onClick={closeDetails} autoFocus>
        <ArrowLeft size={18} />
        <span>{t("media.details.backToBrowse")}</span>
      </button>

      <section className="details-hero" style={backdrop ? { backgroundImage: `url(${backdrop})` } : undefined}>
        <div className="details-hero__shade" aria-hidden="true" />
        <div className="details-hero__glow" aria-hidden="true" />
        <div className="details-hero__inner">
          <aside className="details-artwork" aria-hidden="true">
            {details.posterUrl ? <img src={details.posterUrl} alt="" /> : <span>{details.title.slice(0, 2).toUpperCase()}</span>}
          </aside>

          <div className="details-overview">
            {item.mediaType === "episode"
              ? <>
                {episodeSeriesName && <span className="details-series-name">{episodeSeriesName}</span>}
                <h1 id="media-details-title" aria-label={details.title}>{episodeTitle}</h1>
              </>
              : details.logoUrl
                ? <><img className="details-logo" src={details.logoUrl} alt="" /><h1 id="media-details-title" className="visually-hidden">{details.title}</h1></>
                : <h1 id="media-details-title">{details.title}</h1>}

            <div className="details-meta">
              {episodeSeasonNumber !== undefined && episodeNumber !== undefined && <span>{t("media.episode.seasonEpisode", { season: episodeSeasonNumber, episode: episodeNumber })}</span>}
              {details.releaseInfo && details.releaseInfo !== typeLabel && <span>{details.releaseInfo}</span>}
              {details.voteAverage !== undefined && <span className="rating"><Star size={14} fill="currentColor" /> {details.voteAverage.toFixed(1)}</span>}
              <span>{typeLabel}</span>
              {genres.map((genre) => <span key={genre}>{genre}</span>)}
            </div>

            {(externalTitleLinks.length > 0 || Boolean(details.sources?.length)) && <div className="details-title-links">
              {externalTitleLinks.length > 0 && <div className="details-provider-badges" role="group" aria-label={t("media.details.externalPagesLabel")}>
                {externalTitleLinks.map(({ externalID, provider, mediaType, episode }) => {
                  const label = t("media.details.openExternalPage", { provider: provider.label, id: externalID });
                  return <a key={provider.key} className={`details-provider-badge details-provider-badge--${provider.key}`} href={titleProviderURL(provider.key, externalID, mediaType, episode)} target="_blank" rel="noreferrer" aria-label={label} title={label}>
                    <span className="details-provider-badge__brand">{provider.label}</span>
                    <ExternalLink size={11} aria-hidden="true" />
                  </a>;
                })}
              </div>}
              {details.sources && details.sources.length > 0 && <div className="details-sources"><span>{t("media.details.availableFrom")}</span><div>{details.sources.map((source) => <i key={source.id}>{source.title}</i>)}</div></div>}
            </div>}

            {metaLoading && !details.description
              ? <div className="details-loading" role="status"><LoaderCircle className="spin" size={18} /> {t("media.details.loading")}</div>
              : <p className="details-description">{details.description || t("media.details.noSynopsis")}</p>}
            {metaError && <Notice tone="info">{metaError} {t("media.details.partialInformationShown")}</Notice>}

            <div className="details-actions">
              {canSelectStream && <Button className="details-actions__play" disabled={!selectedStream || !preparation} loading={preparationLoading} onClick={() => setPlaying(true)}>
                <Play size={19} fill="currentColor" />
                {t(item.mediaType === "episode" ? "media.details.playEpisode" : "media.details.playSelectedStream")}
              </Button>}
              <Button variant="secondary" loading={saving} onClick={() => void toggleLibrary()}>
                {saved ? <Check size={19} /> : <Bookmark size={19} />}
                {t(saved ? "library.actions.inLibrary" : "library.actions.add")}
              </Button>
              {item.mediaType === "movie" && !fromContinue && <Button variant="secondary" loading={watchedBusy === titleID} onClick={() => void toggleTitleWatched()}>
                {titleProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}
                {t(titleProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
              </Button>}
              {trailersAvailableForContext && <Button type="button" variant="secondary" disabled={Boolean(activeTrailer)} loading={activeTrailerLoading} aria-label={t(activeTrailerLoading ? "media.trailers.loading" : "media.trailers.title")} aria-busy={activeTrailerLoading} aria-controls="details-trailer" aria-expanded={Boolean(activeTrailer)} onClick={() => void showTrailer()}>
                <Clapperboard size={19} />
                {t("media.trailers.title")}
              </Button>}
            </div>

            {fromContinue && (item.mediaType === "movie" || item.mediaType === "episode") && <div className="details-context-actions">
              <Button variant="ghost" loading={watchedBusy === titleID} onClick={() => void toggleTitleWatched()}>
                {titleProgress?.completed ? <EyeOff size={18} /> : <Eye size={18} />}
                {t(titleProgress?.completed ? "media.watch.actions.markUnwatched" : "media.watch.actions.markWatched")}
              </Button>
              {item.mediaType === "episode" && <Button variant="ghost" onClick={() => setSeriesVisible((visible) => !visible)}>
                <ListVideo size={18} />
                {t(seriesVisible ? "media.series.actions.hideGuide" : "media.series.actions.viewGuide")}
              </Button>}
            </div>}
          </div>
        </div>
      </section>

      <div className="details-content">
        {seriesVisible && <section className="series-browser" aria-labelledby="details-episodes-title">
          <header className="details-section-heading">
            <span className="details-section-heading__icon"><ListVideo size={20} /></span>
            <div>
              <span>{t("media.series.guideEyebrow")}</span>
              <h2 id="details-episodes-title">{t("media.series.episodesTitle")}</h2>
            </div>
            {series && series.episodeOrders.length > 0 && <label className="series-order">
              <span>{t("media.episodeOrder.label")}</span>
              <span className="series-order__field">
                <select aria-label={t("media.episodeOrder.accessibleLabel")} value={series.selectedEpisodeOrderId ?? ""} disabled={episodeOrderLoading} onChange={(event) => void changeEpisodeOrder(event.target.value)}>
                  <option value="">{t("media.episodeOrder.profileDefault")}</option>
                  {series.episodeOrders.map((order) => <option key={order.id} value={order.id}>{episodeOrderLabel(order)}</option>)}
                </select>
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
                      <button type="button" disabled={availableSeasonEpisodes.length === 0 || watchedBusy === seasonID} onClick={() => void toggleSeasonWatched()}>
                        {watchedBusy === seasonID ? <LoaderCircle className="spin" size={15} /> : allSeasonWatched ? <EyeOff size={15} /> : <Eye size={15} />}
                        {t(allSeasonWatched ? "media.season.watch.actions.markUnwatched" : "media.season.watch.actions.markWatched")}
                      </button>
                    </div>

                    <div ref={episodeListRef} className="episode-list">
                      {orderedEpisodes.map((episode) => {
                        const progress = episodeProgress[episode.id];
                        const upcoming = episodeIsUpcoming(episode);
                        const progressPercent = progress && progress.durationSeconds > 0 ? Math.min(100, progress.positionSeconds / progress.durationSeconds * 100) : 0;
                        return <div ref={selectedEpisode?.id === episode.id ? selectedEpisodeRowRef : undefined} key={episode.id} className={selectedEpisode?.id === episode.id ? "is-selected" : ""}>
                          <button type="button" className="episode-main" disabled={upcoming} aria-current={selectedEpisode?.id === episode.id ? "true" : undefined} onClick={() => {
                            autoPlayNextRef.current = false;
                            onOpenEpisode?.(episodeItem(series, episode, details));
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
        </section>}

        {canSelectStream && <section className="details-utility-grid" aria-label={t("media.sources.sectionLabel")}>
          <div className="details-panel details-stream-selector">
            <div className="details-stream-toolbar">
              <span>{streamsLoading ? t("common.status.loading") : t(availableStreams.length === 1 ? "media.sources.availableCount.one" : "media.sources.availableCount.many", { count: availableStreams.length })}</span>
              <IconButton label={t("media.sources.refresh")} disabled={streamsLoading} onClick={() => {
                autoPlayNextRef.current = false;
                setStreamRefreshVersion((version) => version + 1);
              }}>
                <RefreshCw size={17} className={streamsLoading ? "spin" : ""} />
              </IconButton>
            </div>
            {streamsLoading
              ? <div className="details-stream-selector__loading"><LoaderCircle className="spin" size={18} /> {t("media.sources.loading")}</div>
              : availableStreams.length > 0
                ? <div className="details-stream-list" role="radiogroup" aria-label={t("media.sources.availableLabel")}>
                  {availableStreams.map((option) => (
                    <button key={option.sourceRef} type="button" role="radio" aria-checked={selectedStream?.sourceRef === option.sourceRef} className={selectedStream?.sourceRef === option.sourceRef ? "is-selected" : ""} onClick={() => {
                      autoPlayNextRef.current = false;
                      setSelectedStream(option);
                    }}>
                      <span>
                        <strong>{option.name}</strong>
                        {option.description && <small>{option.description}</small>}
                        {!option.description && option.filename && <small>{option.filename}</small>}
                      </span>
                      {selectedStream?.sourceRef === option.sourceRef && <span className="details-stream-list__state">
                        {preparationLoading
                          ? <LoaderCircle className="spin" size={17} />
                          : preparation
                            ? <><Check size={17} /><small>{preparationLabel(preparation)}</small></>
                            : preparationError
                              ? <small>{t("common.status.unavailable")}</small>
                              : <small>{t("common.status.selected")}</small>}
                      </span>}
                    </button>
                  ))}
                </div>
                : <Notice>{streamsError || t("media.sources.empty")}</Notice>}
            {preparationError && <Notice>{preparationError}</Notice>}
          </div>

        </section>}

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

        {trailerOwnerKey === trailerItemKey && trailerMessage && <div className="details-trailer-feedback"><Notice tone={trailerUnavailable ? "info" : "error"}>{trailerMessage}</Notice></div>}
        {actionError && <Notice>{actionError}</Notice>}
      </div>
    </article>
  );
}

type PlayerPhase = "preparing" | "ready" | "playing" | "paused" | "buffering" | "recovering" | "failed" | "ended";
type PlayerPanel = "sources" | "audio" | "subtitles" | "speed" | "stats" | null;
type PlayerPreferences = { volume: number; muted: boolean; rate: number };
type PlayerStats = { bufferedAhead: number; droppedFrames: number; totalFrames: number; width: number; height: number };
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

function playerSourceAvailable(source: PlaybackSource): boolean {
  if (!source.compatible) return false;
  if (source.mode === "transcode" || source.mode === "transcode_audio") return false;
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


export function Player({ item, sourceRef, startSeconds, autoplayNextEpisode, onClose, onSourceExpired, onEnded }: { item: MediaItem; sourceRef: string; startSeconds: number; autoplayNextEpisode: boolean; onClose: () => void; onSourceExpired: () => void; onEnded?: () => void }) {
  const initialPreferences = useRef(loadPlayerPreferences()).current;
  const playerRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
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
  const [retryVersion, setRetryVersion] = useState(0);
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
  const progressVersionRef = useRef(0);
  const resumePositionRef = useRef(startSeconds);
  const lastSavedPositionRef = useRef(0);
  const progressRequestRef = useRef(false);
  const sessionIDRef = useRef("");
  const playbackDurationRef = useRef(0);
  const streamProtocolRef = useRef("");
  const playbackOffsetRef = useRef(0);
  const pausedAtRef = useRef(0);

  useEffect(() => {
    setProgressReady(false);
    let active = true;
    if (item.mediaType !== "movie" && item.mediaType !== "episode") return;
    void (item.titleId ? Promise.resolve(item.titleId) : resolveMediaTitle(item)).then(async (titleID) => {
      if (!active) return;
      titleIDRef.current = titleID;
      const progress = await api.progress(titleID);
      if (!active || !progress) return;
      progressVersionRef.current = progress.version;
      resumePositionRef.current = startSeconds;
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
    if (!progressReady) return;
    let active = true;
    setLoading(true);
    setPhase("preparing");
    setError("");
    setPlaybackBlocked(false);
    setPanel(null);
    setSelected(0);
    setCurrentTime(0);
    setVideoDuration(0);
    setPlaybackStart(undefined);
    setSeekPreview(null);
    void api.resolvePlayback({
      sourceRef,
      startSeconds: Math.max(0, Math.floor(resumePositionRef.current)),
      titleId: item.titleId,
      preferredAudioTrack,
    }).then((session) => {
      if (!active) {
        void api.stopPlayback(session.id).catch(() => undefined);
        return;
      }
      const previousSessionID = sessionIDRef.current;
      sessionIDRef.current = session.id;
      if (previousSessionID) void api.stopPlayback(previousSessionID).catch(() => undefined);
      const resolvedSources = session.sources ?? [];
      setStreams(resolvedSources);
      setSubtitles(session.subtitles ?? []);
      setSelectedAudioTrack(session.selectedAudioTrack);
      setSelectedSubtitleID(session.selectedSubtitleId || "none");
      const compatible = resolvedSources.filter(playerSourceAvailable);
      const selectedIndex = compatible.findIndex((source) => source.id === session.selectedSourceId);
      setSelected(selectedIndex < 0 ? 0 : selectedIndex);
      setPhase("ready");
    }).catch((cause) => {
      if (!active) return;
      stopCurrentSession();
      if (cause instanceof APIError && cause.code === "playback_source_expired") {
        onSourceExpired();
        return;
      }
      if (cause instanceof APIError && cause.code === "playback_source_unsupported") {
        setError(t("player.error.conversionUnsupported"));
        setPhase("failed");
        return;
      }
      setError(notifyError(cause, t("player.error.sourcesUnavailable"), t("player.error.unavailableTitle")));
      setPhase("failed");
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [item.id, item.titleId, onSourceExpired, preferredAudioTrack, progressReady, retryVersion, sourceRef]);

  const playable = useMemo(() => streams.filter(playerSourceAvailable), [streams]);
  const stream = playable[selected];
  const inspectedDuration = stream?.media?.durationSeconds ?? 0;
  const playbackDuration = inspectedDuration > 0 ? inspectedDuration : videoDuration;
  playbackDurationRef.current = playbackDuration;
  streamProtocolRef.current = stream?.protocol ?? "";
  const audioTracks = stream?.media?.audioTracks ?? [];
  const selectedSubtitle = subtitles.find((subtitle) => subtitle.id === selectedSubtitleID);
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
    const playbackOffset = processed ? Math.max(0, Math.floor(playbackStart ?? resumePositionRef.current)) : 0;
    playbackOffsetRef.current = playbackOffset;
    setCurrentTime(playbackOffset);
    setSeekPreview(null);
    setPlaybackBlocked(false);
    setPhase("preparing");
    pausedAtRef.current = 0;
    if (playbackOffset > 0) playbackURL.searchParams.set("start", String(playbackOffset));
    const sourceURL = processed ? `${playbackURL.pathname}${playbackURL.search}` : stream.url;
    const fallbackURL = new URL(playbackURL);
    fallbackURL.searchParams.delete("file");
    fallbackURL.searchParams.set("fallback", "1");
    let disposed = false;
    let fallbackStarted = false;
    let destroyHLS = () => {};
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
    const failPlayback = (message: string) => {
      setPaused(true);
      setError(notifyErrorMessage(message, t("player.error.unavailableTitle")));
      setPhase("failed");
    };
    const handleMediaError = () => {
      if (!startProcessedFallback()) failPlayback(t("player.error.sourcePlayFailed"));
    };
    const startProcessedFallback = (): boolean => {
      if (disposed || fallbackStarted || stream.mode === "direct") return false;
      fallbackStarted = true;
      destroyHLS();
      destroyHLS = () => {};
      setError("");
      setPhase("recovering");
      video.addEventListener("error", handleMediaError, { once: true });
      video.src = `${fallbackURL.pathname}${fallbackURL.search}`;
      video.load();
      startPlayback();
      return true;
    };

    video.volume = volume;
    video.muted = muted;
    video.playbackRate = playbackRate;
    if (!isHLS) {
      video.addEventListener("error", handleMediaError);
      if (processed) startProcessedFallback();
      else {
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
          } else if (!startProcessedFallback()) {
            failPlayback(t("player.error.hlsUnsupported"));
          }
          return;
        }
        const hls = new Hls({
          enableWorker: true,
          autoStartLoad: false,
          startPosition: 0,
          maxBufferLength: 30,
          backBufferLength: 30,
        });
        destroyHLS = () => hls.destroy();
        let mediaRecoveries = 0;
        let networkRecoveries = 0;
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
          setPhase("ready");
          hls.startLoad(0);
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
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR && networkRecoveries < 2) {
            networkRecoveries += 1;
            hls.stopLoad();
            if (String(data.details).toLowerCase().includes("manifest")) hls.loadSource(sourceURL);
            else hls.startLoad(video.currentTime);
            return;
          }
          if (!startProcessedFallback()) failPlayback(t("player.error.hlsStopped"));
        });
        hls.loadSource(sourceURL);
        hls.attachMedia(video);
      }).catch((cause) => {
        if (!disposed && !startProcessedFallback()) {
          setError(notifyError(cause, t("player.error.hlsPlayerLoadFailed"), t("player.error.unavailableTitle")));
          setPhase("failed");
        }
      });
    }

    return () => {
      disposed = true;
      video.removeEventListener("error", handleMediaError);
      destroyHLS();
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
    if (!appRoot) return;
    const wasInert = appRoot.inert;
    const previousAriaHidden = appRoot.getAttribute("aria-hidden");
    const previousBodyOverflow = document.body.style.overflow;
    const previousRootOverflow = document.documentElement.style.overflow;
    if (document.activeElement instanceof HTMLElement && appRoot.contains(document.activeElement)) document.activeElement.blur();
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
    if (!stream?.url && !stream?.infoHash) return;
    const frame = window.requestAnimationFrame(() => playerRef.current?.querySelector<HTMLElement>(".player__external-action, .player__control-primary")?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, [stream?.id, stream?.infoHash, stream?.url]);

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
      const interactive = target instanceof HTMLInputElement || target instanceof HTMLSelectElement || target instanceof HTMLTextAreaElement;
      if (event.key === "Escape" || event.key === "BrowserBack" || event.key === "GoBack") {
        event.preventDefault();
        if (panel) setPanel(null);
        else if (fullscreenKind !== "none" || document.fullscreenElement === playerRef.current) void exitPlayerFullscreen();
        else if (!controlsVisible) revealControls();
        else closePlayer();
        return;
      }
      if (interactive) return;
      if (target instanceof HTMLElement && target.hasAttribute("data-player-control") && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) {
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

  async function persistProgress(completed = false, positionOverride?: number) {
    const video = videoRef.current;
    const titleID = titleIDRef.current;
    if (!video || !titleID || progressRequestRef.current) return;
    const durationSeconds = playbackDurationRef.current > 0 ? playbackDurationRef.current : streamProtocolRef.current !== "hls" ? video.duration : Number.NaN;
    if (!Number.isFinite(durationSeconds) || durationSeconds <= 0) return;
    const positionSeconds = completed ? Math.floor(durationSeconds) : Math.floor(positionOverride ?? playbackOffsetRef.current + video.currentTime);
    if (!completed && positionSeconds <= 0) return;
    progressRequestRef.current = true;
    try {
      const progress = await api.updateProgress(titleID, {
        positionSeconds,
        durationSeconds: Math.floor(durationSeconds),
        completed,
        expectedVersion: progressVersionRef.current,
      });
      progressVersionRef.current = progress.version;
      lastSavedPositionRef.current = positionSeconds;
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409) {
        try {
          const current = await api.progress(titleID);
          progressVersionRef.current = current?.version ?? 0;
        } catch (refreshCause) {
          notifyError(refreshCause, t("player.progress.syncFailed"), t("player.progress.notSavedTitle"));
        }
      } else {
        notifyError(cause, t("player.progress.saveFailed"), t("player.progress.notSavedTitle"));
      }
    } finally {
      progressRequestRef.current = false;
    }
  }

  function stopCurrentSession() {
    const sessionID = sessionIDRef.current;
    if (!sessionID) return;
    sessionIDRef.current = "";
    void api.stopPlayback(sessionID).catch(() => undefined);
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

  function togglePanel(nextPanel: Exclude<PlayerPanel, null>) {
    revealControls();
    setPanel((current) => current === nextPanel ? null : nextPanel);
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
    if (stream?.mode === "direct" && video) {
      video.currentTime = target;
      playbackOffsetRef.current = 0;
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
    setPanel(null);
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
    setPanel(null);
    setPhase("recovering");
    void persistProgress(false, position);
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
      setError(notifyErrorMessage(t("player.error.sourceEndedEarly"), t("player.error.sourceEndedEarlyTitle")));
      setPhase("failed");
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
            : "";

  return createPortal(<div ref={playerRef} className={`player player--${phase}${controlsVisible ? " has-controls" : " controls-hidden"}`} role="dialog" aria-modal="true" aria-label={t("player.playingTitle", { title: item.title })} onPointerMove={revealControls} onPointerDown={revealControls} onFocusCapture={revealControls}>
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
          {selectedSubtitle && <track key={selectedSubtitle.id} src={selectedSubtitle.url} srcLang={selectedSubtitle.language || "und"} label={(selectedSubtitle.language || t("common.fallback.unknown")).toUpperCase()} default />}
        </video> : null}
    </div>

    <header className={`player__header${controlsVisible ? "" : " is-hidden"}`}>
      <div><small>{playerPhaseLabel(phase)} · {modeLabel}</small><strong>{item.title}</strong></div>
      <IconButton label={t("player.actions.close")} onClick={closePlayer} data-player-control><X /></IconButton>
    </header>

    {(loading || phase === "preparing") && <div className="player__loading" aria-live="polite"><span className="player__pulse"><LoaderCircle className="spin" /></span><strong>{t("player.loading.title")}</strong><p>{t("player.loading.description")}</p></div>}
    {(phase === "buffering" || phase === "recovering") && <div className="player__buffering" aria-live="polite"><LoaderCircle className="spin" /><span>{t(phase === "recovering" ? "player.status.recovering" : "player.status.buffering")}</span></div>}
    {seekFeedback && <div key={seekFeedback.id} className={`player__seek-feedback ${seekFeedback.seconds < 0 ? "is-backward" : "is-forward"}`}>{seekFeedback.seconds < 0 ? <RotateCcw /> : <RotateCw />}<span>{t("player.seek.feedbackSeconds", { sign: seekFeedback.seconds > 0 ? "+" : "", seconds: seekFeedback.seconds })}</span></div>}
    {playbackBlocked && phase !== "failed" && <button type="button" className="player__start" onClick={togglePlayback} data-player-control><Play size={30} fill="currentColor" /><span>{t("player.actions.play")}</span></button>}
    {phase === "failed" && <div className="player__failure" role="alert"><ServerCrash size={34} /><strong>{t("player.error.unavailableTitle")}</strong><p>{error || t("player.error.streamPlayFailed")}</p><div><Button onClick={retryPlayback}><RefreshCw size={17} /> {t("common.actions.retry")}</Button><Button variant="secondary" onClick={closePlayer}>{t("common.actions.goBack")}</Button></div></div>}
    {!loading && playable.length === 0 && phase !== "failed" && <div className="player__failure"><ServerCrash size={34} /><strong>{t("player.empty.title")}</strong><p>{error || t("player.empty.description")}</p><Button variant="secondary" onClick={closePlayer}>{t("common.actions.goBack")}</Button></div>}
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
          <button type="button" className="player__control-primary" aria-label={t(paused ? "player.actions.play" : "player.actions.pause")} onClick={togglePlayback} data-player-control>{paused ? <Play size={20} fill="currentColor" /> : <Pause size={20} fill="currentColor" />}</button>
          <button type="button" aria-label={t("player.seek.back10")} onClick={() => seekBy(-10)} data-player-control><RotateCcw size={19} /><small>10</small></button>
          <button type="button" aria-label={t("player.seek.forward10")} onClick={() => seekBy(10)} data-player-control><RotateCw size={19} /><small>10</small></button>
          <button type="button" aria-label={t(muted ? "player.volume.unmute" : "player.volume.mute")} onClick={toggleMute} data-player-control>{muted ? <VolumeX size={19} /> : <Volume2 size={19} />}</button>
          <input className="player__volume" type="range" aria-label={t("player.volume.label")} min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={(event) => changeVolume(Number(event.target.value))} data-player-control />
        </div>
        <div className="player__mode"><span>{modeLabel}</span><small>{stream?.media?.videoTracks[0]?.height ? `${stream.media.videoTracks[0].height}p` : stream?.protocol?.toUpperCase()}</small></div>
        <div className="player__controls-group player__controls-group--right">
          {playable.length > 1 && <button type="button" aria-label={t("player.panel.sources")} className={panel === "sources" ? "is-active" : ""} onClick={() => togglePanel("sources")} data-player-control><Settings2 size={19} /></button>}
          {audioTracks.length > 1 && <button type="button" aria-label={t("player.panel.audio")} className={panel === "audio" ? "is-active" : ""} onClick={() => togglePanel("audio")} data-player-control><AudioLines size={19} /></button>}
          {subtitles.length > 0 && <button type="button" aria-label={t("player.panel.subtitles")} className={panel === "subtitles" ? "is-active" : ""} onClick={() => togglePanel("subtitles")} data-player-control><Captions size={19} /></button>}
          <button type="button" aria-label={t("player.speed.currentLabel", { rate: playbackRate })} className={panel === "speed" ? "is-active" : ""} onClick={() => togglePanel("speed")} data-player-control><Gauge size={19} /><small>{playbackRate}×</small></button>
          <button type="button" aria-label={t("player.panel.diagnostics")} className={panel === "stats" ? "is-active" : ""} onClick={() => togglePanel("stats")} data-player-control><Info size={19} /></button>
          {document.pictureInPictureEnabled && <button type="button" aria-label={t("player.pictureInPicture.label")} onClick={() => void togglePictureInPicture()} data-player-control><PictureInPicture size={19} /></button>}
          {fullscreenSupported && <button type="button" className="player__fullscreen" aria-label={t(fullscreenKind === "none" ? "player.fullscreen.enter" : "player.fullscreen.exit")} onClick={() => void toggleFullscreen()} data-player-control>{fullscreenKind === "none" ? <Maximize size={19} /> : <Minimize size={19} />}</button>}
        </div>
      </div>
    </div>}

    {panel && <section className="player__panel" aria-label={panelTitle}>
      <header><div><small>{t("player.settings.eyebrow")}</small><strong>{panelTitle}</strong></div><button type="button" aria-label={t("player.settings.close")} onClick={() => setPanel(null)} data-player-control><X size={17} /></button></header>
      {panel === "sources" && <div className="player__option-list">{playable.map((candidate, index) => {
        const video = candidate.media?.videoTracks[0];
        const candidateMode = playerModeLabel(candidate.mode, Boolean(candidate.media?.hdrFormat && candidate.media.hdrFormat !== "sdr" && candidate.mode === "transcode"));
        return <button key={candidate.id} type="button" className={selected === index ? "is-active" : ""} onClick={() => selectSource(index)} data-player-control><span><strong>{candidate.name || candidate.title || t("player.sources.fallbackName", { number: index + 1 })}</strong><small>{candidateMode} · {video?.height ? `${video.height}p` : candidate.protocol.toUpperCase()} {video?.codec ? `· ${video.codec.toUpperCase()}` : ""}</small></span>{selected === index && <Check size={17} />}</button>;
      })}</div>}
      {panel === "audio" && <div className="player__option-list">{audioTracks.map((track) => <button key={track.index} type="button" className={selectedAudioTrack === track.index ? "is-active" : ""} onClick={() => {
        const video = videoRef.current;
        if (video) resumePositionRef.current = Math.floor(playbackOffsetRef.current + video.currentTime);
        setSelectedAudioTrack(track.index);
        setPreferredAudioTrack(track.index);
        setPanel(null);
        setPhase("recovering");
      }} data-player-control><span><strong>{track.title || track.language?.toUpperCase() || t("player.audio.fallbackTrack", { number: track.index + 1 })}</strong><small>{playerTrackLabel(track)}</small></span>{selectedAudioTrack === track.index && <Check size={17} />}</button>)}</div>}
      {panel === "subtitles" && <div className="player__option-list"><button type="button" className={selectedSubtitleID === "none" ? "is-active" : ""} onClick={() => { setSelectedSubtitleID("none"); setPanel(null); }} data-player-control><span><strong>{t("player.subtitles.off")}</strong><small>{t("player.subtitles.none")}</small></span>{selectedSubtitleID === "none" && <Check size={17} />}</button>{subtitles.map((subtitle) => <button key={subtitle.id} type="button" className={selectedSubtitleID === subtitle.id ? "is-active" : ""} onClick={() => { setSelectedSubtitleID(subtitle.id); setPanel(null); }} data-player-control><span><strong>{(subtitle.language || t("common.fallback.unknown")).toUpperCase()}</strong><small>{t(subtitle.default ? "player.subtitles.defaultTrack" : "player.subtitles.track")}</small></span>{selectedSubtitleID === subtitle.id && <Check size={17} />}</button>)}</div>}
      {panel === "speed" && <div className="player__speed-grid">{playbackRates.map((rate) => <button key={rate} type="button" className={playbackRate === rate ? "is-active" : ""} onClick={() => changePlaybackRate(rate)} data-player-control>{rate}×</button>)}</div>}
      {panel === "stats" && <dl className="player__stats">
        <div><dt>{t("player.diagnostics.status")}</dt><dd>{playerPhaseLabel(phase)}</dd></div>
        <div><dt>{t("player.diagnostics.mode")}</dt><dd>{modeLabel}</dd></div>
        <div><dt>{t("player.diagnostics.protocol")}</dt><dd>{stream?.protocol?.toUpperCase() || "—"}{stream?.container ? ` / ${stream.container.toUpperCase()}` : ""}</dd></div>
        <div><dt>{t("player.diagnostics.video")}</dt><dd>{stats.width && stats.height ? `${stats.width}×${stats.height}` : "—"}{stream?.media?.videoTracks[0]?.codec ? ` · ${stream.media.videoTracks[0].codec.toUpperCase()}` : ""}</dd></div>
        <div><dt>{t("player.diagnostics.audio")}</dt><dd>{audioTracks.find((track) => track.index === selectedAudioTrack)?.codec.toUpperCase() || audioTracks[0]?.codec.toUpperCase() || "—"}</dd></div>
        <div><dt>{t("player.diagnostics.hdr")}</dt><dd>{stream?.media?.hdrFormat?.toUpperCase() || "SDR"}</dd></div>
        <div><dt>{t("player.diagnostics.buffer")}</dt><dd>{stats.bufferedAhead.toFixed(1)}s</dd></div>
        <div><dt>{t("player.diagnostics.droppedFrames")}</dt><dd>{stats.droppedFrames} / {stats.totalFrames}</dd></div>
      </dl>}
    </section>}
  </div>, document.body);
}
