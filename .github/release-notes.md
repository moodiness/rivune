# Rivune v1.1.1

## Highlights

- The Unraid template now supports standard PostgreSQL on an isolated database network without requiring TLS certificates. `verify-full` with a private CA remains available as the hardened option.
- Both supported Compose deployments are CPU-only by default. AMD/Intel acceleration is enabled explicitly with `compose.amd-intel.yaml`; Unraid GPU mapping is opt-in.
- Pangolin/Newt deployment now has a dedicated `rivune-edge` network, an internal `rivune:8080` target, narrow trusted-proxy guidance, and a documented host-port fallback.
- The root Compose deployment is a complete PostgreSQL 18 stack with PostgreSQL isolated from the edge network.

## Reliability and operations

- Encryption-key validation now explains the exact `version:64-lowercase-hex` format and distinguishes new installations from legacy-key recovery without exposing key material.
- The release gate exercises CPU-only startup, optional GPU configuration, plaintext PostgreSQL on an isolated network, `verify-full` PostgreSQL, Pangolin-compatible edge routing, restart, migration, backup, and proxy behavior.
- Git Bash path handling is covered by the deployment smoke tests used during Windows-hosted development.

## Upgrade notes

- `RIVUNE_EDGE_NETWORK` and `RIVUNE_EDGE_SUBNET` were removed. The supported base stack uses the fixed `rivune-edge` network and `172.31.0.0/24` subnet.
- For Unraid without PostgreSQL TLS, select `RIVUNE_DATABASE_SSLMODE=disable` and leave both PostgreSQL CA fields empty. Keep PostgreSQL confined to its database-only network.
- Existing installations must reuse their original encryption key as version 1. Never generate a replacement key for an existing encrypted database.
- When Newt shares the edge network, target hostname `Rivune`, port `8080`, method `http`. Add a host port only when a shared edge network is impossible, and never forward that port on the router.

## Container image

- `ghcr.io/moodiness/rivune:1.1.1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.1.0...v1.1.1
