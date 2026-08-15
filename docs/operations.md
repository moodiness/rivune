# Production operations

The supported container deployment is the root [`compose.yaml`](../compose.yaml), which runs Rivune with PostgreSQL 18. Put it behind Pangolin/Newt or an operator-managed reverse proxy for public HTTPS. Run the commands in this document from the repository root. The supplied shell scripts explicitly target Linux with Bash, OpenSSL, Docker Engine, Docker Compose v2, and `set -euo pipefail`; CI runs the same scripts on Linux.

## Root operator command

For the standard Linux Compose deployment, use the repository-root command instead of reconstructing Docker arguments:

```sh
./rivune setup --public-url https://media.example.com --version 1.6.2
./rivune up
./rivune status
./rivune logs rivune
./rivune doctor
```

`setup` requires OpenSSL, validates a complete HTTPS origin and stable numeric image version, generates the three database passwords, setup token, and encryption key independently, and atomically creates a mode-0600 `.env`. It refuses every existing file or symlink. Lifecycle commands explicitly select `.env` and `compose.yaml`; backup, verification, and restore preserve their existing positional and trust arguments. Run `./rivune help` for the complete command surface.

`doctor` fails on the first broken invariant: required tools and Compose plugin, private `.env` ownership/mode, required and distinct database secrets, Compose rendering, both healthy containers, PostgreSQL readiness, loopback `/ready`, then the configured public HTTPS `/ready`. It never prints secret values.

## Request correlation and bounded diagnostics

Every native HTTP response carries `X-Request-ID`. Rivune accepts a caller ID only when it is a single 1–128-byte ASCII token from the documented allowlist; otherwise it generates a cryptographically random 128-bit hexadecimal ID. The same value is propagated to supported outbound provider requests and written to completion and panic logs with the matched route, method, status, duration, response bytes, database call count/duration, outbound call count/duration, and upstream bytes. Do not put credentials or media identifiers in caller-supplied IDs.

The global-administrator `GET /api/v1/operations` response adds bounded PostgreSQL pool, tracking-outbox, add-on, and active/transcoding-playback aggregates to the existing metadata and housekeeping status. These fields intentionally contain no profile, title, URL, token, or credential identifiers.

## Portable profile archives

`GET /api/v1/profiles/{profileId}/archive` exports a strict version-1 JSON document. `POST /api/v1/profiles/{profileId}/archive/import` validates and atomically merges it into the target profile. Both routes require a global administrator and revalidate the administrator plus target-profile management authority inside the database transaction.

The archive preserves profile settings, explicitly assigned add-ons and ordering, assignment-free collections, stable title identities, library/progress/favorite/user-data state, and tracking preferences. Import creates target-local resource identities, never imports source UUIDs or category/cross-profile grants, does not delete target data absent from the document, and is idempotent when the same document is merged repeatedly. Passwords, PINs, sessions, tracking tokens, Jellyfin credentials, and instance integration credentials are excluded.

Add-on transport URLs are included because they are required to reconstruct configured add-ons and may themselves contain tokens in path or query components. Treat the response as a credential file: transfer it over the authenticated HTTPS API, retain the `Cache-Control: no-store` behavior, store it with mode 0600 or an equivalent secret-store policy, and delete unneeded copies. The structured import report separates created, updated, and unchanged records; any validation, identity conflict, authorization failure, or database error rolls back the whole document.

## HTTPS behind a reverse proxy

Create the private `.env` file before entering any secrets:

```sh
./scripts/create-env.sh
```

The helper refuses to overwrite an existing file or link and creates `.env`
with mode `0600`. For an existing deployment, run `chmod 600 .env` and stop if
it fails before continuing. Set three independent database secrets, a separate
setup token, a versioned encryption key, the public HTTPS origin, and a pinned
Rivune version. `postgres` is bootstrap-only, `rivune` is the application login,
`rivune_owner` is a non-login owner, and `rivune_restore` is a non-superuser
login used only by the restore scripts.

```dotenv
RIVUNE_PUBLIC_URL=https://media.example.com
RIVUNE_VERSION=1.6.2
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=<output of: openssl rand -hex 32>
RIVUNE_DATABASE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_RESTORE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_SETUP_TOKEN=<different output of: openssl rand -hex 32>
RIVUNE_ENCRYPTION_KEYS=1:<different output of: openssl rand -hex 32>
```

`RIVUNE_ENCRYPTION_KEYS` is active-first. Every entry is a unique positive
integer version, a colon, and exactly 64 lowercase hexadecimal characters. Keys
must also be unique and non-zero. Product settings and provider credentials do
not belong in `.env`; configure them in Rivune Administration after setup.

Collection and addon artwork is fetched through Rivune's same-origin artwork
cache. Public artwork remains restricted to HTTPS on port 443 with public DNS
results. An advanced private Compose override may pass
`RIVUNE_LAN_ARTWORK_ORIGINS` for intentional trusted-LAN artwork hosting. Use
only comma-separated exact private-IP origins with explicit ports; never include
hostnames, paths, queries, fragments, credentials, loopback, or link-local
addresses.

```sh
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
docker compose --env-file .env -f compose.yaml ps
```

Terminate TLS in the external proxy and target hostname `rivune`, port `8080`,
over HTTP from the dedicated `rivune-edge` network. At that trust boundary the
proxy must replace, not append or preserve, inbound `X-Forwarded-For`,
`X-Real-IP`, `X-Forwarded-Proto`, and `X-Forwarded-Host` with values derived
from its own client connection and the public HTTPS request. Otherwise a client
can inject a trusted forwarding chain. `/health` retains the protocol-v20
PostgreSQL readiness contract; `/ready` is its explicit traffic-readiness alias.
Both return `503` while PostgreSQL is unavailable. `/live` checks only the HTTP
process and is the liveness probe. Rivune must trust only the proxy's exact
address or dedicated edge CIDR through `RIVUNE_TRUSTED_PROXIES`, never
`0.0.0.0/0`, a host LAN, the database network, or another client-reachable
range.

After configuring the proxy, require readiness before directing traffic and
use liveness independently for process supervision:

```sh
curl --fail --show-error https://media.example.com/ready
curl --fail --show-error https://media.example.com/live
```

## HTTPS with Pangolin/Newt

The root [`compose.yaml`](../compose.yaml) is a complete CPU-only Rivune and
PostgreSQL 18 stack. PostgreSQL is reachable only on an internal database
network. Rivune also joins the fixed `rivune-edge` bridge on
`172.31.0.0/24`; PostgreSQL never joins that edge network. Set the public
origin before starting the stack:

```dotenv
RIVUNE_PUBLIC_URL=https://rivune.example.com
```

```sh
docker compose pull
docker compose up -d
docker compose ps
```

Configure Newt to join the existing edge network in its own Compose definition;
this persists across Newt recreation:

```yaml
services:
  newt:
    networks:
      - rivune-edge

networks:
  rivune-edge:
    external: true
    name: rivune-edge
```

In Pangolin, configure the public resource target exactly as HTTP to Rivune's
container port, while the public origin remains HTTPS:

```yaml
targets:
  - hostname: rivune
    port: 8080
    method: http
```

These fields follow Pangolin's [public target](https://docs.pangolin.net/manage/resources/public/targets) and [Docker networking](https://docs.pangolin.net/self-host/dns-and-networking) contracts.

Do not use `localhost` from Newt; it refers to Newt itself. Do not publish a
host port when the dedicated edge network is available, and never attach Newt
to the database network. The base stack trusts forwarded network headers only
from its fixed `172.31.0.0/24` edge CIDR. A private topology override must set
`RIVUNE_TRUSTED_PROXIES` to Newt's exact IP or its dedicated edge CIDR, never
the LAN, database network, all private ranges, or `0.0.0.0/0`.

