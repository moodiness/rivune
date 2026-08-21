# Rivune v1.10.1

## Highlights

- Android and Apple clients can now download encrypted offline media for a specific server and profile. Downloads support progress, cancellation, quota enforcement, crash-safe reconciliation, offline playback, and PIN-gated access without exposing plaintext media at rest.
- Native clients add Rivune playback coordination: authenticated devices can hand off playback, remotely control another screen, and host or join private synchronized viewing rooms while preserving profile boundaries.
- Per-profile local recommendations now derive from the viewer's own library and watch state without sending recommendation history to an external service.
- Operators can install a host-supervised PostgreSQL backup schedule that signs every archive, verifies it through a disposable restore, retains only proven backups, and supports explicit authenticated recovery.
- Docker Desktop installations on macOS now publish `_rivune._tcp` through a host Bonjour LaunchAgent, while Linux continues to use the isolated host-network discovery sidecar.
- Android, Apple, and Windows playback surfaces incorporate the new coordination capabilities and related lifecycle, background, focus, and error-state hardening.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, backup, and multi-architecture container gates run before publication.

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

- This release adds database migrations 76 and 77 for playback coordination and private synchronized-room membership. Back up PostgreSQL and the complete encryption keyring before upgrading; Rivune applies both migrations automatically during startup.
- Existing operators can set `RIVUNE_VERSION=1.10.1`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.10.1` image tag.
- When LAN discovery is enabled, use `./rivune up` and `./rivune down`; on macOS these commands manage the per-user Bonjour LaunchAgent outside Docker Desktop.
- Scheduled backups are opt-in and run on the host so Docker cannot retain the signing and restore credentials. Configure and inspect the schedule with the documented `./rivune backup-scheduler` command.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.10.1`
- `ghcr.io/moodiness/rivune:1.10`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.10.0...v1.10.1
