# Rivune v1.8.3

## Highlights

- Android now ships under the stable release filename `Rivune-Android.apk`; the version remains authenticated in `rivune-update.json` and inside the signed package.
- Existing v1.8.2 Android clients accept the stable filename and continue to verify the exact tag-scoped URL, package identity, version, size, SHA-256, and signing certificate.
- The exact release set is `Rivune-Android.apk`, the sole schema-v2 `rivune-update.json` feed, x64 `Rivune-x64.exe`, and ARM64 `Rivune-arm64.exe`.
- Fresh deployment examples and Compose defaults now pin the immutable `1.8.3` container tag.

## Windows installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required.
- Compare the executable's SHA-256 with the digest GitHub publishes for that asset, place it in a user-writable folder, and run it directly; no installer or administrator access is required.
- The executables are intentionally unsigned so distribution remains free. Microsoft Defender SmartScreen may show an unknown-publisher warning; continue only for the exact file from this official release after its SHA-256 matches.
- Installations from v1.8.0 must download `Rivune-x64.exe` manually once because the former x64 asset is no longer published. Architecture-aware checks then select the x64 or ARM64 package automatically, at most once every 24 hours or manually from About.

## Android installation

- Download `Rivune-Android.apk` and complete Android's package-installation prompt. Android 8.0 or newer is required, and the public application ID remains `io.rivune.app`.
- The APK keeps the established Rivune release-signing identity so existing installations accept the update. Rivune verifies package identity, version, size, SHA-256, and signing certificate before Android shows its own confirmation.
- Android v1.7.2 and earlier installations that still request `rivune-android-update.json` must install v1.8.3 manually once. Current installations use the global `rivune-update.json` feed.
- The APK bundles the GPL-enabled libmpv/FFmpeg stack and is conveyed as a GPLv3 combined Android work. Its license and third-party notices are packaged in the APK; other Rivune components retain their separate repository licenses.

## Upgrade notes

- Server operators can pull and recreate Rivune normally. This release adds no database migration and requires no new server environment variable.
- Fresh deployments now default to the exact `1.8.3` container image; existing operators choose when to update `RIVUNE_VERSION` and recreate the service.
- GitHub publishes a SHA-256 digest for every release asset; no separate checksum-list asset is included.

## Container image

- `ghcr.io/moodiness/rivune:1.8.3`
- `ghcr.io/moodiness/rivune:1.8`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.8.2...v1.8.3
