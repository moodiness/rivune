import { describe, expect, it } from "vitest";
import {
  emptyStoredRuntimeState,
  parseLatestRelease,
  parseRuntimeBundle,
  prepareRuntime,
  parseUpdateManifest,
  rollbackRuntime,
  type RuntimeBundle,
} from "./contracts";

const assetNames = [
  "Rivune-Android.apk",
  "Rivune-TV-Installer-Linux-arm64.zip",
  "Rivune-TV-Installer-Linux-x64.zip",
  "Rivune-TV-Installer-Windows-arm64.exe",
  "Rivune-TV-Installer-Windows-x64.exe",
  "Rivune-TV-Installer-macOS-arm64.zip",
  "Rivune-TV-Installer-macOS-x64.zip",
  "Rivune-Tizen.wgt",
  "Rivune-TV-runtime.json",
  "Rivune-arm64.exe",
  "Rivune-iOS-unsigned.ipa",
  "Rivune-macOS.dmg",
  "Rivune-tvOS-unsigned.ipa",
  "Rivune-visionOS-unsigned.ipa",
  "Rivune-webOS.ipk",
  "Rivune-x64.exe",
  "rivune-update.json",
];

function release(version = "2.0.0") {
  const tag = `v${version}`;
  return {
    tag_name: tag,
    name: tag,
    html_url: `https://github.com/moodiness/rivune/releases/tag/${tag}`,
    published_at: "2026-08-23T12:00:00Z",
    draft: false,
    prerelease: false,
    assets: assetNames.map((name, index) => ({
      id: index + 1,
      name,
      state: "uploaded",
      size: name === "Rivune-TV-runtime.json" ? 4096 : name === "rivune-update.json" ? 8202 : 8192 + index,
      digest: `sha256:${(name === "Rivune-TV-runtime.json" ? "3" : name === "rivune-update.json" ? "2" : String((index % 9) + 1)).repeat(64)}`,
      url: `https://api.github.com/repos/moodiness/rivune/releases/assets/${index + 1}`,
      browser_download_url: `https://github.com/moodiness/rivune/releases/download/${tag}/${name}`,
    })),
  };
}

function runtime(version: string): RuntimeBundle {
  return {
    schemaVersion: 1,
    version,
    application: { javascript: "window.applicationLoaded=true;", css: "body{color:white}" },
    platforms: {
      webos: { javascript: "window.platform='webos';" },
      tizen: { javascript: "window.platform='tizen';" },
    },
  };
}

