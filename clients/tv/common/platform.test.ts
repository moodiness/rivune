import { afterEach, describe, expect, it } from "vitest";
import { classifyTvNetwork, tvPlaybackPolicy, type RivunePlatformAdapter } from "./platform";

const adapter = {
  platform: "webos",
  deviceName: async () => "TV",
  capabilities: () => ({
    streamingProtocols: ["hls"], containers: ["mp4"], videoCodecs: ["h264"], audioCodecs: ["aac"], hdrFormats: [],
    processingModes: ["direct", "remux", "transcode_audio", "transcode"] as const,
    maximumHeight: 2160, maximumVideoBitrateKbps: 12_000, maximumAudioChannels: 6,
    subtitleModes: ["external", "burn"] as const,
  }),
  createPlayer: () => { throw new Error("unused"); },
  exitApp: () => undefined,
} as RivunePlatformAdapter;

describe("TV local quality policy", () => {
  afterEach(() => { Object.defineProperty(navigator, "connection", { configurable: true, value: undefined }); });

  it("classifies conservatively and applies the lower of device capacity and preset limit", () => {
    expect(classifyTvNetwork("http://192.168.1.12:8090")).toBe("local");
    expect(classifyTvNetwork("https://media.example.com")).toBe("remote_wifi");
    expect(classifyTvNetwork("https://fcdn.example.com")).toBe("remote_wifi");
    expect(tvPlaybackPolicy(adapter, "https://media.example.com", "economy")).toMatchObject({
      networkClass: "remote_wifi", maximumHeight: 480, maximumVideoBitrateKbps: 2_000, offlineMedia: false,
      capabilities: { maximumHeight: 480, maximumVideoBitrateKbps: 2_000 },
    });
    expect(tvPlaybackPolicy(adapter, "https://media.example.com", "balanced")).toMatchObject({ maximumHeight: 1080, maximumVideoBitrateKbps: 8_000 });
    expect(tvPlaybackPolicy(adapter, "https://media.example.com", "maximum")).toMatchObject({ maximumHeight: 2160, maximumVideoBitrateKbps: 12_000 });
  });

  it("limits automatic mobile playback to 720p and 5000 kb/s", () => {
    Object.defineProperty(navigator, "connection", { configurable: true, value: { type: "cellular", effectiveType: "4g" } });
    expect(tvPlaybackPolicy(adapter, "https://media.example.com", "automatic")).toMatchObject({ networkClass: "mobile", maximumHeight: 720, maximumVideoBitrateKbps: 5_000 });
  });
});
