# Rivune v1.8.0

## Highlights

- Rivune for Windows debuts as a native WinUI 3 client with responsive desktop, compact, TV, reduced-motion, and high-contrast layouts; profile onboarding; Home, Search, Library, and Calendar; title details; and native HTTP/HLS playback.
- The Windows player synchronizes resume and completion state, supports audio and subtitle selection, speed, seeking, chapters, intro/recap/outro actions, next-episode playback, recovery actions, full screen, and keyboard/gamepad navigation.
- Continue Watching and Next Up now carry localized episode titles, stills, and air dates directly from the server. Web, Android, Apple, and Windows models consume the same additive protocol fields, avoiding per-season client fan-out and preserving useful cached Home cards while data refreshes.
- The global `rivune-update.json` feed now advertises both the signed Android APK and one portable Windows x64 executable while retaining `rivune-android-update.json` for already-installed Android v1.7.2 clients.
- Windows updates require consent, enforce the exact GitHub release URL and `Rivune.exe` filename, stream with bounded size and SHA-256 verification, replace the portable executable only after exit, and restore the previous file if replacement fails.

## Windows installation

- Download `Rivune.exe` from this release. Windows 10 build 19041 or newer on x64 is required.
- Compare its SHA-256 with the digest GitHub publishes for the asset, place `Rivune.exe` in a user-writable folder, and run it directly; no installer or administrator access is required.
- The executable is intentionally unsigned so distribution remains free. Microsoft Defender SmartScreen may show an unknown-publisher warning; continue only for the exact file from this official release after its SHA-256 matches.
- Later checks are automatic at most once every 24 hours or manual from About. Rivune never updates without consent and contains no GitHub token.

## Android installation

- Download `rivune-android-1.8.0.apk` and complete Android's package-installation prompt. Android 8.0 or newer is required, and the public application ID remains `io.rivune.app`.
- The APK keeps the established Rivune release-signing identity so existing installations accept the update. Rivune verifies package identity, version, size, SHA-256, and signing certificate before Android shows its own confirmation.
- The APK bundles the GPL-enabled libmpv/FFmpeg stack and is conveyed as a GPLv3 combined Android work. Its license and third-party notices are packaged in the APK; other Rivune components retain their separate repository licenses.

## Upgrade notes

- Server operators can pull and recreate Rivune normally. This release adds no database migration and requires no new server environment variable.
- Continue Watching responses add optional episode metadata; protocol version 20 and existing clients remain compatible.
- GitHub publishes a SHA-256 digest for every release asset.

## Container image

- `ghcr.io/moodiness/rivune:1.8.0`
- `ghcr.io/moodiness/rivune:1.8`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.7.2...v1.8.0
