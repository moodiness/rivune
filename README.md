<p align="center">
  <img src="templates/unraid/rivune-icon.svg" alt="Rivune" width="120" height="120">
</p>

<h1 align="center">Rivune</h1>

<p align="center"><strong>Your self-hosted media universe, on every screen.</strong></p>

<p align="center">
  Open-source media backend and responsive web app with no predefined catalog or hosted service.
</p>

<p align="center">
  <a href="https://github.com/moodiness/rivune/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/moodiness/rivune?style=flat-square"></a>
  <a href="https://github.com/moodiness/rivune/actions/workflows/release-candidate.yml"><img alt="Release build" src="https://img.shields.io/github/actions/workflow/status/moodiness/rivune/release-candidate.yml?style=flat-square&label=release"></a>
  <a href="https://github.com/moodiness/rivune/pkgs/container/rivune"><img alt="Container image" src="https://img.shields.io/badge/container-ghcr.io-2496ED?style=flat-square"></a>
  <a href="LICENSE"><img alt="Apache 2.0 license" src="https://img.shields.io/github/license/moodiness/rivune?style=flat-square"></a>
</p>

<p align="center">
  <a href="#features">Features</a> ·
  <a href="#architecture">Architecture</a> ·
  <a href="#quick-start-with-docker-compose">Quick start</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="#development">Development</a>
</p>

<p align="center">
  Rivune keeps authentication, profiles, collections, playback state, provider credentials, and source resolution on the server selected by the user. Clients receive a consistent experience without learning private provider URLs or headers.
</p>

<p align="center">
  Rivune is under active development. The web client and backend are usable today. Typed protocol-v19 clients for Apple, Android, and Windows are included; their native application interfaces are planned.
</p>

## Features

- **Self-hosted by design:** no predefined catalog, hosted account, or mandatory third-party service
- **Secure access:** first-run claiming, device authentication, rotating refresh tokens, revocable sessions, and category-scoped authorization
- **Independent profiles:** descriptions, preset or custom avatars, PIN protection and throttling, management permissions, availability windows, inherited settings, libraries, and watch progress
- **Access categories:** place profiles and devices behind explicit administrative boundaries, promote one default category, and move assignments without leaking cross-category access
- **Flexible discovery:** profile-scoped addons, TV catalogs, and curated collections backed by addon catalogs, TMDB, Trakt, or MDBList
- **Rich metadata:** optional TMDB, TVDB, and Fanart.tv enrichment, localized season-aware trailers, title logos, original episode artwork, and visible provider identifiers
- **Account tracking:** optional per-profile Trakt and Simkl connections with encrypted provider tokens
- **Private playback resolution:** opaque, session-bound source references keep provider URLs and headers on the server
- **Adaptive playback:** capability-aware direct play, remuxing, audio conversion, H.264/AAC transcoding, and HLS delivery
- **Complete controls:** audio and subtitle selection, external subtitles, seek, speed, Picture in Picture, and resume state
- **Responsive administration:** category, profile, and device management; home curation; search; library; calendar; multilingual settings; maintenance controls; notifications; and live playback activity
- **Operational foundation:** PostgreSQL migrations, bounded media processing, automatic cleanup, diagnostics, and AMD64/ARM64 containers
- **Typed public contract:** OpenAPI at [`protocol/openapi.yaml`](protocol/openapi.yaml) and protocol compatibility rules in [`protocol/COMPATIBILITY.md`](protocol/COMPATIBILITY.md)

## Architecture

```text
Browser / Apple / Android / Windows clients
                     |
                  HTTPS API
                     |
              Rivune Go server
          /          |           \
   PostgreSQL   Addons/providers   FFmpeg / ffprobe
```

The React client is compiled into the Go server binary. Addons, metadata services, tracking providers, and MDBList are contacted by the server rather than by clients. Provider credentials, playback URLs, and private request headers remain server-side.

## Quick start with Docker Compose

Requirements:

- Docker Engine with Compose
- OpenSSL, or another secure random-value generator

