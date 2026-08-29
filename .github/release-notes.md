# Rivune v1.13.7

## Highlights

- Browser password login now recovers when the remembered `deviceId` belongs to another account on the same origin or no longer exists after a restore, instead of returning `deviceId does not belong to this user`.
- The web client retains a bounded, UUID-only list of recent browser device identities so switching between accounts reuses each account's own device rather than creating one on every switch.
- Device ownership remains server-authoritative: candidates are considered only after password verification and the existing user lock, foreign devices are never reassigned or modified, fresh devices still consume the 50-device quota, and native login keeps its strict ownership rejection.
- Browser `Origin`, `X-Rivune-CSRF`, host-only HttpOnly refresh-cookie, and SameSite protections are unchanged.
- This patch does not change protocol version 22 or the database schema from v1.13.6.

## Installation

- Download `Rivune-Windows.exe` on x64 or ARM64 Windows 10 build 19041 or newer. The unsigned setup may trigger SmartScreen; verify its exact GitHub Release URL, digest, and attestations before running it.
- Download `Rivune-TV-Installer-Windows.exe` or `Rivune-TV-Installer-macOS.dmg` to install the packaged LG webOS or Samsung Tizen client from the same local-only companion interface.
- `Rivune-Tizen.wgt` remains unsigned and must be signed locally with an appropriate Samsung/Tizen certificate profile. LG installation continues through Developer Mode tooling.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval.
- `Rivune-Android.apk` keeps the established `io.rivune.app` release-signing identity.

## Upgrade notes

- Before upgrading, create and verify an authenticated backup, record its backup ID separately, and preserve the complete encryption keyring.
- Set `RIVUNE_VERSION=1.13.7`, pull, and recreate only Rivune. No database migration or protocol-version change is required from v1.13.6.
- Retry sign-in normally after the container is ready. Do not clear browser storage: the first successful login promotes the server-selected identity while retaining the recent UUID hints for other accounts.
- Reverse-proxy deployments must keep `RIVUNE_PUBLIC_URL` equal to the exact externally visible HTTPS origin and `RIVUNE_TRUSTED_PROXIES` limited to the proxy's exact address or dedicated edge CIDR.
- Direct private-LAN HTTP still requires a literal private IP and the exact mapped port. HTTP cookies are host-scoped, so trust every HTTP service on that host IP or keep Rivune loopback-only and use HTTPS.
- The release contains exactly thirteen assets: eight application packages, `rivune-update.json`, its detached `rivune-update.json.sig` signature, the shared TV runtime, and two universal TV-installer companions.

## Container image

- `ghcr.io/moodiness/rivune:1.13.7`
- `ghcr.io/moodiness/rivune:1.13`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** [v1.13.6...v1.13.7](https://github.com/moodiness/rivune/compare/v1.13.6...v1.13.7)
