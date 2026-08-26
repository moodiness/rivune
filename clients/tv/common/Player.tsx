import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, RivuneTvClient } from "./api";
import { ErrorPanel, formatTime, Modal, TvButton, useOverlayFocus } from "./components";
import { DEFAULT_ACCESSIBILITY_PREFERENCES, captionsPreferred } from "./accessibility";
import { TvFailoverController } from "./failover";
import { focusFirst } from "./focus";
import { t } from "./i18n";
import { platformAdapter, tvPlaybackPolicy, type TvPlayer, type TvPlayerTrack, type TvQualityPreset } from "./platform";
import type { AccessibilityPreferencesDocument, CoordinatedPlaybackItem, MediaItem, PlaybackCommand, PlaybackDevice, PlaybackDeviceState, PlaybackFailoverState, PlaybackLoadMode, PlaybackPreparation, PlaybackProgress, PlaybackSession, PlaybackSource } from "./types";

type CommandResult = { status: "applied" | "failed"; code: "applied" | "unsupported" | "invalid_state" | "execution_failed" };
type PlayerController = { apply(command: PlaybackCommand): Promise<CommandResult>; stop(): Promise<void> };
type Props = {
  client: RivuneTvClient;
  item: MediaItem;
  sourceRef: string;
  titleId: string;
  startSeconds: number;
  progress: PlaybackProgress | null;
  preparation: PlaybackPreparation;
  failover?: PlaybackFailoverState;
  accessibility?: AccessibilityPreferencesDocument;
  remoteOperationId?: string;
  devices: PlaybackDevice[];
  qualityPreset: TvQualityPreset;
  onSendToDevice: (device: PlaybackDevice, item: CoordinatedPlaybackItem, mode: PlaybackLoadMode, positionMilliseconds?: number) => Promise<void>;
  onControlDevice: (device: PlaybackDevice, command: "play" | "pause" | "seek" | "stop", positionMilliseconds?: number) => Promise<void>;
  onController: (controller: PlayerController | null) => void;
  onPlaybackState: (state: PlaybackDeviceState) => void;
  onRemoteResult: (operationId: string, result: CommandResult) => void;
  onClose: () => void;
  setBackHandler: (handler: () => void) => void;
};

function availableSource(session: PlaybackSession): PlaybackSource | undefined {
  const playable = session.sources.filter((source) => source.compatible && Boolean(source.url));
  return playable.find((source) => source.id === session.selectedSourceId) ?? playable[0];
}

export function classifyPlaybackFailure(detail: string): "source_failed" | "source_timeout" | "decode_failed" | "access_denied" {
  const normalized = detail.toLowerCase();
  if (/timeout|timed out|network stalled/.test(normalized)) return "source_timeout";
  if (/decode|codec|demux|unsupported format/.test(normalized)) return "decode_failed";
  if (/access denied|forbidden|unauthorized|\b401\b|\b403\b/.test(normalized)) return "access_denied";
  return "source_failed";
}