Clone the repository, then create the local environment file before entering any
secrets. On Linux or macOS:

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
./scripts/create-env.sh
```

On Windows PowerShell:

```powershell
git clone https://github.com/moodiness/rivune.git
Set-Location rivune
.\scripts\create-env.ps1
```

Generate four independent credentials with `openssl rand -hex 32`, then place them in `.env`:

```dotenv
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=<generated bootstrap-only database password>
RIVUNE_DATABASE_PASSWORD=<different generated application database password>
RIVUNE_RESTORE_PASSWORD=<different generated restore-role password>
RIVUNE_SETUP_TOKEN=<different generated setup token>
```

When enabling Trakt or Simkl account tracking, generate another independent credential with `openssl rand -hex 32`. Store it directly in `RIVUNE_TRACKING_ENCRYPTION_KEY`; do not reuse any database password, setup token, or provider secret.

TMDB, TVDB, Fanart.tv, MDBList, Trakt, and Simkl integrations are optional and can remain unconfigured. `RIVUNE_TVDB_PIN` is only needed with a TVDB user-supported API key; ordinary project keys authenticate without it. Fanart.tv requires `RIVUNE_FANART_API_KEY`. Per-profile Trakt or Simkl account tracking additionally requires the independently generated encryption key above.

`RIVUNE_VERSION` in `.env` selects the published GHCR image tag. Keep `latest` to follow stable releases, or set a specific release tag to pin deployments.

Pull the selected image and start Rivune:

```sh
docker compose pull
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080). Complete first-run setup with the value of `RIVUNE_SETUP_TOKEN` from `.env`, then create the administrator account and first profile.

This plain-HTTP mode is only for local development: the default Compose file binds port `8080` to `127.0.0.1`, not to the LAN. Rivune rejects an `http://` `RIVUNE_PUBLIC_URL` whose host is not loopback.

Check server health:

```sh
curl http://localhost:8080/health
```

Stop the application without deleting PostgreSQL data:

```sh
docker compose down
```

## Configuration

Create `.env` from [`.env.example`](.env.example) with
`./scripts/create-env.sh` on Linux/macOS or `.\scripts\create-env.ps1` in
Windows PowerShell, then adjust it for the host. The helpers refuse an existing
file or link rather than overwriting it. They create a mode-`0600` file on
POSIX or a protected Windows DACL granting only the current identity access.
For an existing installation, repair the file before using it: run
`chmod 600 .env` on Linux/macOS. On Windows PowerShell, replace the DACL with
one explicit full-control rule for the current identity before entering or using
secrets:

```powershell
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = [Security.AccessControl.FileSecurity]::new()
$acl.SetOwner($sid)
$acl.SetAccessRuleProtection($true, $false)
$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
    $sid,
    [Security.AccessControl.FileSystemRights]::FullControl,
    [Security.AccessControl.AccessControlType]::Allow
))
Set-Acl -LiteralPath .env -AclObject $acl
```

