import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { ErrorPanel, formatTime, Modal, TvButton } from "./components";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import { platformAdapter, type TvPlayer, type TvPlayerTrack } from "./platform";
import type { MediaItem, PlaybackProgress, PlaybackSession, PlaybackSource } from "./types";

type Props = {
  client: RivuneTvClient;
  item: MediaItem;
  sourceRef: string;
  titleId: string;
  startSeconds: number;
  progress: PlaybackProgress | null;
  onClose: () => void;
  setBackHandler: (handler: () => void) => void;
};

function availableSource(session: PlaybackSession): PlaybackSource | undefined {
  const playable = session.sources.filter((source) => source.compatible && Boolean(source.url));
  return playable.find((source) => source.id === session.selectedSourceId) ?? playable[0];
}

export function Player({ client, item, sourceRef, titleId, startSeconds, progress, onClose, setBackHandler }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const player = useRef<TvPlayer | null>(null);
  const sessionId = useRef("");
  const progressVersion = useRef(progress?.version ?? item.progressVersion ?? 0);
  const positionRef = useRef(startSeconds);
  const durationRef = useRef(progress?.durationSeconds ?? item.durationSeconds ?? 0);
  const lastSaved = useRef(startSeconds);
  const closing = useRef(false);
  const hideTimer = useRef(0);
  const [session, setSession] = useState<PlaybackSession | null>(null);
  const [source, setSource] = useState<PlaybackSource | null>(null);
  const [position, setPosition] = useState(startSeconds);
  const [duration, setDuration] = useState(durationRef.current);
  const [state, setState] = useState<"buffering" | "playing" | "paused">("buffering");
  const [tracks, setTracks] = useState<TvPlayerTrack[]>([]);
  const [trackPanel, setTrackPanel] = useState<"audio" | "subtitle" | null>(null);
  const [error, setError] = useState("");
  const [controls, setControls] = useState(true);

  const showControls = useCallback(() => {
    setControls(true);
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => setControls(false), 7000);
  }, []);

  const saveProgress = useCallback(async (completed = false) => {
    if (!titleId || (item.mediaType !== "movie" && item.mediaType !== "episode")) return;
    const nextPosition = Math.max(0, Math.floor(positionRef.current));
    const nextDuration = Math.max(0, Math.floor(durationRef.current));
    if (!completed && Math.abs(nextPosition - lastSaved.current) < 5) return;
    try {
      const updated = await client.updatePlaybackProgress(titleId, {
        positionSeconds: nextPosition,
        durationSeconds: nextDuration,
        completed,
        expectedVersion: progressVersion.current,
      });
      progressVersion.current = updated.version;
      lastSaved.current = nextPosition;
    } catch (cause) {
      if (cause instanceof APIError && cause.code === "progress_version_conflict") {
        const latest = await client.playbackProgress(titleId).catch(() => null);
        if (latest) progressVersion.current = latest.version;
      }
    }
  }, [client, item.mediaType, titleId]);

  const close = useCallback(async (completed = false) => {
    if (closing.current) return;
    closing.current = true;
    window.clearTimeout(hideTimer.current);
    await saveProgress(completed).catch(() => undefined);
    player.current?.destroy();
    player.current = null;
    const id = sessionId.current;
    sessionId.current = "";
    if (id) await client.stopPlayback(id).catch(() => undefined);
    onClose();
  }, [client, onClose, saveProgress]);

  useEffect(() => {
    setBackHandler(() => { void close(false); });
  }, [close, setBackHandler]);

  useEffect(() => {
    let active = true;
    void client.resolvePlayback(sourceRef, { titleId, startSeconds: Math.max(0, Math.floor(startSeconds)) }).then((resolved) => {
      if (!active) {
        void client.stopPlayback(resolved.id).catch(() => undefined);
        return;
      }
      sessionId.current = resolved.id;
      const selected = availableSource(resolved);
      if (!selected) throw new Error(t("error.playback"));
      setSession(resolved);
      setSource(selected);
    }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : t("error.playback"));
    });
    return () => { active = false; };
  }, [client, sourceRef, startSeconds, titleId]);

  useEffect(() => {
    if (!source || !session || !host.current) return;
    let disposed = false;
    const adapter = platformAdapter();
    const nativePlayer = adapter.createPlayer(host.current);
    player.current = nativePlayer;
    const resolvedURL = source.url ? client.resolveResourceUrl(source.url) : null;
    if (!resolvedURL) {
      setError(t("error.playback"));
      return () => nativePlayer.destroy();
    }
    let playbackURL = resolvedURL;
    if (source.mode !== "direct" && startSeconds > 0) {
      const value = new URL(resolvedURL);
      value.searchParams.set("start", String(Math.floor(startSeconds)));
      playbackURL = value.href;
    }
    const subtitles = session.subtitles.filter((subtitle) => subtitle.delivery === "external" && subtitle.url).map((subtitle) => ({
      id: subtitle.id,
      url: client.resolveResourceUrl(subtitle.url!) ?? subtitle.url!,
      label: subtitle.language || subtitle.id,
      language: subtitle.language ?? undefined,
      forced: Boolean(subtitle.forced),
      selected: subtitle.id === session.selectedSubtitleId,
    }));
    void nativePlayer.load({
      url: playbackURL,
      title: item.title,
      protocol: source.protocol,
      container: source.container ?? undefined,
      startSeconds,
      subtitles,
    }, {
      onReady: (nextDuration) => {
        durationRef.current = nextDuration || source.media?.durationSeconds || durationRef.current;
        setDuration(durationRef.current);
      },
      onTime: (nextPosition, nextDuration) => {
        positionRef.current = nextPosition;
        durationRef.current = nextDuration || durationRef.current;
        setPosition(nextPosition);
        setDuration(durationRef.current);
      },
      onState: setState,
      onTracks: setTracks,
      onEnded: () => { void close(true); },
      onError: (detail) => setError(detail || t("error.playback")),
    }).then(async () => {
      if (disposed) return;
      if (session.selectedAudioTrack != null) await nativePlayer.selectAudio(session.selectedAudioTrack).catch(() => undefined);
      if (session.selectedSubtitleId) await nativePlayer.selectSubtitle(session.selectedSubtitleId).catch(() => undefined);
      await nativePlayer.play();
      setState("playing");
      showControls();
    }).catch((cause) => {
      if (!disposed) setError(cause instanceof Error ? cause.message : t("error.playback"));
    });
    return () => {
      disposed = true;
      nativePlayer.destroy();
      if (player.current === nativePlayer) player.current = null;
    };
  }, [client, close, item.title, session, showControls, source, startSeconds]);

  useEffect(() => {
    const timer = window.setInterval(() => { void saveProgress(false); }, 15_000);
    const keys = (event: KeyboardEvent) => {
      showControls();
      const code = event.keyCode;
      if (event.key === " " || event.key === "MediaPlayPause" || code === 10252) {
        event.preventDefault();
        if (state === "playing") void player.current?.pause(); else void player.current?.play();
      } else if (event.key === "MediaPlay" || code === 415) void player.current?.play();
      else if (event.key === "MediaPause" || code === 19) void player.current?.pause();
      else if (event.key === "MediaStop" || code === 413) void close(false);
    };
    document.addEventListener("keydown", keys, true);
    return () => { window.clearInterval(timer); document.removeEventListener("keydown", keys, true); };
  }, [close, saveProgress, showControls, state]);

  useEffect(() => { if (trackPanel) focusFirst(document.querySelector(".tv-modal") ?? document); }, [trackPanel]);

  async function toggle() {
    showControls();
    if (state === "playing") await player.current?.pause(); else await player.current?.play();
  }
  async function seek(delta: number) {
    showControls();
    const target = Math.max(0, Math.min(durationRef.current || Number.MAX_SAFE_INTEGER, positionRef.current + delta));
    await player.current?.seek(target);
    positionRef.current = target;
    setPosition(target);
  }
  async function selectTrack(track: TvPlayerTrack | null) {
    if (trackPanel === "audio" && track) await player.current?.selectAudio(track.index);
    if (trackPanel === "subtitle") await player.current?.selectSubtitle(track?.id ?? null);
    setTracks((current) => current.map((candidate) => candidate.kind === trackPanel ? { ...candidate, selected: candidate.id === track?.id } : candidate));
    setTrackPanel(null);
  }

  const percent = duration > 0 ? Math.min(100, Math.max(0, position / duration * 100)) : 0;
  const selectedTracks = tracks.filter((track) => track.kind === trackPanel);
  return <div className="tv-player" onMouseMove={showControls} onClick={showControls}>
    <div ref={host} className="tv-player__surface">
    </div>
    {state === "buffering" && !error && <div className="tv-player__status">{t("player.buffering")}</div>}
    <div className={`tv-player__chrome${controls ? "" : " is-hidden"}`}>
      <h1>{item.title}</h1>
      <div className="tv-player__timeline"><span>{formatTime(position)}</span><div className="tv-player__bar"><span style={{ width: `${percent}%` }} /></div><span>{formatTime(duration)}</span></div>
      <div className="tv-player__controls">
        <TvButton icon="skipBack" aria-label={t("player.seekBack")} onClick={() => void seek(-10)}>{t("player.seekBack")}</TvButton>
        <TvButton icon={state === "playing" ? "pause" : "play"} tone="primary" onClick={() => void toggle()}>{t(state === "playing" ? "player.pause" : "player.resume")}</TvButton>
        <TvButton icon="skipForward" aria-label={t("player.seekForward")} onClick={() => void seek(10)}>{t("player.seekForward")}</TvButton>
        {tracks.some((track) => track.kind === "audio") && <TvButton onClick={() => setTrackPanel("audio")}>{t("player.audio")}</TvButton>}
        {(tracks.some((track) => track.kind === "subtitle") || session?.subtitles.length) && <TvButton onClick={() => setTrackPanel("subtitle")}>{t("player.subtitles")}</TvButton>}
        <TvButton tone="danger" onClick={() => void close(false)}>{t("player.stop")}</TvButton>
      </div>
    </div>
    {trackPanel && <Modal title={t(trackPanel === "audio" ? "player.audio" : "player.subtitles")} onClose={() => setTrackPanel(null)}>
      <div className="tv-track-list">{trackPanel === "subtitle" && <TvButton onClick={() => void selectTrack(null)}>{t("player.off")}</TvButton>}{selectedTracks.map((track) => <TvButton key={`${track.kind}:${track.id}`} tone={track.selected ? "primary" : "default"} onClick={() => void selectTrack(track)}>{track.label}</TvButton>)}</div>
    </Modal>}
    {error && <ErrorPanel message={error} onRetry={() => window.location.reload()} onClose={() => void close(false)} />}
  </div>;
}
