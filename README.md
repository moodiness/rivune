# Rivune

Rivune is a self-hosted media backend and responsive web client. It ships with no catalog, provider, or hosted service: users connect to a server they choose, and that server owns authentication, profiles, collections, playback state, and source resolution.

Rivune is under active development. The web client and backend are usable today. Typed protocol-v16 clients for Apple, Android, and Windows are included; their native application interfaces are planned.

## What works

- Secure first-run setup, device authentication, refresh tokens, and revocable sessions
- Profiles with PIN protection and throttling, management permissions, disable/date/daily access windows, independent settings, libraries, and watch progress
- Per-profile media source addons with in-place transport and profile-assignment editing, plus configurable collections
- Optional TMDB, TVDB, and Trakt metadata integrations, localized season-aware trailers, and visible provider identifiers
- Opaque, session-bound playback source references that keep provider URLs and private headers on the server
- Direct playback, remuxing, audio conversion, full H.264/AAC transcoding, and HLS delivery
- Audio and subtitle selection, external subtitles, seek, playback speed, Picture in Picture, and resume state
- Responsive home, search, library, settings, collection editor, and playback activity administration
- PostgreSQL migrations, bounded media processing, automatic session cleanup, and media diagnostics
- OpenAPI contract at [`protocol/openapi.yaml`](protocol/openapi.yaml)

## Architecture

```text
Browser / Apple / Android / Windows clients
              |
           HTTPS API
              |
       Rivune Go server
       |      |       |
  PostgreSQL  Addons  FFmpeg / ffprobe
```

The React client is compiled into the Go server binary. Addons and metadata providers are contacted by the server, not directly by clients. Playback URLs returned by providers remain server-side.

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

TMDB, TVDB, and Trakt credentials are optional and can remain empty in `.env`. `RIVUNE_TVDB_PIN` is only needed with a TVDB user-supported API key; ordinary project keys authenticate without it.

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
| `RIVUNE_VIDEO_GROUP_ID` | Host video/render group exposed to the process | `109` |
| `RIVUNE_REMUX_CONCURRENCY` | Concurrent remux jobs | `2` |
| `RIVUNE_TRANSCODE_THREADS` | FFmpeg threads per transcode | `4` |
| `RIVUNE_MEDIA_MAX_STORAGE_MB` | Temporary media workspace limit | `20480` |

The Trakt and Simkl environment variables identify the Rivune server application; they do not connect a global user account. Each Rivune profile links and controls its own Trakt and/or Simkl account from profile settings, and Rivune stores that profile's provider tokens encrypted with `RIVUNE_TRACKING_ENCRYPTION_KEY`.

For internet-facing installations, terminate HTTPS at a trusted reverse proxy, set `RIVUNE_PUBLIC_URL` to the HTTPS origin, and configure `RIVUNE_TRUSTED_PROXIES`. The `.env` file contains credentials and must never be committed.

Production HTTPS, upgrades, PostgreSQL backup/restore, disaster-recovery verification, and executable migration/proxy checks are documented in [Production operations](docs/operations.md). Maintainers should follow the [Semantic Versioning and release procedure](docs/releasing.md); container publication is gated by the same CI checks used for pull requests.


## Unraid

Use the template at [`templates/unraid/rivune.xml`](templates/unraid/rivune.xml), or add this template URL to Unraid:

```text
https://raw.githubusercontent.com/moodiness/rivune/main/templates/unraid/rivune.xml
```

The Unraid template exposes the PostgreSQL password, initial setup token, TMDB token, TVDB credentials, and Trakt client ID as masked environment variables. No secret files or `/run/secrets` mounts are required. A reachable PostgreSQL server remains required; when it runs in another container, place both containers on the same custom Docker network.

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