export function Player({ client, item, sourceRef, titleId, startSeconds, progress, preparation, failover, accessibility = DEFAULT_ACCESSIBILITY_PREFERENCES, remoteOperationId, devices, qualityPreset, onSendToDevice, onControlDevice, onController, onPlaybackState, onRemoteResult, onClose, setBackHandler }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const playerRoot = useRef<HTMLDivElement>(null);
  const player = useRef<TvPlayer | null>(null);
  const sessionId = useRef("");
  const progressVersion = useRef(progress?.version ?? item.progressVersion ?? 0);
  const positionRef = useRef(startSeconds);
  const durationRef = useRef(progress?.durationSeconds ?? item.durationSeconds ?? 0);
  const lastSaved = useRef(startSeconds);
  const closing = useRef(false);
  const hideTimer = useRef(0);
  const remoteReported = useRef(false);
  const controlsVisible = useRef(true);
  const stateRef = useRef<"buffering" | "playing" | "paused">("buffering");
  const failoverController = useRef(failover ? new TvFailoverController(client, failover) : null);
  const failoverAdvancing = useRef(false);
  const [session, setSession] = useState<PlaybackSession | null>(null);
  const [source, setSource] = useState<PlaybackSource | null>(null);
  const [activeSourceRef, setActiveSourceRef] = useState(sourceRef);
  const [playbackStart, setPlaybackStart] = useState(startSeconds);
  const [failoverNotice, setFailoverNotice] = useState("");
  const [position, setPosition] = useState(startSeconds);
  const [duration, setDuration] = useState(durationRef.current);
  const [state, setState] = useState<"buffering" | "playing" | "paused">("buffering");
  const [tracks, setTracks] = useState<TvPlayerTrack[]>([]);
  const [trackPanel, setTrackPanel] = useState<"audio" | "subtitle" | null>(null);
  const [error, setError] = useState("");
  const [controls, setControls] = useState(true);
  const [coordinationError, setCoordinationError] = useState("");
  const coordinatedItem: CoordinatedPlaybackItem = {
    titleId,
    mediaType: item.mediaType as CoordinatedPlaybackItem["mediaType"],
    resourceId: item.resourceId || item.id,
    ...(item.sourceAddonId ? { sourceAddonId: item.sourceAddonId } : {}),
    title: item.title,
    ...(item.posterUrl ? { posterUrl: item.posterUrl } : {}),
  };
  const policy = tvPlaybackPolicy(platformAdapter(), client.issuer, qualityPreset);
  const decision = source?.decision ?? preparation.decision;
  const hideControls = useCallback(() => {
    const root = playerRoot.current;
    if (root?.querySelector("[data-tv-focus-scope='true']")) return;
    controlsVisible.current = false;
    setControls(false);
    const chrome = root?.querySelector<HTMLElement>(".tv-player__chrome");
    if (root && chrome?.contains(document.activeElement)) root.focus();
  }, []);

  const showControls = useCallback(() => {
    if (!controlsVisible.current) {
      controlsVisible.current = true;
      setControls(true);
      window.requestAnimationFrame(() => focusFirst(playerRoot.current?.querySelector(".tv-player__chrome") ?? document));
    }
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(hideControls, 7000);
  }, [hideControls]);

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
    await failoverController.current?.cancel().catch(() => undefined);
    player.current?.destroy();
    player.current = null;
    const id = sessionId.current;
    sessionId.current = "";
    if (id) await client.stopPlayback(id).catch(() => undefined);
    onClose();
  }, [client, onClose, saveProgress]);
  const advanceFailover = useCallback(async (failure: "source_failed" | "source_timeout" | "ended_early", fallbackMessage: string): Promise<boolean> => {
    const controller = failoverController.current;
    if (!controller || failoverAdvancing.current || closing.current) return false;
    failoverAdvancing.current = true;
    try {
      const next = await controller.advance(failure, positionRef.current);
      if (!next) {
        setError(controller.snapshot.explanation || fallbackMessage);
        return false;
      }
      player.current?.destroy();
      player.current = null;
      const id = sessionId.current;
      sessionId.current = "";
      if (id) await client.stopPlayback(id).catch(() => undefined);
      positionRef.current = next.positionSeconds;
      setPosition(next.positionSeconds);
      setPlaybackStart(next.positionSeconds);
      setSession(null);
      setSource(null);
      setError("");
      setFailoverNotice(`Trying backup source ${next.state.currentPosition + 1} of ${next.state.candidateHealth.length}.`);
      setActiveSourceRef(next.sourceRef);
      return true;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : fallbackMessage);
      return false;
    } finally {
      failoverAdvancing.current = false;
    }
  }, [client]);
  useOverlayFocus(playerRoot);

  useEffect(() => {
    setBackHandler(() => { void close(false); });
  }, [close, setBackHandler]);


  useEffect(() => {
    let active = true;
    void client.resolvePlayback(activeSourceRef, { titleId, startSeconds: Math.max(0, Math.floor(playbackStart)) }).then((resolved) => {
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
      if (!active) return;
      const message = cause instanceof Error ? cause.message : t("error.playback");
      void advanceFailover("source_failed", message).then((advanced) => {
        if (advanced || !remoteOperationId || remoteReported.current) return;
        remoteReported.current = true;
        onRemoteResult(remoteOperationId, { status: "failed", code: "execution_failed" });
      });
    });
    return () => { active = false; };
  }, [activeSourceRef, advanceFailover, client, playbackStart, remoteOperationId, onRemoteResult, titleId]);

  useEffect(() => {
    if (!source || !session || !host.current) return;
    let disposed = false;
    const adapter = platformAdapter();
    const nativePlayer = adapter.createPlayer(host.current);
    player.current = nativePlayer;
    const resolvedURL = source.url ? client.resolveResourceUrl(source.url) : null;
    if (!resolvedURL) {
      void advanceFailover("source_failed", t("error.playback"));
      return () => nativePlayer.destroy();
    }
    let playbackURL = resolvedURL;
    if (source.mode !== "direct" && playbackStart > 0) {
      const value = new URL(resolvedURL);
      value.searchParams.set("start", String(Math.floor(playbackStart)));
      playbackURL = value.href;
    }
    const subtitles = session.subtitles.filter((subtitle) => subtitle.delivery === "external" && subtitle.url).map((subtitle) => ({
      id: subtitle.id,
      url: client.resolveResourceUrl(subtitle.url!) ?? subtitle.url!,
      label: subtitle.language || subtitle.id,
      language: subtitle.language ?? undefined,
      forced: Boolean(subtitle.forced),
      selected: subtitle.id === session.selectedSubtitleId || captionsPreferred(accessibility) && !session.selectedSubtitleId && !subtitle.forced,
    }));
    void nativePlayer.load({
      url: playbackURL,
      title: item.title,
      protocol: source.protocol,
      container: source.container ?? undefined,
      startSeconds: playbackStart,
      subtitles,
    }, {
      onReady: (nextDuration) => {
        durationRef.current = nextDuration || source.media?.durationSeconds || durationRef.current;
        setDuration(durationRef.current);
        onPlaybackState({ status: "paused", item: coordinatedItem, positionMilliseconds: Math.round(positionRef.current * 1_000), durationMilliseconds: Math.round(durationRef.current * 1_000) });
      },
      onTime: (nextPosition, nextDuration) => {
        positionRef.current = nextPosition;
        durationRef.current = nextDuration || durationRef.current;
        setPosition(nextPosition);
        setDuration(durationRef.current);
        onPlaybackState({ status: stateRef.current === "buffering" ? "paused" : stateRef.current, item: coordinatedItem, positionMilliseconds: Math.round(nextPosition * 1_000), durationMilliseconds: Math.round(durationRef.current * 1_000) });
      },
      onState: (nextState) => {
        stateRef.current = nextState;
        setState(nextState);
        onPlaybackState({ status: nextState === "buffering" ? "paused" : nextState, item: coordinatedItem, positionMilliseconds: Math.round(positionRef.current * 1_000), durationMilliseconds: Math.round(durationRef.current * 1_000) });
      },
      onTracks: (nextTracks) => {
        setTracks(nextTracks);
        if (accessibility.audioDescription && session.selectedAudioTrack == null) {
          const described = nextTracks.find((track) => track.kind === "audio" && /audio description|descriptive|described/i.test(track.label));
          if (described) void nativePlayer.selectAudio(described.index);
        }
      },
      onEnded: () => {
        const early = durationRef.current > 60 && positionRef.current < durationRef.current - 30;
        if (early) void advanceFailover("ended_early", t("error.playback")).then((advanced) => { if (!advanced) void close(true); });
        else void close(true);
      },
      onError: (detail) => {
        const message = detail || t("error.playback");
        const failure = classifyPlaybackFailure(message);
        if (failure === "source_failed" || failure === "source_timeout") void advanceFailover(failure, message);
        else setError(message);
      },
    }).then(async () => {
      if (disposed) return;
      if (session.selectedAudioTrack != null) await nativePlayer.selectAudio(session.selectedAudioTrack).catch(() => undefined);
      const preferredSubtitle = session.selectedSubtitleId ?? (captionsPreferred(accessibility) ? subtitles.find((subtitle) => !subtitle.forced)?.id : undefined);
      if (preferredSubtitle) await nativePlayer.selectSubtitle(preferredSubtitle).catch(() => undefined);
      await nativePlayer.play();
      stateRef.current = "playing";
      setState("playing");
      onPlaybackState({ status: "playing", item: coordinatedItem, positionMilliseconds: Math.round(positionRef.current * 1_000), durationMilliseconds: Math.round(durationRef.current * 1_000) });
      if (remoteOperationId && !remoteReported.current) {
        remoteReported.current = true;
        onRemoteResult(remoteOperationId, { status: "applied", code: "applied" });
      }
      showControls();
    }).catch((cause) => {
      if (disposed) return;
      const message = cause instanceof Error ? cause.message : t("error.playback");
      const failure = classifyPlaybackFailure(message);
      const advance = failure === "source_failed" || failure === "source_timeout" ? advanceFailover(failure, message) : Promise.resolve(false);
      void advance.then((advanced) => {
        if (advanced) return;
        setError(message);
        if (!remoteOperationId || remoteReported.current) return;
        remoteReported.current = true;
        onRemoteResult(remoteOperationId, { status: "failed", code: "execution_failed" });
      });
    });
    return () => {
      disposed = true;
      nativePlayer.destroy();
      if (player.current === nativePlayer) player.current = null;
    };
  }, [accessibility, advanceFailover, client, close, item.title, onPlaybackState, onRemoteResult, playbackStart, remoteOperationId, session, showControls, source]);

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


  async function toggle() {
    showControls();
    const nextState = state === "playing" ? "paused" : "playing";
    if (state === "playing") await player.current?.pause(); else await player.current?.play();
    stateRef.current = nextState;
    setState(nextState);
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

  const applyRemote = useCallback(async (command: PlaybackCommand): Promise<CommandResult> => {
    if (!player.current && command.command !== "stop") return { status: "failed", code: "invalid_state" };
    try {
      if (command.command === "play") {
        await player.current?.play();
        stateRef.current = "playing";
        setState("playing");
      } else if (command.command === "pause") {
        await player.current?.pause();
        stateRef.current = "paused";
        setState("paused");
      } else if (command.command === "seek" && command.positionMilliseconds !== undefined) {
        const target = Math.max(0, Math.min(durationRef.current || Number.MAX_SAFE_INTEGER, command.positionMilliseconds / 1_000));
        await player.current?.seek(target);
        positionRef.current = target;
        setPosition(target);
      } else if (command.command === "stop") {
        await close(false);
      } else {
        return { status: "failed", code: "unsupported" };
      }
      return { status: "applied", code: "applied" };
    } catch {
      return { status: "failed", code: "execution_failed" };
    }
  }, [close]);

  useEffect(() => {
    const controller: PlayerController = { apply: applyRemote, stop: () => close(false) };
    onController(controller);
    return () => onController(null);
  }, [applyRemote, close, onController]);

  async function remote(device: PlaybackDevice, command: "play" | "pause" | "seek" | "stop", positionMilliseconds?: number) {
    setCoordinationError("");
    try { await onControlDevice(device, command, positionMilliseconds); }
    catch (cause) { setCoordinationError(cause instanceof Error ? cause.message : t("coordination.failed")); }
  }

  async function handoff(device: PlaybackDevice) {
    setCoordinationError("");
    try { await onSendToDevice(device, coordinatedItem, "handoff", Math.round(positionRef.current * 1_000)); }
    catch (cause) { setCoordinationError(cause instanceof Error ? cause.message : t("coordination.failed")); }
  }

  const percent = duration > 0 ? Math.min(100, Math.max(0, position / duration * 100)) : 0;
  const selectedTracks = tracks.filter((track) => track.kind === trackPanel);
  return <div ref={playerRoot} className="tv-player" role="region" aria-label={item.title} tabIndex={-1} data-tv-focus-scope="true" onMouseMove={showControls} onClick={showControls}>
    <div ref={host} className="tv-player__surface">
    </div>
    {state === "buffering" && !error && <div className="tv-player__status">{t("player.buffering")}</div>}
    {failoverNotice && <div className="tv-player__status" role="status" aria-live="assertive">{failoverNotice}</div>}
    <div className={`tv-player__chrome${controls ? "" : " is-hidden"}`} aria-hidden={!controls} inert={!controls}>
      <h1>{item.title}</h1>
      <div className="tv-player__timeline"><span>{formatTime(position)}</span><div className="tv-player__bar" role="progressbar" aria-label={t("player.progress")} aria-valuemin={0} aria-valuemax={Math.max(0, Math.round(duration))} aria-valuenow={Math.max(0, Math.min(Math.round(duration), Math.round(position)))} aria-valuetext={`${formatTime(position)} / ${formatTime(duration)}`}><span style={{ width: `${percent}%` }} /></div><span>{formatTime(duration)}</span></div>
      <div className="tv-player__controls">
        <TvButton icon="skipBack" aria-label={t("player.seekBack")} onClick={() => void seek(-10)}>{t("player.seekBack")}</TvButton>
        <TvButton icon={state === "playing" ? "pause" : "play"} tone="primary" onClick={() => void toggle()}>{t(state === "playing" ? "player.pause" : "player.resume")}</TvButton>
        <TvButton icon="skipForward" aria-label={t("player.seekForward")} onClick={() => void seek(10)}>{t("player.seekForward")}</TvButton>
        {tracks.some((track) => track.kind === "audio") && <TvButton onClick={() => setTrackPanel("audio")}>{t("player.audio")}</TvButton>}
        {(tracks.some((track) => track.kind === "subtitle") || session?.subtitles.length) && <TvButton onClick={() => setTrackPanel("subtitle")}>{t("player.subtitles")}</TvButton>}
        <TvButton tone="danger" onClick={() => void close(false)}>{t("player.stop")}</TvButton>
        {decision && <button type="button" className="tv-playback-decision" onClick={showControls}>
          <strong>{t(`playback.outcome.${decision.reason}`)}</strong>
          <span>{decision.reasons.length ? decision.reasons.map((reason) => t(`playback.reason.${reason}`)).join(" · ") : t("playback.reason.none")}</span>
          <small>{t("playback.quality", { preset: qualityPreset, network: policy.networkClass, height: policy.maximumHeight, bitrate: policy.maximumVideoBitrateKbps })}</small>
        </button>}
        {devices.length > 0 && <div className="tv-player__remote" aria-label={t("coordination.remote")}>
          {devices.map((device) => <span key={device.sessionId} className="tv-player__remote-device">
            <strong>{device.name}</strong>
            <TvButton onClick={() => void remote(device, "play")}>{t("player.resume")}</TvButton>
            <TvButton onClick={() => void remote(device, "pause")}>{t("player.pause")}</TvButton>
            <TvButton onClick={() => void remote(device, "seek", Math.max(0, device.state.positionMilliseconds - 10_000))}>{t("player.seekBack")}</TvButton>
            <TvButton onClick={() => void remote(device, "seek", device.state.positionMilliseconds + 10_000)}>{t("player.seekForward")}</TvButton>
            <TvButton tone="danger" onClick={() => void remote(device, "stop")}>{t("player.stop")}</TvButton>
            <TvButton tone="primary" onClick={() => void handoff(device)}>{t("coordination.handoff")}</TvButton>
          </span>)}
        </div>}
      </div>
    </div>
    {trackPanel && <Modal title={t(trackPanel === "audio" ? "player.audio" : "player.subtitles")} onClose={() => setTrackPanel(null)}>
      <div className="tv-track-list">{trackPanel === "subtitle" && <TvButton onClick={() => void selectTrack(null)}>{t("player.off")}</TvButton>}{selectedTracks.map((track) => <TvButton key={`${track.kind}:${track.id}`} tone={track.selected ? "primary" : "default"} onClick={() => void selectTrack(track)}>{track.label}</TvButton>)}</div>
    </Modal>}
    {error && <ErrorPanel message={error} onRetry={() => window.location.reload()} onClose={() => void close(false)} />}
    {coordinationError && <ErrorPanel message={coordinationError} onClose={() => setCoordinationError("")} />}
  </div>;
}