The Unraid XML template targets the same topology but expects an existing
PostgreSQL 18 container. Standard PostgreSQL on a database-only custom network
uses `RIVUNE_DATABASE_SSLMODE=disable`, an empty CA mount, and an empty CA path.
Connect Rivune and Newt to a separate dedicated edge network, then use hostname
`Rivune`, port `8080`, and method `http` in Pangolin. If sharing that network is
impossible, manually add an Unraid TCP Port mapping from container port `8080`
to an unused host port such as `18080`, target the Unraid LAN address and that
host port from Newt, and never forward it on the router.

For a new installation, generate the masked Unraid keyring field once with
`printf '1:'; openssl rand -hex 32`. For a legacy upgrade, use `1:` followed by
the existing `RIVUNE_TRACKING_ENCRYPTION_KEY`; generating a replacement makes
existing encrypted credentials unrecoverable.

The Unraid template persists downloaded artwork at
`/mnt/cache/appdata/rivune/artwork`, mounted as `/var/lib/rivune/artwork` in the
container. Existing v1.5.0 template installations should add that read-write
Path mapping before recreating Rivune; already cached files that lived only in
the old container layer are not recoverable after that container was removed.

### Advanced host topology

Keep deployment topology out of product Settings. The Compose files accept
host-only overrides for the public origin, trusted proxy/NAT64 networks, LAN
artwork origins, container identity, host ports, and component database
coordinates. `RIVUNE_DATABASE_URL` is the direct-DSN escape hatch and takes
precedence over component database fields. Because it normally contains a
password, supply it through a protected operator environment or secret manager,
not a committed override file.

External PostgreSQL, private certificate mounts, a different edge subnet, or
different CPU/memory/workspace resources should be expressed in a private
Compose override. When changing an edge subnet, change Rivune's trusted proxy
CIDR to exactly the same dedicated network. Do not attach a reverse proxy to the
database network. Publish the raw Rivune listener only for the documented
Pangolin fallback, bind it to the intended Unraid address, and never forward it
from the router or treat it as the public origin.

## Persisted settings and encrypted credentials

Timezone, Jellyfin compatibility and diagnostics, transcoding policy, preferred
video codec, quality preset, concurrency, hardware acceleration, bitrate, and
media/artwork quotas are database-backed instance settings. Provider credentials
are stored in separate encrypted database rows.
Administrators manage both from the web application; integration responses show
only whether a credential is configured and when it changed. Secret values are
never returned through settings, audit, activity, or integration responses.

Most setting changes and every provider update apply live. Hardware acceleration,
`preferredTranscodeVideoCodec`, `transcodeQualityPreset`, and
`transcodeConcurrency` are restart-bound: saving records the requested values,
while the active values remain unchanged and the fields stay `pending restart`.
Restart or recreate the `rivune` service, then wait for the active values to
match the requests and for the pending markers to clear. Persisted requests are
not proof that a GPU or codec path is functional.

### Keyring backup and rotation

The database archive and `RIVUNE_ENCRYPTION_KEYS` are one recovery set. Back up
the keyring through a protected secret store separate from the database archive,
but retain every key version needed by every retained archive. A database-only
restore cannot decrypt integration credentials, Trakt/Simkl profile tokens, or
in-flight tracking authorizations without the matching key versions. There is no
recovery bypass and no provider-secret export endpoint.

Rotate keys in this order:

1. Generate a new independent 32-byte key and prepend a new version, for example
   `RIVUNE_ENCRYPTION_KEYS=2:<new>,1:<old>`. Keep the old entry.
2. Restart Rivune and confirm it starts with the complete keyring. New or replaced
   ciphertext uses the first version.
3. Re-enter each configured integration credential in Administration so its row
   is encrypted with the active key. Disconnect and reconnect Trakt/Simkl profile
   accounts that still use the retired version; allow or remove old pending
   authorization rows according to the account workflow.
4. Inspect version references without selecting ciphertext or secret columns:

   ```sql
   SELECT 'integration' AS source, encryption_key_version, count(*)
   FROM instance_integration_credentials GROUP BY encryption_key_version
   UNION ALL
   SELECT 'tracking_account', encryption_key_version, count(*)
   FROM profile_tracking_accounts GROUP BY encryption_key_version
   UNION ALL
   SELECT 'tracking_authorization', encryption_key_version, count(*)
   FROM profile_tracking_authorizations GROUP BY encryption_key_version
   ORDER BY source, encryption_key_version;
   ```

5. Remove an old key from `.env` only after no live row and no retained backup
   requires it. Restart again. Existing rows are not re-encrypted merely because
   a new version is first in the list.

### One-shot legacy environment import

On the first release containing persisted configuration, Compose forwards old
values only to perform one transactional startup import. Existing database
values always win. The import writes only redacted audit metadata and never logs
or returns imported secrets. It is marked complete even when every legacy input
is empty, so these names never become runtime fallbacks on later starts:

- `TZ`, `RIVUNE_JELLYFIN_ENABLED`, `RIVUNE_JELLYFIN_DEBUG`,
  `RIVUNE_ALLOW_TRANSCODING`, `RIVUNE_HARDWARE_ACCELERATION`,
  `RIVUNE_TRANSCODE_MAX_BITRATE_KBPS`, `RIVUNE_MEDIA_MAX_STORAGE_MB`, and
  `RIVUNE_ARTWORK_MAX_STORAGE_MB`;
- `RIVUNE_TMDB_ACCESS_TOKEN`, `RIVUNE_FANART_API_KEY`,
  `RIVUNE_MDBLIST_API_KEY`, `RIVUNE_TVDB_API_KEY`, `RIVUNE_TVDB_PIN`,
  `RIVUNE_TRAKT_CLIENT_ID`, `RIVUNE_TRAKT_CLIENT_SECRET`, and
  `RIVUNE_SIMKL_CLIENT_ID`;
- `RIVUNE_TRACKING_ENCRYPTION_KEY`, which is accepted only as the version-1
  keyring migration input when `RIVUNE_ENCRYPTION_KEYS` is absent.

Before upgrading, take an authenticated database backup and preserve the old
private `.env`. Start the new image once, sign in, verify requested/active
settings and configured integration statuses, then replace the old encryption
input with `RIVUNE_ENCRYPTION_KEYS` and remove every legacy name from `.env`.
Never leave old values expecting them to override or repair database state.

### Existing-volume role migration

The initialization script creates the separated roles on a new volume and is safe to rerun. An existing volume created by an older Compose file still has `rivune` as its bootstrap superuser; environment changes do not alter that cluster. Before starting the new Compose definition, keep the old database running and run the migration below in Bash. Enter two new, independent secrets at the prompts; silent input keeps them out of terminal echo and shell history.

