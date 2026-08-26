import type { RivuneTvClient } from "./api";
import type {
  PlaybackCommand,
  PlaybackCommandInput,
  PlaybackCommandResultCode,
  PlaybackCommandStatus,
  PlaybackDevice,
  PlaybackDeviceState,
  PlaybackLoadMode,
} from "./types";

export type TvCommandResult = { status: Exclude<PlaybackCommandStatus, "pending">; code: PlaybackCommandResultCode };
export type TvCoordinationHandlers = {
  state(): PlaybackDeviceState;
  devices(devices: PlaybackDevice[]): void;
  command(command: PlaybackCommand): Promise<TvCommandResult>;
};

const FAILED: TvCommandResult = { status: "failed", code: "execution_failed" };
const MAX_COMPLETED_RESULTS = 256;
const MAX_REPORT_ATTEMPTS = 6;
const MAX_REPORT_AGE_MILLISECONDS = 5 * 60_000;
const MAX_REPORTS_PER_TICK = 4;
const REPORT_RETRY_BASE_MILLISECONDS = 2_000;
const REPORT_RETRY_CAP_MILLISECONDS = 30_000;

type CompletedResult = {
  result: TvCommandResult;
  createdAt: number;
  attempts: number;
  nextAttemptAt: number;
  inFlight: boolean;
};

export function operationId(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  return Array.from(bytes, (value, index) => `${index === 4 || index === 6 || index === 8 || index === 10 ? "-" : ""}${value.toString(16).padStart(2, "0")}`).join("");
}

export class TvCoordination {
  private timer = 0;
  private stopped = true;
  private running = false;
  private after: string | undefined;
  private readonly executing = new Set<string>();
  private readonly completed = new Map<string, CompletedResult>();
  private readonly seen = new Set<string>();
  private lastPresenceAt = 0;
  private lastCommandAt = 0;
  private readonly visibilityChanged = () => {
    window.clearTimeout(this.timer);
    if (!this.stopped && document.visibilityState === "visible") {
      this.lastPresenceAt = 0;
      void this.tick();
    }
  };

  constructor(private readonly client: RivuneTvClient, private readonly handlers: TvCoordinationHandlers) {}
  start(): void {
    if (!this.stopped) return;
    this.stopped = false;
    document.addEventListener("visibilitychange", this.visibilityChanged);
    if (document.visibilityState === "visible") void this.tick();
  }

  stop(): void {
    this.stopped = true;
    window.clearTimeout(this.timer);
    document.removeEventListener("visibilitychange", this.visibilityChanged);
  }

  async send(
    device: PlaybackDevice,
    command: PlaybackCommandInput["command"],
    values: Omit<PlaybackCommandInput, "operationId" | "command" | "targetRevision"> = {},
  ): Promise<string> {
    const id = operationId();
    await this.client.sendPlaybackCommand(device.sessionId, { operationId: id, command, targetRevision: device.revision, ...values });
    this.lastCommandAt = Date.now();
    return id;
  }

  async sendLoad(device: PlaybackDevice, input: Omit<PlaybackCommandInput, "operationId" | "command" | "targetRevision" | "mode">, mode: PlaybackLoadMode): Promise<string> {
    return this.send(device, "load", { ...input, mode });
  }

  async waitOutgoing(id: string, timeoutMilliseconds = 30_000): Promise<TvCommandResult> {
    const deadline = Date.now() + timeoutMilliseconds;
    while (Date.now() < deadline) {
      const outcome = await this.client.outgoingPlaybackCommand(id);
      if (outcome.status !== "pending") {
        return { status: outcome.status, code: outcome.resultCode ?? "execution_failed" };
      }
      await new Promise<void>((resolve) => { window.setTimeout(resolve, 750); });
    }
    return { status: "expired", code: "expired" };
  }

