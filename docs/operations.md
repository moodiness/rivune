# Operations

The supported deployment is [`compose.yaml`](../compose.yaml) with PostgreSQL 18. Run commands from the repository root. Linux and macOS use `./rivune`; Windows uses `./rivune.ps1` after creating `.env` with `./scripts/create-env.ps1`.

## Install and diagnose

```sh
./rivune setup --public-url https://media.example.com --version 1.12.3
./rivune up
./rivune status
./rivune logs rivune
./rivune doctor
```

`setup` creates a private `.env`, generates independent database passwords, setup token, and encryption key, and refuses to overwrite an existing path. Omit `--public-url` for loopback-only use. `doctor` checks configuration, Compose, PostgreSQL, containers, and `/ready` without printing secrets.

Claim a new instance with `RIVUNE_SETUP_TOKEN`. Configure providers, quotas, transcoding, and Jellyfin compatibility in **Administration → Settings**, not `.env`. Provider changes apply live. Hardware acceleration, preferred codec, quality, and concurrency require a restart; confirm that `pending restart` clears before treating the requested value as active.

## Network and HTTPS

For public access, terminate TLS at Pangolin/Newt or another reverse proxy. Join the proxy to `rivune-edge` and target `http://rivune:8080`. The proxy must replace forwarded client, host, and scheme headers. Trust only its exact address or dedicated edge CIDR; never `0.0.0.0/0`, the LAN, or the database network.

- `/live`: process liveness.
- `/ready`: application and PostgreSQL readiness.
- `/health`: protocol-compatible readiness response.

For trusted-LAN-only HTTP, set `RIVUNE_BIND_ADDRESS=0.0.0.0` and use a literal private-IP `RIVUNE_PUBLIC_URL`, such as `http://192.168.1.20:8080`. Restrict the host firewall and never forward this port from the router.

`./rivune setup --public-url ...` also configures `_rivune._tcp` discovery. The wrapper manages the Linux sidecar, macOS LaunchAgent, or Windows scheduled publisher. Leave `RIVUNE_DISCOVERY_URL` empty to disable discovery. Raw Compose users on Linux must enable it explicitly:

```sh
docker compose --profile discovery up -d
```

Unraid should use the same dedicated edge network and an existing PostgreSQL 18 container. Prefer hostname `Rivune`, port `8080`, and HTTP from Newt. A host-port fallback must stay on the trusted LAN and must never be router-forwarded.

## Secrets and upgrades

`RIVUNE_ENCRYPTION_KEYS` is an active-first list of `version:64-lowercase-hex` values. The database and complete matching keyring are one recovery set. To rotate, prepend a new version, restart, re-save provider credentials, reconnect tracking accounts, and retain old keys until no live row or retained backup needs them.

Before upgrading, create and verify an authenticated backup, record its printed backup ID outside the backup repository, set an exact released `RIVUNE_VERSION`, then recreate only Rivune:

```sh
target_version=1.12.3
backup="backups/rivune-before-${target_version}.dump"
COMPOSE_FILE=compose.yaml ./scripts/postgres-backup.sh "${backup}"
./scripts/postgres-verify-backup.sh --expect-backup-id '<recorded ID>' "${backup}"
# set RIVUNE_VERSION=${target_version} in .env
docker compose --env-file .env -f compose.yaml pull rivune
docker compose --env-file .env -f compose.yaml up -d rivune
curl --fail --show-error https://media.example.com/ready
```

Never regenerate an encryption key during an upgrade. Legacy deployments whose `rivune` role is still a PostgreSQL superuser must migrate roles before upgrading; use `scripts/postgres-init-rivune.sh` and verify that both `rivune` and `rivune_restore` have `rolsuper = false`.

## Playback and GPU

The base Compose stack is CPU-only. For AMD/Intel, set `RIVUNE_VIDEO_DEVICE` and `RIVUNE_VIDEO_GROUP_ID`, then use the supported overlay:

```sh
stat -c '%g' /dev/dri/renderD128
docker compose -f compose.yaml -f compose.amd-intel.yaml up -d
```