```bash
(
  set -euo pipefail
  cleanup() {
    unset COMPOSE_FILE RIVUNE_POSTGRES_SUPERUSER_PASSWORD RIVUNE_RESTORE_PASSWORD
  }
  trap cleanup EXIT

  read -r -s -p 'New bootstrap password: ' RIVUNE_POSTGRES_SUPERUSER_PASSWORD
  printf '\n'
  read -r -s -p 'New restore password: ' RIVUNE_RESTORE_PASSWORD
  printf '\n'
  export RIVUNE_POSTGRES_SUPERUSER_PASSWORD RIVUNE_RESTORE_PASSWORD
  export COMPOSE_FILE=compose.yaml

  docker compose exec -T \
    -e RIVUNE_POSTGRES_SUPERUSER_PASSWORD \
    -e RIVUNE_RESTORE_PASSWORD \
    postgres psql --username rivune --dbname rivune \
    --set ON_ERROR_STOP=1 <<'SQL'
\getenv bootstrap_password RIVUNE_POSTGRES_SUPERUSER_PASSWORD
\getenv restore_password RIVUNE_RESTORE_PASSWORD
\if :{?bootstrap_password}
\else
  \echo 'The bootstrap password was not forwarded.'
  \quit 1
\endif
\if :{?restore_password}
\else
  \echo 'The restore password was not forwarded.'
  \quit 1
\endif

SELECT length(:'bootstrap_password') > 0
   AND length(:'restore_password') > 0
   AND :'bootstrap_password' <> :'restore_password'
  AS role_passwords_valid \gset
\if :role_passwords_valid
\else
  \echo 'Bootstrap and restore passwords must be non-empty and distinct.'
  \quit 1
\endif

SELECT 'CREATE ROLE postgres LOGIN SUPERUSER'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgres') \gexec
ALTER ROLE postgres WITH LOGIN SUPERUSER CREATEDB CREATEROLE REPLICATION
  PASSWORD :'bootstrap_password';

SELECT 'CREATE ROLE rivune_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rivune_owner') \gexec
SELECT 'CREATE ROLE rivune_restore LOGIN NOSUPERUSER CREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rivune_restore') \gexec
ALTER ROLE rivune_restore WITH LOGIN NOSUPERUSER CREATEDB NOCREATEROLE NOREPLICATION
  PASSWORD :'restore_password';

REASSIGN OWNED BY rivune TO rivune_owner;
ALTER DATABASE rivune OWNER TO rivune_owner;
ALTER DATABASE postgres OWNER TO postgres;
ALTER DATABASE template0 OWNER TO postgres;
ALTER DATABASE template1 OWNER TO postgres;
GRANT rivune_owner TO rivune, rivune_restore;
ALTER ROLE rivune WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
SQL
)
```

The subshell and its `EXIT` trap discard the exported secrets even when migration fails. Compose receives each secret through the environment and its command line contains only environment-variable names; `psql` imports the values with `\getenv`, so neither password is expanded into a local or container process argument. Put those exact new secrets in `.env`, take an authenticated backup as described below, then recreate PostgreSQL and Rivune. Confirm the invariant with `SELECT rolname, rolsuper FROM pg_roles WHERE rolname IN ('rivune', 'rivune_restore');`: both rows must be `false`. Do not remove the bootstrap credential from `.env`; the official PostgreSQL image needs it for disaster recovery, but Rivune and the backup workflows never use it.

After exporting the signing and verification key paths, lineage, and trusted state path described below, update to a stable release by changing `RIVUNE_VERSION` to an exact released version, backing up first, recording the printed backup ID outside the repository, and recreating only the application:

```sh
COMPOSE_FILE=compose.yaml ./scripts/postgres-backup.sh backups/rivune-before-1.6.2.dump
./scripts/postgres-verify-backup.sh --expect-backup-id '<recorded ID>' backups/rivune-before-1.6.2.dump
# edit RIVUNE_VERSION=1.6.2 in .env
docker compose --env-file .env -f compose.yaml pull rivune
docker compose --env-file .env -f compose.yaml up -d rivune
curl --fail --show-error https://media.example.com/ready
```

## Playback processing and hardware

Rivune fixes the executable, input-validation, read-rate, thread, and HLS safety
boundaries. Administrators set transcoding permission, bitrate, preferred video
codec, quality preset, concurrency, media/artwork quotas, and the requested
encoder mode in Administration. Do not add removed FFmpeg/runtime variables or
the new database-backed settings to `.env` or a Compose override.

The provided Compose manifest is CPU-only by default. For AMD/Intel, set
`RIVUNE_VIDEO_DEVICE` and `RIVUNE_VIDEO_GROUP_ID`, determine the group with
`stat -c '%g' /dev/dri/renderD128`, and add the supported overlay:

```sh
docker compose -f compose.yaml -f compose.amd-intel.yaml up -d
```

On Unraid, manually add a Device mapping from host `/dev/dri/renderD128` to the
same container path, then set the optional render group field. Add neither on a
host without that node. These are deployment topology controls rather than
product settings. For NVIDIA, configure NVIDIA Container Toolkit in a private
Compose override and grant only the compute, video, and utility capabilities
required by FFmpeg.

After changing a restart-bound transcoding setting, restart the service and
verify its active value as described above. `auto` probes only devices exposed
by the deployment and falls back to software. A failed hardware job can fall
back once before output publication; neither a saved request, an FFmpeg encoder
listing, nor a visible device proves that hardware encoding is functional.

### Planning, probes, and headless devices

Playback planning preserves this order: Direct Play, remux, audio-only
transcode, then video transcode. Direct Play returns the validated source without
launching FFmpeg. Remux and audio-only transcode retain packet-copy video. A
video transcode selects a target from the client profile, the active backend,
and the functional software fallback; `auto` prefers H.264 for compatibility,
while an explicit codec is tried before the remaining compatible codecs. HEVC
and AV1 outputs always use fragmented MP4 HLS, and the decision target, encoder,
RFC 6381 codec, MP4 sample entry, and segment container must agree.

The restart-bound planning settings are:

| Setting | Accepted values | Default | Effect |
| --- | --- | --- | --- |
| `preferredTranscodeVideoCodec` | `auto`, `h264`, `hevc`, `av1` | `auto` | Orders target-codec selection; it cannot force a codec absent from the client/backend/software intersection. |
| `transcodeQualityPreset` | `speed`, `balanced`, `quality` | `balanced` | Selects backend-specific rate-control and preset arguments. It is not a portable FFmpeg preset name. |
| `transcodeConcurrency` | integer `1..32` | `4` | Bounds concurrent FFmpeg HLS processing jobs; probe, subtitle and trick-play pools keep their separate safety limits. |
| `hardwareAcceleration` | `auto`, `software`, `vaapi`, `hybrid`, `qsv`, `nvenc`, `amf` | `auto` | Selects a backend to probe. `hybrid` is Linux VA-API decode/encode with CPU filtering; `amf` is Windows-only. |

FFmpeg can *list* an encoder, decoder, filter, or hardware API even when the
driver, device permission, runtime library, pixel format, or complete filter
graph is unusable. These read-only inventory commands are safe to run because
they contain no media location or credential:

```sh
ffmpeg -hide_banner -encoders
ffmpeg -hide_banner -decoders
ffmpeg -hide_banner -filters
ffmpeg -hide_banner -hwaccels
```

Rivune treats that inventory only as a candidate set. At startup it runs bounded
one-frame functional probes for the selected encode/decode and, where needed,
tone-map path. The normalized encode/decode codec lists and backend in
Administration diagnostics describe the active probed capability, not GPU load,
quality, or real-time throughput. A functional startup probe is stronger than a
listing but still does not guarantee that a particular 4K source sustains
`1.00x`; confirm the actual session in Activity.

Linux VA-API and QSV require the mapped `/dev/dri/renderD128` (or explicitly
configured render node), its supplementary render group, and matching userspace
drivers. Linux QSV is derived from that VA-API render device. NVIDIA requires
the headless driver device nodes and encode/decode libraries exposed by NVIDIA
Container Toolkit; an X11 or Wayland display is not required. Vulkan tone
mapping additionally requires a working Vulkan ICD and direct interop on the
same DRM render device. Do not mount an entire host `/dev` tree to make a probe
pass.

On a native Windows server, `auto` tries AMF, QSV, then NVENC without inspecting
Unix paths and stops at the first backend with a functional encoder. AMF hardware
decode uses D3D11VA; QSV explicitly selects its D3D11VA hardware implementation
rather than VA-API or the `auto_any` implementation. FFmpeg child windows are
hidden, so an interactive desktop is not required; the service identity must
still be able to open the adapter and
the vendor driver/runtime must be installed. The release gate compiles the
server and all Go test binaries on Windows but deliberately performs no GPU
claim. The supplied Linux containers remain the supported Compose deployment
and are CPU-only until the operator exposes a device as described above.

