<p align="center">
  <img src="templates/unraid/rivune-icon.svg" alt="Rivune" width="120" height="120">
</p>

<h1 align="center">Rivune</h1>

<p align="center"><strong>Your self-hosted media universe, on every screen.</strong></p>

Rivune is an open-source media backend and responsive web app with no predefined catalogue or hosted account. It keeps authentication, profiles, collections, playback state, provider credentials, source URLs, and private request headers on the server selected by the operator.

## Install with Docker Compose

Requirements: Docker Engine with Compose, a secure random-value generator such as OpenSSL, and an AMD/Intel render node exposed by the Linux host.

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
./scripts/create-env.sh
```

On Windows PowerShell, run `.\scripts\create-env.ps1` instead. The helpers create a private `.env` without overwriting an existing path.

Generate independent values for the three database passwords, setup token, and encryption key. Set the host render node and its numeric group, then fill these entries in `.env`:

```dotenv
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=<output of: openssl rand -hex 32>
RIVUNE_DATABASE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_RESTORE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_SETUP_TOKEN=<different output of: openssl rand -hex 32>
RIVUNE_ENCRYPTION_KEYS=1:<different output of: openssl rand -hex 32>
RIVUNE_HOST=media.example.com
RIVUNE_VERSION=latest
RIVUNE_VIDEO_DEVICE=/dev/dri/renderD128
RIVUNE_VIDEO_GROUP_ID=<output of: stat -c '%g' /dev/dri/renderD128>
```

`RIVUNE_ENCRYPTION_KEYS` is strict: entries are active-first `version:64-lowercase-hex` pairs with unique positive versions and unique, non-zero keys. Back up the keyring separately and securely. A database backup cannot recover encrypted integration credentials or profile tracking tokens without every matching key version.

For a local, loopback-only installation:

```sh
docker compose pull
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080). For a normal HTTPS installation, point `RIVUNE_HOST` at this host and use the supported Caddy deployment:

```sh
docker compose --env-file .env -f deploy/caddy/compose.yaml pull
docker compose --env-file .env -f deploy/caddy/compose.yaml up -d
```

Then open `https://<RIVUNE_HOST>`. Enter `RIVUNE_SETUP_TOKEN` to claim the instance and create the first administrator.

## First-run configuration

A new deployment needs no provider or media-tuning environment variables. In **Administration → Settings**:

1. choose the timezone, Jellyfin compatibility, transcoding policy, storage quotas, bitrate ceiling, and hardware-acceleration mode;
2. add only the provider integrations you use;
3. verify the requested and active settings shown by the application.

Integration responses expose configured status and update time only; Rivune never returns provider secret values. Provider changes are applied live. Hardware acceleration is restart-required: saving it persists the requested value, but the previous active value remains in force and `pending restart` remains visible until the service restarts and reconciles the request. Do not treat the requested mode as active before then.

## Host deployment choices

Both provided Compose manifests map the host render node selected by
`RIVUNE_VIDEO_DEVICE` to the fixed `/dev/dri/renderD128` path inside Rivune and
grant the application `RIVUNE_VIDEO_GROUP_ID`. Set the numeric group from
`stat -c '%g' /dev/dri/renderD128` before starting Compose. These values describe
deployment topology; the requested encoder mode remains configurable in Rivune.
NVIDIA encoding still requires NVIDIA Container Toolkit configuration in a
private Compose override.

For Unraid, use [`templates/unraid/rivune.xml`](templates/unraid/rivune.xml). The template contains bootstrap, database, TLS, reverse-proxy, identity, storage, and GPU host controls only. Configure product settings and provider credentials in Rivune after first run.

See [Production operations](docs/operations.md) for HTTPS topology, upgrades from legacy environment configuration, encryption-key rotation, GPU activation, Unraid PostgreSQL TLS, and authenticated database backup, restore, and rollback.

## Development

Backend requirements are Go 1.26 and PostgreSQL 18. Frontend requirements are Node.js 22 and npm. Typed clients live under [`clients/`](clients/); the public contract is [`protocol/openapi.yaml`](protocol/openapi.yaml).

```sh
cd web && npm ci && npm run build
cd ../server && go test ./...
```

## License

Licensed under the [Apache License 2.0](LICENSE). Third-party notices are listed in [`NOTICE`](NOTICE).
