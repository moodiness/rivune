import { describe, expect, it, vi } from "vitest";
import type { RivuneTvClient } from "./api";
import { TvFailoverController } from "./failover";
import { classifyPlaybackFailure } from "./Player";
import type { PlaybackFailoverState } from "./types";

function state(overrides: Partial<PlaybackFailoverState> = {}): PlaybackFailoverState {
  return {
    id: "failover", currentSourceRef: "source-ref-000001", currentPosition: 0, positionSeconds: 12,
    attemptCount: 0, maximumAttempts: 2, revision: 1, status: "active",
    candidateHealth: [{ position: 0, status: "current" }, { position: 1, status: "available" }], expiresAt: "2099-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("TV playback failover", () => {
  it("advances an eligible source failure with position and optimistic revision", async () => {
    const advancePlaybackFailover = vi.fn().mockResolvedValue(state({ currentSourceRef: "source-ref-000002", currentPosition: 1, positionSeconds: 47, attemptCount: 1, revision: 2 }));
    const controller = new TvFailoverController({ advancePlaybackFailover } as unknown as RivuneTvClient, state());
    await expect(controller.advance("source_timeout", 47)).resolves.toMatchObject({ sourceRef: "source-ref-000002", positionSeconds: 47 });
    expect(advancePlaybackFailover).toHaveBeenCalledWith("failover", { error: "source_timeout", positionSeconds: 47, expectedRevision: 1 });
  });

  it("never advances policy/decode failures or a closed attempt budget", async () => {
    const advancePlaybackFailover = vi.fn();
    const client = { advancePlaybackFailover } as unknown as RivuneTvClient;
    await expect(new TvFailoverController(client, state()).advance("decode_failed", 4)).resolves.toBeNull();
    await expect(new TvFailoverController(client, state({ attemptCount: 2 })).advance("source_failed", 4)).resolves.toBeNull();
    expect(advancePlaybackFailover).not.toHaveBeenCalled();
  });

  it("cancels once and uses closed local error classification", async () => {
    const cancelPlaybackFailover = vi.fn().mockResolvedValue(undefined);
    const controller = new TvFailoverController({ cancelPlaybackFailover } as unknown as RivuneTvClient, state());
    await controller.cancel();
    await controller.cancel();
    expect(cancelPlaybackFailover).toHaveBeenCalledOnce();
    expect(classifyPlaybackFailure("network timed out")).toBe("source_timeout");
    expect(classifyPlaybackFailure("codec decode failed")).toBe("decode_failed");
    expect(classifyPlaybackFailure("HTTP 403 forbidden")).toBe("access_denied");
  });
});