Hardware decode is used only for a codec that the active backend successfully
probed and only while decoder, filters and encoder share a safe frame type.
Subtitle composition and software filters explicitly download frames to CPU;
VA-API/QSV upload them again when hardware encode remains selected. This copy
boundary is intentional and prevents an advertised decoder from being mistaken
for a zero-copy path.

### Representative generated FFmpeg argv

The following are **redacted argv shapes**, not copy-and-paste operator commands.
Rivune generates them from the media inspection, client profile, active settings
and successful functional probes. `INPUT` replaces the validated private egress
input; FFmpeg runs with the private workspace represented by `OUTPUT` as its
working directory, so generated output names are relative. `THREADS` is the
bounded server thread count; `...` and bracketed labels denote other bounded
generated arguments. None is a literal shell value. Encoder examples below use
the generated `balanced` quality mapping; `speed` and `quality` generate the
backend-specific alternatives.
Source URLs, headers, cookies, and tokens are intentionally absent and must never
be added to tickets, logs, diagnostics, or command examples.

Direct Play has no command at all:

```text
mode=direct   FFmpeg processes=0
```

A remux copies both packet streams; audio-only transcode changes only the audio
arguments (`-c:a aac ...`) and still uses `-c:v copy`:

```text
ffmpeg ... -i INPUT -map 0:v:0 -map 0:a:0? -sn -dn \
  -c:v copy -c:a copy -f hls -hls_segment_type mpegts \
  -hls_flags split_by_time+temp_file+delete_segments index.m3u8
```

Software target selection changes the encoder and compatible pixel format. H.264
may use MPEG-TS; HEVC and AV1 use fMP4 (`init.mp4` plus `.m4s` segments):

```text
# H.264, balanced
ffmpeg ... -i INPUT ... -threads THREADS -c:v libx264 -preset superfast \
  -crf 18 -pix_fmt yuv420p -tune zerolatency -c:a aac ... -f hls ... index.m3u8

# HEVC, balanced
ffmpeg ... -i INPUT ... -threads THREADS -c:v libx265 -preset superfast \
  -crf 23 -pix_fmt yuv420p -tune zerolatency -tag:v hvc1 -c:a aac ... \
  -f hls -hls_segment_type fmp4 -hls_fmp4_init_filename init.mp4 \
  -hls_segment_filename segment-%06d.m4s index.m3u8

# AV1, balanced
ffmpeg ... -i INPUT ... -threads THREADS -c:v libsvtav1 -preset 8 \
  -crf 30 -pix_fmt yuv420p -c:a aac ... \
  -f hls -hls_segment_type fmp4 -hls_fmp4_init_filename init.mp4 \
  -hls_segment_filename segment-%06d.m4s index.m3u8
```

Representative zero-copy Linux hardware paths follow. The codec suffix can be
`h264`, `hevc`, or `av1` only when that exact decoder/encoder combination probed:

```text
# VA-API, HEVC balanced
ffmpeg -init_hw_device vaapi=hw:/dev/dri/renderD128 -filter_hw_device hw \
  -hwaccel vaapi -hwaccel_device hw -hwaccel_output_format vaapi ... -i INPUT \
  -vf scale_vaapi=w=-2:h=1080:format=nv12 -c:v hevc_vaapi -profile:v main \
  -quality 4 -tag:v hvc1 ... index.m3u8

# Intel QSV on Linux, H.264 balanced
ffmpeg -init_hw_device vaapi=va:/dev/dri/renderD128 \
  -init_hw_device qsv=hw@va -filter_hw_device hw \
  -hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv ... -i INPUT \
  -vf scale_qsv=w=-2:h=1080:format=nv12 -c:v h264_qsv -profile:v high \
  -preset medium -look_ahead 0 ... index.m3u8

# NVIDIA NVENC, AV1 balanced
ffmpeg -hwaccel cuda -hwaccel_output_format cuda ... -i INPUT \
  -c:v av1_nvenc -profile:v main -preset p4 -tune ll -rc vbr \
  -spatial_aq 1 -zerolatency 1 ... index.m3u8
```

On Windows, generated QSV arguments do not contain a VA-API device. AMF uses
D3D11VA decoding only for a successfully probed codec and a GPU-safe filter
graph; otherwise decode/filtering occurs on CPU before AMF encode:

```text
# Windows QSV, HEVC balanced (no /dev/dri and no vaapi=...)
ffmpeg -init_hw_device qsv=hw:hw,child_device_type=d3d11va -filter_hw_device hw \
  -hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv ... -i INPUT \
  -c:v hevc_qsv -profile:v main -preset medium -look_ahead 0 -tag:v hvc1 ... index.m3u8

# Windows AMF, H.264 balanced with D3D11VA decode
ffmpeg -init_hw_device d3d11va=hw -filter_hw_device hw \
  -hwaccel d3d11va -hwaccel_device hw -hwaccel_output_format d3d11 ... -i INPUT \
  -c:v h264_amf -profile:v high -quality balanced ... index.m3u8
```

An HDR VA-API/Vulkan path is selected only after the complete DRM-to-Vulkan and
back-to-VA-API graph probes. The shortened shape is:

```text
ffmpeg -init_hw_device drm=dr:/dev/dri/renderD128 \
  -init_hw_device vaapi=hw@dr -init_hw_device vulkan=vk@dr -filter_hw_device hw \
  ... -vf hwmap=derive_device=vulkan:mode=read+direct,format=vulkan,\
libplacebo=...:tonemapping=bt.2390:color_primaries=bt709:color_trc=bt709,\
hwmap=derive_device=vaapi:mode=read+direct,format=vaapi \
  -c:v h264_vaapi -profile:v high -quality 4 ... index.m3u8
```

Text and bitmap subtitle burn-in require decoded frames and therefore break a
zero-copy path. Rivune builds the filter graph as a direct argv value rather
than shell text; the redacted shapes are:

```text
# Text subtitle from the selected embedded stream
ffmpeg ... -i INPUT -filter_complex \
  "[0:v:0]subtitles=filename='INPUT':si=N,[generated scale/tone-map/upload][vout]" \
  -map "[vout]" ...

# Bitmap subtitle
ffmpeg ... -i INPUT -filter_complex \
  "[0:v:0][0:s:N]overlay=eof_action=pass:repeatlast=0,[generated filters][vout]" \
  -map "[vout]" ...
```

If a probed hardware job fails before HLS publication, Rivune deletes partial
output and makes at most one retry with the software encoder for the **same**
target codec. For example, `-c:v hevc_vaapi` becomes `-c:v libx265`; it does not
silently become H.264. A target without a functional same-codec software
fallback is not planned. Failure after publication is reported rather than
starting a second timeline.

For HDR-to-SDR playback, Rivune probes the complete hardware filter path rather
than trusting the selected encoder name. AMD render devices (`0x1002`) try the
VAAPI-to-Vulkan `libplacebo` path before `tonemap_vaapi`, while other vendors
retain VAAPI-first probing; each path falls through to the next backend on probe
failure. The container image includes the Mesa Vulkan runtime for the AMD path.
If hardware tone mapping is unavailable in automatic mode, HEVC Main 10 sources
retain VAAPI decode and encode around the software tone mapper when the frame
format is known, and new tone-mapped sessions are capped at 1080p to preserve
real-time playback. The explicit `hybrid` mode instead probes HEVC Main 10 VAAPI
decode, P010 readback, CPU tone mapping, NV12 upload, and VAAPI encode at startup.
It leaves the requested maximum resolution in force so an operator with a strong
CPU and a small integrated GPU can measure that path directly. Administration
Activity and the initialization log report the selected `vulkan`, `vaapi`,
`hybrid`, or `software` tone-map backend while retaining the hardware capability
indicator. A sustained job speed below `1.00x` still means the active pipeline
cannot feed continuous playback.

