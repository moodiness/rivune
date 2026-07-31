import { AudioLines, Bookmark, Captions, Check, Clapperboard, ExternalLink, Eye, EyeOff, Gauge, Info, ListVideo, LoaderCircle, Maximize, Minimize, Pause, PictureInPicture, Play, RefreshCw, RotateCcw, RotateCw, ServerCrash, Settings2, SkipForward, Star, Volume2, VolumeX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from "react";
import { createPortal } from "react-dom";
import { api, APIError } from "./api";
import { Button, IconButton, Modal, Notice } from "./components";
import { notifyError, notifyErrorMessage, notifySuccess } from "./notifications";
import type { EpisodeMetadata, MediaItem, PlaybackCapabilities, PlaybackPreparation, PlaybackProgress, PlaybackSource, PlaybackSourceOption, PlaybackSubtitle, ResourceBatch, SeasonMetadata, SeriesMetadata, TrailerMetadata } from "./types";

function record(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.fromEntries(Object.entries(value));
}

function trailerLanguageBadge(trailer: TrailerMetadata): string {
  const language = trailer.language.trim().replaceAll("_", "-").toLowerCase();
  if (language === "fr" || language.startsWith("fr-")) return "Français";
  if (language === "en" || language.startsWith("en-")) return "English";
  return language.toUpperCase();
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
  const mode = preparation.mode === "direct" ? "Direct play"
    : preparation.mode === "remux" ? "Lossless remux"
      : preparation.mode === "transcode_audio" ? "Audio conversion"
        : preparation.mode === "transcode" ? "Video conversion"
          : preparation.mode === "youtube" ? "YouTube"
            : "External player";
  const video = preparation.media?.videoTracks[0];
  const resolution = video?.height ? `${video.height}p` : "";
  const codec = video?.codec ? video.codec.toUpperCase() : "";
  return [mode, resolution, codec].filter(Boolean).join(" · ");
}
function webPlaybackCapabilities(): PlaybackCapabilities {
  const video = document.createElement("video");
  const containers: string[] = [];
  const videoCodecs: string[] = [];
  const audioCodecs: string[] = [];
  if (video.canPlayType('video/mp4; codecs="avc1.42E01E"')) {
    containers.push("mp4", "m4v", "mov");
    videoCodecs.push("h264");
    audioCodecs.push("aac");
  }
  if (video.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') || video.canPlayType('video/mp4; codecs="hev1.1.6.L93.B0"')) {
    if (!containers.includes("mp4")) containers.push("mp4", "m4v", "mov");
    videoCodecs.push("h265");
  }
  if (video.canPlayType('video/webm; codecs="vp9"')) {
    containers.push("webm");
    videoCodecs.push("vp9");
  }
  const streamingProtocols = ["http", "youtube"];
  if (video.canPlayType("application/vnd.apple.mpegurl") || "MediaSource" in window) streamingProtocols.push("hls");
  return { streamingProtocols, containers, videoCodecs, audioCodecs, hdrFormats: ["sdr"] };
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
  if (mediaType === "tv") return "Live TV";
  if (mediaType === "series") return "Series";
  if (mediaType === "episode") return "Episode";
  return "Movie";
}

const DETAIL_ID_PROVIDERS = [
  { key: "imdb", label: "IMDb" },
  { key: "tmdb", label: "TMDB" },
  { key: "tvdb", label: "TVDB" },
] as const;

function detailProviderURL(
  provider: typeof DETAIL_ID_PROVIDERS[number]["key"],
  externalID: string,
  mediaType: string,
  episode?: Pick<EpisodeMetadata, "seasonNumber" | "episodeNumber">,
): string {
  const id = encodeURIComponent(externalID);
  if (provider === "imdb") return `https://www.imdb.com/title/${id}/`;
  if (provider === "tmdb") {
    if (mediaType === "episode" && episode) {
      return `https://www.themoviedb.org/tv/${id}/season/${episode.seasonNumber}/episode/${episode.episodeNumber}`;
    }
    return `https://www.themoviedb.org/${mediaType === "movie" ? "movie" : "tv"}/${id}`;
  }
  const kind = mediaType === "movie" ? "movie" : mediaType === "episode" ? "episode" : "series";
  return `https://thetvdb.com/dereferrer/${kind}/${id}`;
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
    title: `${series.name} · S${String(episode.seasonNumber).padStart(2, "0")}E${String(episode.episodeNumber).padStart(2, "0")} · ${episode.name}`,
    posterUrl: episode.stillUrl || fallback.posterUrl,
    backgroundUrl: episode.stillUrl || fallback.backgroundUrl,
    description: episode.overview,
    releaseInfo: episode.airDate,
    released: episode.airDate,
    externalIds: episode.externalIds,
  };
}

function episodeIsUpcoming(episode: EpisodeMetadata): boolean {
  if (!episode.airDate) return false;
  const airDate = new Date(`${episode.airDate}T23:59:59Z`);
  return Number.isFinite(airDate.getTime()) && airDate.getTime() > Date.now();
}

