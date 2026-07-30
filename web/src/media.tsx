import { AudioLines, Bookmark, Captions, Check, ChevronDown, ExternalLink, Eye, EyeOff, ListVideo, LoaderCircle, Maximize, Pause, Play, RefreshCw, ServerCrash, Star, Volume2, VolumeX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, APIError } from "./api";
import { Button, EmptyState, IconButton, Modal, Notice } from "./components";
import { notifyError, notifyErrorMessage, notifySuccess } from "./notifications";
import type { EpisodeMetadata, MediaItem, PlaybackCapabilities, PlaybackPreparation, PlaybackProgress, PlaybackSource, PlaybackSourceOption, PlaybackSubtitle, ResourceBatch, SeasonMetadata, SeriesMetadata } from "./types";

function record(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.fromEntries(Object.entries(value));
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
  });
  return resolved.titleId;
}


export function mediaTypeLabel(mediaType: string): string {
  if (mediaType === "tv") return "Live TV";
  if (mediaType === "series") return "Series";
  if (mediaType === "episode") return "Episode";
  return "Movie";
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
  if (episode.externalIds.imdb) return episode.externalIds.imdb;
  const seriesIMDB = series.externalIds.imdb;
  if (seriesIMDB) return `${seriesIMDB}:${episode.seasonNumber}:${episode.episodeNumber}`;
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
  const [seriesVisible, setSeriesVisible] = useState(item.mediaType === "series");
  const [seriesLoading, setSeriesLoading] = useState(item.mediaType === "series");
  const [seriesError, setSeriesError] = useState("");
  const [seasonID, setSeasonID] = useState("");
  const [season, setSeason] = useState<SeasonMetadata>();
  const [seasonLoading, setSeasonLoading] = useState(false);
  const [episodeOrder, setEpisodeOrder] = useState("aired");
  const [selectedEpisode, setSelectedEpisode] = useState<EpisodeMetadata>();
  const [episodeProgress, setEpisodeProgress] = useState<Record<string, PlaybackProgress | undefined>>({});
  const autoPlayNextRef = useRef(false);
  const sourceRefreshAttemptRef = useRef("");
  const [titleProgress, setTitleProgress] = useState<PlaybackProgress>();
  const [watchedBusy, setWatchedBusy] = useState("");
  const nextSourceRef = useRef<SourceIdentity | undefined>(undefined);
  const streamResourceID = selectedEpisode && series ? episodeResourceID(series, selectedEpisode, item.id) : item.id;
  const playbackMediaType = selectedEpisode || item.mediaType === "episode" ? "episode" : item.mediaType;
  const continueSeriesID = typeof item.raw?.continueSeriesId === "string" ? item.raw.continueSeriesId : "";
  const continueSeasonID = typeof item.raw?.continueSeasonId === "string" ? item.raw.continueSeasonId : "";
  const continueEpisodeID = typeof item.raw?.continueEpisodeId === "string" ? item.raw.continueEpisodeId : "";
  const selectedProgress = selectedEpisode ? episodeProgress[selectedEpisode.id] : titleProgress;
  const preparationStartSeconds = selectedProgress?.completed ? 0 : Math.max(0, Math.floor(selectedProgress?.positionSeconds ?? 0));
  const fromContinue = item.raw?.continueReason === "resume" || item.raw?.continueReason === "next_episode";

  useEffect(() => {
    let active = true;
    void api.resources("meta", item.mediaType === "episode" ? "series" : item.mediaType, item.id).then((batch) => {
      const metas = payloadRecords(batch, "meta");
      const fallback = batch.results.map((result) => record(result.payload.meta)).find((value) => value !== null);
      const meta = metas[0] ?? fallback;
      if (!active || !meta) return;
      setDetails((current) => ({
        ...current,
        title: String(meta.name ?? meta.title ?? current.title),
        description: String(meta.description ?? current.description ?? ""),
        posterUrl: String(meta.poster ?? current.posterUrl ?? ""),
        backgroundUrl: String(meta.background ?? meta.backgroundUrl ?? current.backgroundUrl ?? ""),
        logoUrl: String(meta.logo ?? current.logoUrl ?? ""),
        releaseInfo: String(meta.releaseInfo ?? meta.year ?? current.releaseInfo ?? ""),
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
      const progress = await api.progress(resolvedTitleID).catch(() => undefined);
      if (!active) return;
      setTitleID(resolvedTitleID);
      setTitleProgress(progress);
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
      const seasons = [...resolved.seasons].sort((left, right) => left.seasonNumber - right.seasonNumber);
      const initial = seasons.find((candidate) => candidate.id === continueSeasonID)
        ?? seasons.find((candidate) => candidate.seasonNumber > 0)
        ?? seasons[0];
      setSeasonID(initial?.id ?? "");
    })().catch((cause) => {
      if (active) setSeriesError(notifyError(cause, "Seasons and episodes could not be loaded.", "Series unavailable"));
    }).finally(() => { if (active) setSeriesLoading(false); });
    return () => { active = false; };
  }, [continueSeasonID, continueSeriesID, item.id, item.mediaType, item.titleId, seriesVisible]);

  useEffect(() => {
    let active = true;
    if (!seasonID) {
      setSeason(undefined);
      return;
    }
    setSeasonLoading(true);
    setSelectedEpisode(undefined);
    setEpisodeProgress({});
    void api.seasonDetails(seasonID).then(async (resolved) => {
      if (!active) return;
      setSeason(resolved);
      const progressEntries = await Promise.all(resolved.episodes.map(async (episode) => [episode.id, await api.progress(episode.id).catch(() => undefined)] as const));
      if (!active) return;
      setEpisodeProgress(Object.fromEntries(progressEntries));
      if (item.mediaType === "episode" && seasonID === continueSeasonID && continueEpisodeID) {
        setSelectedEpisode(resolved.episodes.find((episode) => episode.id === continueEpisodeID));
      }
      if (autoPlayNextRef.current) {
        const first = resolved.episodes.find((episode) => !episodeIsUpcoming(episode));
        if (first) setSelectedEpisode(first);
        else autoPlayNextRef.current = false;
      }
    }).catch((cause) => {
      if (active) setSeriesError(notifyError(cause, "Episodes could not be loaded.", "Season unavailable"));
    }).finally(() => { if (active) setSeasonLoading(false); });
    return () => { active = false; };
  }, [seasonID]);
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
      setSaved((value) => !value);
      notifySuccess(removing ? `${details.title} has been removed from your library.` : `${details.title} has been added to your library.`, removing ? "Removed from library" : "Added to library");
    } catch (cause) {
      setActionError(notifyError(cause, "Your library could not be updated.", "Library not updated"));
    } finally {
      setSaving(false);
    }
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


  const orderedEpisodes = useMemo(() => {
    const episodes = [...(season?.episodes ?? [])];
    if (episodeOrder === "aired") return episodes.sort((left, right) => left.episodeNumber - right.episodeNumber);
    return episodes.sort((left, right) => (left.airDate || "9999-99-99").localeCompare(right.airDate || "9999-99-99") || left.episodeNumber - right.episodeNumber);
  }, [episodeOrder, season]);
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

  if (playing && selectedStream) {
    return <Player item={activePlayerItem} sourceRef={selectedStream.sourceRef} startSeconds={preparationStartSeconds} onClose={() => setPlaying(false)} onSourceExpired={() => { setPlaying(false); setStreamRefreshVersion((version) => version + 1); }} onEnded={selectedEpisode ? handleEpisodeEnded : undefined} />;
  }

  const typeLabel = mediaTypeLabel(details.mediaType);
  return <Modal onClose={onClose} className="details-modal">
    <div className="details-hero" style={backdrop ? { backgroundImage: `url(${backdrop})` } : undefined}><div className="details-hero__shade" /></div>
    <div className="details-content">
      {details.logoUrl ? <img className="details-logo" src={details.logoUrl} alt={details.title} /> : <h1>{details.title}</h1>}
      <div className="details-meta">
        {details.releaseInfo && details.releaseInfo !== typeLabel && <span>{details.releaseInfo}</span>}
        {details.voteAverage !== undefined && <span className="rating"><Star size={14} fill="currentColor" /> {details.voteAverage.toFixed(1)}</span>}
        <span>{typeLabel}</span>
        {genres.map((genre) => <span key={genre}>{genre}</span>)}
      </div>
      {metaLoading && !details.description ? <div className="details-loading"><LoaderCircle className="spin" size={18} /> Loading details</div> : <p className="details-description">{details.description || "No synopsis is available for this title."}</p>}
      {fromContinue && (item.mediaType === "movie" || item.mediaType === "episode") && <div className="details-context-actions">
        <Button variant="secondary" loading={watchedBusy === titleID} onClick={() => void toggleTitleWatched()}>{titleProgress?.completed ? <EyeOff size={19} /> : <Eye size={19} />}{titleProgress?.completed ? "Mark unwatched" : "Mark watched"}</Button>
        {item.mediaType === "episode" && <Button variant="secondary" onClick={() => setSeriesVisible((visible) => !visible)}><ListVideo size={19} />{seriesVisible ? "Hide series & season" : "View series & season"}</Button>}
      </div>}
      {seriesVisible && <section className="series-browser">
        <header><div><ListVideo size={18} /><span>Episodes</span></div>{series?.episodeOrders && series.episodeOrders.length > 0 && <label>Order<select aria-label="Episode order" value={episodeOrder} onChange={(event) => setEpisodeOrder(event.target.value)}><option value="aired">Aired</option>{series.episodeOrders.filter((order) => order.type !== "aired" && order.type !== "official").map((order) => <option key={order.id} value={order.type || order.id}>{order.name}</option>)}</select></label>}</header>
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
      </div>
      {actionError && <Notice>{actionError}</Notice>}
      {details.sources && details.sources.length > 0 && <div className="details-sources"><span>Available from</span>{details.sources.map((source) => <i key={source.id}>{source.title}</i>)}</div>}
    </div>
  </Modal>;
}

export function Player({ item, sourceRef, startSeconds, onClose, onSourceExpired, onEnded }: { item: MediaItem; sourceRef: string; startSeconds: number; onClose: () => void; onSourceExpired: () => void; onEnded?: () => void }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [streams, setStreams] = useState<PlaybackSource[]>([]);
  const [subtitles, setSubtitles] = useState<PlaybackSubtitle[]>([]);
  const [selected, setSelected] = useState(0);
  const [loading, setLoading] = useState(true);
  const [progressReady, setProgressReady] = useState(false);
  const [error, setError] = useState("");
  const [controlsOpen, setControlsOpen] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [playbackStart, setPlaybackStart] = useState<number>();
  const [seekPreview, setSeekPreview] = useState<number | null>(null);
  const [paused, setPaused] = useState(false);
  const [muted, setMuted] = useState(false);
  const [volume, setVolume] = useState(1);
  const [preferredAudioTrack, setPreferredAudioTrack] = useState<number>();
  const [selectedAudioTrack, setSelectedAudioTrack] = useState<number>();
  const [selectedSubtitleID, setSelectedSubtitleID] = useState("none");
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
    setError("");
    setSelected(0);
    setCurrentTime(0);
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
      setStreams(session.sources);
      setSubtitles(session.subtitles);
      setSelectedAudioTrack(session.selectedAudioTrack);
      setSelectedSubtitleID(session.selectedSubtitleId || "none");
      const compatible = session.sources.filter((source) => source.compatible && Boolean(source.url || source.ytId));
      const selectedIndex = compatible.findIndex((source) => source.id === session.selectedSourceId);
      setSelected(selectedIndex < 0 ? 0 : selectedIndex);
    }).catch((cause) => {
      if (!active) return;
      if (cause instanceof APIError && cause.code === "playback_source_expired") {
        onSourceExpired();
        return;
      }
      setError(notifyError(cause, "Playback sources are unavailable.", "Playback unavailable"));
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [item.id, item.titleId, onSourceExpired, preferredAudioTrack, progressReady, sourceRef]);
  const playable = useMemo(() => streams.filter((stream) => stream.compatible && Boolean(stream.url || stream.ytId)), [streams]);
  const stream = playable[selected];
  const playbackDuration = stream?.media?.durationSeconds ?? 0;
  playbackDurationRef.current = playbackDuration;
  streamProtocolRef.current = stream?.protocol ?? "";
  const audioTracks = stream?.media?.audioTracks ?? [];
  const customTransport = Boolean(stream?.url && stream.mode !== "direct" && playbackDuration > 0);
  const transportTime = seekPreview ?? currentTime;
  const selectedSubtitle = subtitles.find((subtitle) => subtitle.id === selectedSubtitleID);

  useEffect(() => {
    const video = videoRef.current;
    if (!progressReady) return;
    if (!video || !stream?.url) return;
    const playbackURL = new URL(stream.url, window.location.origin);
    const processed = stream.mode !== "direct";
    const playbackOffset = processed ? Math.max(0, Math.floor(playbackStart ?? resumePositionRef.current)) : 0;
    playbackOffsetRef.current = playbackOffset;
    setCurrentTime(playbackOffset);
    setSeekPreview(null);
    if (playbackOffset > 0) playbackURL.searchParams.set("start", String(playbackOffset));
    const sourceURL = processed ? `${playbackURL.pathname}${playbackURL.search}` : stream.url;
    const fallbackURL = new URL(playbackURL);
    fallbackURL.searchParams.delete("file");
    fallbackURL.searchParams.set("fallback", "1");
    let disposed = false;
    let fallbackStarted = false;
    let destroyHLS = () => {};
    const isHLS = stream.protocol === "hls";
    const usesNativePlayback = !isHLS;
    const startPlayback = () => void video.play().catch(() => undefined);
    const startProcessedFallback = (): boolean => {
      if (disposed || fallbackStarted || stream.mode === "direct") return false;
      fallbackStarted = true;
      destroyHLS();
      destroyHLS = () => {};
      setError("");
      video.src = `${fallbackURL.pathname}${fallbackURL.search}`;
      video.load();
      startPlayback();
      return true;
    };
    const handleMediaError = () => {
      if (!startProcessedFallback()) {
        setError(notifyErrorMessage("The selected media source could not be played.", "Playback unavailable"));
      }
    };
    if (usesNativePlayback) video.addEventListener("error", handleMediaError);

    if (processed && !isHLS) {
      startProcessedFallback();
    } else if (!isHLS) {
      video.src = sourceURL;
      startPlayback();
    } else {
      void import("hls.js").then(({ default: Hls }) => {
        if (disposed) return;
        if (!Hls.isSupported()) {
          if (video.canPlayType("application/vnd.apple.mpegurl")) {
            video.addEventListener("error", handleMediaError);
            video.src = sourceURL;
            startPlayback();
          } else if (!startProcessedFallback()) {
            setError(notifyErrorMessage("This browser cannot play the selected HLS stream.", "Playback unavailable"));
          }
          return;
        }
        const hls = new Hls({
          enableWorker: true,
          autoStartLoad: false,
          startPosition: 0,
        });
        destroyHLS = () => hls.destroy();
        let recoveryAttempts = 0;
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
          hls.startLoad(0);
          startPlayback();
        });
        hls.loadSource(sourceURL);
        hls.attachMedia(video);
        hls.on(Hls.Events.ERROR, (_event, data) => {
          if (!data.fatal || disposed) return;
          if (recoveryAttempts === 0 && data.type === Hls.ErrorTypes.MEDIA_ERROR) {
            recoveryAttempts += 1;
            hls.recoverMediaError();
            return;
          }
          if (recoveryAttempts === 0 && data.type === Hls.ErrorTypes.NETWORK_ERROR) {
            recoveryAttempts += 1;
            hls.startLoad();
            return;
          }
          if (!startProcessedFallback()) {
            setError(notifyErrorMessage("The selected HLS stream stopped responding.", "Playback unavailable"));
          }
        });
      }).catch((cause) => {
        if (!disposed && !startProcessedFallback()) setError(notifyError(cause, "The HLS player could not be loaded.", "Playback unavailable"));
      });
    }

    return () => {
      disposed = true;
      video.removeEventListener("error", handleMediaError);
      destroyHLS();
      video.removeAttribute("src");
      video.load();
    };
  }, [playbackStart, progressReady, stream]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    Array.from(video.textTracks).forEach((track) => {
      track.mode = "showing";
    });
  }, [selectedSubtitleID, stream, subtitles]);

  useEffect(() => () => stopCurrentSession(), []);

  async function persistProgress(completed = false, positionOverride?: number) {
    const video = videoRef.current;
    const titleID = titleIDRef.current;
    if (!video || !titleID || progressRequestRef.current) return;
    const inspectedDuration = playbackDurationRef.current;
    const durationSeconds = inspectedDuration > 0 ? inspectedDuration : streamProtocolRef.current !== "hls" ? video.duration : Number.NaN;
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
    stopCurrentSession();
    onClose();
  }

  function resumePlayback(video: HTMLVideoElement) {
    const position = resumePositionRef.current;
    const duration = playbackDurationRef.current;
    if (playbackOffsetRef.current > 0) return;
    if (position <= 0 || (duration > 0 && position >= duration - 10)) return;
    video.currentTime = position;
    void persistProgress(false, position);
  }

  function trackPlayback(video: HTMLVideoElement) {
    const position = playbackOffsetRef.current + video.currentTime;
    if (seekPreview === null) setCurrentTime(position);
    if (Math.floor(position) - lastSavedPositionRef.current >= 15) void persistProgress(false, position);
  }

  function togglePlayback() {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) void video.play().catch(() => undefined);
    else video.pause();
  }

  function commitSeek(rawPosition: number) {
    const target = Math.min(playbackDurationRef.current, Math.max(0, Math.floor(rawPosition)));
    resumePositionRef.current = target;
    playbackOffsetRef.current = target;
    setSeekPreview(null);
    setCurrentTime(target);
    setPlaybackStart(target);
    void persistProgress(false, target);
  }

  function changeVolume(nextVolume: number) {
    const video = videoRef.current;
    if (!video) return;
    const normalized = Math.min(1, Math.max(0, nextVolume));
    video.volume = normalized;
    video.muted = normalized === 0;
    setVolume(normalized);
    setMuted(video.muted);
  }

  function toggleMute() {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !video.muted;
    setMuted(video.muted);
  }

  function toggleFullscreen() {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
      return;
    }
    const player = videoRef.current?.closest(".player");
    if (player instanceof HTMLElement) void player.requestFullscreen();
  }

  function handlePlaybackStarted(video: HTMLVideoElement) {
    const pausedAt = pausedAtRef.current;
    pausedAtRef.current = 0;
    setPaused(false);
    if (stream?.protocol !== "hls" || stream.mode === "direct" || pausedAt === 0 || Date.now() - pausedAt < 30_000) return;
    const position = Math.floor(playbackOffsetRef.current + video.currentTime);
    resumePositionRef.current = position;
    playbackOffsetRef.current = position;
    setPlaybackStart(position);
  }

  function handlePlaybackPaused() {
    pausedAtRef.current = Date.now();
    setPaused(true);
    void persistProgress();
  }

  function handlePlaybackEnded(video: HTMLVideoElement) {
    const position = playbackOffsetRef.current + video.currentTime;
    const duration = playbackDurationRef.current;
    if (duration > 0 && position < duration - 10) {
      setError(notifyErrorMessage("The selected media source ended before the movie or episode was complete.", "Source ended early"));
      return;
    }
    void persistProgress(true);
    onEnded?.();
  }

  return createPortal(<div className="player" role="dialog" aria-modal="true" aria-label={`Playing ${item.title}`}>
    <header className="player__header"><div><small>Now playing</small><strong>{item.title}</strong></div><IconButton label="Close player" onClick={closePlayer}><X /></IconButton></header>
    {loading ? <div className="player__loading"><span className="player__pulse"><Play fill="currentColor" /></span><p>Preparing the selected stream…</p></div> : playable.length === 0 ? <EmptyState icon={<ServerCrash size={42} />} title="No playable source" description={error || "The selected stream is not compatible with this device."} action={<Button variant="secondary" onClick={closePlayer}>Go back</Button>} /> : stream.ytId ? <iframe className="player__video" src={`https://www.youtube-nocookie.com/embed/${encodeURIComponent(stream.ytId)}?autoplay=1`} allow="autoplay; encrypted-media; picture-in-picture" allowFullScreen title={item.title} /> :
      <video key={`${stream.id}:${stream.url}:${playbackStart ?? 0}`} ref={videoRef} className="player__video" controls={!customTransport} autoPlay playsInline crossOrigin="anonymous" onLoadedMetadata={(event) => resumePlayback(event.currentTarget)} onTimeUpdate={(event) => trackPlayback(event.currentTarget)} onPlay={(event) => handlePlaybackStarted(event.currentTarget)} onPause={handlePlaybackPaused} onEnded={(event) => handlePlaybackEnded(event.currentTarget)}>
        {selectedSubtitle && <track key={selectedSubtitle.id} src={selectedSubtitle.url} srcLang={selectedSubtitle.language || "und"} label={(selectedSubtitle.language || "Unknown").toUpperCase()} default />}
      </video>}
    {customTransport && <div className="player__transport">
      <button type="button" aria-label={paused ? "Play" : "Pause"} onClick={togglePlayback}>{paused ? <Play size={18} fill="currentColor" /> : <Pause size={18} fill="currentColor" />}</button>
      <span>{formatPlaybackTime(transportTime)}</span>
      <input className="player__timeline" type="range" aria-label="Playback position" min={0} max={Math.max(1, Math.floor(playbackDuration))} step={1} value={Math.min(playbackDuration, Math.max(0, transportTime))} onChange={(event) => setSeekPreview(Number(event.target.value))} onPointerUp={(event) => commitSeek(Number(event.currentTarget.value))} onKeyUp={(event) => { if (["ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(event.key)) commitSeek(Number(event.currentTarget.value)); }} />
      <span>{formatPlaybackTime(playbackDuration)}</span>
      <button type="button" aria-label={muted ? "Unmute" : "Mute"} onClick={toggleMute}>{muted ? <VolumeX size={18} /> : <Volume2 size={18} />}</button>
      <input className="player__volume" type="range" aria-label="Volume" min={0} max={1} step={0.05} value={muted ? 0 : volume} onChange={(event) => changeVolume(Number(event.target.value))} />
      <button type="button" aria-label="Fullscreen" onClick={toggleFullscreen}><Maximize size={18} /></button>
    </div>}
    {playable.length > 0 && <div className="player__source-picker"><button onClick={() => setControlsOpen((value) => !value)}><span><ExternalLink size={16} /> {stream?.name || stream?.title || `Source ${selected + 1}`}</span><ChevronDown size={18} /></button>{controlsOpen && <div>{playable.map((candidate, index) => <button key={candidate.id} className={selected === index ? "is-active" : ""} onClick={() => { setSelected(index); setControlsOpen(false); }}>{candidate.name || candidate.title || `Source ${index + 1}`}<small>{candidate.protocol.toUpperCase()}{candidate.container ? ` · ${candidate.container.toUpperCase()}` : ""}</small></button>)}</div>}</div>}
    {(audioTracks.length > 1 || subtitles.length > 0) && <div className="player__track-controls">
      {audioTracks.length > 1 && <label><AudioLines size={15} /><span>Audio</span><select aria-label="Audio track" value={selectedAudioTrack ?? audioTracks[0]?.index} onChange={(event) => { const track = Number(event.target.value); const video = videoRef.current; if (video) resumePositionRef.current = Math.floor(playbackOffsetRef.current + video.currentTime); setSelectedAudioTrack(track); setPreferredAudioTrack(track); }}>{audioTracks.map((track) => <option key={track.index} value={track.index}>{track.title || track.language?.toUpperCase() || `Track ${track.index + 1}`} · {track.codec.toUpperCase()}</option>)}</select></label>}
      {subtitles.length > 0 && <label><Captions size={15} /><span>Subtitles</span><select aria-label="Subtitle track" value={selectedSubtitleID} onChange={(event) => setSelectedSubtitleID(event.target.value)}><option value="none">Off</option>{subtitles.map((subtitle) => <option key={subtitle.id} value={subtitle.id}>{(subtitle.language || "Unknown").toUpperCase()}</option>)}</select></label>}
    </div>}
    {error && playable.length > 0 && <Notice>{error}</Notice>}
  </div>, document.body);
}
