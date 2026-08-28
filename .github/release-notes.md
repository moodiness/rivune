# Rivune v1.13.5

## Highlights

- Browser authentication now treats the validated `RIVUNE_PUBLIC_URL` as the authoritative externally visible origin. Login, device-code exchange, refresh, and logout work behind reverse proxies that rewrite the upstream host while rejecting mismatched or spoofed browser origins.
- Deployments without `RIVUNE_PUBLIC_URL` retain direct-request and trusted-forwarding origin detection. Public deployments must configure the exact HTTPS origin shown in the browser address bar.
- Outbound provider requests now bound unread HTTP/1 response shutdown without pooling abandoned connections, while preserving HTTP/2 reuse and correct handling of bodyless responses.
- This patch does not change the protocol version or database schema from v1.13.3.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL, digest, and attestations before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Before upgrading, create and verify an authenticated backup, record its backup ID separately, and preserve the complete encryption keyring.
- Set `RIVUNE_VERSION=1.13.5`, pull, and recreate only Rivune. No database migration or protocol-version change is required from v1.13.3.
- Reverse-proxy deployments must set `RIVUNE_PUBLIC_URL` to the exact externally visible HTTPS origin. Keep `RIVUNE_TRUSTED_PROXIES` limited to the proxy's exact address or dedicated edge CIDR.
- The release contains exactly thirteen assets: eight application packages, `rivune-update.json`, its detached `rivune-update.json.sig` signature, the shared TV runtime, and two universal TV-installer companions.

## Container image

- `ghcr.io/moodiness/rivune:1.13.5`
- `ghcr.io/moodiness/rivune:1.13`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** [v1.13.3...v1.13.5](https://github.com/moodiness/rivune/compare/v1.13.3...v1.13.5)
