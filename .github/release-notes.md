# Rivune v1.5.1

## Fixes

- Rivune can again remove a stale `rivune-media` workspace owned by a previous `PUID` or `PGID`. The hardened Compose and Unraid profiles now grant `DAC_OVERRIDE` only to the root entrypoint; the application still starts under the configured non-root identity with no effective capabilities.
- The Unraid smoke test now runs with the exact minimized capability set and reproduces cleanup of a non-empty `0700` workspace owned by a different user, preventing the v1.5.0 false positive.
- The Unraid template now persists downloaded posters, backdrops, logos, and cast images at `/mnt/cache/appdata/rivune/artwork` instead of leaving the cache in the disposable container layer.

## Upgrade notes

- Compose installations already use the persistent `artwork_cache` volume; pull and recreate Rivune normally.
- Existing Unraid v1.5.0 installations should update the template, or add a read-write Path from `/mnt/cache/appdata/rivune/artwork` to `/var/lib/rivune/artwork` and add `--cap-add=DAC_OVERRIDE` to Extra Parameters before recreating the container.
- Artwork is a bounded cache and may be downloaded again. PostgreSQL remains authoritative for its source registrations; this release has no database migration and keeps wire protocol version 20.

## Container image

- `ghcr.io/moodiness/rivune:1.5.1`
- `ghcr.io/moodiness/rivune:1.5`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.5.0...v1.5.1
