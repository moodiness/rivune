# Rivune v1.10.0

## Highlights

- Android, Apple, and Windows clients can now discover nearby self-hosted Rivune servers through the opt-in `_rivune._tcp` DNS-SD service. Each client shows the announced origin and transport security before an explicit connection; manual address entry remains available.
- The public Rivune applications page is now available in English and French with platform detection, immutable GitHub release links, QR codes, complete SHA-256 fingerprints, and platform-specific installation warnings.
- Apple users can generate a local command for iOS, tvOS, or visionOS that checks out this exact release, builds with their selected team and bundle identifier, verifies the signature and provisioning profile, and installs on the connected device. Apple credentials and signing material never leave Xcode and the local Keychain.
- The schema-v2 update manifest and release pipeline now describe, fingerprint, and validate all seven application packages while preserving the exact eight-asset release contract.
- Horizontal web carousels retain momentum when Chromium rounds subpixel scroll positions, eliminating premature drag stops.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, and multi-architecture container gates run before publication.

## Apple installation

- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- The applications page generates the exact `clients/apple/Scripts/sign-and-install.sh` command for this release. The script accepts only the platform, development team, unique bundle identifier, and connected-device identifier; it accepts no password, private key, certificate, or provisioning profile.
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

- This release adds no database migration or required server environment variable. Back up PostgreSQL and the complete encryption keyring before every upgrade.
- Automatic LAN discovery is optional and requires Linux host networking. Existing operators can set `RIVUNE_DISCOVERY_URL` to their reachable HTTPS or literal private-IP HTTP origin and optionally set `RIVUNE_DISCOVERY_NAME`; leave the URL empty to keep discovery disabled and use manual server entry.
- Existing operators can set `RIVUNE_VERSION=1.10.0`, pull, and recreate only the Rivune service. Fresh Compose deployments now default to the immutable `1.10.0` image tag.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.10.0`
- `ghcr.io/moodiness/rivune:1.10`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.9.1...v1.10.0