export function MediaDetails({ item, onClose }: { item: MediaItem; onClose: () => void }) {
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
  const [titleProgress, setTitleProgress] = useState<PlaybackProgress>();
  const [watchedBusy, setWatchedBusy] = useState("");
  const nextSourceRef = useRef<SourceIdentity | undefined>(undefined);
  const selectedTrailerSeason = item.mediaType === "series" ? series?.seasons.find((candidate) => candidate.id === seasonID) : undefined;
  const trailerItemKey = `${item.mediaType}:${item.titleId ?? item.id}:${selectedTrailerSeason ? `season:${selectedTrailerSeason.seasonNumber}` : "title"}`;
  trailerItemRef.current = trailerItemKey;
  const activeTrailers = trailerOwnerKey === trailerItemKey ? trailers : [];
  const activeTrailer = trailerOwnerKey === trailerItemKey ? selectedTrailer : undefined;
  const activeTrailerLoading = trailerOwnerKey === trailerItemKey && trailerLoading;
  const streamResourceID = selectedEpisode && series ? episodeResourceID(series, selectedEpisode, item.id) : item.id;
  const playbackMediaType = selectedEpisode || item.mediaType === "episode" ? "episode" : item.mediaType;
  const continueSeriesID = typeof item.raw?.continueSeriesId === "string" ? item.raw.continueSeriesId : "";
  const continueSeasonID = typeof item.raw?.continueSeasonId === "string" ? item.raw.continueSeasonId : "";
  const continueEpisodeID = typeof item.raw?.continueEpisodeId === "string" ? item.raw.continueEpisodeId : "";
  const continueSeasonNumber = typeof item.raw?.continueSeasonNumber === "number" ? item.raw.continueSeasonNumber : undefined;
  const continueEpisodeNumber = typeof item.raw?.continueEpisodeNumber === "number" ? item.raw.continueEpisodeNumber : undefined;
  const selectedProgress = selectedEpisode ? episodeProgress[selectedEpisode.id] : titleProgress;
  const preparationStartSeconds = selectedProgress?.completed ? 0 : Math.max(0, Math.floor(selectedProgress?.positionSeconds ?? 0));
  const fromContinue = item.raw?.continueReason === "resume" || item.raw?.continueReason === "next_episode";
  const autoplayNextEpisode = document.documentElement.dataset.autoplayNextEpisode !== "false";

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
    }).catch(() => undefined).finally(() => { if (active) setMetaLoading(false); });
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
        voteCount: movie.voteCount,
        externalIds: { ...current.externalIds, ...movie.externalIds },
      }));
    })().catch(() => undefined);
    return () => { active = false; };
  }, [item.id, item.mediaType, item.titleId]);

  useEffect(() => {
    let active = true;
    if (!seriesVisible) {
      setSeriesLoading(false);
      return;
    }
    setSeriesLoading(true);
    setSeriesError("");
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
        voteCount: resolved.voteCount,
        externalIds: { ...resolved.externalIds, ...current.externalIds },
      }));
      const seasons = [...resolved.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber);
      const requestedSeason = seasons.find((candidate) => candidate.id === continueSeasonID)
        ?? (continueSeasonNumber !== undefined ? seasons.find((candidate) => candidate.seasonNumber === continueSeasonNumber) : undefined);
      const initial = requestedSeason
        ?? seasons.find((candidate) => candidate.seasonNumber > 0)
        ?? seasons[0];
      setSeasonID(initial?.id ?? "");
      if (resolved.mappingProvider === "tvdb" && continueEpisodeID && !requestedSeason) {
        for (const candidate of seasons) {
          let mappedSeason: SeasonMetadata;
          try {
            mappedSeason = await api.seasonDetails(candidate.id);
          } catch {
            continue;
          }
          if (!active) return;
          seasonCacheRef.current.set(candidate.id, mappedSeason);
          if (mappedSeason.episodes.some((episode) => episode.id === continueEpisodeID)) {
            setSeasonID(candidate.id);
            break;
          }
        }
      }
    })().catch((cause) => {
      if (active) setSeriesError(notifyError(cause, "Seasons and episodes could not be loaded.", "Series unavailable"));
    }).finally(() => { if (active) setSeriesLoading(false); });
    return () => { active = false; };
  }, [continueEpisodeID, continueSeasonID, continueSeasonNumber, continueSeriesID, item.id, item.mediaType, item.titleId, seriesVisible]);

  useEffect(() => {
    let active = true;
    if (!seasonID) {
      setSeason(undefined);
      return;
    }
    setSeasonLoading(true);
    setSelectedEpisode(undefined);
    setEpisodeProgress({});
    void (seasonCacheRef.current.has(seasonID) ? Promise.resolve(seasonCacheRef.current.get(seasonID)!) : api.seasonDetails(seasonID)).then(async (resolved) => {
      if (!active) return;
      setSeason(resolved);
      if (autoPlayNextRef.current) {
        const first = resolved.episodes.find((episode) => !episodeIsUpcoming(episode));
        if (first) setSelectedEpisode(first);
        else autoPlayNextRef.current = false;
      } else if (item.mediaType === "episode") {
        const requested = continueEpisodeID
          ? resolved.episodes.find((episode) => episode.id === continueEpisodeID)
          : undefined;
        const requestedByNumber = continueEpisodeNumber !== undefined
          ? resolved.episodes.find((episode) => episode.episodeNumber === continueEpisodeNumber)
          : undefined;
        setSelectedEpisode(requested ?? requestedByNumber ?? resolved.episodes[0]);
      }
      const progressEntries = await Promise.all(resolved.episodes.map(async (episode) => [episode.id, await api.progress(episode.id).catch(() => undefined)] as const));
      if (!active) return;
      setEpisodeProgress(Object.fromEntries(progressEntries));
    }).catch((cause) => {
      if (active) setSeriesError(notifyError(cause, "Episodes could not be loaded.", "Season unavailable"));
    }).finally(() => { if (active) setSeasonLoading(false); });
    return () => { active = false; };
  }, [continueEpisodeID, continueEpisodeNumber, item.mediaType, seasonID]);
  useEffect(() => {
    if (item.mediaType !== "episode" || !selectedEpisode) return;
    const episodeCode = `S${String(selectedEpisode.seasonNumber).padStart(2, "0")}E${String(selectedEpisode.episodeNumber).padStart(2, "0")}`;
    setDetails((current) => ({
      ...current,
      title: [series?.name, episodeCode, selectedEpisode.name].filter(Boolean).join(" · "),
      description: selectedEpisode.overview || current.description,
      posterUrl: selectedEpisode.stillUrl || current.posterUrl,
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
    if (item.mediaType === "series" && !selectedEpisode) {
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
      setStreamsError(notifyError(cause, "Streams could not be loaded.", "Streams unavailable"));
    }).finally(() => { if (active) setStreamsLoading(false); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [item.mediaType, playbackMediaType, selectedEpisode, streamRefreshVersion, streamResourceID]);

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
      setPreparationError(notifyError(cause, "The selected stream could not be prepared.", "Stream unavailable"));
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
      notifySuccess(removing ? `${details.title} has been removed from your library.` : `${details.title} has been added to your library.`, removing ? "Removed from library" : "Added to library");
    } catch (cause) {
      setActionError(notifyError(cause, "Your library could not be updated.", "Library not updated"));
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
      const resolvedTitleID = await resolveMediaTitle(item);
      if (!requestIsCurrent()) return;
      setTitleID(resolvedTitleID);
      trailerRequested = true;
      const metadata = await api.trailers(resolvedTitleID, requestedSeasonNumber);
      if (!requestIsCurrent()) return;
      const nextTrailers = Array.isArray(metadata.trailers) ? metadata.trailers : [];
      if (nextTrailers.length === 0) {
        setTrailerUnavailable(true);
        setTrailerMessage(requestedSeasonNumber === undefined ? "No trailer is available for this title." : `No trailer is available for ${requestedSeasonNumber === 0 ? "Specials" : `Season ${requestedSeasonNumber}`}.`);
        return;
      }
      setTrailers(nextTrailers);
      setSelectedTrailer(nextTrailers[0]);
    } catch (cause) {
      if (!requestIsCurrent()) return;
      if (trailerRequested && cause instanceof APIError && cause.status === 404) {
        setTrailerUnavailable(true);
        setTrailerMessage(requestedSeasonNumber === undefined ? "No trailer is available for this title." : `No trailer is available for ${requestedSeasonNumber === 0 ? "Specials" : `Season ${requestedSeasonNumber}`}.`);
      } else {
        setTrailerMessage(notifyError(cause, "The trailer could not be loaded.", "Trailer unavailable"));
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
      notifySuccess(watched ? `${details.title} is marked as watched.` : `${details.title} is marked as unwatched.`, watched ? "Marked as watched" : "Marked as unwatched");
    } catch (cause) {
      setActionError(notifyError(cause, "The watched state could not be updated.", "Watch state not updated"));
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
      setActionError(notifyError(cause, "The episode watch state could not be updated.", "Watch state not updated"));
    } finally {
      setWatchedBusy("");
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
      notifySuccess(watched ? "Every available episode in this season is marked as watched." : "Every episode in this season is marked as unwatched.", watched ? "Season watched" : "Season unwatched");
    } catch (cause) {
      const refreshed = await Promise.all(episodes.map(async (episode) => [episode.id, await api.progress(episode.id).catch(() => undefined)] as const));
      setEpisodeProgress(Object.fromEntries(refreshed));
      setActionError(notifyError(cause, "Some episode watch states could not be updated.", "Season not fully updated"));
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
    ? "Preferred subtitles requested when available"
    : activeTrailer?.isFallback ? "Fallback language" : "Preferred language";

  if (playing && selectedStream) {
    return <Player item={activePlayerItem} sourceRef={selectedStream.sourceRef} startSeconds={preparationStartSeconds} autoplayNextEpisode={autoplayNextEpisode} onClose={() => setPlaying(false)} onSourceExpired={() => { setPlaying(false); setStreamRefreshVersion((version) => version + 1); }} onEnded={selectedEpisode ? handleEpisodeEnded : undefined} />;
  }

  const typeLabel = mediaTypeLabel(details.mediaType);
  return <Modal onClose={closeDetails} className="details-modal">
    <div className="details-hero" style={backdrop ? { backgroundImage: `url(${backdrop})` } : undefined}><div className="details-hero__shade" /></div>
    <div className="details-content">
      {details.logoUrl ? <img className="details-logo" src={details.logoUrl} alt={details.title} /> : <h1>{details.title}</h1>}
      <div className="details-meta">
        {details.releaseInfo && details.releaseInfo !== typeLabel && <span>{details.releaseInfo}</span>}
        {details.voteAverage !== undefined && <span className="rating"><Star size={14} fill="currentColor" /> {details.voteAverage.toFixed(1)}</span>}
        <span>{typeLabel}</span>
        {genres.map((genre) => <span key={genre}>{genre}</span>)}
      </div>
      {(item.mediaType === "movie" || item.mediaType === "series" || item.mediaType === "episode") && <div className="details-provider-badges" role="group" aria-label="External title pages">
        {DETAIL_ID_PROVIDERS.map((provider) => {
          const externalID = item.mediaType === "episode"
            ? provider.key === "tmdb" ? series?.externalIds.tmdb : selectedEpisode?.externalIds[provider.key]
            : details.externalIds?.[provider.key];
          if (!externalID) return null;
          const label = `Open ${provider.label} title page · ID ${externalID}`;
          return <a key={provider.key} className={`details-provider-badge details-provider-badge--${provider.key}`} href={detailProviderURL(provider.key, externalID, item.mediaType, selectedEpisode)} target="_blank" rel="noreferrer" aria-label={label} title={label}>
            <span className="details-provider-badge__brand">{provider.label}</span>
            <ExternalLink size={11} aria-hidden="true" />
          </a>;
        })}
      </div>}
      {metaLoading && !details.description ? <div className="details-loading"><LoaderCircle className="spin" size={18} /> Loading details</div> : <p className="details-description">{details.description || "No synopsis is available for this title."}</p>}
      {fromContinue && (item.mediaType === "movie" || item.mediaType === "episode") && <div className="details-context-actions">
        <Button variant="secondary" loading={watchedBusy === titleID} onClick={() => void toggleTitleWatched()}>{titleProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}{titleProgress?.completed ? "Mark unwatched" : "Mark watched"}</Button>
        {item.mediaType === "episode" && <Button variant="secondary" onClick={() => setSeriesVisible((visible) => !visible)}><ListVideo size={19} />{seriesVisible ? "Hide series & season" : "View series & season"}</Button>}
      </div>}
      {seriesVisible && <section className="series-browser">
        <header><div><ListVideo size={18} /><span>Episodes</span></div></header>
        {seriesLoading ? <div className="series-browser__loading"><LoaderCircle className="spin" size={18} /> Loading seasons…</div> : seriesError && !series ? <Notice>{seriesError}</Notice> : series && <>
          <div className="season-tabs" role="tablist" aria-label="Seasons">{[...series.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber).map((candidate) => <button key={candidate.id} type="button" role="tab" aria-selected={seasonID === candidate.id} className={seasonID === candidate.id ? "is-active" : ""} onClick={() => { autoPlayNextRef.current = false; setSeasonID(candidate.id); }}>{candidate.seasonNumber === 0 ? "Specials" : `Season ${candidate.seasonNumber}`}<small>{candidate.episodeCount} episodes</small></button>)}</div>
          {seasonLoading ? <div className="series-browser__loading"><LoaderCircle className="spin" size={18} /> Loading episodes…</div> : <>
            <div className="season-watch-state"><span>{watchedEpisodeCount} of {availableSeasonEpisodes.length} watched</span><button type="button" disabled={availableSeasonEpisodes.length === 0 || watchedBusy === seasonID} onClick={() => void toggleSeasonWatched()}>{watchedBusy === seasonID ? <LoaderCircle className="spin" size={15} /> : allSeasonWatched ? <EyeOff size={15} /> : <Eye size={15} />}{allSeasonWatched ? "Mark season unwatched" : "Mark season watched"}</button></div>
            <div className="episode-list">{orderedEpisodes.map((episode) => {
            const progress = episodeProgress[episode.id];
            const upcoming = episodeIsUpcoming(episode);
            const progressPercent = progress && progress.durationSeconds > 0 ? Math.min(100, progress.positionSeconds / progress.durationSeconds * 100) : 0;
            return <div key={episode.id} className={selectedEpisode?.id === episode.id ? "is-selected" : ""}>
              <button type="button" className="episode-main" disabled={upcoming} onClick={() => { autoPlayNextRef.current = false; setSelectedEpisode(episode); }}>
                <span className="episode-number">{episode.episodeNumber}</span>{episode.stillUrl ? <img src={episode.stillUrl} alt="" loading="lazy" /> : <span className="episode-placeholder"><Play size={18} /></span>}
                <span className="episode-copy"><strong>{episode.name || `Episode ${episode.episodeNumber}`}</strong><small>{episode.runtimeMinutes ? `${episode.runtimeMinutes} min` : ""}{episode.airDate ? ` · ${episode.airDate}` : ""}{upcoming ? " · Upcoming" : ""}</small><p>{episode.overview || "No synopsis is available."}</p>{progressPercent > 0 && <i><span style={{ width: `${progressPercent}%` }} /></i>}</span>
                <Play size={16} />
              </button>
              <button type="button" className={progress?.completed ? "episode-watched is-watched" : "episode-watched"} aria-label={progress?.completed ? `Mark ${episode.name} unwatched` : `Mark ${episode.name} watched`} title={progress?.completed ? "Mark unwatched" : "Mark watched"} disabled={upcoming || watchedBusy === episode.id || watchedBusy === seasonID} onClick={() => void toggleEpisodeWatched(episode)}>{watchedBusy === episode.id ? <LoaderCircle className="spin" size={17} /> : progress?.completed ? <Check size={17} /> : <Eye size={17} />}</button>
            </div>;
          })}</div></>}
        </>}
      </section>}
      {(item.mediaType !== "series" || selectedEpisode) && <div className="details-stream-selector">
        <div className="details-stream-selector__header"><strong>Choose a stream</strong><div><span>{streamsLoading ? "Loading…" : `${availableStreams.length} available`}</span><IconButton label="Refresh streams" disabled={streamsLoading} onClick={() => { autoPlayNextRef.current = false; setStreamRefreshVersion((version) => version + 1); }}><RefreshCw className={streamsLoading ? "spin" : undefined} size={16} /></IconButton></div></div>
        {streamsLoading ? <div className="details-stream-selector__loading"><LoaderCircle className="spin" size={18} /> Loading streams</div> :
          availableStreams.length > 0 ? <div className="details-stream-list" role="radiogroup" aria-label="Available streams">{availableStreams.map((option) =>
            <button key={option.sourceRef} type="button" role="radio" aria-checked={selectedStream?.sourceRef === option.sourceRef} className={selectedStream?.sourceRef === option.sourceRef ? "is-selected" : ""} onClick={() => { autoPlayNextRef.current = false; setSelectedStream(option); }}>
              <span><strong>{option.name}</strong>{option.description && <small>{option.description}</small>}{!option.description && option.filename && <small>{option.filename}</small>}</span>
              {selectedStream?.sourceRef === option.sourceRef && <span className="details-stream-list__state">{preparationLoading ? <LoaderCircle className="spin" size={17} /> : preparation ? <><Check size={17} /><small>{preparationLabel(preparation)}</small></> : preparationError ? <small>Unavailable</small> : <small>Selected</small>}</span>}
            </button>)}</div> :
          <Notice>{streamsError || "No stream is available for this title."}</Notice>}
        {preparationError && <Notice>{preparationError}</Notice>}
      </div>}
      <div className="details-actions">
        <Button disabled={!selectedStream || !preparation} loading={preparationLoading} onClick={() => setPlaying(true)}><Play size={19} fill="currentColor" /> {selectedEpisode ? "Play episode" : "Play selected stream"}</Button>
        <Button variant="secondary" loading={saving} onClick={() => void toggleLibrary()}>{saved ? <Check size={19} /> : <Bookmark size={19} />}{saved ? "In your library" : "Add to library"}</Button>
        {item.mediaType === "movie" && !fromContinue && <Button variant="secondary" loading={watchedBusy === titleID} onClick={() => void toggleTitleWatched()}>{titleProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}{titleProgress?.completed ? "Mark unwatched" : "Mark watched"}</Button>}
        {(item.mediaType === "movie" || item.mediaType === "series") && <Button type="button" variant="secondary" disabled={Boolean(activeTrailer)} loading={activeTrailerLoading} aria-label={activeTrailerLoading ? "Loading trailers" : "Trailers"} aria-busy={activeTrailerLoading} aria-controls="details-trailer" aria-expanded={Boolean(activeTrailer)} onClick={() => void showTrailer()}><Clapperboard size={19} /> Trailers</Button>}
      </div>
      {activeTrailer && <section id="details-trailer" className="details-trailer" aria-label={`Trailers for ${details.title}`}>
        <header className="details-trailer__header">
          <span className="details-trailer__heading"><Clapperboard size={17} /><span><strong>Trailers</strong><small>{activeTrailers.length > 1 ? "Choose a version" : "Now playing"}</small></span></span>
          <IconButton label="Dismiss trailer" onClick={dismissTrailer}><X size={17} /></IconButton>
        </header>
        <div className="details-trailer__frame"><iframe key={`${activeTrailer.youtubeId}:${activeTrailer.captionPreference ?? ""}`} src={trailerURL} title={`${activeTrailer.name || "Trailer"} — ${details.title}`} allow="autoplay; encrypted-media; picture-in-picture" referrerPolicy="strict-origin-when-cross-origin" allowFullScreen /></div>
        <div className="details-trailer__active">
          <span className={activeTrailer.isFallback ? "details-trailer__badge" : "details-trailer__badge is-preferred"}>{activeTrailerBadge}</span>
          <span><strong title={activeTrailer.name || "Trailer"}>{activeTrailer.name || "Trailer"}</strong><small>{trailerAvailability}</small></span>
        </div>
        {activeTrailers.length > 1 && <div className="details-trailer__chooser">
          <div className="details-trailer__chooser-heading"><strong>Choose a trailer</strong><span>{activeTrailers.length} results</span></div>
          <div className="details-trailer__options" role="radiogroup" aria-label={`Available trailers for ${details.title}`}>
            {activeTrailers.map((option, index) => {
              const selected = option.youtubeId === activeTrailer.youtubeId;
              const badge = trailerLanguageBadge(option);
              return <button key={option.youtubeId} type="button" role="radio" aria-checked={selected} tabIndex={selected ? 0 : -1} aria-label={`${option.name || "Trailer"}, ${badge}${option.isFallback ? "" : ", preferred language"}`} className={`${selected ? "is-selected " : ""}${option.isFallback ? "" : "is-preferred"}`.trim()} onClick={() => setSelectedTrailer(option)} onKeyDown={(event) => handleTrailerOptionKeyDown(event, index)}>
                <span className="details-trailer__radio" aria-hidden="true" />
                <strong title={option.name || "Trailer"}>{option.name || "Trailer"}</strong>
                <span className="details-trailer__badge">{badge}</span>
              </button>;
            })}
          </div>
        </div>}
      </section>}
      {trailerOwnerKey === trailerItemKey && trailerMessage && <div className="details-trailer-feedback"><Notice tone={trailerUnavailable ? "info" : "error"}>{trailerMessage}</Notice></div>}
      {actionError && <Notice>{actionError}</Notice>}
      {details.sources && details.sources.length > 0 && <div className="details-sources"><span>Available from</span>{details.sources.map((source) => <i key={source.id}>{source.title}</i>)}</div>}
    </div>
  </Modal>;
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
  if (mode === "direct") return "Direct play";
  if (mode === "remux") return "Lossless remux";
  if (mode === "transcode_audio") return "Audio conversion";
  if (mode === "transcode") return toneMapped ? "HDR conversion" : "Video conversion";
  if (mode === "youtube") return "YouTube";
  if (mode === "external") return "External player";
  return "Playback";
}

function playerPhaseLabel(phase: PlayerPhase): string {
  return {
    preparing: "Preparing",
    ready: "Ready",
    playing: "Playing",
    paused: "Paused",
    buffering: "Buffering",
    recovering: "Recovering",
    failed: "Failed",
    ended: "Ended",
  }[phase];
}

function playerTrackLabel(track: { codec: string; channels?: number }): string {
  const channelLabel = track.channels ? track.channels === 2 ? "2.0" : `${track.channels} channels` : "";
  return `${track.codec.toUpperCase()}${channelLabel ? ` · ${channelLabel}` : ""}`;
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
      const compatible = resolvedSources.filter((source) => source.compatible && Boolean(source.url || source.ytId));
      const selectedIndex = compatible.findIndex((source) => source.id === session.selectedSourceId);
      setSelected(selectedIndex < 0 ? 0 : selectedIndex);
      setPhase("ready");
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof APIError && cause.code === "playback_source_expired") {
        onSourceExpired();
        return;
      }
      setError(notifyError(cause, "Playback sources are unavailable.", "Playback unavailable"));
      setPhase("failed");
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [item.id, item.titleId, onSourceExpired, preferredAudioTrack, progressReady, retryVersion, sourceRef]);

  const playable = useMemo(() => streams.filter((candidate) => candidate.compatible && Boolean(candidate.url || candidate.ytId)), [streams]);
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
  const customTransport = Boolean(stream?.url);
  const toneMapped = Boolean(stream?.media?.hdrFormat && stream.media.hdrFormat !== "sdr" && stream.mode === "transcode");
  const modeLabel = playerModeLabel(stream?.mode, toneMapped);

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
    if (!progressReady || !video || !stream?.url) return;
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
        setError(notifyError(cause, "The browser could not start media playback.", "Playback unavailable"));
        setPhase("failed");
      });
    };
    const failPlayback = (message: string) => {
      setPaused(true);
      setError(notifyErrorMessage(message, "Playback unavailable"));
      setPhase("failed");
    };
    const handleMediaError = () => {
      if (!startProcessedFallback()) failPlayback("The selected media source could not be played.");
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
            failPlayback("This browser cannot play the selected HLS stream.");
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
          if (!startProcessedFallback()) failPlayback("The selected HLS stream stopped responding.");
        });
        hls.loadSource(sourceURL);
        hls.attachMedia(video);
      }).catch((cause) => {
        if (!disposed && !startProcessedFallback()) {
          setError(notifyError(cause, "The HLS player could not be loaded.", "Playback unavailable"));
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
    if (!stream?.url) return;
    const frame = window.requestAnimationFrame(() => playerRef.current?.querySelector<HTMLElement>(".player__control-primary")?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, [stream?.id, stream?.url]);

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
      setError(notifyErrorMessage("Playback could not recover before the safety timeout.", "Playback recovery timed out"));
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
      if (target instanceof HTMLButtonElement && ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) {
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
          notifyError(refreshCause, "Watch progress could not be synchronized.", "Progress not saved");
        }
      } else {
        notifyError(cause, "Watch progress could not be saved.", "Progress not saved");
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
      setError(notifyError(cause, "The browser could not start media playback.", "Playback unavailable"));
      setPhase("failed");
    });
  }

  function commitSeek(rawPosition: number) {
    const duration = playbackDurationRef.current;
    const target = duration > 0 ? Math.min(duration, Math.max(0, Math.floor(rawPosition))) : Math.max(0, Math.floor(rawPosition));
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
      if (reportFailure) notifyError(cause, "Fullscreen could not be closed.", "Fullscreen unavailable");
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
        notifyError(cause, "Fullscreen could not be opened.", "Fullscreen unavailable");
        return;
      }
    }

    if (typeof video?.webkitEnterFullscreen === "function") {
      try {
        video.webkitEnterFullscreen();
        updateFullscreenKind("webkit");
        return;
      } catch (cause) {
        notifyError(cause, "Fullscreen could not be opened.", "Fullscreen unavailable");
        return;
      }
    }

    unlockPlayerOrientation();
    notifyErrorMessage("This browser does not support fullscreen playback.", "Fullscreen unavailable");
  }

  async function togglePictureInPicture() {
    const video = videoRef.current;
    if (!video || !document.pictureInPictureEnabled) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await video.requestPictureInPicture();
    } catch (cause) {
      notifyError(cause, "Picture in Picture could not be opened.", "Picture in Picture unavailable");
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
      setError(notifyErrorMessage("The selected media source ended before the movie or episode was complete.", "Source ended early"));
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
  const panelTitle = panel === "sources" ? "Sources and quality"
    : panel === "audio" ? "Audio track"
      : panel === "subtitles" ? "Subtitles"
        : panel === "speed" ? "Playback speed"
          : panel === "stats" ? "Playback diagnostics"
            : "";

  return createPortal(<div ref={playerRef} className={`player player--${phase}${controlsVisible ? " has-controls" : " controls-hidden"}`} role="dialog" aria-modal="true" aria-label={`Playing ${item.title}`} onPointerMove={revealControls} onPointerDown={revealControls} onFocusCapture={revealControls}>
    <div className="player__surface" onClick={handleSurfaceClick} onDoubleClick={handleSurfaceDoubleClick} onPointerUp={handleSurfacePointerUp}>
      {stream?.ytId ? <iframe className="player__video" src={`https://www.youtube-nocookie.com/embed/${encodeURIComponent(stream.ytId)}?autoplay=1`} allow="autoplay; encrypted-media; picture-in-picture" allowFullScreen title={item.title} /> :
        stream?.url ? <video key={`${stream.id}:${stream.url}:${playbackStart ?? 0}:${playbackGeneration}`} ref={videoRef} className="player__video" controls={false} playsInline crossOrigin="anonymous"
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
          {selectedSubtitle && <track key={selectedSubtitle.id} src={selectedSubtitle.url} srcLang={selectedSubtitle.language || "und"} label={(selectedSubtitle.language || "Unknown").toUpperCase()} default />}
        </video> : null}
    </div>

    <header className={`player__header${controlsVisible ? "" : " is-hidden"}`}>
      <div><small>{playerPhaseLabel(phase)} · {modeLabel}</small><strong>{item.title}</strong></div>
      <IconButton label="Close player" onClick={closePlayer} data-player-control><X /></IconButton>
    </header>

    {(loading || phase === "preparing") && <div className="player__loading" aria-live="polite"><span className="player__pulse"><LoaderCircle className="spin" /></span><strong>Preparing playback</strong><p>The best compatible stream is being prepared…</p></div>}
    {(phase === "buffering" || phase === "recovering") && <div className="player__buffering" aria-live="polite"><LoaderCircle className="spin" /><span>{phase === "recovering" ? "Recovering playback…" : "Buffering…"}</span></div>}
    {seekFeedback && <div key={seekFeedback.id} className={`player__seek-feedback ${seekFeedback.seconds < 0 ? "is-backward" : "is-forward"}`}>{seekFeedback.seconds < 0 ? <RotateCcw /> : <RotateCw />}<span>{seekFeedback.seconds > 0 ? "+" : ""}{seekFeedback.seconds}s</span></div>}
    {playbackBlocked && phase !== "failed" && <button type="button" className="player__start" onClick={togglePlayback} data-player-control><Play size={30} fill="currentColor" /><span>Play</span></button>}
    {phase === "failed" && <div className="player__failure" role="alert"><ServerCrash size={34} /><strong>Playback unavailable</strong><p>{error || "The selected stream could not be played."}</p><div><Button onClick={retryPlayback}><RefreshCw size={17} /> Retry</Button><Button variant="secondary" onClick={closePlayer}>Go back</Button></div></div>}
    {!loading && playable.length === 0 && phase !== "failed" && <div className="player__failure"><ServerCrash size={34} /><strong>No playable source</strong><p>{error || "The selected stream is not compatible with this device."}</p><Button variant="secondary" onClick={closePlayer}>Go back</Button></div>}
    {showNextEpisode && <button type="button" className="player__next" onClick={playNextEpisode} data-player-control><span>Up next</span><strong>Next episode</strong><small>{autoplayNextEpisode && phase !== "ended" ? `Starts in ${Math.ceil(remainingSeconds)}s` : "Play next episode"}</small><SkipForward size={20} fill="currentColor" /></button>}

    {customTransport && <div className={`player__chrome${controlsVisible ? "" : " is-hidden"}`}>
      <div className="player__timeline-row">
        <span>{formatPlaybackTime(transportTime)}</span>
        <div className="player__timeline-wrap">
          {seekPreview !== null && <output style={{ left: `${progressPercent}%` }}>{formatPlaybackTime(seekPreview)}</output>}
          <input className="player__timeline" style={timelineStyle} type="range" aria-label="Playback position" min={0} max={Math.max(1, Math.floor(playbackDuration))} step={1} value={Math.min(playbackDuration || 1, Math.max(0, transportTime))}
            onChange={(event) => setSeekPreview(Number(event.target.value))}
            onPointerUp={(event) => commitSeek(Number(event.currentTarget.value))}
            onKeyUp={(event) => { if (["ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(event.key)) commitSeek(Number(event.currentTarget.value)); }}
            data-player-control />
        </div>
        <span>{formatPlaybackTime(playbackDuration)}</span>
      </div>
      <div className="player__controls">
        <div className="player__controls-group">
          <button type="button" className="player__control-primary" aria-label={paused ? "Play" : "Pause"} onClick={togglePlayback} data-player-control>{paused ? <Play size={20} fill="currentColor" /> : <Pause size={20} fill="currentColor" />}</button>
          <button type="button" aria-label="Back 10 seconds" onClick={() => seekBy(-10)} data-player-control><RotateCcw size={19} /><small>10</small></button>
          <button type="button" aria-label="Forward 10 seconds" onClick={() => seekBy(10)} data-player-control><RotateCw size={19} /><small>10</small></button>
          <button type="button" aria-label={muted ? "Unmute" : "Mute"} onClick={toggleMute} data-player-control>{muted ? <VolumeX size={19} /> : <Volume2 size={19} />}</button>
          <input className="player__volume" type="range" aria-label="Volume" min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={(event) => changeVolume(Number(event.target.value))} data-player-control />
        </div>
        <div className="player__mode"><span>{modeLabel}</span><small>{stream?.media?.videoTracks[0]?.height ? `${stream.media.videoTracks[0].height}p` : stream?.protocol?.toUpperCase()}</small></div>
        <div className="player__controls-group player__controls-group--right">
          {playable.length > 1 && <button type="button" aria-label="Sources and quality" className={panel === "sources" ? "is-active" : ""} onClick={() => togglePanel("sources")} data-player-control><Settings2 size={19} /></button>}
          {audioTracks.length > 1 && <button type="button" aria-label="Audio track" className={panel === "audio" ? "is-active" : ""} onClick={() => togglePanel("audio")} data-player-control><AudioLines size={19} /></button>}
          {subtitles.length > 0 && <button type="button" aria-label="Subtitles" className={panel === "subtitles" ? "is-active" : ""} onClick={() => togglePanel("subtitles")} data-player-control><Captions size={19} /></button>}
          <button type="button" aria-label={`Playback speed ${playbackRate}x`} className={panel === "speed" ? "is-active" : ""} onClick={() => togglePanel("speed")} data-player-control><Gauge size={19} /><small>{playbackRate}×</small></button>
          <button type="button" aria-label="Playback diagnostics" className={panel === "stats" ? "is-active" : ""} onClick={() => togglePanel("stats")} data-player-control><Info size={19} /></button>
          {document.pictureInPictureEnabled && <button type="button" aria-label="Picture in Picture" onClick={() => void togglePictureInPicture()} data-player-control><PictureInPicture size={19} /></button>}
          {fullscreenSupported && <button type="button" className="player__fullscreen" aria-label={fullscreenKind === "none" ? "Enter fullscreen" : "Exit fullscreen"} onClick={() => void toggleFullscreen()} data-player-control>{fullscreenKind === "none" ? <Maximize size={19} /> : <Minimize size={19} />}</button>}
        </div>
      </div>
    </div>}

    {panel && <section className="player__panel" aria-label={panelTitle}>
      <header><div><small>Player settings</small><strong>{panelTitle}</strong></div><button type="button" aria-label="Close settings" onClick={() => setPanel(null)} data-player-control><X size={17} /></button></header>
      {panel === "sources" && <div className="player__option-list">{playable.map((candidate, index) => {
        const video = candidate.media?.videoTracks[0];
        const candidateMode = playerModeLabel(candidate.mode, Boolean(candidate.media?.hdrFormat && candidate.media.hdrFormat !== "sdr" && candidate.mode === "transcode"));
        return <button key={candidate.id} type="button" className={selected === index ? "is-active" : ""} onClick={() => selectSource(index)} data-player-control><span><strong>{candidate.name || candidate.title || `Source ${index + 1}`}</strong><small>{candidateMode} · {video?.height ? `${video.height}p` : candidate.protocol.toUpperCase()} {video?.codec ? `· ${video.codec.toUpperCase()}` : ""}</small></span>{selected === index && <Check size={17} />}</button>;
      })}</div>}
      {panel === "audio" && <div className="player__option-list">{audioTracks.map((track) => <button key={track.index} type="button" className={selectedAudioTrack === track.index ? "is-active" : ""} onClick={() => {
        const video = videoRef.current;
        if (video) resumePositionRef.current = Math.floor(playbackOffsetRef.current + video.currentTime);
        setSelectedAudioTrack(track.index);
        setPreferredAudioTrack(track.index);
        setPanel(null);
        setPhase("recovering");
      }} data-player-control><span><strong>{track.title || track.language?.toUpperCase() || `Track ${track.index + 1}`}</strong><small>{playerTrackLabel(track)}</small></span>{selectedAudioTrack === track.index && <Check size={17} />}</button>)}</div>}
      {panel === "subtitles" && <div className="player__option-list"><button type="button" className={selectedSubtitleID === "none" ? "is-active" : ""} onClick={() => { setSelectedSubtitleID("none"); setPanel(null); }} data-player-control><span><strong>Off</strong><small>No subtitles</small></span>{selectedSubtitleID === "none" && <Check size={17} />}</button>{subtitles.map((subtitle) => <button key={subtitle.id} type="button" className={selectedSubtitleID === subtitle.id ? "is-active" : ""} onClick={() => { setSelectedSubtitleID(subtitle.id); setPanel(null); }} data-player-control><span><strong>{(subtitle.language || "Unknown").toUpperCase()}</strong><small>{subtitle.default ? "Default subtitle" : "Subtitle track"}</small></span>{selectedSubtitleID === subtitle.id && <Check size={17} />}</button>)}</div>}
      {panel === "speed" && <div className="player__speed-grid">{playbackRates.map((rate) => <button key={rate} type="button" className={playbackRate === rate ? "is-active" : ""} onClick={() => changePlaybackRate(rate)} data-player-control>{rate}×</button>)}</div>}
      {panel === "stats" && <dl className="player__stats">
        <div><dt>Status</dt><dd>{playerPhaseLabel(phase)}</dd></div>
        <div><dt>Mode</dt><dd>{modeLabel}</dd></div>
        <div><dt>Protocol</dt><dd>{stream?.protocol?.toUpperCase() || "—"}{stream?.container ? ` / ${stream.container.toUpperCase()}` : ""}</dd></div>
        <div><dt>Video</dt><dd>{stats.width && stats.height ? `${stats.width}×${stats.height}` : "—"}{stream?.media?.videoTracks[0]?.codec ? ` · ${stream.media.videoTracks[0].codec.toUpperCase()}` : ""}</dd></div>
        <div><dt>Audio</dt><dd>{audioTracks.find((track) => track.index === selectedAudioTrack)?.codec.toUpperCase() || audioTracks[0]?.codec.toUpperCase() || "—"}</dd></div>
        <div><dt>HDR</dt><dd>{stream?.media?.hdrFormat?.toUpperCase() || "SDR"}</dd></div>
        <div><dt>Buffer</dt><dd>{stats.bufferedAhead.toFixed(1)}s</dd></div>
        <div><dt>Dropped frames</dt><dd>{stats.droppedFrames} / {stats.totalFrames}</dd></div>
      </dl>}
    </section>}
  </div>, document.body);
}