The backend label proves filter selection, not real-time throughput. No finite
buffer can compensate for a job that remains below `1.00x`: lower the effective
profile or server **Maximum resolution**, close the existing player session,
and reopen the title so Rivune creates a new decision. Use the highest target
that stays above real time without draining its buffer; integrated AMD hardware
may require 1080p for 4K HDR input even when the Vulkan probe succeeds.

To compare the CPU-assisted path, select **Hybrid (VA-API + CPU)** under the
server hardware-acceleration setting, save, restart the service, close the old
player session, and reopen the same title. Hybrid keeps codec decode and encode
on VA-API but runs HDR tone mapping and scaling on the CPU. It does not guarantee
real-time 4K output: retain it only when Activity stays above `1.00x` without
draining the buffer; otherwise lower **Maximum resolution** or return to `auto`.

To remove the GPU from the media path entirely, select **Software**, save, and
restart. An explicitly selected software backend honors **Maximum resolution**
and uses CPU decode, tone mapping, scaling, and the selected compatible H.264,
HEVC, or AV1 encoder; automatic software fallbacks remain capped at 1080p. Full
software 2160p has the highest CPU and power cost, so keep it only when Activity
proves sustained real-time throughput.

Seekable transcoding keeps a duration-aware production margin instead of
running at exactly real time. The initial HLS buffer defaults to 6 seconds, and
requests up to 10 three-second segments (30 seconds) ahead reuse and wait for
the current generation rather than replacing it. The margin is bounded by the
retained HLS window, so transient processing or network stalls can recover
without evicting the next segment required by a continuously playing client.

The Compose stack uses read-only root filesystems, a fixed 256 MiB
`noexec,nosuid,nodev` `/tmp` tmpfs, fixed CPU/memory limits, minimized
capabilities, and PID ceilings. Transcode, remux, and HLS work is stored on the
disk-backed `media_workspace` volume at `/var/lib/rivune/media`, selected by
`RIVUNE_MEDIA_TEMP_DIR`; Rivune cleans that workspace on startup and retains its
20 GiB application storage ceiling. Downloaded posters, backdrops, logos, and
cast images use the persistent `artwork_cache` volume at
`/var/lib/rivune/artwork`. The Unraid template maps these paths to
`/mnt/cache/appdata/rivune/transcode` and `/mnt/cache/appdata/rivune/artwork`.
The underlying filesystem can impose lower practical ceilings. Advanced
operators who replace these volume declarations should use writable local
storage, preserve the documented container paths, and keep host capacity
consistent with the requested quotas.

Startup cleanup requires the documented root entrypoint and a writable local
volume or bind mount. NFS root-squash, server-side ACL denials, read-only or
immutable storage, and a forced Docker `--user` cannot be repaired by container
capabilities; pre-own such storage for the configured PUID/PGID or use the
supported local-volume topology.

Administration Activity reports bounded selected-encoder, target codec, quality,
encode/decode capability, pool, quota, pipeline, and job diagnostics. It never
returns a complete argv, command output, source URL, header, token, provider
credential, or private provider detail. Fixed safety limits are not exposed as
environment settings; diagnose capability, capacity, storage, source, or
processing failures instead of making those guardrails configurable.

## Jellyfin-compatible client access

Rivune includes a limited Jellyfin-compatible API adapter. It is not a complete Jellyfin server, does not implement every Jellyfin endpoint or client feature, and provides no LAN UDP discovery. Configure it manually with Rivune's public URL.

The adapter is off by default. A global administrator enables it with the
`jellyfinEnabled` setting in Administration; `jellyfinDebug` enables bounded
compatibility-route diagnostics when temporarily needed. Both apply live and do
not require environment variables or a service restart.

Diagnostics record normalized route metadata and bounded response shape, never
query/header/body values, credentials, cookies, provider URLs, payload bytes, or
item/session identifiers. Disable the setting after collecting the required
evidence. For every non-loopback deployment, keep Rivune behind the documented
HTTPS reverse proxy and give clients the same HTTPS origin configured as the
public URL. Never publish raw port 8080 to a LAN or the internet.

Open the profile's **Preferences → Connections** page and generate its Jellyfin credential. Enter the displayed UUID as the client username and the generated profile-only application password as the password. The password is shown once; copy it before closing the dialog, or rotate it to issue a replacement. Rivune account passwords, administrator credentials, profile names, and profile PINs are never accepted by the compatibility login.

Early development builds of the profile-credential cutover could remove the old Jellyfin mapping after its revoked session had already been purged while leaving the dedicated device row counted against the per-user quota. Migration 66 removes these rows whenever cutover-session evidence remains. If an instance ran one of those unreleased builds and a new application-password login still fails at the device limit, open **Administration → Devices**, remove only the stale duplicate compatibility-device entries for the affected profile, and retry once. Do not remove unrelated native devices.

Clients may use either the exact Jellyfin-style root paths or one lowercase `/emby` prefix. For example, public discovery is available at `/System/Info/Public` and `/emby/System/Info/Public`; nested prefixes, case variants, path normalization, and implicit method fallbacks are rejected. The bounded compatibility contract covers:

- public server identity and availability probes;
- profile-credential login, enabled profile-bound Quick Connect initiation/poll/exchange, the credential-bound user, session/capability projection, logout, and bounded WebSocket liveness;
- library views, items, movie/series hierarchy, enabled metadata and add-on catalog search, item artwork, and deterministic profile avatars;
- lazy multi-source `PlaybackInfo`, direct/remux/transcode delivery through Rivune's existing playback pipeline, byte ranges, seeking, opaque HLS child requests, and capability-scoped WebVTT subtitles;
- playing/progress/stopped events, played state, favorites, resume items, and next-up.

Private provider URLs, headers, native playback tokens, and source references remain server-side. Query authentication is accepted for Jellyfin protocol compatibility, but Rivune-generated playback URLs contain only an owner/item/source/TTL-bound capability and never the profile credential. Use HTTPS for every non-loopback deployment. When the adapter is enabled, `GET /QuickConnect/Enabled` reports the wired profile-bound flow: initiation requires stable client-device metadata, creates a 10-minute code, and directs a signed-in manager to `/pair`; polling is non-consuming and bound to the initiating device; exchange is single-use and creates no password fallback. Approval revalidates the manager's active manageable profile and binds the compatibility session to exactly that profile and its category. Plugin and package lists are empty, and unknown paths or methods return `404`. The exact request/response schemas, limits, and status codes are in [`protocol/jellyfin-compat-openapi.yaml`](../protocol/jellyfin-compat-openapi.yaml).

Jellyfin Media Player/Desktop is incompatible with this adapter because it loads the server-hosted Jellyfin Web application from `/` after discovery, while Rivune intentionally serves its own web application there. A successful API probe in that desktop shell therefore does not validate the standalone application flows covered by the adapter.

To roll back, disable Jellyfin from the administrator settings page. Confirm normal Rivune access through the HTTPS origin afterward.

### Playback evidence and real-client validation

The compatibility route smoke is not a media certification. A route match, discovery response, `PlaybackInfo` response, or successful `2xx` proves only that exchange. Claiming a playback workflow requires the emitted URL to be consumed, media bytes to be decoded/rendered, seek and selected tracks to be observed, and the session to stop cleanly.

The optional FFmpeg byte-path tests create only short lavfi video/audio and local text in temporary directories. They do not download or retain media. Run them only on a development host with `ffmpeg` and `ffprobe` on `PATH`:

```sh
cd server
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/playback \
  -run '^TestExternalMedia' -count=1
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/jellyfin \
  -run '^TestPlaybackGatewayReadsPlaylistAndChildBytesAndRejectsOutOfOrderChild$' -count=1
```