| Variable | Purpose | Default |
| --- | --- | --- |
| `RIVUNE_POSTGRES_SUPERUSER_PASSWORD` | Required bootstrap-only password for the bundled PostgreSQL service; never used by Rivune or restore workflows | none |
| `RIVUNE_DATABASE_PASSWORD` | Required password for Rivune's non-superuser application role | none |
| `RIVUNE_DATABASE_SSLMODE` | Required explicit TLS policy for component database settings; the Unraid template fixes this to fail-closed `verify-full` | none |
| `RIVUNE_DATABASE_SSLROOTCERT` | Optional CA certificate path added to a component-built PostgreSQL DSN; required by the Unraid template | empty |
| `RIVUNE_RESTORE_PASSWORD` | Required independent password for the non-superuser restore role | none |
| `RIVUNE_SETUP_TOKEN` | Required one-time value used to claim a new Rivune instance | none |
| `RIVUNE_TMDB_ACCESS_TOKEN` | Optional TMDB API read access token | empty |
| `RIVUNE_TVDB_API_KEY` / `RIVUNE_TVDB_PIN` | Optional TVDB project key and user-supported-key PIN | empty |
| `RIVUNE_FANART_API_KEY` | Optional Fanart.tv project API key for posters, backdrops, logos, and season artwork | empty |
| `RIVUNE_MDBLIST_API_KEY` | Optional MDBList API key for movie and series collection sources | empty |
| `RIVUNE_TRAKT_CLIENT_ID` | Optional Trakt client ID for collection sources and account tracking | empty |
| `RIVUNE_TRAKT_CLIENT_SECRET` | Trakt client secret required with the client ID for account tracking | empty |
| `RIVUNE_SIMKL_CLIENT_ID` | Optional Simkl client ID for account tracking | empty |
| `RIVUNE_TRACKING_ENCRYPTION_KEY` | 64-character hexadecimal key encoding exactly 32 bytes, required when account tracking is enabled | empty |
| `RIVUNE_PUBLIC_URL` | Public origin used by the server; HTTPS is required except for loopback-only local HTTP | `http://localhost:8080` |
| `RIVUNE_JELLYFIN_ENABLED` | Initial default for the limited Jellyfin-compatible API; accepts only `true` or `false`. After setup, the persisted administrator setting is authoritative | `false` |
| `RIVUNE_PORT` | Loopback host port mapped to Rivune by the default Compose file | `8080` |
| `RIVUNE_LAN_ARTWORK_ORIGINS` | Optional comma-separated exact origins for trusted private-IP LAN artwork servers; explicit ports are required and paths, queries, credentials, DNS names, and special-use addresses are rejected | empty |
| `TZ` | IANA timezone used by profile access dates and daily hours | `UTC` |
| `PUID` / `PGID` | Non-root identity used inside the container | `65532` |
| `RIVUNE_FFMPEG_PATH` | FFmpeg executable name or path | `ffmpeg` |
| `RIVUNE_FFPROBE_PATH` | ffprobe executable name or path | `ffprobe` |
| `RIVUNE_HARDWARE_ACCELERATION` | Video encoder selection: `auto`, `software`, `vaapi`, `qsv`, or `nvenc` | `auto` |
| `RIVUNE_VIDEO_DEVICE` | Absolute Linux render-device path used for hardware acceleration | `/dev/dri/renderD128` |
| `RIVUNE_VIDEO_GROUP_ID` | GID that owns `/dev/dri/renderD128`; verify with `stat -c '%g' /dev/dri/renderD128` | host-dependent (`109` in Compose, usually `18` on Unraid) |
| `RIVUNE_REMUX_CONCURRENCY` | Capacity of each FFmpeg process, probe, and subtitle pool (`1`–`16`); trickplay has one independent slot | `4` |
| `RIVUNE_TRANSCODE_THREADS` | FFmpeg threads per transcode (`1`–`32`) | `4` |
| `RIVUNE_TRANSCODE_MAX_BITRATE_KBPS` | Server transcode video bitrate ceiling (`64`–`200000` kb/s) | `12000` |
| `RIVUNE_TRANSCODE_MAX_READ_RATE` | Maximum FFmpeg input burst rate (`1.0`–`4.0` × real time); automatically reduced under process-pool pressure | `1.5` |
| `RIVUNE_HLS_INITIAL_BUFFER_SECONDS` | Complete HLS media buffered before the first playlist response (`3`–`30` seconds) | `6` |
| `RIVUNE_MEDIA_TEMP_DIR` | Parent directory for the private `rivune-media` workspace | system temporary directory |
| `RIVUNE_MEDIA_MAX_STORAGE_MB` | Temporary media workspace limit (`512`–`102400` MiB) | `20480` for the binary; `4096` in Compose |

The Compose targets run Rivune with a read-only root filesystem, a bounded `/tmp` tmpfs, dropped capabilities, `no-new-privileges`, PID/CPU/memory ceilings, a non-root application process, and a native `/health` container check. `RIVUNE_MEDIA_TMPFS_SIZE_MB` and `RIVUNE_MEDIA_MAX_STORAGE_MB` both default to `4096` in Compose and should remain aligned. Container ceilings are overridable with `RIVUNE_CPU_LIMIT`, `RIVUNE_MEMORY_LIMIT`, `RIVUNE_POSTGRES_CPU_LIMIT`, `RIVUNE_POSTGRES_MEMORY_LIMIT`, and, for the Caddy target, `RIVUNE_CADDY_CPU_LIMIT` / `RIVUNE_CADDY_MEMORY_LIMIT`.

