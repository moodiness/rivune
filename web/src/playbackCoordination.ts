import { useEffect, useRef } from "react";
import { api } from "./api";
import type { MediaItem, PlaybackCommand, PlaybackCommandResultCode, PlaybackCoordinationItem, PlaybackDeviceState } from "./types";

const stateEvent = "rivune:playback-state";
const commandEvent = "rivune:playback-command";
const resultEvent = "rivune:playback-command-result";

export type IncomingPlaybackLoad = { operationId: string; mode: "handoff" | "play-copy" };

type PlaybackCommandResult = { operationId: string; status: "applied" | "failed"; code: PlaybackCommandResultCode };

export function publishPlaybackState(state: Omit<PlaybackDeviceState, "updatedAt">): void {
  window.dispatchEvent(new CustomEvent(stateEvent, { detail: { ...state, updatedAt: new Date().toISOString() } satisfies PlaybackDeviceState }));
}

export function publishPlaybackCommandResult(result: PlaybackCommandResult): void {
  window.dispatchEvent(new CustomEvent(resultEvent, { detail: result }));
}

export function playbackItem(item: MediaItem): PlaybackCoordinationItem | null {
  const titleId = item.titleId?.trim();
  const resourceId = (item.resourceId || item.id).trim();
  if (!titleId || !resourceId || !item.mediaType.trim() || !item.title.trim()) return null;
  return { titleId, mediaType: item.mediaType, resourceId, title: item.title, ...(item.sourceAddonId ? { sourceAddonId: item.sourceAddonId } : {}), ...(item.posterUrl ? { posterUrl: item.posterUrl } : {}) };
}

function mediaItem(item: PlaybackCoordinationItem, operation: IncomingPlaybackLoad): MediaItem {
  return { id: item.resourceId, titleId: item.titleId, resourceId: item.resourceId, mediaType: item.mediaType, title: item.title, sourceAddonId: item.sourceAddonId, posterUrl: item.posterUrl, raw: { startFromBeginning: true, coordinationOperationId: operation.operationId, coordinationMode: operation.mode } };
}

export function usePlaybackCoordination(enabled: boolean, onLoad: (item: MediaItem, operation: IncomingPlaybackLoad) => void): void {
  const state = useRef<PlaybackDeviceState>({ status: "idle", positionMilliseconds: 0, durationMilliseconds: 0, updatedAt: new Date().toISOString() });
  const playerActive = useRef(false);
  const cursor = useRef<string | undefined>(undefined);
  const inFlight = useRef(false);
  const onLoadRef = useRef(onLoad);
  onLoadRef.current = onLoad;

  useEffect(() => {
    const updateState = (event: Event) => {
      state.current = (event as CustomEvent<PlaybackDeviceState>).detail;
      playerActive.current = state.current.status !== "idle";
    };
    const complete = (event: Event) => {
      const result = (event as CustomEvent<PlaybackCommandResult>).detail;
      void api.completePlaybackCommand(result.operationId, result.status, result.code).catch(() => undefined);
    };
    window.addEventListener(stateEvent, updateState);
    window.addEventListener(resultEvent, complete);
    return () => {
      window.removeEventListener(stateEvent, updateState);
      window.removeEventListener(resultEvent, complete);
    };
  }, []);

  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let heartbeatTimer: number | undefined;
    let pollTimer: number | undefined;
    const visible = () => document.visibilityState !== "hidden";
    const clearTimers = () => {
      if (heartbeatTimer !== undefined) window.clearTimeout(heartbeatTimer);
      if (pollTimer !== undefined) window.clearTimeout(pollTimer);
      heartbeatTimer = undefined;
      pollTimer = undefined;
    };
    const scheduleHeartbeat = (delay = 15_000) => {
      if (!active || !visible()) return;
      if (heartbeatTimer !== undefined) window.clearTimeout(heartbeatTimer);
      heartbeatTimer = window.setTimeout(heartbeat, delay);
    };
    const heartbeat = () => {
      heartbeatTimer = undefined;
      if (!active || !visible()) return;
      void api.playbackHeartbeat({ capabilities: ["playback", "remote-control", "load"], state: state.current })
        .catch(() => undefined)
        .finally(() => scheduleHeartbeat());
    };
    const execute = async (command: PlaybackCommand) => {
      if (command.status !== "pending") return;
      if (Date.now() >= Date.parse(command.expiresAt)) {
        await api.completePlaybackCommand(command.operationId, "expired", "expired");
        return;
      }
      if (command.command === "load") {
        if (!command.item || !command.mode) {
          await api.completePlaybackCommand(command.operationId, "failed", "unsupported");
          return;
        }
        onLoadRef.current(mediaItem(command.item, { operationId: command.operationId, mode: command.mode }), { operationId: command.operationId, mode: command.mode });
        return;
      }
      if (!playerActive.current) {
        await api.completePlaybackCommand(command.operationId, "failed", "invalid_state");
        return;
      }
      window.dispatchEvent(new CustomEvent(commandEvent, { detail: command }));
    };
    const schedulePoll = (delay = playerActive.current ? 2_000 : 30_000) => {
      if (!active || !visible()) return;
      if (pollTimer !== undefined) window.clearTimeout(pollTimer);
      pollTimer = window.setTimeout(() => void poll(), delay);
    };
    const poll = async () => {
      pollTimer = undefined;
      if (!active || !visible()) return;
      if (inFlight.current) { schedulePoll(); return; }
      inFlight.current = true;
      try {
        const response = await api.playbackCommands(cursor.current);
        if (!active) return;
        for (const command of response.commands) {
          cursor.current = command.operationId;
          await execute(command);
          if (!active) return;
        }
      } catch {
        // Coordination is additive; transient polling failures must not interrupt local playback.
      } finally {
        inFlight.current = false;
        schedulePoll();
      }
    };
    const resume = () => {
      clearTimers();
      if (!visible()) return;
      heartbeat();
      void poll();
    };
    const playbackStateChanged = () => {
      if (playerActive.current && visible()) schedulePoll(0);
    };
    document.addEventListener("visibilitychange", resume);
    window.addEventListener(stateEvent, playbackStateChanged);
    resume();
    return () => {
      active = false;
      clearTimers();
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener(stateEvent, playbackStateChanged);
    };
  }, [enabled]);
}

export function listenForPlaybackCommands(handler: (command: PlaybackCommand) => Promise<PlaybackCommandResultCode> | PlaybackCommandResultCode): () => void {
  const listener = (event: Event) => {
    const command = (event as CustomEvent<PlaybackCommand>).detail;
    void Promise.resolve(handler(command)).then((code) => publishPlaybackCommandResult({ operationId: command.operationId, status: code === "applied" ? "applied" : "failed", code })).catch(() => publishPlaybackCommandResult({ operationId: command.operationId, status: "failed", code: "execution_failed" }));
  };
  window.addEventListener(commandEvent, listener);
  return () => window.removeEventListener(commandEvent, listener);
}
