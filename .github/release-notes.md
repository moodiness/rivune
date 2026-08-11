# Rivune v1.1.2

## Highlights

- Resumed seekable HLS playback now starts at the saved scene instead of displaying the saved timestamp over content from the beginning.
- AMD HDR-to-SDR conversion now prefers the VAAPI-to-Vulkan `libplacebo` path, while retaining `tonemap_vaapi` and bounded software fallbacks.
- Administration Activity and startup logs identify the active `vulkan`, `vaapi`, or `software` tone-map backend.

## Playback reliability

- The initial HLS buffer now defaults to 12 seconds, and the ten-segment client preload window waits for the active generation instead of being mistaken for a seek.
- Seekable transcoding keeps a bounded production margin without letting generated media evict the next segment required by a continuously playing client.
- HEVC Main 10 software tone mapping retains hardware decode and encode when possible and caps new software-fallback sessions at 1080p.
- The container image includes the Mesa Vulkan runtime on both supported architectures.

## Upgrade notes

- No database migration, protocol-version change, or new environment variable is required.
- AMD and Intel containers still require the existing `compose.amd-intel.yaml` overlay or equivalent Unraid `/dev/dri/renderD128` device mapping.
- A selected hardware backend does not guarantee real-time throughput. If Activity reports a sustained speed below `1.00x`, lower the effective **Maximum resolution**, close the existing player session, and reopen the title. Integrated AMD hardware may require 1080p for 4K HDR input.

## Container image

- `ghcr.io/moodiness/rivune:1.1.2`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.1.1...v1.1.2
