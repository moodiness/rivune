# Rivune v1.12.2

## Highlights

- Windows now ships as one `Rivune-Windows.exe` for x64 and ARM64. The setup detects the PC architecture and offers either a per-user installation or a portable extraction.
- Per-user installation adds Start Menu and Windows uninstall integration without administrator access. The desktop shortcut remains optional and disabled by default; settings, cache, and sessions stay in Windows AppData in both modes.
- The local TV installer companion is consolidated into one universal Windows EXE and one universal macOS DMG. The public Linux companion downloads are removed.
- `rivune-update.json` schema 3 records the universal Windows bundle plus the exact size and SHA-256 of each embedded architecture executable, allowing Windows updates to verify both layers before replacement.
- Apple clients keep media, profile, library, search, calendar, and offline flows in single-column navigation instead of opening an unintended sidebar.
- Windows localization now handles synchronous UI property callbacks without recursive writes.
- Release validation now rebuilds the universal Windows TV installer without checkout-state metadata, so candidate and publication artifacts compare byte-for-byte.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL and SHA-256 before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Existing operators can set `RIVUNE_VERSION=1.12.2`, pull, and recreate Rivune. Fresh Compose deployments default to the immutable `1.12.2` image tag.
- `v1.12.1` was never published as a GitHub Release; v1.12.2 supersedes that failed candidate.
- The release contains exactly twelve assets: eight application packages, `rivune-update.json`, the shared TV runtime, and the two universal TV installer companions.
- Schema 3 is a clean update-contract cutover. Clients from v1.12.0 cannot consume the new automatic-update metadata; install v1.12.2 once from the exact GitHub Release, after which automatic checks use the universal contract.

## Container image

- `ghcr.io/moodiness/rivune:1.12.2`
- `ghcr.io/moodiness/rivune:1.12`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.12.0...v1.12.2
