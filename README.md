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
  <a href="https://github.com/moodiness/rivune/actions/workflows/container.yml"><img alt="Release build" src="https://img.shields.io/github/actions/workflow/status/moodiness/rivune/container.yml?style=flat-square&label=release"></a>
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
  Rivune is under active development. The web client and backend are usable today. Typed protocol-v17 clients for Apple, Android, and Windows are included; their native application interfaces are planned.
</p>

## Features

- **Self-hosted by design:** no predefined catalog, hosted account, or mandatory third-party service
- **Secure access:** first-run claiming, device authentication, rotating refresh tokens, and revocable sessions
- **Independent profiles:** PIN protection and throttling, management permissions, availability windows, settings, libraries, and watch progress
- **Flexible discovery:** profile-scoped addons and curated collections backed by addon catalogs, TMDB, Trakt, or MDBList
- **Rich metadata:** optional TMDB, TVDB, and Fanart.tv enrichment, localized season-aware trailers, title logos, and visible provider identifiers
- **Account tracking:** optional per-profile Trakt and Simkl connections with encrypted provider tokens
- **Private playback resolution:** opaque, session-bound source references keep provider URLs and headers on the server
- **Adaptive playback:** direct play, remuxing, audio conversion, H.264/AAC transcoding, and HLS delivery
- **Complete controls:** audio and subtitle selection, external subtitles, seek, speed, Picture in Picture, and resume state
- **Responsive administration:** home curation, search, library, calendar, multilingual settings, maintenance controls, notifications, and live playback activity
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

Clone the repository and create the local environment file:

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
cp .env.example .env
```

Generate two independent credentials with `openssl rand -hex 32`, then place them in `.env`:

```dotenv
RIVUNE_DATABASE_PASSWORD=<generated database password>
RIVUNE_SETUP_TOKEN=<generated setup token>
```

When enabling Trakt or Simkl account tracking, generate a third independent credential with `openssl rand -base64 32` and assign its output to `RIVUNE_TRACKING_ENCRYPTION_KEY`. Do not reuse the database password, setup token, or a provider secret.

TMDB, TVDB, Fanart.tv, MDBList, Trakt, and Simkl integrations are optional and can remain empty in `.env`. `RIVUNE_TVDB_PIN` is only needed with a TVDB user-supported API key; ordinary project keys authenticate without it. Fanart.tv requires a project API key and supports an optional personal client key. Per-profile Trakt or Simkl account tracking additionally requires the independently generated encryption key above.

Start Rivune:

```sh
docker compose up -d --build
```

Open [http://localhost:8080](http://localhost:8080). Complete first-run setup with the value of `RIVUNE_SETUP_TOKEN` from `.env`, then create the administrator account and first profile.

Check server health:

```sh
curl http://localhost:8080/health
```

Stop the application without deleting PostgreSQL data:

```sh
docker compose down
```

## Configuration

Copy [`.env.example`](.env.example) to `.env` and adjust it for the host. Important settings include:

| Variable | Purpose | Default |
| --- | --- | --- |
| `RIVUNE_DATABASE_PASSWORD` | Required password shared by Rivune and the bundled PostgreSQL service | none |
| `RIVUNE_SETUP_TOKEN` | Required one-time value used to claim a new Rivune instance | none |
| `RIVUNE_TMDB_ACCESS_TOKEN` | Optional TMDB API read access token | empty |
| `RIVUNE_TVDB_API_KEY` / `RIVUNE_TVDB_PIN` | Optional TVDB project key and user-supported-key PIN | empty |
| `RIVUNE_FANART_API_KEY` / `RIVUNE_FANART_CLIENT_KEY` | Optional Fanart.tv project key and personal client key for posters, backdrops, logos, and season artwork | empty |
| `RIVUNE_MDBLIST_API_KEY` | Optional MDBList API key for movie and series collection sources | empty |
| `RIVUNE_TRAKT_CLIENT_ID` | Optional Trakt client ID for collection sources and account tracking | empty |
| `RIVUNE_TRAKT_CLIENT_SECRET` | Trakt client secret required with the client ID for account tracking | empty |
| `RIVUNE_SIMKL_CLIENT_ID` | Optional Simkl client ID for account tracking | empty |
| `RIVUNE_TRACKING_ENCRYPTION_KEY` | Base64-encoded 32-byte key required when account tracking is enabled | empty |
| `RIVUNE_PUBLIC_URL` | Public origin used by the server | `http://localhost:8080` |
| `RIVUNE_PORT` | Host port mapped to Rivune | `8080` |
| `TZ` | IANA timezone used by profile access dates and daily hours | `UTC` |
| `PUID` / `PGID` | Non-root identity used inside the container | `65532` |
| `RIVUNE_HARDWARE_ACCELERATION` | Video encoder selection: `auto`, a supported encoder, or software | `auto` |
| `RIVUNE_VIDEO_DEVICE` | Linux render device used for hardware acceleration | `/dev/dri/renderD128` |
| `RIVUNE_VIDEO_GROUP_ID` | GID that owns `/dev/dri/renderD128`; verify with `stat -c '%g' /dev/dri/renderD128` | host-dependent (`109` in Compose, usually `18` on Unraid) |
| `RIVUNE_REMUX_CONCURRENCY` | Concurrent remux jobs | `2` |
| `RIVUNE_TRANSCODE_THREADS` | FFmpeg threads per transcode | `4` |
| `RIVUNE_MEDIA_MAX_STORAGE_MB` | Temporary media workspace limit | `20480` |

The Trakt and Simkl environment variables identify the Rivune server application; they do not connect a global user account. Each Rivune profile links and controls its own Trakt and/or Simkl account from profile settings, and Rivune stores that profile's provider tokens encrypted with `RIVUNE_TRACKING_ENCRYPTION_KEY`.

For internet-facing installations, terminate HTTPS at a trusted reverse proxy, set `RIVUNE_PUBLIC_URL` to the HTTPS origin, and configure `RIVUNE_TRUSTED_PROXIES`. The `.env` file contains credentials and must never be committed.

Production HTTPS, upgrades, PostgreSQL backup/restore, disaster-recovery verification, and executable migration/proxy checks are documented in [Production operations](docs/operations.md). Maintainers should follow the [Semantic Versioning and release procedure](docs/releasing.md); pushing a `v*` tag runs the complete release gate before any container or GitHub Release is published.

## Unraid

Use the template at [`templates/unraid/rivune.xml`](templates/unraid/rivune.xml), or add this template URL to Unraid:

```text
https://raw.githubusercontent.com/moodiness/rivune/main/templates/unraid/rivune.xml
```

The Unraid template exposes the PostgreSQL password, initial setup token, TMDB, TVDB, Fanart.tv, MDBList, Trakt, and Simkl credentials, plus the tracking encryption key, as masked environment variables. No secret files or `/run/secrets` mounts are required. A reachable PostgreSQL server remains required; when it runs in another container, place both containers on the same custom Docker network.

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

Build the complete container:

```sh
docker compose build server
```

## License

Licensed under the [Apache License 2.0](LICENSE). Third-party notices are listed in [`NOTICE`](NOTICE).