The first command covers generated MP4/MKV/WebM/AVI/MPEG-PS inputs, direct
ranges, TS/fMP4 HLS remux/transcode, H.264/HEVC/AV1 outputs, a real 4K-to-1080p
scale, HDR10-to-SDR tone mapping with decoded colorimetry, overlapping AAC track
selection, 5.1 downmix, AC-3/E-AC-3/TrueHD/DTS/FLAC/Opus conversion, UTF-8
subtitle seek, embedded ASS burn-in, and HLS child delivery. The second generates
a one-second H.264/AAC MPEG-TS child and exercises the Jellyfin adapter gateway.
A skip is not a pass: record it as `ABSENT` when FFmpeg is unavailable. Dolby
Vision, generated HLG pixels, bitmap-subtitle bytes, and named-client rendering
are not supplied by these tests.

Targeted microbenchmarks are deliberately finite and carry no release budget:

```sh
cd server
go test ./internal/playback -run '^$' \
  -bench '^(BenchmarkRewritePlaylist|BenchmarkDirectorySize|BenchmarkPlaybackDecision)$' \
  -benchmem -count=5
```

Record the CPU model, OS, Go version, revision and complete benchmark output when comparing runs. The cases use fixed playlists of 8/120 references, a fixed 32-file directory and the four planner outcomes. Do not turn one machine's measurements into an invented p95, concurrency capacity or pass/fail threshold.

For Infuse, Streamyfin or VidHub, use this protocol for each exact released client version:

1. Record the client name/version/build, device OS/version, Rivune revision or image digest, FFmpeg version, hardware-acceleration mode and UTC interval. Use a fresh compatibility credential dedicated to the validation device; never put it in a command argument or artefact.
2. Enable `jellyfinDebug` in Administration and confirm logs expose only normalized client family/version, route/method, query **names and cardinalities**, selected-header presence, bounded response metadata and bounded JSON shape. Stop if any value, ID, URL, query, token, provider name or codec appears as a label.
3. In the actual client UI, perform discovery/manual connection, login, catalogue navigation, item detail and artwork. Then play long enough to observe rendered video and audible audio; select another audio track and subtitle where the licensed local fixture permits it; seek forward and backward; pause/resume; and stop. Record unsupported steps as `ABSENT`, not skipped success.
4. Correlate the bounded server trace with `PlaybackInfo`, the chosen mode (`direct`, `remux`, `transcode_audio`, or `transcode`), the exact same-origin stream/master/child sequence, byte ranges or decoded segments, Playing/Progress/Stopped, and FFmpeg teardown. Status codes alone are insufficient: retain a human observation of rendering and client controls plus bounded server-side media/lifecycle evidence.
5. Scrub a copy of the trace before it leaves the validation host. Remove or replace all credentials, cookies, authorization headers, user/profile/item/session/source/device IDs, IPs/hostnames, full URLs, query values, provider data, filesystem paths and media titles. Preserve the ordered methods, normalized route templates, status, content type, range presence, byte counts, timing buckets, decision mode and failure enum. Review the scrubbed copy manually; never publish the raw trace.
6. Store the scrubbed trace, environment manifest, command outputs and a result sheet together in the controlled release evidence store under client/version/date. Give each step `PASSED`, `ABSENT` or `BLOCKED` and link its artefact. Revoke the temporary credential, disable `jellyfinDebug`, and verify no playback process remains.

Current evidence must be summarized narrowly: Infuse 8 has a partially observed hierarchy scan but no validated player playback; Streamyfin 0.31.0 has a reported HTTP-level HLS profile replay on NAS but no validated application rendering; VidHub has no audited trace or validation. None may be advertised as generally compatible until the versioned, scrubbed real-client bundle above exists.

## Optional Unraid PostgreSQL TLS

The common Unraid mode uses `sslmode=disable` only while PostgreSQL is confined
to its database-only custom Docker network. Leave both PostgreSQL CA fields
empty in that mode. Never publish PostgreSQL on the Unraid host, attach Newt or
another reverse proxy to the database network, or use plaintext across a shared
or untrusted network.

For encrypted database transport, select `verify-full`, mount the public
`ca.crt` read-only, and set the container CA path. Create the private CA and
server key on a protected administration machine, not in a container or
repository. Set `DB_HOST` to the exact value entered as
`RIVUNE_DATABASE_HOST`. Use `DNS:` for a hostname and `IP:` for a literal
address; `verify-full` rejects a certificate whose SAN does not match.

```sh
umask 077
DB_HOST=postgres
SAN="DNS:${DB_HOST}" # use SAN="IP:192.0.2.10" when DB_HOST is that IP
TLS_WORK="$HOME/rivune-postgres-pki"
install -d -m 700 "${TLS_WORK}"

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 \
  -out "${TLS_WORK}/ca.key"
openssl req -x509 -new -sha256 -days 3650 \
  -key "${TLS_WORK}/ca.key" \
  -subj "/CN=Rivune PostgreSQL CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -out "${TLS_WORK}/ca.crt"

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 \
  -out "${TLS_WORK}/server.key"
openssl req -new -sha256 \
  -key "${TLS_WORK}/server.key" \
  -subj "/CN=${DB_HOST}" \
  -out "${TLS_WORK}/server.csr"
cat >"${TLS_WORK}/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=${SAN}
EOF
openssl x509 -req -sha256 -days 397 \
  -in "${TLS_WORK}/server.csr" \
  -CA "${TLS_WORK}/ca.crt" \
  -CAkey "${TLS_WORK}/ca.key" \
  -CAcreateserial \
  -extfile "${TLS_WORK}/server.ext" \
  -out "${TLS_WORK}/server.crt"
openssl verify -CAfile "${TLS_WORK}/ca.crt" "${TLS_WORK}/server.crt"
openssl x509 -in "${TLS_WORK}/server.crt" -noout -subject -issuer -dates -ext subjectAltName
```

Keep `ca.key` encrypted or on offline protected storage and back it up
separately. Never copy it to Unraid, Rivune, or PostgreSQL. Transfer only
`server.crt`, `server.key`, and `ca.crt` over an authenticated channel. On
Unraid, determine the numeric identity used by the official image and install
the files without making the server key group/world-readable:

```sh
POSTGRES_UID="$(docker run --rm postgres:18-trixie id -u postgres)"
POSTGRES_GID="$(docker run --rm postgres:18-trixie id -g postgres)"
install -d -o "${POSTGRES_UID}" -g "${POSTGRES_GID}" -m 700 \
  /mnt/user/appdata/postgres/tls
install -o "${POSTGRES_UID}" -g "${POSTGRES_GID}" -m 600 \
  "${TLS_WORK}/server.key" /mnt/user/appdata/postgres/tls/server.key
install -o "${POSTGRES_UID}" -g "${POSTGRES_GID}" -m 644 \
  "${TLS_WORK}/server.crt" /mnt/user/appdata/postgres/tls/server.crt
install -d -m 755 /mnt/user/appdata/rivune/postgres-tls
install -m 644 "${TLS_WORK}/ca.crt" \
  /mnt/user/appdata/rivune/postgres-tls/ca.crt
```

In the official `postgres:18-trixie` Unraid template, bind-mount
`server.crt` and `server.key` at `/run/postgresql-tls/server.crt` and
`/run/postgresql-tls/server.key`, both read-only. Pass these image arguments:

```text
postgres -c ssl=on -c ssl_cert_file=/run/postgresql-tls/server.crt -c ssl_key_file=/run/postgresql-tls/server.key
```

Restart PostgreSQL and run `SHOW ssl;`; it must return `on`. The Rivune
template separately mounts only the public CA at
`/run/rivune-postgres-tls/ca.crt` and sets
`RIVUNE_DATABASE_SSLROOTCERT` to that path. Leave
`RIVUNE_DATABASE_SSLMODE=verify-full`.

To migrate an existing plaintext Unraid installation to TLS, use a planned
maintenance window:

1. Provision the CA and server certificate, enable PostgreSQL TLS, and confirm
   `SHOW ssl` before changing Rivune.
