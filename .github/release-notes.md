# Rivune v1.12.3

## Highlights

- Apple clients now use a redesigned native browsing experience with a compact macOS dock, adaptive collection layouts, cinematic media and season pages, source-provider filters, and integrated playback controls.
- macOS can open compatible streams in an installed video application, while Windows discovers VLC, mpv, MPC-HC, MPC-BE, PotPlayer, Kodi, and Plex and exposes the same external-player fallback beside internal playback.
- Marking a series watched or unwatched on Apple now updates every episode across every season. Series and episode rails on macOS drag continuously with bounded momentum instead of snapping one card at a time.
- Apple collection folders preserve source-specific artwork and configured merged, category, or folder layouts. Offline downloads identify the active source and report upstream HTTP failures instead of a generic cancellation.
- Apple media presentation, profiles, PIN entry, settings, sheets, and player chrome now stay within responsive single-column surfaces across supported Apple form factors.
- Debug macOS clients persist authenticated sessions in an isolated local credential store, including protocol-required nullable token and category fields.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL and SHA-256 before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Existing operators can set `RIVUNE_VERSION=1.12.3`, pull, and recreate Rivune. Fresh Compose deployments default to the immutable `1.12.3` image tag.
- The release contains exactly twelve assets: eight application packages, `rivune-update.json`, the shared TV runtime, and the two universal TV installer companions.
- Clients upgrading from v1.12.0 still require one manual installation from the exact GitHub Release before automatic updates can consume the schema 3 universal package contract.

## Container image

- `ghcr.io/moodiness/rivune:1.12.3`
- `ghcr.io/moodiness/rivune:1.12`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.12.2...v1.12.3
