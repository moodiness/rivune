# Rivune v1.9.1

## Highlights

- This maintenance release refreshes supported dependencies across the server build, web client, Android client, Windows client, and GitHub release pipeline.
- The container frontend build now uses Node.js 24 LTS instead of the non-LTS Node.js 26 proposal.
- The web stack moves to Vite 8 and refreshes HLS.js, React tooling, and icons; Android refreshes Compose, Kotlin, Gradle, and OkHttp 5.
- Release automation updates its pinned build, language, and artifact actions while preserving immutable inputs and the existing publication policy.
- No public protocol, database schema, configuration, or user workflow changes are introduced by this release.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, and multi-architecture container gates run before publication.

## Apple installation

- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Every Apple artifact embeds Rivune, MPVKit, mpv, and FFmpeg licensing notices.

## Windows installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required.
- Keep the executable in a user-writable local folder. Rivune is portable and has no installer, but local preferences and DPAPI-protected sessions remain under `%LOCALAPPDATA%\Rivune\`.
- The Windows executables are not Authenticode-signed and may trigger SmartScreen. Verify that the download URL is the matching `moodiness/rivune` GitHub Release and compare the asset SHA-256 before running it.

## Android installation

- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. Android 8.0 or newer is required, and the application ID remains `io.rivune.app`.
- The APK keeps the established Rivune release-signing identity. Rivune and Android verify package identity, version, size, SHA-256, and signing certificate during an update.
- The APK bundles the GPL-enabled libmpv/FFmpeg stack and is conveyed as a GPLv3 combined Android work; the required license and source-location notices are packaged in the application.

## Upgrade notes

- This release adds no database migration and requires no new server environment variable. Back up PostgreSQL and the complete encryption keyring before every upgrade.
- Existing operators can set `RIVUNE_VERSION=1.9.1`, pull, and recreate only the Rivune service. Fresh Compose deployments now default to the immutable `1.9.1` image tag.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.9.1`
- `ghcr.io/moodiness/rivune:1.9`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.9.0...v1.9.1