  private async tick(): Promise<void> {
    if (this.stopped || this.running || document.visibilityState !== "visible") return;
    this.running = true;
    try {
      const now = Date.now();
      this.expireCompleted(now);
      if (now - this.lastPresenceAt >= 15_000) {
        try {
          await this.client.updatePlaybackDevice({ capabilities: ["remote-control", "load-target", "playback-command-results"], state: this.handlers.state() });
          const deviceList = await this.client.playbackDevices();
          this.handlers.devices(deviceList.devices.filter((device) => !device.current && device.capabilities.includes("remote-control")));
          this.lastPresenceAt = now;
        } catch {
          // Presence failures do not block the command channel.
        }
      }
      const commandList = await this.client.playbackCommands(this.after);
      for (const command of commandList.commands) {
        this.after = command.operationId;
        this.lastCommandAt = Date.now();
        if (!this.seen.has(command.operationId)) {
          this.seen.add(command.operationId);
          if (this.seen.size > 1_024) this.seen.delete(this.seen.values().next().value!);
          this.executing.add(command.operationId);
          const expired = Number.isFinite(new Date(command.expiresAt).getTime()) && new Date(command.expiresAt).getTime() <= Date.now();
          const execution = expired ? Promise.resolve<TvCommandResult>({ status: "expired", code: "expired" }) : this.handlers.command(command).catch(() => FAILED);
          void execution.then((result) => {
            this.retainCompleted(command.operationId, result);
            this.executing.delete(command.operationId);
          });
        }
      }
    } catch {
      // Presence, commands, and completed-result reports retry independently.
    } finally {
      if (!this.stopped && document.visibilityState === "visible") this.retryCompleted(Date.now());
      this.running = false;
      if (!this.stopped && document.visibilityState === "visible") {
        const state = this.handlers.state();
        const active = state.status !== "idle" || this.executing.size > 0 || Date.now() - this.lastCommandAt < 15_000;
        this.timer = window.setTimeout(() => void this.tick(), active ? 2_000 : 15_000);
      }
    }
  }

  private retainCompleted(id: string, result: TvCommandResult): void {
    const now = Date.now();
    if (this.completed.size >= MAX_COMPLETED_RESULTS) this.completed.delete(this.completed.keys().next().value!);
    const completed: CompletedResult = { result, createdAt: now, attempts: 0, nextAttemptAt: now, inFlight: false };
    this.completed.set(id, completed);
    this.attemptReport(id, completed);
  }

  private retryCompleted(now: number): void {
    let started = 0;
    for (const [id, completed] of this.completed) {
      if (started >= MAX_REPORTS_PER_TICK) break;
      if (completed.inFlight || completed.nextAttemptAt > now) continue;
      started += 1;
      this.attemptReport(id, completed);
    }
  }

  private expireCompleted(now: number): void {
    for (const [id, completed] of this.completed) {
      if (completed.attempts >= MAX_REPORT_ATTEMPTS || now - completed.createdAt >= MAX_REPORT_AGE_MILLISECONDS) this.completed.delete(id);
    }
  }

  private attemptReport(id: string, completed: CompletedResult): void {
    completed.inFlight = true;
    completed.attempts += 1;
    void Promise.resolve().then(() => this.client.reportPlaybackCommandResult(id, completed.result)).then(() => {
      if (this.completed.get(id) === completed) this.completed.delete(id);
    }, () => {
      if (this.completed.get(id) !== completed) return;
      completed.inFlight = false;
      const now = Date.now();
      if (completed.attempts >= MAX_REPORT_ATTEMPTS || now - completed.createdAt >= MAX_REPORT_AGE_MILLISECONDS) {
        this.completed.delete(id);
        return;
      }
      // Move a failed attempt behind untouched results so a large batch receives fair retry work.
      this.completed.delete(id);
      this.completed.set(id, completed);
      completed.nextAttemptAt = now + Math.min(REPORT_RETRY_CAP_MILLISECONDS, REPORT_RETRY_BASE_MILLISECONDS * 2 ** (completed.attempts - 1));
    });
  }
}
