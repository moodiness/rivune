# Rivune v1.11.3

## Highlights

- Android, Apple, and Windows clients now expose the same bounded, token-free support report from their settings surfaces.
- Each process keeps at most 64 KiB of structured event codes in memory, and every exported UTF-8 report is capped at 64 KiB.
- Reports use a strict allowlist: app/build, platform and device metadata, selected local preferences, the server origin/name/version/protocol, and timestamped lifecycle event codes.
- Credentials, cookies, headers, payloads, media and profile identifiers, titles, provider data, filesystem paths, raw errors, and URL credentials/path/query/fragment are excluded by construction.
- Rivune never uploads diagnostics. Android, Apple, and Windows use user-initiated local copy or export surfaces; Android TV displays the report locally, and Apple TV adds a local QR representation because tvOS has no public clipboard, share sheet, or file exporter.
- Windows copies opt out of clipboard history and roaming/device sync and clear after 60 seconds. Apple device copies are local-only and expire after 60 seconds. While Rivune remains alive, Android marks copies sensitive and clears them after 60 seconds or when Rivune leaves the foreground, whichever comes first; Android has no standard per-clip local-only or process-death-safe expiry guarantee, so use **Export logs** wherever clipboard synchronization or retention cannot be ruled out.
- Connection, catalogue, playback, update, and export outcomes are recorded only as stable event codes without attached values or exception text.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, backup, and multi-architecture container gates run before publication.

## Using diagnostics

- On Android mobile or tablet, open Settings → About, then choose **Copy diagnostics** or **Export logs**. On Android TV, choose **View diagnostics** to inspect or photograph the local report; **Export logs** remains available when the TV provides a document destination.
- On iPhone, iPad, or visionOS, open Settings → Diagnostics, then copy locally or export the text report. On macOS, export the text report to a user-selected file.
- On Apple TV, open Settings → Diagnostics and choose **View or scan diagnostics**. Scan the QR code on the local display or photograph the visible allowlisted report.
- On Windows, open Settings → About, then choose **Copy diagnostics** or **Export logs**.
- Review every report before sharing it. Diagnostics are designed to exclude secrets, but device and server-origin metadata may still identify an installation.

## Application installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required. The unsigned executables may trigger SmartScreen; verify the matching GitHub Release URL and asset SHA-256.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. The application ID remains `io.rivune.app`, and the APK keeps the established Rivune release-signing identity.

## Upgrade notes

- This patch release adds no database migration. The current schema remains unchanged from v1.11.0.
- Existing operators can set `RIVUNE_VERSION=1.11.3`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.11.3` image tag.
- Diagnostic history exists only in process memory and begins empty after every app restart; no new persistent file or permission is introduced.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.11.3`
- `ghcr.io/moodiness/rivune:1.11`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.2...v1.11.3