Expose only the render node and its group. NVIDIA requires a private override with NVIDIA Container Toolkit. Rivune functionally probes encoders and filters at startup; a visible device or listed FFmpeg encoder is not proof. Confirm the active backend and sustained speed in Administration Activity. Lower maximum resolution or use software when transcoding cannot stay above `1.00x`.

Media work uses `/var/lib/rivune/media`; artwork uses `/var/lib/rivune/artwork`. Keep both writable and local. Do not force a container user or mount the entire host `/dev` tree.

## Jellyfin-compatible access

The optional Jellyfin adapter is limited and disabled by default. Enable it in Administration, then issue a profile-specific application credential from **Preferences → Connections**. The username is the profile UUID; Rivune account passwords and profile PINs are never accepted.

Supported areas include discovery, authentication/Quick Connect, library browsing, artwork, playback planning and delivery, subtitles, progress, favorites, resume, and next-up. This is not a complete Jellyfin server; Jellyfin Media Player/Desktop is incompatible because it expects Jellyfin Web at `/`. Current evidence and known gaps are in [Jellyfin compatibility](jellyfin-compatibility.md).

## Backup and restore

Backups require an RSA signing key, public verification key, stable deployment lineage, and protected generation-state file outside the backup directory:

```sh
key_dir="${XDG_CONFIG_HOME:-$HOME/.config}/rivune/backup-auth"
install -d -m 700 "${key_dir}"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out "${key_dir}/signing.pem"
openssl pkey -in "${key_dir}/signing.pem" -pubout -out "${key_dir}/verification.pem"
chmod 600 "${key_dir}"/*.pem
export RIVUNE_BACKUP_SIGNING_KEY_FILE="${key_dir}/signing.pem"
export RIVUNE_BACKUP_VERIFY_KEY_FILE="${key_dir}/verification.pem"
export RIVUNE_BACKUP_STATE_FILE="${key_dir}/generation.state"
export RIVUNE_BACKUP_LINEAGE="prod-$(openssl rand -hex 16)"
```

Create and verify a backup:

```sh
backup="backups/rivune-$(date -u +%Y%m%dT%H%M%SZ).dump"
COMPOSE_FILE=compose.yaml ./scripts/postgres-backup.sh "${backup}"
./scripts/postgres-verify-backup.sh --expect-backup-id '<recorded ID>' "${backup}"
```

Store the archive, manifest, and signature off-host. Store the printed ID, keyring, verification key, lineage, and state separately. The scheduler performs a backup plus disposable restore verification before pruning:

```sh
./rivune backup-scheduler --once /srv/rivune-backups
```

Before restoration, back up the current database and restore every encryption-key version required by the selected archive. Normal restore requires the externally recorded current backup ID:

```sh
export RIVUNE_RESTORE_PASSWORD='<protected value>'
./scripts/postgres-restore.sh \
  --expect-backup-id '<externally recorded ID>' \
  backups/rivune-selected.dump
unset RIVUNE_RESTORE_PASSWORD
```

Use `--initialize-state` only for disaster recovery when protected state is genuinely unavailable. Use `--allow-rollback` only for an explicitly selected older backup. The restore stages and validates a separate database before swapping names and restores the prior live database if activation fails.

## Optional PostgreSQL TLS

Plain PostgreSQL is acceptable only on the isolated database network. Across any shared or untrusted network, use `RIVUNE_DATABASE_SSLMODE=verify-full`, mount only the trusted CA into Rivune, and issue a PostgreSQL server certificate whose SAN exactly matches `RIVUNE_DATABASE_HOST`. Keep the CA private key offline; PostgreSQL receives only its certificate, private key, and CA. Confirm `SHOW ssl;` and `pg_stat_ssl`, then reject `hostnossl` connections.

## Local deployment smoke

```sh
docker build --build-arg VERSION=ci-local -t rivune-ci:current -f server/Dockerfile .
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/migrations.sh
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/reverse-proxy-smoke.sh
```
