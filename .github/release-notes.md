# Rivune v1.5.3

## Fixes

- Seeking within seekable transcoded HLS playback now keeps the existing browser media pipeline, playback session, and playlist. Rivune requests the target segment directly instead of rebuilding HLS and showing the full playback-preparation screen on every seek.

## Upgrade notes

- Pull and recreate Rivune normally. This release has no database migration, configuration change, or protocol change and keeps wire protocol version 20.

## Container image

- `ghcr.io/moodiness/rivune:1.5.3`
- `ghcr.io/moodiness/rivune:1.5`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.5.2...v1.5.3
