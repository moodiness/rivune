import { useEffect, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { ErrorPanel, Modal, Spinner, TvButton } from "./components";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import { mediaFromEpisode, mediaFromMovie, mediaFromSeries, resourceId, titleResolveInput } from "./media";
import { platformAdapter } from "./platform";
import type {
  MediaItem,
  Movie,
  PlaybackProgress,
  PlaybackSourceOption,
  Season,
  Series,
} from "./types";

type PlayerRequest = { item: MediaItem; titleId: string; sourceRef: string; startSeconds: number; progress: PlaybackProgress | null };

type DetailState = {
  item: MediaItem;
  titleId: string;
  movie: Movie | null;
  series: Series | null;
  progress: PlaybackProgress | null;
  inLibrary: boolean;
};

export function Detail({ client, item, onClose, onOpen, onPlay }: { client: RivuneTvClient; item: MediaItem; onClose: () => void; onOpen: (item: MediaItem) => void; onPlay: (request: PlayerRequest) => void }) {
  const [detail, setDetail] = useState<DetailState | null>(null);
  const [season, setSeason] = useState<Season | null>(null);
  const [sources, setSources] = useState<PlaybackSourceOption[] | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");

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
      const result = await client.playbackSources(detail.item.mediaType, resourceId(detail.item), platformAdapter().capabilities(), detail.item.sourceAddonId);
      if (result.sources.length === 0) throw new Error(t("source.empty"));
      if (result.sources.length === 1) await start(result.sources[0]);
      else setSources(result.sources);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("source.empty"));
    } finally { setBusy(false); }
  }

  async function start(source: PlaybackSourceOption) {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      const startSeconds = detail.progress?.completed ? 0 : detail.progress?.positionSeconds ?? detail.item.resumePositionSeconds ?? 0;
      await client.preparePlayback(source.sourceRef, Math.max(0, Math.floor(startSeconds)));
      setSources(null);
      onPlay({ item: detail.item, titleId: detail.titleId, sourceRef: source.sourceRef, startSeconds, progress: detail.progress });
    } catch (cause) {
      if (cause instanceof APIError && cause.code === "playback_source_expired") setSources(null);
      setError(cause instanceof Error ? cause.message : t("error.playback"));
    } finally { setBusy(false); }
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
        </div>
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
