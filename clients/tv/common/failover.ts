import type { RivuneTvClient } from "./api";
import type { PlaybackFailoverError, PlaybackFailoverState } from "./types";

const ELIGIBLE_FAILURES: Record<PlaybackFailoverError, boolean> = {
  source_failed: true,
  source_timeout: true,
  ended_early: true,
  decode_failed: false,
  access_denied: false,
  user_cancelled: false,
};

export interface FailoverAdvance {
  sourceRef: string;
  positionSeconds: number;
  state: PlaybackFailoverState;
}

export class TvFailoverController {
  private state: PlaybackFailoverState;
  private cancelled = false;

  constructor(private readonly client: RivuneTvClient, initial: PlaybackFailoverState) {
    this.state = initial;
  }

  get snapshot(): PlaybackFailoverState {
    return this.state;
  }

  async advance(error: PlaybackFailoverError, positionSeconds: number): Promise<FailoverAdvance | null> {
    if (this.cancelled || this.state.status !== "active" || !ELIGIBLE_FAILURES[error] || this.state.attemptCount >= this.state.maximumAttempts) return null;
    const safePosition = Math.max(0, Math.min(86_400, positionSeconds));
    const next = await this.client.advancePlaybackFailover(this.state.id, {
      error,
      positionSeconds: safePosition,
      expectedRevision: this.state.revision,
    });
    this.state = next;
    if (next.status !== "active" || !next.currentSourceRef || next.attemptCount > next.maximumAttempts) return null;
    return { sourceRef: next.currentSourceRef, positionSeconds: next.positionSeconds, state: next };
  }

  async cancel(): Promise<void> {
    if (this.cancelled || this.state.status !== "active") return;
    this.cancelled = true;
    await this.client.cancelPlaybackFailover(this.state.id);
  }
}
