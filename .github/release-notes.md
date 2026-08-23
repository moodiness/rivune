# Rivune v1.12.0

## Highlights

- Rivune now ships dedicated packaged clients for compatible LG webOS and Samsung Tizen televisions. They are TV applications, not wrappers around the server frontend.
- Both TV clients use the protocol-v20 TV scope for self-hosted server selection, device-code pairing, `profileContext` browsing, and playback source resolution.
- A remote-first interface supports directional-pad navigation, and playback uses each platform's native media stack.
- The release adds `Rivune-webOS.ipk`, `Rivune-Tizen.wgt`, and a shared `Rivune-TV-runtime.json` payload without changing established Android, Apple, Windows, or `rivune-update.json` asset names.
- Android, Apple, and Windows device pairing now survives application relaunches and retryable refresh failures. Active native pairings are migrated and remain refreshable until the user disconnects or an administrator revokes the session or device.

## TV installation

- Install `Rivune-webOS.ipk` on a compatible LG television through LG Developer Mode tooling.
- `Rivune-Tizen.wgt` is published unsigned. Sign it locally with an appropriate Samsung/Tizen certificate profile before installing it on a compatible Samsung television.
- Neither packaged client contains a hosted Rivune account or fixed catalogue. On first run, select or enter the self-hosted Rivune server and complete device-code pairing.
- After the first install, both TV clients check the latest stable GitHub Release, validate `rivune-update.json`, verify the separate runtime payload by its declared size and SHA-256, and activate it on restart. A failed cached-runtime startup rolls back to the packaged runtime.

## Other application installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required. The unsigned executables may trigger SmartScreen; verify the matching GitHub Release URL and asset SHA-256.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. The application ID remains `io.rivune.app`, and the APK keeps the established Rivune release-signing identity.

## Upgrade notes

- Existing operators can set `RIVUNE_VERSION=1.12.0`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.12.0` image tag.
- GitHub publishes exactly eleven release assets: `Rivune-Android.apk`, `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, `Rivune-visionOS-unsigned.ipa`, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, `Rivune-arm64.exe`, `Rivune-webOS.ipk`, `Rivune-Tizen.wgt`, and `Rivune-TV-runtime.json`. The manifest remains the authoritative contract; the runtime file is the verified implementation payload.

## Container image

- `ghcr.io/moodiness/rivune:1.12.0`
- `ghcr.io/moodiness/rivune:1.12`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.5...v1.12.0
