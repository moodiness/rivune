# Rivune v1.11.4

## Highlights

- Rivune for iPhone, iPad, Apple TV, Apple Vision Pro, and Mac checks the published Rivune update manifest on launch, subject to a successful-check throttle of 24 hours per installed version.
- Every Apple client also exposes **Settings → Application Update → Check now**. The first-connection screen reports an available release even before a server is paired.
- Update metadata is accepted only from Rivune's fixed HTTPS GitHub endpoint and bounded to 256 KiB. Redirects are limited to five and restricted to GitHub release hosts.
- The client validates manifest schema 2, Semantic Versioning precedence, channel, tag, publication date, release URL, and the exact package contract for its platform: format, architectures, minimum OS, bundle identifier, unsigned state, filename, size, SHA-256, and asset URL.
- Validated results are cached so the last state survives relaunch. Automatic checks run immediately after the installed app version changes, while failed checks never advance the 24-hour throttle.
- Rivune never downloads or installs an Apple update automatically. iPhone, iPad, Apple Vision Pro, and Mac open the verified GitHub Release; Apple TV displays the same release as a local QR code.
- Existing bounded diagnostics record only stable update outcome codes and never include manifest contents, URLs, package data, or raw errors.

## Using Apple update checks

- Rivune checks on launch if no successful check was completed for the installed version during the previous 24 hours.
- Open **Settings → Application Update** to see the installed version, the last validated result, or a stable failure state, and choose **Check now** to bypass the daily throttle.
- On Apple TV, choose **View release QR code** and scan it from another device.
- Public iOS, tvOS, and visionOS IPA files remain unsigned and require an authorized signing identity and provisioning profile before installation. The macOS DMG remains unsigned and may require explicit Gatekeeper approval.

## Application installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required. The unsigned executables may trigger SmartScreen; verify the matching GitHub Release URL and asset SHA-256.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. The application ID remains `io.rivune.app`, and the APK keeps the established Rivune release-signing identity.

## Upgrade notes

- This patch release adds no database migration. The current schema remains unchanged from v1.11.0.
- Existing operators can set `RIVUNE_VERSION=1.11.4`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.11.4` image tag.
- The Apple update checker stores only its last successful time, installed-version key, last-notified version, and, when an update exists, validated public release metadata. It adds no credential, background-service, or installation permission.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.11.4`
- `ghcr.io/moodiness/rivune:1.11`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.3...v1.11.4
