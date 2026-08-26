import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RivuneTvClient } from "./api";
import type { TvPlayer, TvPlayerEvents } from "./platform";
import { Player } from "./Player";

class FakePlayer implements TvPlayer {
  events?: TvPlayerEvents;
  play = vi.fn(async () => { this.events?.onState("playing"); });
  pause = vi.fn(async () => { this.events?.onState("paused"); });
  seek = vi.fn(async (position: number) => { this.events?.onTime(position, 120); });
  selectAudio = vi.fn(async () => undefined);
  selectSubtitle = vi.fn(async () => undefined);
  stop = vi.fn(async () => undefined);
  destroy = vi.fn();
  async load(_request: unknown, events: TvPlayerEvents) {
    this.events = events;
    events.onReady(120);
    events.onTime(5, 120);
  }
}

describe("TV Player v22 playback presentation", () => {
  let container: HTMLDivElement;
  let root: Root;
  let nativePlayer: FakePlayer;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    nativePlayer = new FakePlayer();
    window.RivunePlatformAdapter = {
      platform: "tizen",
      deviceName: async () => "Samsung TV",
      capabilities: () => ({
        streamingProtocols: ["hls"], containers: ["mp4"], videoCodecs: ["h264"], audioCodecs: ["aac"], hdrFormats: [],
        processingModes: ["direct", "remux", "transcode_audio", "transcode"], maximumHeight: 2160,
        maximumVideoBitrateKbps: 20_000, maximumAudioChannels: 6, subtitleModes: ["external", "burn"],
      }),
      createPlayer: () => nativePlayer,
      exitApp: vi.fn(),
    };
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows a focusable closed-outcome card, effective quality, and applies transport once", async () => {
    const onRemoteResult = vi.fn();
    const timerSpy = vi.spyOn(window, "setTimeout");
    let controller: { apply(command: { command: "pause"; operationId: string; senderDeviceName: string; status: "pending"; createdAt: string; expiresAt: string }): Promise<unknown> } | null = null;
    const client = {
      issuer: "https://media.example.com",
      resolvePlayback: vi.fn(async () => ({
        id: "session-1", selectedSourceId: "source-1", subtitles: [], providerErrors: [], expiresAt: "later",
        sources: [{ id: "source-1", addonId: "addon-1", manifestId: "manifest", mode: "transcode", protocol: "hls", container: "mp4", url: "/stream", compatible: true,
          decision: { reason: "video_transcode_required", reasons: ["resolution_limit", "bitrate_limit"], videoAction: "transcode", audioAction: "copy", subtitleAction: "none", toneMapping: false } }],
      })),
      resolveResourceUrl: vi.fn(() => "https://media.example.com/stream"),
      stopPlayback: vi.fn(async () => undefined),
    } as unknown as RivuneTvClient;

    await act(async () => {
      root.render(<Player
        client={client} item={{ id: "movie-1", titleId: "title-1", resourceId: "tmdb:1", mediaType: "movie", title: "Movie" }}
        sourceRef="ref" titleId="title-1" startSeconds={5} progress={null}
        preparation={{ sourceRef: "ref", mode: "transcode", protocol: "hls", subtitleCount: 0, expiresAt: "later", decision: { reason: "video_transcode_required", reasons: ["resolution_limit", "bitrate_limit"], videoAction: "transcode", audioAction: "copy", subtitleAction: "none", toneMapping: false } }}
        remoteOperationId="44444444-4444-4444-8444-444444444444" devices={[]} qualityPreset="balanced"
        onSendToDevice={vi.fn()} onControlDevice={vi.fn()} onController={(value) => { controller = value; }} onPlaybackState={vi.fn()}
        onRemoteResult={onRemoteResult} onClose={vi.fn()} setBackHandler={vi.fn()}
      />);
    });

    await vi.waitFor(() => expect(onRemoteResult).toHaveBeenCalledWith("44444444-4444-4444-8444-444444444444", { status: "applied", code: "applied" }));
    const card = container.querySelector<HTMLButtonElement>(".tv-playback-decision");
    expect(card?.textContent).toContain("Video conversion");
    expect(card?.textContent).toContain("Resolution limited");
    expect(card?.textContent).toContain("8000 kb/s");
    expect(card?.tabIndex).toBe(0);
    const progressbar = container.querySelector<HTMLElement>("[role='progressbar']");
    expect(progressbar?.getAttribute("aria-label")).toBe("Playback progress");
    expect(progressbar?.getAttribute("aria-valuenow")).toBe("5");
    expect(progressbar?.getAttribute("aria-valuemax")).toBe("120");

    const hideControls = timerSpy.mock.calls.find(([, delay]) => delay === 7000)?.[0];
    expect(typeof hideControls).toBe("function");
    await act(async () => { if (typeof hideControls === "function") hideControls(); });
    const chrome = container.querySelector<HTMLElement>(".tv-player__chrome");
    expect(chrome?.getAttribute("aria-hidden")).toBe("true");
    expect(chrome?.hasAttribute("inert")).toBe(true);

    await act(async () => { await controller?.apply({ operationId: "op", command: "pause", senderDeviceName: "Phone", status: "pending", createdAt: "now", expiresAt: "later" }); });
    expect(nativePlayer.pause).toHaveBeenCalledTimes(1);
  });
});
