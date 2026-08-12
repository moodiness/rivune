# Rivune v1.5.0

## Highlights

- Adaptive playback now selects validated software, hybrid, VAAPI, QSV, NVENC, and AMF paths across H.264, HEVC, and AV1 instead of relying on codec-name heuristics.
- HDR playback preserves compatible HDR10, HLG, and Dolby Vision base layers only when the browser also reports a high-dynamic-range display; incompatible output is tone-mapped through bounded hardware or software fallbacks.
- Browser media profiles negotiate container, video, audio, bit depth, channel count, and processing modes independently, improving Direct Play and remux choices while retaining high-quality AAC when audio conversion is required.
- Resume, skip-marker, subtitle, track, and next-episode playback remain in one responsive player across mobile, desktop, and TV-sized layouts.

## Playback reliability and efficiency

- Prepared source inspection is deduplicated and cached with policy-sensitive keys; burn-only clients avoid unnecessary external-subtitle provider calls.
- HLS jobs share one storage cadence, perform an exact final quota scan, and stop the timer after the final writer. The initial HLS buffer is now six seconds while the bounded preload and production margins remain intact.
- Progress writes are serialized and coalesced per title, recover from optimistic-version conflicts, and preserve a completed episode while the next episode starts on a slow connection.
- Missing external subtitle URLs are not offered, a failed subtitle track falls back to Off without failing video playback, and embedded burn subtitles remain available.

## Native protocol-v20 clients

- Apple, Android, and Windows clients now expose maintenance state, setup/demo discovery state, cast, selected episode order, targeted add-on source requests, media timelines, and complete playback diagnostics.
- All three clients include typed intro/recap/outro markers, progress lookup and mutation, watched-state batches, Continue Watching listing and dismissal, and explicit handling of the no-progress `204` response.
- Closed playback values are native enums, synchronization versions remain 64-bit, and unknown additive response properties remain forward-compatible.

## Deployment and operations

- `/live` provides dependency-free process liveness, while `/health` and `/ready` verify PostgreSQL; `/ready` is used by the container readiness check. Successful probes no longer flood request logs.
- The supported root Compose stack isolates PostgreSQL on an internal network, exposes Rivune only on loopback, and provides a fixed `rivune-edge` network for Pangolin/Newt or another operator-managed proxy.
- Temporary media uses a dedicated named volume with a bounded `/tmp` tmpfs. Startup removes stale HLS data before dropping privileges, including after a `PUID` or `PGID` change.
- Release, development, and gate builds share a trusted BuildKit cache. The release gate still validates both CPU-only and AMD/Intel Compose modes, migrations, HTTPS proxy behavior, and both published architectures.

## Security and upgrade notes

- `RIVUNE_PUBLIC_URL` must be a pure HTTP(S) origin without credentials, path, query, or fragment; non-loopback HTTP remains rejected.
- Reverse proxies must replace inbound forwarding headers and join only the dedicated edge network. The previous bundled proxy deployment has been removed; existing users should keep the root `compose.yaml` and configure their operator-managed TLS proxy as documented.
- The database automatically migrates instance settings schema v2 to v3. Back up both PostgreSQL and the complete `RIVUNE_ENCRYPTION_KEYS` keyring before upgrading.
- Hardware acceleration and other restart-bound media settings continue to show requested and active values separately until Rivune restarts.
- The Rivune wire protocol remains version 20.

## Container image

- `ghcr.io/moodiness/rivune:1.5.0`
- `ghcr.io/moodiness/rivune:1.5`
- `ghcr.io/moodiness/rivune:1`
- `ghcr.io/moodiness/rivune:latest`
- Platforms: `linux/amd64`, `linux/arm64`

**Full changelog:** https://github.com/moodiness/rivune/compare/v1.1.2...v1.5.0