describe("GitHub TV release validation", () => {
  it("binds an available runtime mirror to exact GitHub release metadata", () => {
    expect(parseLatestRelease(release(), "1.12.0", "webos")).toEqual({
      updateAvailable: true,
      release: {
        version: "2.0.0",
        tagName: "v2.0.0",
        manifest: {
          size: 8202,
          sha256: "2".repeat(64),
          mirrorUrl: "https://moodiness.github.io/rivune/tv-runtime/v2.0.0/rivune-update.json",
        },
        runtime: {
          size: 4096,
          sha256: "3".repeat(64),
        },
      },
    });
  });

  it("treats an older published release as current without requiring future TV assets", () => {
    const oldRelease = release("1.11.4");
    oldRelease.assets = [];
    expect(parseLatestRelease(oldRelease, "1.12.0", "tizen")).toEqual({ updateAvailable: false, version: "1.11.4" });
  });

  it("rejects incomplete, redirected, or unhashed update assets", () => {
    const missing = release();
    missing.assets.pop();
    expect(() => parseLatestRelease(missing, "1.12.0", "webos")).toThrow();

    const redirected = release();
    redirected.assets[2].browser_download_url = "https://evil.example/runtime.json";
    expect(() => parseLatestRelease(redirected, "1.12.0", "webos")).toThrow();

    const unhashed = release();
    unhashed.assets[2].digest = "sha256:pending";
    expect(() => parseLatestRelease(unhashed, "1.12.0", "tizen")).toThrow();
  });

  it("uses rivune-update.json as the authoritative runtime contract", () => {
    const checked = parseLatestRelease(release(), "1.12.0", "webos");
    if (!checked.updateAvailable) throw new Error("Expected an available update fixture.");
    const manifest = {
      schemaVersion: 2,
      channel: "stable",
      version: "2.0.0",
      tagName: "v2.0.0",
      publishedAt: "2026-08-23T12:00:00Z",
      releaseUrl: "https://github.com/moodiness/rivune/releases/tag/v2.0.0",
      packages: {
        android: {}, ios: {}, tvos: {}, visionos: {}, macos: {}, windowsX64: {}, windowsArm64: {},
        webos: {
          format: "ipk", architectures: ["universal"], minimumOsVersion: "4.0", applicationId: "io.rivune.app.webos",
          signature: "unsigned", fileName: "Rivune-webOS.ipk", url: "https://github.com/moodiness/rivune/releases/download/v2.0.0/Rivune-webOS.ipk",
          size: 8192, sha256: "8".repeat(64),
        },
        tizen: {
          format: "wgt", architectures: ["universal"], minimumOsVersion: "5.5", applicationId: "RivuneTV01.Rivune",
          signature: "unsigned", fileName: "Rivune-Tizen.wgt", url: "https://github.com/moodiness/rivune/releases/download/v2.0.0/Rivune-Tizen.wgt",
          size: 8192, sha256: "9".repeat(64),
        },
        tvRuntime: {
          format: "json", platforms: ["webos", "tizen"], fileName: "Rivune-TV-runtime.json",
          url: "https://github.com/moodiness/rivune/releases/download/v2.0.0/Rivune-TV-runtime.json",
          size: 4096, sha256: "3".repeat(64),
        },
      },
    };
    expect(parseUpdateManifest(manifest, checked.release, "webos")).toEqual({
      version: "2.0.0",
      tagName: "v2.0.0",
      size: 4096,
      sha256: "3".repeat(64),
      mirrorUrl: "https://moodiness.github.io/rivune/tv-runtime/v2.0.0/Rivune-TV-runtime.json",
    });
    manifest.packages.tvRuntime.sha256 = "4".repeat(64);
    expect(() => parseUpdateManifest(manifest, checked.release, "webos")).toThrow();
  });
});

describe("TV runtime activation", () => {
  it("validates both platform adapters and exact runtime versions", () => {
    expect(parseRuntimeBundle(runtime("1.12.0"), "1.12.0").platforms.tizen.javascript).toContain("tizen");
    expect(() => parseRuntimeBundle({ ...runtime("1.12.0"), extra: true })).toThrow();
    expect(() => parseRuntimeBundle(runtime("1.13.0"), "1.12.0")).toThrow();
  });

  it("promotes a staged runtime only on the next boot and records rollback state", () => {
    const packaged = runtime("1.12.0");
    const active = runtime("1.13.0");
    const staged = runtime("1.14.0");
    const prepared = prepareRuntime(packaged, {
      ...emptyStoredRuntimeState(),
      active,
      staged,
    });
    expect(prepared.runtime.version).toBe("1.14.0");
    expect(prepared.source).toBe("cached");
    expect(prepared.state.previous?.version).toBe("1.13.0");
    expect(prepared.state.pendingVersion).toBe("1.14.0");
  });

  it("rolls back an unconfirmed runtime and lets a newer package supersede cache", () => {
    const packaged = runtime("1.14.0");
    const failed = runtime("1.13.0");
    const previous = runtime("1.12.0");
    const afterFailedBoot = prepareRuntime(packaged, {
      ...emptyStoredRuntimeState(),
      active: failed,
      previous,
      pendingVersion: failed.version,
    });
    expect(afterFailedBoot.runtime).toBe(packaged);
    expect(afterFailedBoot.state.active).toBeNull();

    const rolledBack = rollbackRuntime({
      ...emptyStoredRuntimeState(),
      active: runtime("1.15.0"),
      previous: runtime("1.14.1"),
      pendingVersion: "1.15.0",
    });
    expect(rolledBack.active?.version).toBe("1.14.1");
    expect(rolledBack.pendingVersion).toBeNull();
  });
});
