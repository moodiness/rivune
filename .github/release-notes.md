# Rivune v1.7.2

## Highlights

- Playback source delivery now distinguishes upstream startup timeouts, bounds stalled reads, preserves direct-capable streams for embedded mpv, and keeps HLS timelines aligned after seeking.
- Android now includes an embedded mpv engine alongside Media3, with automatic fallback and explicit player selection.
- Android playback recovery now offers retry, start-over, and source-selection actions without duplicate sessions, stale responses, or premature external-player launches.
- Embedded mpv startup and lifecycle handling now detect stalled playback, survive surface and audio-focus transitions, and report actionable failures.
- Media details now prefetch playback sources, optionally show them automatically, display labeled watched, trailer, and library actions, and include series cast on episode details.
- Stream panels and detail navigation now keep source requests cached while respecting Back, player close, loading, and mutation state.
- Player controls use a transparent bottom timeline surface, and active detail icons remain white for consistent contrast.

## Android installation

- Download `rivune-android-1.7.2.apk` from this release and complete Android's package-installation prompt. Android 8.0 or newer is required.
- The public application ID is `io.rivune.app`.
- `rivune-android-update.json` is the stable Android update feed. `SHA256SUMS` covers exactly the APK, update manifest, and matching `rivune-android-1.7.2-corresponding-source.tar.gz`; the source archive includes rebuild instructions, the exact Rivune revision, the complete libmpv/native source graph, and authenticated sources/build metadata for every packaged JVM dependency.
- The APK bundles the GPL-enabled libmpv/FFmpeg stack and is conveyed as a GPLv3 combined Android work. Its license and third-party notices are packaged in the APK; other Rivune components retain their separate repository licenses.
- Rivune never performs a silent install and contains no GitHub token. Android may require you to allow installs from the app that opens the APK before returning to Rivune.

## Upgrade notes

- Server operators can pull and recreate Rivune normally. This release adds no database migration and requires no new server environment variable.
- Android app preferences are device-local; profile playback and metadata preferences continue to be stored by the connected Rivune server.

## Container image

- `ghcr.io/moodiness/rivune:1.7.2`
- `ghcr.io/moodiness/rivune:1.7`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.7.0...v1.7.2