The base Compose file exposes no host GPU device. Add exactly one explicit overlay when hardware encoding is intended: `docker compose -f compose.yaml -f compose.vaapi.yaml up -d` for AMD/Intel render nodes, or `docker compose -f compose.yaml -f compose.nvidia.yaml up -d` for NVIDIA. `auto` probes only devices made visible by that overlay and falls back cleanly to software; never grant the container broad host-device access.


The Trakt and Simkl credentials identify the Rivune server application; they do not connect a global user account. Each Rivune profile links and controls its own Trakt and/or Simkl account from profile settings, and Rivune stores that profile's provider tokens encrypted with `RIVUNE_TRACKING_ENCRYPTION_KEY`.

### Jellyfin-compatible clients

Rivune can expose a limited Jellyfin-compatible API for supported third-party native client workflows. It is disabled by default and does not turn Rivune into a complete Jellyfin server; unsupported Jellyfin features remain unavailable. For non-loopback installations, clients must use Rivune's HTTPS reverse-proxy origin rather than exposing the raw application port. See [Jellyfin-compatible client access](docs/operations.md#jellyfin-compatible-client-access) for activation, restart, login, and rollback steps.

### Provider credentials

Set optional provider credentials and the tracking encryption key directly in the private `.env` file. Leave credentials for unused providers empty.

For every non-loopback installation, terminate HTTPS at a trusted reverse proxy, set `RIVUNE_PUBLIC_URL` to the HTTPS origin, and configure `RIVUNE_TRUSTED_PROXIES`. Use [`deploy/caddy/compose.yaml`](deploy/caddy/compose.yaml) for the supported HTTPS deployment; do not change the direct Compose binding to expose raw port `8080` on the LAN. The `.env` file contains credentials, must have the private permissions described above, and must never be committed.

Production HTTPS, upgrades, PostgreSQL backup/restore, disaster-recovery verification, and executable migration/proxy checks are documented in [Production operations](docs/operations.md). Maintainers should follow the [Semantic Versioning and release procedure](docs/releasing.md); pushing a `v*` tag runs the complete release gate before any container or GitHub Release is published.

## Unraid

Use the template at [`templates/unraid/rivune.xml`](templates/unraid/rivune.xml), or add this template URL to Unraid:

```text
https://raw.githubusercontent.com/moodiness/rivune/main/templates/unraid/rivune.xml
```

The Unraid template exposes the PostgreSQL password, initial setup token, TMDB, TVDB, Fanart.tv, MDBList, Trakt, and Simkl credentials, plus the tracking encryption key, as masked environment variables. It requires the public PostgreSQL CA certificate as a read-only file mount and fixes the component DSN to `sslmode=verify-full`; the PostgreSQL certificate SAN must exactly match the configured database hostname or IP address.

Create separate custom Docker networks for the edge and database tiers. Connect Rivune to both, the HTTPS reverse proxy only to edge, and PostgreSQL only to database; never put the proxy and PostgreSQL together on one shared network. The template intentionally publishes no raw HTTP host port: enter the proxy's HTTPS origin as `RIVUNE_PUBLIC_URL`, trust only the narrow edge network in `RIVUNE_TRUSTED_PROXIES`, and follow [Unraid PostgreSQL TLS](docs/operations.md#unraid-postgresql-tls) before first start or migration. A missing, expired, untrusted, or host-mismatched certificate must prevent Rivune from connecting rather than trigger a plaintext fallback.

## Development

Backend requirements: Go 1.26 and PostgreSQL 18. Frontend requirements: Node.js 22 and npm.
Typed native client requirements are Swift 5.9 or newer for Apple, JDK 17 plus Android SDK 35 for Kotlin/Android, and .NET 10 for Windows. The packages live under [`clients/`](clients/).

Build the web client into the server embed directory:

```sh
cd web
npm ci
npm run build
```

Run backend tests:

```sh
cd server
go test ./...
```

Build the complete container locally:

```sh
docker build --build-arg VERSION=dev -t rivune:dev -f server/Dockerfile .
```

## License

Licensed under the [Apache License 2.0](LICENSE). Third-party notices are listed in [`NOTICE`](NOTICE).
