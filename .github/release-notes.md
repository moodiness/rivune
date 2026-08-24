# Rivune v1.12.4

## Highlights

- Apple clients preserve the complete series progress snapshot when opening and closing an episode, hydrate series longer than 100 episodes in bounded batches, and retain successful watched updates when a later batch conflicts.
- Apple search now paginates every compatible catalog, the personal library supports pagination and movie, series, and live-TV filters, and the calendar can move between months without leaving its tab.
- Native Apple playback exposes the next episode for manual or automatic continuation and applies intro, recap, and outro markers consistently across AVPlayer and MPV playback.
- macOS external playback preserves encoded paths and signed query parameters, handles installed applications whose paths contain spaces, and prevents horizontal rail drags from opening a card accidentally.
- Browser tabs now coordinate rotated access and refresh tokens without logging out a peer or replaying a failed request under a different session.
- Container migration checks wait for the real PostgreSQL process and an authenticated SQL query before exercising clean installation, one-version upgrade, and idempotency.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL and SHA-256 before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Existing operators can set `RIVUNE_VERSION=1.12.4`, pull, and recreate Rivune. Fresh Compose deployments default to the immutable `1.12.4` image tag.
- This patch has no protocol or storage-schema cutover; existing v1.12.3 installations can update directly.
- The release contains exactly twelve assets: eight application packages, `rivune-update.json`, the shared TV runtime, and the two universal TV installer companions.
- Clients upgrading from v1.12.0 still require one manual installation from the exact GitHub Release before automatic updates can consume the schema 3 universal package contract.

## Container image

- `ghcr.io/moodiness/rivune:1.12.4`
- `ghcr.io/moodiness/rivune:1.12`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.12.3...v1.12.4
