<p align="center">
  <img src="templates/unraid/rivune-icon.svg" alt="Rivune" width="120" height="120">
</p>

<h1 align="center">Rivune</h1>

<p align="center"><strong>Your self-hosted media universe, on every screen.</strong></p>

Rivune is an open-source media backend and responsive web app with no predefined catalogue or hosted account. It keeps authentication, profiles, collections, playback state, provider credentials, source URLs, and private request headers on the server selected by the operator.

## Install with Docker Compose

Requirements: Docker Engine with Compose and a secure random-value generator such as OpenSSL.

```sh
git clone https://github.com/moodiness/rivune.git
cd rivune
./scripts/create-env.sh
```

On Windows PowerShell, run `.\scripts\create-env.ps1` instead. The helpers create a private `.env` without overwriting an existing path.

Generate independent values for the three database passwords, setup token, and encryption key, then fill these entries in `.env`:

```dotenv
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=<output of: openssl rand -hex 32>
RIVUNE_DATABASE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_RESTORE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_SETUP_TOKEN=<different output of: openssl rand -hex 32>
RIVUNE_ENCRYPTION_KEYS=1:<different output of: openssl rand -hex 32>
RIVUNE_PUBLIC_URL=https://media.example.com
RIVUNE_VERSION=latest
```

`RIVUNE_ENCRYPTION_KEYS` is strict: entries are active-first `version:64-lowercase-hex` pairs with unique positive versions and unique, non-zero keys. Back up the keyring separately and securely. A database backup cannot recover encrypted integration credentials or profile tracking tokens without every matching key version.

For a local, loopback-only installation:

```sh
docker compose pull
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080). For a normal HTTPS installation, keep using `compose.yaml`, set `RIVUNE_PUBLIC_URL` to the public HTTPS origin, and put Rivune behind Pangolin/Newt or an operator-managed reverse proxy. The proxy must terminate TLS and target Rivune over HTTP on port `8080`; then open `RIVUNE_PUBLIC_URL`. Enter `RIVUNE_SETUP_TOKEN` to claim the instance and create the first administrator.

## First-run configuration

A new deployment needs no provider or media-tuning environment variables. In **Administration → Settings**:

1. choose the timezone, Jellyfin compatibility, transcoding policy, storage quotas, bitrate ceiling, and hardware-acceleration mode;
2. add only the provider integrations you use;
3. verify the requested and active settings shown by the application.

Integration responses expose configured status and update time only; Rivune never returns provider secret values. Provider changes are applied live. Hardware acceleration is restart-required: saving it persists the requested value, but the previous active value remains in force and `pending restart` remains visible until the service restarts and reconciles the request. Do not treat the requested mode as active before then.

## Host deployment choices

The provided Compose manifest is CPU-only by default and needs no GPU device. For optional AMD/Intel acceleration, set `RIVUNE_VIDEO_DEVICE` and `RIVUNE_VIDEO_GROUP_ID` in `.env`, then add the single supported overlay:

```sh
docker compose -f compose.yaml -f compose.amd-intel.yaml up -d
```

The base Compose file is a complete PostgreSQL 18 stack for Pangolin/Newt or another operator-managed proxy. It creates a database-only internal network and the dedicated `rivune-edge` network. Attach the proxy only to `rivune-edge` and target hostname `rivune`, port `8080`, over HTTP. For Pangolin, configure Newt on that network with those target values. The loopback-only host port is for local access and is not used by a container proxy.

For Unraid's XML template with an existing PostgreSQL server, TLS, a host port, and AMD/Intel GPU access are all opt-in. Prefer the same dedicated edge network and `Rivune:8080` target. If Newt cannot share that network, manually add an Unraid TCP Port mapping from container port `8080` to an unused host port such as `18080`; never forward it on the router.

See [Production operations](docs/operations.md) for the complete Pangolin network configuration, optional PostgreSQL TLS, upgrades from legacy environment configuration, encryption-key rotation, GPU activation, and authenticated database backup, restore, and rollback.

## Development

Backend requirements are Go 1.26 and PostgreSQL 18. Frontend requirements are Node.js 22 and npm. Typed clients live under [`clients/`](clients/); the public contract is [`protocol/openapi.yaml`](protocol/openapi.yaml).

```sh
cd web && npm ci && npm run build
cd ../server && go test ./...
```

## License

Licensed under the [Apache License 2.0](LICENSE). Third-party notices are listed in [`NOTICE`](NOTICE).
