import { afterEach, describe, expect, it, vi } from "vitest";
import type { RivuneTvClient } from "./api";
import { TvCoordination } from "./coordination";
import type { PlaybackCommand, PlaybackDevice } from "./types";

const device: PlaybackDevice = {
  sessionId: "33333333-3333-4333-8333-333333333333", deviceId: "device", name: "TV", platform: "webos",
  capabilities: ["remote-control"], state: { status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 },
  current: false, lastSeenAt: "now", revision: 12,
};
const command: PlaybackCommand = {
  operationId: "44444444-4444-4444-8444-444444444444", command: "pause", senderDeviceName: "Phone", status: "pending",
  createdAt: "now", expiresAt: "later",
};

function retainedResultCount(engine: TvCoordination): number {
  // Focused white-box assertion: retention is intentionally not part of the public coordination API.
  const internals = engine as unknown as { readonly completed: ReadonlyMap<string, unknown> };
  return internals.completed.size;
}

describe("TV v22 coordination engine", () => {
  afterEach(() => vi.useRealTimers());

  it("applies an operationId once when the server retries the pending command", async () => {
    vi.useFakeTimers();
    const apply = vi.fn(async () => ({ status: "applied", code: "applied" } as const));
    const report = vi.fn()
      .mockRejectedValueOnce(new Error("temporary network failure"))
      .mockResolvedValue({ ...command, status: "applied", resultCode: "applied" });
    const client = {
      updatePlaybackDevice: vi.fn(async () => ({ ...device, current: true })),
      playbackDevices: vi.fn(async () => ({ devices: [device] })),
      playbackCommands: vi.fn().mockResolvedValueOnce({ commands: [command] }).mockResolvedValue({ commands: [] }),
      reportPlaybackCommandResult: report,
    } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, {
      state: () => ({ status: "idle", positionMilliseconds: 0, durationMilliseconds: 0 }), devices: vi.fn(), command: apply,
    });

    engine.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(apply).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(apply).toHaveBeenCalledTimes(1);
    expect(report).toHaveBeenCalledTimes(2);
    expect(retainedResultCount(engine)).toBe(0);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(report).toHaveBeenCalledTimes(2);
    engine.stop();
  });

  it("caps retained failures and per-tick retry work, expires them, and returns to idle polling", async () => {
    vi.useFakeTimers();
    const commands = Array.from({ length: 300 }, (_, index): PlaybackCommand => ({
      ...command,
      operationId: `44444444-4444-4444-8444-${String(index).padStart(12, "0")}`,
    }));
    const report = vi.fn(async () => { throw new Error("offline"); });
    const playbackCommands = vi.fn().mockResolvedValueOnce({ commands }).mockResolvedValue({ commands: [] });
    const client = {
      updatePlaybackDevice: vi.fn(async () => ({ ...device, current: true })),
      playbackDevices: vi.fn(async () => ({ devices: [] })),
      playbackCommands,
      reportPlaybackCommandResult: report,
    } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, {
      state: () => device.state, devices: vi.fn(), command: vi.fn(async () => ({ status: "applied", code: "applied" } as const)),
    });

    engine.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(report).toHaveBeenCalledTimes(300);
    expect(retainedResultCount(engine)).toBe(256);

    await vi.advanceTimersByTimeAsync(2_000);
    expect(report).toHaveBeenCalledTimes(304);
    expect(playbackCommands).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(report).toHaveBeenCalledTimes(308);
    expect(playbackCommands).toHaveBeenCalledTimes(3);

    await vi.advanceTimersByTimeAsync(297_000);
    expect(report.mock.calls.length).toBeLessThanOrEqual(404);
    expect(playbackCommands.mock.calls.length).toBeLessThan(30);
    expect(retainedResultCount(engine)).toBe(0);
    const expiredReportCalls = report.mock.calls.length;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(report).toHaveBeenCalledTimes(expiredReportCalls);
    engine.stop();
  });

  it("uses capped exponential backoff and drops a result after the attempt limit", async () => {
    vi.useFakeTimers();
    const report = vi.fn(async () => { throw new Error("offline"); });
    const playbackCommands = vi.fn().mockResolvedValueOnce({ commands: [command] }).mockResolvedValue({ commands: [] });
    const client = {
      updatePlaybackDevice: vi.fn(async () => ({ ...device, current: true })),
      playbackDevices: vi.fn(async () => ({ devices: [] })),
      playbackCommands,
      reportPlaybackCommandResult: report,
    } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, {
      state: () => device.state, devices: vi.fn(), command: vi.fn(async () => ({ status: "applied", code: "applied" } as const)),
    });

    engine.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(report).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(report).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(3_999);
    expect(report).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(report).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(8_000);
    expect(report).toHaveBeenCalledTimes(4);
    await vi.advanceTimersByTimeAsync(17_000);
    expect(report).toHaveBeenCalledTimes(5);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(report).toHaveBeenCalledTimes(6);
    expect(retainedResultCount(engine)).toBe(0);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(report).toHaveBeenCalledTimes(6);
    engine.stop();
  });

  it("keeps polling commands while a retry transport is unresolved", async () => {
    vi.useFakeTimers();
    const pending = new Promise<never>(() => undefined);
    const report = vi.fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockReturnValue(pending);
    const playbackCommands = vi.fn().mockResolvedValueOnce({ commands: [command] }).mockResolvedValue({ commands: [] });
    const client = {
      updatePlaybackDevice: vi.fn(async () => ({ ...device, current: true })),
      playbackDevices: vi.fn(async () => ({ devices: [] })),
      playbackCommands,
      reportPlaybackCommandResult: report,
    } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, {
      state: () => device.state, devices: vi.fn(), command: vi.fn(async () => ({ status: "applied", code: "applied" } as const)),
    });

    engine.start();
    await vi.advanceTimersByTimeAsync(2_000);
    expect(report).toHaveBeenCalledTimes(2);
    expect(playbackCommands).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(report).toHaveBeenCalledTimes(2);
    expect(playbackCommands).toHaveBeenCalledTimes(3);
    engine.stop();
  });

  it("polls idle commands at 15s, suspends hidden, and refreshes immediately when visible", async () => {
    vi.useFakeTimers();
    let visibility: DocumentVisibilityState = "visible";
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visibility });
    const updatePlaybackDevice = vi.fn(async () => ({ ...device, current: true }));
    const playbackDevices = vi.fn(async () => ({ devices: [] }));
    const playbackCommands = vi.fn(async () => ({ commands: [] }));
    const client = { updatePlaybackDevice, playbackDevices, playbackCommands } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, { state: () => device.state, devices: vi.fn(), command: vi.fn() });

    engine.start();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(playbackCommands.mock.calls.length).toBeLessThanOrEqual(5);
    expect(updatePlaybackDevice.mock.calls.length).toBeLessThanOrEqual(5);
    expect(playbackDevices).toHaveBeenCalledTimes(updatePlaybackDevice.mock.calls.length);

    visibility = "hidden";
    document.dispatchEvent(new Event("visibilitychange"));
    const hiddenCalls = playbackCommands.mock.calls.length;
    await vi.advanceTimersByTimeAsync(20_000);
    expect(playbackCommands).toHaveBeenCalledTimes(hiddenCalls);

    visibility = "visible";
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(0);
    expect(playbackCommands.mock.calls.length).toBe(hiddenCalls + 1);
    engine.stop();
  });

  it("polls active playback commands within two seconds", async () => {
    vi.useFakeTimers();
    const playbackCommands = vi.fn(async () => ({ commands: [] }));
    const client = {
      updatePlaybackDevice: vi.fn(async () => ({ ...device, current: true })), playbackDevices: vi.fn(async () => ({ devices: [] })), playbackCommands,
    } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, { state: () => ({ ...device.state, status: "playing", item: { titleId: "title", mediaType: "movie", resourceId: "movie", title: "Movie" } }), devices: vi.fn(), command: vi.fn() });
    engine.start();
    await vi.advanceTimersByTimeAsync(1_999);
    expect(playbackCommands).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(playbackCommands).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(58_000);
    expect(playbackCommands.mock.calls.length).toBeGreaterThanOrEqual(30);
    engine.stop();
  });

  it("copies the observed revision and does not wait outgoing when stale send fails", async () => {
    const stale = Object.assign(new Error("stale"), { status: 409, code: "stale_target" });
    const sendPlaybackCommand = vi.fn(async () => { throw stale; });
    const outgoingPlaybackCommand = vi.fn();
    const client = { sendPlaybackCommand, outgoingPlaybackCommand } as unknown as RivuneTvClient;
    const engine = new TvCoordination(client, { state: vi.fn(), devices: vi.fn(), command: vi.fn() });

    await expect(engine.send(device, "play")).rejects.toBe(stale);
    expect(sendPlaybackCommand).toHaveBeenCalledWith(device.sessionId, expect.objectContaining({ command: "play", targetRevision: 12 }));
    expect(outgoingPlaybackCommand).not.toHaveBeenCalled();
  });
});
