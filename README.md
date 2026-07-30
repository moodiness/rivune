# Rivune

Rivune is a self-hosted media backend and responsive web client. It ships with no catalog, provider, or hosted service: users connect to a server they choose, and that server owns authentication, profiles, collections, playback state, and source resolution.

Rivune is under active development. The web client and backend are usable today; native Apple and Android clients are planned.

## What works

- Secure first-run setup, device authentication, refresh tokens, and revocable sessions
- Profiles with PIN protection, management permissions, independent settings, libraries, and watch progress
- Per-profile Stremio-compatible addons and configurable collections
- Optional TMDB, TVDB, and Trakt metadata integrations
- Opaque, session-bound playback source references that keep provider URLs and private headers on the server
- Direct playback, remuxing, audio conversion, full H.264/AAC transcoding, and HLS delivery
- Audio and subtitle selection, external subtitles, seek, playback speed, Picture in Picture, and resume state
- Responsive home, search, library, settings, collection editor, and playback activity administration
- PostgreSQL migrations, bounded media processing, automatic session cleanup, and media diagnostics
- OpenAPI contract at [`protocol/openapi.yaml`](protocol/openapi.yaml)

## Architecture

```text
Browser / future native clients
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

Clone the repository, then create the required secret files:

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
mkdir -p secrets
openssl rand -base64 32 > secrets/postgres_password.txt
openssl rand -base64 32 > secrets/setup_token.txt
: > secrets/tmdb_access_token.txt
: > secrets/tvdb_api_key.txt
: > secrets/tvdb_pin.txt
: > secrets/trakt_client_id.txt
cp .env.example .env
```

The metadata credentials are optional, but the empty files must exist because Compose mounts them as secrets. Add provider credentials before starting if you want those integrations.

Start Rivune:

```sh
docker compose up -d --build
```

Open [http://localhost:8080](http://localhost:8080). Complete first-run setup with the value from `secrets/setup_token.txt`, then create the administrator account and first profile.

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
| `RIVUNE_PUBLIC_URL` | Public origin used by the server | `http://localhost:8080` |
| `RIVUNE_PORT` | Host port mapped to Rivune | `8080` |
| `PUID` / `PGID` | Non-root identity used inside the container | `65532` |
| `RIVUNE_HARDWARE_ACCELERATION` | Video encoder selection: `auto`, a supported encoder, or software | `auto` |
| `RIVUNE_VIDEO_DEVICE` | Linux render device used for hardware acceleration | `/dev/dri/renderD128` |
| `RIVUNE_VIDEO_GROUP_ID` | Host video/render group exposed to the process | `109` |
| `RIVUNE_REMUX_CONCURRENCY` | Concurrent remux jobs | `2` |
| `RIVUNE_TRANSCODE_THREADS` | FFmpeg threads per transcode | `4` |
| `RIVUNE_MEDIA_MAX_STORAGE_MB` | Temporary media workspace limit | `20480` |

For internet-facing installations, terminate HTTPS at a trusted reverse proxy, set `RIVUNE_PUBLIC_URL` to the HTTPS origin, and configure `RIVUNE_TRUSTED_PROXIES`. Never commit files from `secrets/`.

## Development

Backend requirements: Go 1.26 and PostgreSQL 18. Frontend requirements: Node.js 22 and npm.

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
