# Jellyfin compatibility evidence

Rivune exposes a limited Jellyfin-compatible adapter. This document records evidence, not a promise of general client compatibility. A route, DTO, or `2xx` response does not prove that a client can browse, decode, seek, switch tracks, or stop playback correctly.

## Baseline

- Oracle: Jellyfin `10.11.11`, commit `1fbd8739292cce610231be93daf43368733edf63`.
- Image: `jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db`.
- Rivune contract: [`protocol/jellyfin-compat-openapi.yaml`](../protocol/jellyfin-compat-openapi.yaml).
- Differential harness: [`scripts/jellyfin-compat`](../scripts/jellyfin-compat/README.md).

The harness uses only synthetic Rivune assets listed in `NOTICE`; no Jellyfin code or third-party media is copied. Evidence labels mean:

- **Passed**: the named local artifact proves the stated behavior.
- **Partial**: a bounded subset is implemented and tested.
- **Absent**: no acceptable artifact exists.
- **Blocked**: controlled external execution is required.

## Implemented surface

The 122 registered route specifications cover bounded subsets of:

- public server identity and availability;
- profile application-secret login, profile-bound Quick Connect, sessions, logout, and WebSocket liveness;
- views, items, movie/series hierarchy, search, metadata, artwork, avatars, and display preferences;
- playback information, direct streams, byte ranges, HLS children, WebVTT subtitles, progress, played state, favorites, resume, and next-up;
- conservative empty or minimal responses for unsupported probes expected by consumers.

The OpenAPI contract and route registry are authoritative. Unsupported parameters are ignored or rejected according to the specific route; an installed stub is not feature parity.

## Media evidence

Local FFmpeg tests generate temporary media and verify bytes rather than only planner decisions:

```sh
cd server
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/playback \
  -run '^TestExternalMedia' -count=1
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/jellyfin \
  -run '^TestPlaybackGatewayReadsPlaylistAndChildBytesAndRejectsOutOfOrderChild$' -count=1
```

Covered locally:

- MP4, MKV, WebM, AVI, and MPEG-PS inputs;
- direct ranges, MPEG-TS/fMP4 HLS, H.264/HEVC/AV1 output, and 4K-to-1080p scaling;
- AAC track selection, 5.1 downmix, common audio-codec conversion, subtitle conversion/seek, and embedded ASS burn-in;
- synthetic HDR10-to-SDR metadata/colorimetry and HLS child delivery.

Not proven:

- Dolby Vision playback, base-layer extraction, or tone mapping;
- generated HLG pixels and bitmap-subtitle rendering;
- visual/audio rendering in an actual Infuse, VidHub, or Streamyfin application.

A skipped optional FFmpeg test remains **Absent**, never Passed.

## Consumer status

| Client | Current evidence | Status |
| --- | --- | --- |
| Infuse 8 | A real Apple TV hierarchy scan exposed and helped fix field/cancellation issues. No complete player workflow was retained. | Partial |
| Streamyfin 0.31.0 | Source/SDK review plus an HTTP-level NAS HLS replay with ranges, seeks, and stop. No application rendering evidence. | Partial |
| VidHub | No audited package, source, trace, or fixture. | Absent |
| Jellyfin Media Player/Desktop | Expects Jellyfin Web at `/`, while Rivune serves its own app. | Incompatible |

## Differential result

The archived oracle smoke runs 11 bounded comparisons. Nine match semantically. Logout is the only exact comparison. Known differences remain in the unauthenticated system error shape and `/Items/0/ChildCount`; these are recorded differences, not hidden skips.

Run the full harness from the repository root:

```sh
cp scripts/jellyfin-compat/targets.env.example scripts/jellyfin-compat/targets.env
chmod 600 scripts/jellyfin-compat/targets.env
scripts/jellyfin-compat/run.sh
```

The target file contains private credentials and must never be committed. See the harness README for prerequisites and artifact handling.

## Promotion rule

A client may be advertised as compatible only after a pinned released version completes discovery/login, hierarchy and artwork, playback with rendered video and audio, seek, track/subtitle selection where applicable, state updates, and clean stop. Retain a scrubbed ordered trace and environment manifest; remove credentials, IDs, URLs, hosts, paths, provider data, and media titles. HTTP status alone is insufficient.
