# Rivune v1.11.2

## Highlights

- Windows Docker Desktop hosts can now publish Rivune through `_rivune._tcp` without relying on multicast escaping the Docker VM. The new `.\rivune.ps1` lifecycle wrapper installs and supervises a native Windows DNS-SD publisher when `RIVUNE_DISCOVERY_URL` is configured.
- The publisher runs as a limited per-user scheduled task with no elevation, starts immediately and at logon, retries bounded failures, and is removed by `.\rivune.ps1 down`.
- Windows publishes the same `url`, `protocol=20`, and `version` TXT contract as Linux and macOS. The release gate starts the real scheduled publisher and discovers its emitted TXT record through the Windows client's mDNS parser.
- Discovery configuration, bounded logs, and active PID state remain private to the current Windows identity under `%LOCALAPPDATA%\Rivune\discovery`; no application secret is copied there or advertised.
- `.\rivune.ps1` pins `.env` and `compose.yaml`, clears ambient Compose control variables, validates discovery before starting containers, and provides `up`, `down`, `restart`, `pull`, `status`, and bounded service-log delegation.
- Discovery still accepts only HTTPS origins or literal private-IP HTTP origins and rejects credentials, paths, queries, fragments, loopback, wildcard, multicast, and link-local addresses.
- The complete backend, browser, Android, Apple, Windows, migration, proxy, backup, and multi-architecture container gates run before publication.

## Windows host installation

- Run `.\scripts\create-env.ps1`, fill the private `.env`, then use `.\rivune.ps1 up`. Set `RIVUNE_DISCOVERY_URL` to the reachable HTTPS origin or literal private-IP HTTP origin and optionally set `RIVUNE_DISCOVERY_NAME`.
- Use `.\rivune.ps1 status` to inspect both Compose and the host publisher, `.\rivune.ps1 logs discovery` for its private bounded log, and `.\rivune.ps1 down` to remove the scheduled publisher.
- A direct trusted-LAN HTTP origin also requires `RIVUNE_BIND_ADDRESS=0.0.0.0`; keep that port restricted to the trusted LAN and never forward it from the router.
- Raw Docker Compose does not install the Windows host publisher. Manual server entry remains available when discovery is disabled.

## Application installation

- Download `Rivune-x64.exe` on x64 Windows or `Rivune-arm64.exe` on ARM64 Windows. Windows 10 build 19041 or newer is required. The unsigned executables may trigger SmartScreen; verify the matching GitHub Release URL and asset SHA-256.
- `Rivune-iOS-unsigned.ipa`, `Rivune-tvOS-unsigned.ipa`, and `Rivune-visionOS-unsigned.ipa` must be re-signed with an identity and provisioning profile authorized for the destination device. Stock devices cannot install them as downloaded.
- `Rivune-macOS.dmg` contains an unsigned universal arm64/x86_64 application. Gatekeeper may require explicit local approval; rebuilding from source with Xcode remains the recommended trusted path.
- Download `Rivune-Android.apk` and complete Android's normal package-installation prompt. The application ID remains `io.rivune.app`, and the APK keeps the established Rivune release-signing identity.

## Upgrade notes

- This patch release adds no database migration. The current schema remains unchanged from v1.11.0.
- Existing operators can set `RIVUNE_VERSION=1.11.2`, pull, and recreate Rivune. Fresh Compose deployments now default to the immutable `1.11.2` image tag.
- Existing Windows `.env` files remain valid. Use `.\rivune.ps1 up` instead of raw Compose to activate host-network discovery; leave `RIVUNE_DISCOVERY_URL` empty to keep it disabled.
- GitHub publishes exactly eight release assets: `Rivune-Android.apk`, the three unsigned IPA files, `Rivune-macOS.dmg`, `rivune-update.json`, `Rivune-x64.exe`, and `Rivune-arm64.exe`.

## Container image

- `ghcr.io/moodiness/rivune:1.11.2`
- `ghcr.io/moodiness/rivune:1.11`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Provenance and SBOM attestations are published for both runnable platforms.

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.11.1...v1.11.2
