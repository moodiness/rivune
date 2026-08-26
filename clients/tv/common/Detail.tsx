import { useEffect, useRef, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { PendingMutationJournal } from "./featureState";
import { ErrorPanel, Modal, Spinner, TvButton } from "./components";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import { mediaFromEpisode, mediaFromMovie, mediaFromSeries, resourceId, titleResolveInput } from "./media";
import { platformAdapter, tvPlaybackPolicy, type TvQualityPreset } from "./platform";
import type {
  AccessibilityPreferencesDocument,
  CoordinatedPlaybackItem,
  MediaItem,
  Movie,
  PlaybackCommand,
  PlaybackDevice,
  PlaybackFailoverState,
  PlaybackLoadMode,
  PlaybackPreparation,
  PlaybackProgress,
  PlaybackSourceOption,
  Season,
  Series,
} from "./types";

type PlayerRequest = { item: MediaItem; titleId: string; sourceRef: string; startSeconds: number; progress: PlaybackProgress | null; preparation: PlaybackPreparation; failover?: PlaybackFailoverState; accessibility: AccessibilityPreferencesDocument; remoteOperationId?: string };

type DetailState = {
  item: MediaItem;
  titleId: string;
  movie: Movie | null;
  series: Series | null;
  progress: PlaybackProgress | null;
  inLibrary: boolean;
};

export function Detail({ client, item, profileId, timezone, accessibility, qualityPreset, devices, remoteCommand, onClose, onOpen, onPlay, onSendToDevice, onRemoteResult }: {
  client: RivuneTvClient; item: MediaItem; profileId: string; timezone: string; accessibility: AccessibilityPreferencesDocument; qualityPreset: TvQualityPreset; devices: PlaybackDevice[]; remoteCommand?: PlaybackCommand | null;
  onClose: () => void; onOpen: (item: MediaItem) => void; onPlay: (request: PlayerRequest) => void;
  onSendToDevice: (device: PlaybackDevice, item: CoordinatedPlaybackItem, mode: PlaybackLoadMode, positionMilliseconds?: number) => Promise<void>;
  onRemoteResult: (operationId: string, result: { status: "failed"; code: "execution_failed" }) => void;
}) {
  const [detail, setDetail] = useState<DetailState | null>(null);
  const [season, setSeason] = useState<Season | null>(null);
  const [sources, setSources] = useState<PlaybackSourceOption[] | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const remoteStarted = useRef("");
  const [remoteBusy, setRemoteBusy] = useState("");
  const [queued, setQueued] = useState(false);
  const [tracked, setTracked] = useState(false);
  const mutationJournal = useRef(new PendingMutationJournal());

  useEffect(() => {
    let active = true;
    setBusy(true);
    setDetail(null);
    setSeason(null);
    setError("");
    void (async () => {
      const titleId = item.titleId || (await client.resolveTitle(titleResolveInput(item))).titleId;
      const [progress, library] = await Promise.all([
        client.playbackProgress(titleId).catch(() => null),
        client.library(undefined, 1, 100).catch(() => ({ items: [], page: 1, totalPages: 0, totalResults: 0 })),
      ]);
      let movie: Movie | null = null;
      let series: Series | null = null;
      let enriched: MediaItem = { ...item, titleId };
      if (item.mediaType === "movie") {
        movie = await client.movie(titleId);
        enriched = mediaFromMovie(movie, enriched);
      } else if (item.mediaType === "series") {
        series = await client.series(titleId);
        enriched = mediaFromSeries(series, enriched);
      }
      if (!active) return;
      setDetail({ item: enriched, titleId, movie, series, progress, inLibrary: library.items.some((entry) => entry.titleId === titleId) });
    })().then(() => {
      if (active) setBusy(false);
    }, (cause: unknown) => {
      if (!active) return;
      setError(cause instanceof Error ? cause.message : t("error.network"));
      setBusy(false);
    });
    return () => { active = false; };
  }, [client, item]);

  useEffect(() => { if (detail) focusFirst(document.querySelector(".tv-detail") ?? document); }, [detail]);
  useEffect(() => { if (sources) focusFirst(document.querySelector(".tv-modal") ?? document); }, [sources]);
  useEffect(() => {
    if (!detail || remoteCommand?.command !== "load" || remoteStarted.current === remoteCommand.operationId) return;
    remoteStarted.current = remoteCommand.operationId;
    void chooseSource();
  }, [detail, remoteCommand]);

  async function openSeason(id: string) {
    setBusy(true);
    setError("");
    try { setSeason(await client.season(id, detail?.series?.mappingProvider)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("error.network")); }
    finally { setBusy(false); }
  }

  async function chooseSource() {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      const policy = tvPlaybackPolicy(platformAdapter(), client.issuer, qualityPreset);
      const result = await client.playbackSources(detail.item.mediaType, resourceId(detail.item), policy.capabilities, detail.item.sourceAddonId);
      if (result.sources.length === 0) throw new Error(t("source.empty"));
      if (result.sources.length === 1 || remoteCommand) await start(result.sources[0], result.sources);
      else setSources(result.sources);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("source.empty"));
      if (remoteCommand) onRemoteResult(remoteCommand.operationId, { status: "failed", code: "execution_failed" });
    } finally { setBusy(false); }
  }

  async function start(source: PlaybackSourceOption, candidates: PlaybackSourceOption[] = sources ?? [source]) {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      const startSeconds = remoteCommand?.positionMilliseconds !== undefined ? remoteCommand.positionMilliseconds / 1_000 : detail.progress?.completed ? 0 : detail.progress?.positionSeconds ?? detail.item.resumePositionSeconds ?? 0;
      const candidateSourceRefs = Array.from(new Set([source.sourceRef, ...candidates.map((candidate) => candidate.sourceRef)]));
      const failover = candidateSourceRefs.length >= 2
        ? await client.createPlaybackFailover({ candidateSourceRefs, selectedSourceRef: source.sourceRef, maximumAttempts: Math.min(3, candidateSourceRefs.length - 1) })
        : undefined;
      const preparation = await client.preparePlayback(source.sourceRef, Math.max(0, Math.floor(startSeconds)));
      setSources(null);
      onPlay({ item: detail.item, titleId: detail.titleId, sourceRef: source.sourceRef, startSeconds, progress: detail.progress, preparation, failover, accessibility, ...(remoteCommand ? { remoteOperationId: remoteCommand.operationId } : {}) });
    } catch (cause) {
      if (cause instanceof APIError && cause.code === "playback_source_expired") setSources(null);
      setError(cause instanceof Error ? cause.message : t("error.playback"));
      if (remoteCommand) onRemoteResult(remoteCommand.operationId, { status: "failed", code: "execution_failed" });
    } finally { setBusy(false); }
  }

  async function sendTo(device: PlaybackDevice, mode: PlaybackLoadMode) {
    if (!detail || remoteBusy) return;
    setRemoteBusy(device.sessionId);
    setError("");
    const coordinated: CoordinatedPlaybackItem = {
      titleId: detail.titleId,
      mediaType: detail.item.mediaType as CoordinatedPlaybackItem["mediaType"],
      resourceId: resourceId(detail.item),
      ...(detail.item.sourceAddonId ? { sourceAddonId: detail.item.sourceAddonId } : {}),
      title: detail.item.title,
      ...(detail.item.posterUrl ? { posterUrl: detail.item.posterUrl } : {}),
    };
    try { await onSendToDevice(device, coordinated, mode, Math.round((detail.progress?.completed ? 0 : detail.progress?.positionSeconds ?? detail.item.resumePositionSeconds ?? 0) * 1_000)); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("coordination.failed")); }
    finally { setRemoteBusy(""); }
  }

  async function toggleLibrary() {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      if (detail.inLibrary) await client.removeLibraryTitle(detail.titleId);
      else await client.addLibraryTitle(detail.titleId);
      setDetail({ ...detail, inLibrary: !detail.inLibrary });
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("error.network")); }
    finally { setBusy(false); }
  }

  async function addToQueue() {
    if (!detail || queued) return;
    setBusy(true);
    setError("");
    try {
      const queue = await client.readingQueue(profileId);
      const pending = mutationJournal.current.begin(profileId, `add:${detail.item.mediaType}:${resourceId(detail.item)}`, queue.revision);
      await client.addReadingQueueItem(profileId, {
        operationId: pending.operationId,
        expectedRevision: pending.expectedRevision,
        mediaType: detail.item.mediaType === "movie" || detail.item.mediaType === "episode" || detail.item.mediaType === "tv" ? detail.item.mediaType : "series",
        resourceId: resourceId(detail.item),
        ...(detail.item.sourceAddonId ? { sourceAddonId: detail.item.sourceAddonId } : {}),
        titleId: detail.titleId,
        title: detail.item.title,
        ...(detail.item.posterUrl ? { posterUrl: detail.item.posterUrl } : {}),
      });
      mutationJournal.current.complete(pending.operationId);
      setQueued(true);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "This title could not be added to the queue.");
    } finally { setBusy(false); }
  }

  async function followTitle() {
    if (!detail || tracked) return;
    setBusy(true);
    setError("");
    try {
      await client.followMediaNotifications(detail.titleId, { timezone, horizonDays: 90, leadDays: 1 });
      setTracked(true);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Media notifications could not be enabled.");
    } finally { setBusy(false); }
  }

  const shown = detail?.item ?? item;
  const backdrop = client.resolveArtworkUrl(shown.backgroundUrl || shown.posterUrl || "");
  const logo = client.resolveArtworkUrl(shown.logoUrl || "");
  const playable = shown.mediaType !== "series";
  const runtime = detail?.movie?.runtimeMinutes;
  const rating = detail?.movie?.voteAverage || detail?.series?.voteAverage || shown.voteAverage;
  return <section className="tv-detail">
    {backdrop && <div className="tv-detail__backdrop" style={{ backgroundImage: `url(${JSON.stringify(backdrop).slice(1, -1)})` }} />}
    <div className="tv-detail__shade" />
    <div className="tv-detail__content">
      <TvButton icon="back" tone="quiet" onClick={onClose}>{t("common.back")}</TvButton>
      {busy && !detail ? <Spinner /> : <div className="tv-detail__top">
        {logo ? <img className="tv-detail__logo" src={logo} alt={shown.title} /> : <h1>{shown.title}</h1>}
        <div className="tv-detail__meta">{shown.releaseInfo && <span>{shown.releaseInfo}</span>}{runtime && <span>{t("media.runtime", { minutes: runtime })}</span>}{rating && <span>{t("media.rating", { rating: rating.toFixed(1) })}</span>}</div>
        {shown.description && <p className="tv-detail__description">{shown.description}</p>}
        <div className="tv-actions">
          {playable && <TvButton icon="play" tone="primary" disabled={busy} onClick={() => void chooseSource()}>{detail?.progress && detail.progress.positionSeconds > 0 && !detail.progress.completed ? t("media.resume", { time: Math.floor(detail.progress.positionSeconds / 60) + "m" }) : t("media.play")}</TvButton>}
          {detail && <TvButton icon={detail.inLibrary ? "check" : "library"} disabled={busy} onClick={() => void toggleLibrary()}>{detail.inLibrary ? t("library.title") + " · ✓" : t("library.title") + " +"}</TvButton>}
          {detail && <TvButton disabled={busy || queued} onClick={() => void addToQueue()}>{queued ? "Queued · ✓" : "Add to queue"}</TvButton>}
          {detail && <TvButton disabled={busy || tracked} onClick={() => void followTitle()}>{tracked ? "Tracking · ✓" : "Track releases"}</TvButton>}
        </div>
        {detail && devices.length > 0 && <section className="tv-coordination-card" tabIndex={0}>
          <h2>{t("coordination.targets")}</h2>
          <div className="tv-actions">{devices.map((device) => <span className="tv-coordination-target" key={device.sessionId}>
            <TvButton disabled={Boolean(remoteBusy)} onClick={() => void sendTo(device, "play-copy")}>{t("coordination.playCopy", { device: device.name })}</TvButton>
            <TvButton tone="primary" disabled={Boolean(remoteBusy)} onClick={() => void sendTo(device, "handoff")}>{t("coordination.handoff")}</TvButton>
          </span>)}</div>
        </section>}
      </div>}

      {detail?.series && <section className="tv-section"><div className="tv-section__heading"><h2>{t("media.seasons")}</h2></div><div className="tv-season-list">{detail.series.seasons.filter((entry) => entry.episodeCount > 0).map((entry) => <TvButton key={entry.id} className="tv-season" tone={season?.id === entry.id ? "primary" : "default"} onClick={() => void openSeason(entry.id)}>{entry.name}</TvButton>)}</div></section>}
      {season && detail?.series && <section className="tv-section"><div className="tv-section__heading"><h2>{season.name} · {t("media.episodes")}</h2></div>{season.episodes.map((episode) => {
        const episodeItem = mediaFromEpisode(episode, detail.series!, season, detail.item);
        const art = client.resolveArtworkUrl(episodeItem.posterUrl || "");
        return <button type="button" className="tv-episode" key={episode.id} onClick={() => onOpen(episodeItem)}><span className="tv-episode__art">{art && <img src={art} alt="" />}</span><span className="tv-episode__copy"><strong>{episode.episodeNumber}. {episode.name}</strong><span>{episode.airDate || ""}{episode.runtimeMinutes ? ` · ${episode.runtimeMinutes} min` : ""}</span><p>{episode.overview}</p></span></button>;
      })}</section>}
      {detail && (detail.movie?.cast.length || detail.series?.cast.length) ? <section className="tv-section"><div className="tv-section__heading"><h2>{t("media.cast")}</h2></div><div className="tv-row">{(detail.movie?.cast ?? detail.series?.cast ?? []).slice(0, 16).map((person) => <div className="tv-card" key={person.id}><span className="tv-card__art">{client.resolveArtworkUrl(person.profileUrl || "") ? <img src={client.resolveArtworkUrl(person.profileUrl || "")!} alt="" /> : <span className="tv-card__fallback">{person.name.slice(0, 1)}</span>}</span><span className="tv-card__copy"><strong>{person.name}</strong><span>{person.character || ""}</span></span></div>)}</div></section> : null}
    </div>
    {sources && <Modal title={t("source.title")} onClose={() => setSources(null)}>{sources.map((source) => <button type="button" className="tv-source" key={source.sourceRef} onClick={() => void start(source)}><strong>{source.name}</strong><span>{[source.description, source.protocol.toUpperCase(), source.container?.toUpperCase()].filter(Boolean).join(" · ")}</span></button>)}</Modal>}
    {error && <ErrorPanel message={error} onClose={() => setError("")} />}
  </section>;
}

export type { PlayerRequest };
