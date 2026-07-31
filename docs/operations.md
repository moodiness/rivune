# Production operations

The supported container deployment uses PostgreSQL 18 and puts Rivune behind Caddy. Run the commands in this document from the repository root. The supplied shell scripts explicitly target Linux with Bash, Docker Engine, Docker Compose v2, and `set -euo pipefail`; CI runs the same scripts on Linux.

## HTTPS with Caddy

Create `.env` with independent random database and setup secrets, a public DNS name, and a pinned Rivune version:

```dotenv
RIVUNE_HOST=media.example.com
RIVUNE_VERSION=1.4.2
RIVUNE_DATABASE_PASSWORD=<output of: openssl rand -hex 32>
RIVUNE_SETUP_TOKEN=<different output of: openssl rand -hex 32>
TZ=UTC
```

Point the `A`/`AAAA` records for `RIVUNE_HOST` at the host, allow inbound TCP 80 and TCP/UDP 443, then start the supported configuration:

```sh
docker compose --env-file .env -f deploy/caddy/compose.yaml pull
docker compose --env-file .env -f deploy/caddy/compose.yaml up -d
docker compose --env-file .env -f deploy/caddy/compose.yaml ps
curl --fail --show-error https://media.example.com/health
```

Caddy obtains and renews a publicly trusted certificate automatically. [`deploy/caddy/Caddyfile`](../deploy/caddy/Caddyfile) replaces inbound `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto`, and `X-Forwarded-Host`; clients therefore cannot inject a trusted chain. The Compose network has the fixed `172.30.0.0/24` edge subnet, and Rivune trusts only that subnet through `RIVUNE_TRUSTED_PROXIES`. Do not change it to `0.0.0.0/0`, a host LAN, or another client-reachable range. If the edge subnet must change, update both the network subnet and `RIVUNE_TRUSTED_PROXIES` to the same dedicated range.

Rivune is not published directly to the host in this configuration. PostgreSQL is isolated on the private database network. Caddy is the only externally reachable service.

To update to a stable release, change `RIVUNE_VERSION` to an exact released version, back up first, and recreate only the application:

```sh
COMPOSE_FILE=deploy/caddy/compose.yaml ./scripts/postgres-backup.sh backups/rivune-before-1.5.0.dump
./scripts/postgres-verify-backup.sh backups/rivune-before-1.5.0.dump
# edit RIVUNE_VERSION=1.5.0 in .env
docker compose --env-file .env -f deploy/caddy/compose.yaml pull rivune
docker compose --env-file .env -f deploy/caddy/compose.yaml up -d rivune
curl --fail --show-error https://media.example.com/health
```

## PostgreSQL backup

`postgres-backup.sh` takes a transactionally consistent custom-format dump while Rivune remains online. It writes through a private temporary file, validates the archive table of contents, refuses to overwrite an existing backup, and atomically renames a successful dump.

```sh
mkdir -p backups
COMPOSE_FILE=deploy/caddy/compose.yaml ./scripts/postgres-backup.sh "backups/rivune-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

Store backups encrypted on a different machine or object store. The database password, setup state, accounts, sessions, profiles, collections, and watch state are sensitive. A backup that has not been restored is not verified.

Verify every backup by restoring it into a disposable PostgreSQL 18 container and checking the migration ledger:

```sh
./scripts/postgres-verify-backup.sh backups/rivune-20260731T120000Z.dump
sha256sum backups/rivune-20260731T120000Z.dump > backups/rivune-20260731T120000Z.dump.sha256
```

The verification container and its data are removed whether verification succeeds or fails. Retain the checksum with the backup and verify it before a real restore:

```sh
sha256sum --check backups/rivune-20260731T120000Z.dump.sha256
```

## PostgreSQL restore

A restore is destructive: it replaces the current `rivune` database. Take a backup of the current database first. Run the restore against the root `compose.yaml`; for the Caddy deployment, pass its Compose file through `COMPOSE_FILE`:

```sh
export COMPOSE_FILE=deploy/caddy/compose.yaml
export RIVUNE_SERVICE=rivune
./scripts/postgres-backup.sh backups/rivune-before-restore.dump
./scripts/postgres-verify-backup.sh backups/rivune-before-restore.dump
./scripts/postgres-restore.sh backups/rivune-20260731T120000Z.dump
unset COMPOSE_FILE RIVUNE_SERVICE
```

The restore script validates the archive before stopping Rivune, terminates database connections, recreates the database, restores with `--exit-on-error`, verifies that the migration ledger is populated, and restarts Rivune if it was running before the restore. If any restore step fails, its error is returned and a previously running Rivune service is started again; inspect both services before accepting traffic:

```sh
export COMPOSE_FILE=deploy/caddy/compose.yaml
export RIVUNE_SERVICE=rivune
docker compose ps
docker compose logs --tail=100 postgres rivune
curl --fail --show-error https://media.example.com/health
docker compose exec -T postgres psql --username rivune --dbname rivune \
  --tuples-only --no-align \
  --command 'SELECT count(*), max(version) FROM schema_migrations;'
unset COMPOSE_FILE RIVUNE_SERVICE
```

Keep the original dump until application login, profiles, collections, and playback history have been checked.

## Migration and proxy validation

CI builds the current image and runs both scripts below. They create uniquely named disposable Docker networks, containers, and volumes and clean them on every exit.

```sh
docker build --build-arg VERSION=ci-local -t rivune-ci:current -f server/Dockerfile .
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/migrations.sh
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/reverse-proxy-smoke.sh
```

The migration check performs a clean install, checks the migration count and current version, restarts to prove idempotency, constructs the immediately previous schema, upgrades it with the current image, and checks idempotency again. The proxy smoke test serves a real Rivune discovery response over Caddy's locally trusted TLS, then verifies that the same supported Caddy configuration overwrites spoofed forwarding headers and forwards HTTPS scheme and host values.
