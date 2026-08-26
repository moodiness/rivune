# Rivune v1.13.1

## Highlights

- Rivune protocol 22 adds a durable profile reading queue, playback source failover, saved searches, smart collections, extension incident recovery, media notifications, and synchronized accessibility preferences.
- Web, Android, Apple, Windows, webOS, and Tizen publish bounded search batches progressively, deduplicate bridged provider identities, reject stale results, and expose partial-source failures without blocking fast results.
- Connected devices can transfer playback and control TV clients through idempotent operation IDs, adaptive polling, bounded result retries, and revision-aware commands.
- Add-on installation now consumes expiring one-shot verification snapshots, encrypts successful private transport URLs at rest, and records sanitized incident classifications without leaking upstream requests or credentials.
- Browser authentication keeps rotating refresh credentials in an HttpOnly same-origin cookie; TV clients no longer persist bearer tokens in JavaScript storage.
- Android and Windows support verified application updates; Apple clients publish verified update notices; webOS and Tizen can atomically replace the signed shared runtime when persistent storage is available.
- The release pipeline promotes one tested multi-architecture OCI digest, signs the update manifest, and publishes SBOM, build provenance, and release-identity attestations for every artifact.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL, digest, and attestations before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Before upgrading, create and verify an authenticated backup, record its backup ID separately, and preserve the complete encryption keyring.
- Set `RIVUNE_VERSION=1.13.1`, pull, and recreate only Rivune. Startup applies embedded migrations `000082` through `000094` transactionally before readiness.
- Protocol 21 clients are incompatible with protocol 22. Upgrade the server and every first-party client together, wait for `/ready`, and then rediscover `/.well-known/rivune` instead of retrying cached v21 requests.
- Migration `000092` deliberately removes pending short-lived add-on verification snapshots while replacing plaintext transport storage with encrypted envelopes; verify affected add-ons again after upgrade.
- The release contains exactly thirteen assets: eight application packages, `rivune-update.json`, its detached `rivune-update.json.sig` signature, the shared TV runtime, and two universal TV-installer companions.

## Container image

- `ghcr.io/moodiness/rivune:1.13.1`
- `ghcr.io/moodiness/rivune:1.13`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.12.4...v1.13.1
