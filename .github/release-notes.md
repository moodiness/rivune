# Rivune v1.6.0

## Highlights

- Rivune now ships an official universal Android APK for phones, tablets, and Android TV from each GitHub Release.
- The Android app can check for updates automatically at most once every 24 hours or manually from preferences. It asks before downloading, verifies the APK package, version, size, SHA-256, and signing certificate, then hands installation to Android's mandatory system confirmation.
- Release publication now verifies the signed Android artifact and its dedicated update manifest before publishing the multi-architecture container image. GitHub Release assets remain hidden in a draft until the complete set has been uploaded and verified.

## Android installation

- Download `rivune-android-1.6.0.apk` from this release and complete Android's package-installation prompt. Android 8.0 or newer is required.
- The public application ID is `io.rivune.app`.
- `rivune-android-update.json` is the stable, Android-specific update feed. `SHA256SUMS` covers exactly the APK and that manifest.
- Rivune never performs a silent install and contains no GitHub token. Android may require you to allow installs from the app that opens the APK before returning to Rivune.

## Upgrade notes

- Server operators can pull and recreate Rivune normally. This release does not require a new server environment variable for Android updates.
- Existing web and container deployments continue to use the normal release image. Android update signing credentials exist only in the protected GitHub release environment and are not server configuration.

## Container image

- `ghcr.io/moodiness/rivune:1.6.0`
- `ghcr.io/moodiness/rivune:1.6`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.5.3...v1.6.0
