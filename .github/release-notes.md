# Rivune v1.9.0

## Highlights

- Rivune now includes native SwiftUI applications for iPhone, iPad, Apple TV, Apple Vision Pro, and macOS, alongside the existing Android and Windows clients.
- The first direct Apple release publishes unsigned iOS, tvOS, and visionOS archives plus a universal unsigned macOS disk image. These artifacts contain no project signing identity or provisioning profile.
- Windows x64 and ARM64 remain portable unsigned executables. Automatic replacement verifies the exact GitHub asset URL, manifest version, size, SHA-256, and `ProductVersion` before and after staging, but it cannot verify an Authenticode publisher identity.
- Native clients now isolate credentials by server origin, reject unsafe credential transports, and bound API response sizes. Android and Windows retain the explicit trusted-LAN HTTP exception for loopback and private addresses.
- Playback proxy admission, playlist processing, startup reads, and idle reads are bounded to prevent a slow or oversized upstream from exhausting the server.
- The release pipeline now covers backend, browser E2E, OpenAPI, Android, Apple, Windows x64/ARM64, migrations, HTTPS proxy behavior, and native multi-architecture container builds before publication.

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
- Existing operators can set `RIVUNE_VERSION=1.9.0`, pull, and recreate only the Rivune service. Fresh Compose deployments now default to the immutable `1.9.0` image tag.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.9.0`
- `ghcr.io/moodiness/rivune:1.9`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.8.3...v1.9.0