2. From the database network, test the exact hostname and CA independently:

   ```sh
   read -r -s -p 'Rivune database password: ' PGPASSWORD
   printf '\n'
   export PGPASSWORD
   docker run --rm --network rivune-database \
     -e PGPASSWORD \
     --mount type=bind,src=/mnt/user/appdata/rivune/postgres-tls/ca.crt,dst=/run/postgresql-ca/ca.crt,readonly \
     postgres:18-trixie \
     psql "host=postgres port=5432 dbname=rivune user=rivune sslmode=verify-full sslrootcert=/run/postgresql-ca/ca.crt" \
     -c 'SELECT current_database();'
   unset PGPASSWORD
   ```

3. In the Rivune template, select `verify-full`, add the CA mount, and set
   `RIVUNE_DATABASE_SSLROOTCERT=/run/rivune-postgres-tls/ca.crt`, then restart
   Rivune. Do not use `allow`, `prefer`, `require`, or `verify-ca` as a partial
   substitute for hostname-verifying TLS.
4. As the PostgreSQL administrator, prove that the application session is
   encrypted and inspect the negotiated protocol and cipher:

   ```sql
   SELECT a.usename, a.application_name, s.ssl, s.version, s.cipher
   FROM pg_stat_activity AS a
   JOIN pg_stat_ssl AS s USING (pid)
   WHERE a.datname = 'rivune' AND a.usename = 'rivune';
   ```

   At least one Rivune row must exist and every `ssl` value must be `true`.
5. To reject plaintext for every database client, put the following rules
   before broader host rules in the file reported by `SHOW hba_file;`, reload
   PostgreSQL with `SELECT pg_reload_conf();`, and verify that a deliberate
   `sslmode=disable` connection is rejected:

   ```text
   hostnossl all all 0.0.0.0/0 reject
   hostnossl all all ::/0      reject
   ```

Monitor expiry with `openssl x509 -checkend` and rotate before the check fails.
For a server-certificate rotation under the same CA, install the new
certificate and mode-`0600` key atomically, restart PostgreSQL, and repeat the
`pg_stat_ssl` proof. For a CA rotation, first mount a CA bundle containing both
old and new CA certificates in Rivune, then deploy a server certificate signed
by the new CA, verify all Rivune sessions, and finally remove the old CA from
the bundle. Reapply ownership and permissions after every rotation. Retain the
old CA private key only according to the deployment's revocation and recovery
policy; it never belongs on an application host.

## PostgreSQL backup authentication

The backup scripts require an RSA private signing key, the corresponding public
verification key, a deployment lineage, and a trusted generation-state file. Keep
the keys and state in a trusted configuration directory that is **not** the backup
directory or backup repository. The scripts reject symlinks, keys or state not
owned by the invoking user, group/other-accessible keys and state, unsafe parent
directories, and authentication material stored below the archive directory.

```sh
KEY_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/rivune/backup-auth"
install -d -m 700 "${KEY_DIR}"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 \
  -out "${KEY_DIR}/backup-signing-2026.pem"
openssl pkey -in "${KEY_DIR}/backup-signing-2026.pem" -pubout \
  -out "${KEY_DIR}/backup-verification-2026.pem"
chmod 600 "${KEY_DIR}/backup-signing-2026.pem" \
  "${KEY_DIR}/backup-verification-2026.pem"
export RIVUNE_BACKUP_SIGNING_KEY_FILE="${KEY_DIR}/backup-signing-2026.pem"
export RIVUNE_BACKUP_VERIFY_KEY_FILE="${KEY_DIR}/backup-verification-2026.pem"
export RIVUNE_BACKUP_STATE_FILE="${KEY_DIR}/generation.state"
# Generate once for this database deployment, record it in protected configuration,
# and keep it stable across key rotation and disaster recovery.
export RIVUNE_BACKUP_LINEAGE="prod-$(openssl rand -hex 16)"
```

`postgres-backup.sh` reserves a monotonic sequence in the trusted state file,
takes a transactionally consistent custom-format dump while Rivune remains
online, and emits `ARCHIVE`, `ARCHIVE.manifest`, and `ARCHIVE.sig`. New backups
use the canonical nine-line `rivune-backup-manifest-v2`; its signature binds a
random backup ID, deployment lineage, sequence, UTC creation time, canonical
archive filename, exact `archive_size`, archive SHA-256 digest, and signing-key
fingerprint. The script authenticates the manifest, checks the archive's exact
size and digest, invokes `pg_restore --list`, publishes private mode-600 files
without overwriting, and then commits the generation as current in the trusted
state. A failed reservation may leave a harmless sequence gap; sequence numbers
are never reused.

The signed bytes have this exact order (with concrete values and a final newline):

```text
format=rivune-backup-manifest-v2
lineage=...
sequence=...
backup_id=...
created_at=...
archive_name=...
archive_size=...
archive_sha256=...
signing_key_id=...
```

```sh
mkdir -p backups
BACKUP="backups/rivune-$(date -u +%Y%m%dT%H%M%SZ).dump"
COMPOSE_FILE=compose.yaml ./scripts/postgres-backup.sh "${BACKUP}"
```

The success line prints the backup ID, sequence, and digest. Record those values
in an immutable object version, change ticket, password-manager note, or other
operator-controlled system outside the attacker-writable backup repository.
Move the archive, manifest, and signature to encrypted off-host storage, but keep
the verification key, lineage, and generation state separately protected.
Adjacent digests, manifests, and signatures alone do not establish freshness:
an attacker can replay an older valid set. Normal restore therefore requires both
the externally recorded exact ID and an exact match with trusted current state.

Verify every backup by selecting the externally recorded ID, restoring into a
disposable PostgreSQL 18 container, and checking the migration ledger:

```sh
./scripts/postgres-verify-backup.sh \
  --expect-backup-id '<externally recorded 32-hex backup ID>' \
  backups/rivune-20260731T120000Z.dump
```

The manifest and signature are copied to bounded private snapshots and
authenticated first. The external ID expectation—and, for production restore,
the protected current/rollback/initialization policy—is checked before any
archive byte is staged. Only then is an exact-size private archive snapshot
authenticated before `pg_restore` parsing. The disposable container has
`--network none`, a read-only root filesystem, reduced capabilities, and no
egress; the archive is restored as non-superuser `rivune_restore`, never as the
bootstrap superuser. Generated passwords are inherited through named environment
variables and never placed in process arguments or output. The container and its
data are removed on success or failure.

An authenticated PostgreSQL archive is still executable database input: it can
contain SQL and object definitions chosen by whoever controls the signing key.
Verification limits privileges and egress but does not make an untrusted signer
safe. Authorize signers narrowly, protect the private key, and inspect provenance
before a destructive production restore.

Create `RIVUNE_BACKUP_STAGING_DIR` outside the backup repository on a local
filesystem with an enforced quota appropriate for one restore; make it owned by
the invoking user and mode `0700`. If it is unset, `${TMPDIR:-/tmp}` is used, so
that filesystem must provide the same local quota guarantee; an unbounded shared
or network temporary directory is not suitable.
The scripts also reject backup, staging, key, or state paths below an ancestor
owned by another user or writable without a root-owned sticky-directory
boundary.
Before reading an archive, the
scripts use GNU `fallocate` to reserve the permitted bytes physically and fail
closed if allocation or quota enforcement fails—there is no sparse fallback.
GNU `dd` opens the repository pathname with `nofollow,nonblock,fullblock` and
`count_bytes`, so an oversize or raced source can write at most one byte beyond
the authenticated size before rejection. The SHA-256 is computed only from that
private snapshot. `RIVUNE_BACKUP_MAX_BYTES` remains a ceiling (1 TiB by default)
on signed v2 sizes; it is not the staging size selected for a v2 archive.

For key rotation, generate a newly dated pair, change both exported key paths for
new backups, and test a newly signed backup. Do not change the lineage or state
file. Remove the retired private key after the overlap period, but retain each old
public key in the separate trust store as long as an explicitly restorable archive
uses it. The signed key fingerprint makes a wrong key selection fail closed; do
not copy verification keys into the backup repository.

