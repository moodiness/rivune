# Rivune v1.1.0

## Highlights

- Persisted runtime configuration with live application state and restart-required reporting.
- Encrypted integration credentials and configuration audit history, managed from the Administration interface.
- Expanded Jellyfin-compatible client workflows and source selection.
- Hardened playback preparation, transcoding, trickplay, and shared HLS delivery.

## Reliability and security

- Bounded artwork fetch and transformation queues now absorb temporary saturation instead of failing immediately.
- Container deployments now use stricter identities, resource limits, read-only filesystems, explicit GPU access, and separated database/edge networks.
- PostgreSQL backup and restore credentials no longer travel in process arguments.
- Release publication now requires an annotated tag, the complete release gate, immutable release refs, and a protected release environment.

## Upgrade notes

- Configure `RIVUNE_ENCRYPTION_KEYS` as an active-first keyring, for example `1:<64-lowercase-hex>`; retain every key version needed by existing encrypted data.
- Legacy runtime and integration environment variables are imported once into the database. After verifying the upgrade, remove the legacy variables.
- The supported Unraid template expects an HTTPS reverse proxy and PostgreSQL TLS. Rivune listens internally on port `8080`.
- AMD/Intel hardware acceleration requires `/dev/dri/renderD128` and the matching `RIVUNE_VIDEO_GROUP_ID`.

## Container image

- `ghcr.io/moodiness/rivune:1.1.0`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.0.2...v1.1.0
