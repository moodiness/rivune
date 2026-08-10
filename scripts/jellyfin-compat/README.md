# Jellyfin 10.11.11 Differential Harness

This directory runs the same HTTP manifest against the stable Jellyfin 10.11.11
oracle and a Rivune instance. It produces reproducible observations; it neither
certifies nor promises general Jellyfin compatibility.

## Prerequisites

- Bash, `curl`, `jq`, Go, and `sha256sum` (or `shasum`);
- Docker with Compose v2;
- an already-running Rivune instance with a compatible profile and a movie named
  `Rivune Demo` in its library.

The oracle is exactly
`jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db`.
Compose exposes it only on `127.0.0.1:18096`. Its configuration and cache
volumes are disposable; `run.sh` removes them with `docker compose down
--volumes` on exit. The synthetic media is mounted read-only.

## Private Configuration

From the repository root:

```sh
cp scripts/jellyfin-compat/targets.env.example scripts/jellyfin-compat/targets.env
chmod 600 scripts/jellyfin-compat/targets.env
```

Replace every example value. Git ignores `targets.env`, and the harness loads it
as trusted Bash. `run.sh` rejects symbolic links and any POSIX mode that grants
permissions to the group or others. Secrets remain in the environment or in
private temporary files: they are neither placed in arguments nor written to
snapshots, errors, or logs. To select another private file, set
`JFCOMPAT_ENV_FILE`; the same checks apply.

Manifest secrets in the form `{{secret:name}}` are resolved independently for
each target: `JFCOMPAT_UPSTREAM_NAME` and `JFCOMPAT_RIVUNE_NAME`. Captures,
including the access token, are also isolated by target. A capture marked
`secret` is redacted before artifacts are written.

## Running the Harness

```sh
scripts/jellyfin-compat/run.sh
```

The script:

1. verifies the five SHA-256 values recorded in `NOTICE`, then copies only the
   synthetic Rivune media into `work/media`;
2. starts the pinned oracle and waits for its health check;
3. verifies that the oracle is fresh, completes the Startup APIs, creates the
   `movies` library, authenticates, and polls the `RefreshLibrary` task until a
   new successful run completes (bounded backoff, with no assumed scan duration);
4. validates the manifest, runs both targets, and compares their artifacts;
5. stops the oracle and destroys its volumes, even if the run fails.

By default, results are written to a timestamped directory under `work/runs/`.
The private value `JFCOMPAT_OUT_DIR` can select another path; an existing path
is never overwritten.

The three core commands can also be run from `server/`:

```sh
go run ./cmd/jellyfin-compat validate -manifest ../scripts/jellyfin-compat/requests.json
go run ./cmd/jellyfin-compat run -manifest ../scripts/jellyfin-compat/requests.json -target upstream=http://127.0.0.1:18096 -target rivune=http://127.0.0.1:8080 -out ../scripts/jellyfin-compat/work/manual
go run ./cmd/jellyfin-compat compare -left ../scripts/jellyfin-compat/work/manual/upstream -right ../scripts/jellyfin-compat/work/manual/rivune -out ../scripts/jellyfin-compat/work/manual/diff
```

For these manual commands, first export the four private variables from the
environment file and start and bootstrap the oracle. Avoid placing secrets in
the shell history.

## Observation Scope

The manifest covers ping, public system information, the network endpoint,
authentication, the current user, views, items, search, `HEAD` artwork,
`PlaybackInfo`, and logout. Each step bounds the response status, type, or size.
Identifiers, tokens, sessions, paths, playback URLs, and timestamps are captured
or canonicalized with a local justification.

`compare: per-target` is intentional when identities, libraries, metadata
providers, or topologies differ. In particular, the `observed-gap` cases make
network detection and artwork availability explicit. These snapshots remain
inspectable, but their differences are not turned into a false equivalence.
Only the content-free logout contract is compared exactly.

The bootstrap process configures only the oracle. Rivune must therefore already
expose the expected profile and synthetic title; the harness neither creates a
native Rivune account nor modifies its library. Transcoding, provider, and codec
differences remain deployment-dependent.

### Why the Manifest Does Not Certify Playback

The manifest intentionally stops at `PlaybackInfo`. The oracle and Rivune share
neither source identifiers, delivery URL shapes, nor transcoding decisions;
capturing a URL and then accepting a different `2xx` response for each target
would prove neither the bytes, decoding, nor seeking. Such a step would be a
false certification and is therefore not added to `requests.json`.

The bounded media smoke tests live in the playback tests. They use only a local
server, lavfi bytes, and text files generated in `t.TempDir`:

```sh
cd server
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/playback \
  -run '^TestExternalMedia' -count=1
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/jellyfin \
  -run '^TestPlaybackGatewayReadsPlaylistAndChildBytesAndRejectsOutOfOrderChild$' -count=1
```

These optional commands require `ffmpeg`/`ffprobe`; a skipped run remains
`ABSENT`, never `PASSED`. They cover synthetic MP4/MKV, byte ranges, TS/fMP4 HLS,
AAC track selection, synthetic 5.1-to-stereo downmix, WebVTT/seeking, and embedded
ASS burn-in verified against a decoded frame. They provide no pixel-level Dolby
Vision/HDR evidence, third-party audio codec evidence, bitmap subtitle evidence,
or playback evidence for Infuse, Streamyfin, or VidHub. The protocol for
real-device validation and scrubbed traces is in `docs/operations.md`.

## Provenance and Licenses

The MP4, VTT, and SVG files come exclusively from
`server/internal/demo/assets`. Their creation, copyright, Apache-2.0 license,
and SHA-256 values are documented in the root `NOTICE`. The harness downloads no
media and copies no Jellyfin code. Docker may pull the pinned oracle image if it
is not already available locally; Jellyfin remains separate GPL-2.0-or-later
software run only as an oracle. This repository's original scripts and manifest
remain under Apache-2.0.
