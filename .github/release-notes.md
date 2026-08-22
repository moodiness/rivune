# Rivune v1.11.1

## Highlights

- Rivune for Windows now downloads single-file HTTP media into profile-scoped encrypted offline storage. Media is authenticated in independent AES-256-GCM chunks under a random per-profile key protected by Windows DPAPI for the current Windows user.
- The Windows source picker exposes download progress and rejects HLS, DASH, redirects, and any resolved URL outside the connected Rivune origin. Interrupted downloads are removed and manifests are committed atomically.
- Downloaded profiles can be opened without contacting the server. Profile PINs remain required, access locks when Rivune moves to the background, and offline playback resumes from locally persisted position and completion state.
- The Windows home surface lists downloaded titles, supports deletion, and streams decrypted ranges only through an unguessable loopback endpoint. Plaintext media is never written to disk.
- Offline storage is isolated by canonical server origin and profile, reconciles incomplete or orphaned archives, and enforces a 20 GiB quota.
- Windows now provides the same end-user offline, handoff, remote-control, and synchronized-room surface as the Android and Apple clients. HLS/DASH downloads remain intentionally out of scope.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, backup, and multi-architecture container gates run before publication.

## Apple installation

- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- The applications page generates the exact `clients/apple/Scripts/sign-and-install.sh` command for this release. The script accepts only the platform, development team, unique bundle identifier, and connected-device identifier; it accepts no password, private key, certificate, or provisioning profile.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Every Apple artifact embeds Rivune, MPVKit, mpv, and FFmpeg licensing notices.

## Windows installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required.
- Keep the executable in a user-writable local folder. Rivune is portable, while preferences, DPAPI-protected sessions, and encrypted offline media remain under `%LOCALAPPDATA%\Rivune\`.
- Offline keys and archives are bound to the current Windows user and installation; they are not portable backups. Download media again after moving to another account or Windows installation.
- The Windows executables are not Authenticode-signed and may trigger SmartScreen. Verify that the download URL is the matching `moodiness/rivune` GitHub Release and compare the asset SHA-256 before running it.

## Android installation

- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. Android 8.0 or newer is required, and the application ID remains `io.rivune.app`.
- The APK keeps the established Rivune release-signing identity. Rivune and Android verify package identity, version, size, SHA-256, and signing certificate during an update.
- The APK bundles the GPL-enabled libmpv/FFmpeg stack and is conveyed as a GPLv3 combined Android work; the required license and source-location notices are packaged in the application.

## Upgrade notes

- This patch release adds no database migration. The current schema remains unchanged from v1.11.0.
- Existing operators can set `RIVUNE_VERSION=1.11.1`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.11.1` image tag.
- Windows offline archives are local client state and are not included in PostgreSQL backups. Server profile, library, and progress data continue to use the normal authenticated backup path.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.11.1`
- `ghcr.io/moodiness/rivune:1.11`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.0...v1.11.1