## PostgreSQL restore

A restore replaces the current `rivune` database, but it does not destroy that
database in place. Take and verify a new authenticated backup of the current
database first. Export the restore password from the protected deployment secret;
it is independent from the application and bootstrap passwords. Keep the
signing/verification key, lineage, and state exports from the backup section.
Before running the restore, put a protected `RIVUNE_ENCRYPTION_KEYS` recovery copy
containing every version referenced by the selected database into `.env`; the
script validates a staging copy and may restart Rivune after activation, and
encrypted rows cannot be recovered with a newer key alone. Select the backup ID
from the separate operator-controlled record, never from the adjacent manifest:

```sh
export COMPOSE_FILE=compose.yaml
export RIVUNE_SERVICE=rivune
export RIVUNE_RESTORE_PASSWORD='<value from protected .env>'
./scripts/postgres-backup.sh backups/rivune-before-restore.dump
./scripts/postgres-verify-backup.sh \
  --expect-backup-id '<ID printed by that backup command and recorded separately>' \
  backups/rivune-before-restore.dump
./scripts/postgres-restore.sh \
  --expect-backup-id '<externally recorded ID of the selected current backup>' \
  backups/rivune-20260731T120000Z.dump
unset COMPOSE_FILE RIVUNE_SERVICE RIVUNE_RESTORE_PASSWORD
```

Normal restore has no implicit archive selection and no rollback fallback. It
first authenticates the signed manifest, filename, exact archive size, key
fingerprint, and lineage. It then checks the exact external ID and requires the
manifest generation, ID, and digest to equal the latest values in protected
state, all before staging the archive. It holds the state lock across exact-size
archive staging, digest authentication, parsing, and a state reread immediately
before stopping Rivune. Thus an older valid archive/manifest/signature set cannot
consume archive staging capacity or silently replace a newer snapshot, even when
all adjacent files in the backup repository are replayed together.

On a replacement disaster-recovery host where the separately protected state
file is genuinely unavailable, bootstrap it once with the externally recorded
exact ID:

```sh
./scripts/postgres-restore.sh \
  --initialize-state '<externally recorded 32-hex backup ID>' \
  backups/rivune-20260731T120000Z.dump
```

Initialization fails if a state file already exists and records a mode-600
`state-initialized` audit event beside the state file. Copying an ID from the
attacker-writable repository does not establish freshness; obtain it from the
independent record. Keep the recovered lineage unchanged for later backups.

For a deliberate rollback within the same signed lineage, use the distinct
rollback verb and type the old backup's externally recorded exact ID:

```sh
./scripts/postgres-restore.sh \
  --allow-rollback '<externally recorded old 32-hex backup ID>' \
  backups/rivune-20260715T120000Z.dump
```

This path accepts only a sequence older than protected current state, records
`rollback-authorized` before PostgreSQL parsing and `rollback-completed` after
the ledger check in `${RIVUNE_BACKUP_STATE_FILE}.audit`, and never lowers the
trusted high-water mark. Any later restore of that generation therefore requires
another explicit rollback authorization. Protect and retain the audit file with
the state file.

The rollback database can reference encryption-key versions no longer used by
the current database. Restore the selected archive's matching keyring versions
to `.env` before invoking `--allow-rollback`; do not wait for startup decryption
failures to discover that recovery material was discarded.

Archives created with a v1 manifest or the previous detached-archive-signature
format have no signed size. They are rejected unless the operator supplies
`RIVUNE_BACKUP_LEGACY_MAX_BYTES`, a positive conservative cap no greater than
`RIVUNE_BACKUP_MAX_BYTES`. They never inherit the 1 TiB v2 ceiling as an
implicit staging allowance. The script reserves `cap + 1` bytes and copies at
most that amount; because detached legacy authentication covers the archive
itself, it necessarily authenticates only after this bounded copy.

Use the legacy verb only for a detached archive, with an exact SHA-256 digest
from a trusted record outside the repository:

```sh
export RIVUNE_BACKUP_LEGACY_MAX_BYTES=1073741824 # choose for this known archive
./scripts/postgres-restore.sh \
  --allow-legacy '<externally recorded 64-hex archive SHA-256>' \
  backups/rivune-legacy.dump
unset RIVUNE_BACKUP_LEGACY_MAX_BYTES
```

The legacy path refuses manifested backups, verifies the old RSA/SHA-256 archive
signature with the explicitly selected retained public key, requires
`RIVUNE_BACKUP_LINEAGE`, and records authorization and completion in the audit
log. It is a migration/rollback exception, not a default. The same explicit cap
is required to inspect it with `postgres-verify-backup.sh --allow-legacy`.

For a v1 manifested archive, use the normal `--expect-backup-id` or exceptional
restore verb plus the same explicit cap. Migrate v1 and detached archives
offline on a trusted, quota-limited host: verify them under the old key and
external ID/digest, then create a normal new backup. New output is always signed
v2; never add an unsigned size beside an old manifest or raise the legacy cap
merely to make an unknown archive pass.

After policy authorization, the script connects as non-superuser
`rivune_restore`, assumes non-login `rivune_owner` for restored objects, safely
removes only a stale fixed-name staging database, restores the archive into a new
staging database with `--exit-on-error`, and checks its migration ledger while
the live database and Rivune remain untouched. It then stops Rivune and atomically
renames the live database to a retained prior name and the validated staging
database to `rivune`. Every reserved database identifier is passed through `psql`'s
identifier quoting (or PostgreSQL `format('%I')` for generated statements); the
restore password is passed to Compose by environment-variable name, not as an
argument or log value. Missing, unsafe, oversized, inconsistent, replayed,
incorrectly selected, unrestorable, or ledger-invalid material fails before the
application is stopped or the live database name changes.

If Rivune was running before the restore, the script starts it with Compose's
health wait after the database-name swap. Only after Compose reports it healthy
does the script drop the retained prior database. If the swap, startup, or
readiness check fails, it stops Rivune, atomically restores the prior database
name, removes the inactive failed staging copy, and restores the prior running
state. Thus no failure after archive verification destroys the prior working
database. If automatic name rollback itself fails, the script leaves Rivune
stopped, retains the prior database under the fixed `rivune_restore_prior` name,
does not run destructive cleanup, returns failure, and requires operator
attention. A prior database that was intentionally stopped remains stopped.
Inspect both services before accepting traffic:

```sh
export COMPOSE_FILE=compose.yaml
export RIVUNE_SERVICE=rivune
docker compose ps
docker compose logs --tail=100 postgres rivune
curl --fail --show-error https://media.example.com/ready
docker compose exec -T postgres psql --username rivune --dbname rivune \
  --tuples-only --no-align \
  --command 'SELECT count(*), max(version) FROM schema_migrations;'
unset COMPOSE_FILE RIVUNE_SERVICE
```

Keep the original archive, manifest, signature, applicable trusted public key,
matching Rivune encryption-key versions, external ID record, protected
generation state, and audit log until application login, integration statuses,
profiles, collections, tracking connections, and playback history have been
checked.

## Migration and proxy validation

CI builds the current image and runs both scripts below. They create uniquely named disposable Docker networks, containers, and volumes and clean them on every exit.

```sh
docker build --build-arg VERSION=ci-local -t rivune-ci:current -f server/Dockerfile .
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/migrations.sh
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/reverse-proxy-smoke.sh
```

The migration check performs a clean install, checks the migration count and current version, restarts to prove idempotency, constructs the immediately previous schema, upgrades it with the current image, and checks idempotency again. The proxy smoke test generates a disposable self-signed certificate and Nginx configuration, serves a real Rivune discovery response over HTTPS, then verifies that the proxy overwrites spoofed forwarding headers and forwards the HTTPS scheme and host values.
