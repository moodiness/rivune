# Operations

The supported deployment is [`compose.yaml`](../compose.yaml) with PostgreSQL 18. Run commands from the repository root. Linux and macOS use `./rivune`; Windows uses `./rivune.ps1` after creating `.env` with `./scripts/create-env.ps1`.

## Install and diagnose

```sh
./rivune setup --public-url https://media.example.com --version 1.13.1
./rivune up
./rivune status
./rivune logs rivune
./rivune doctor
```

`setup` creates a private `.env`, generates independent database passwords, setup token, and encryption key, and refuses to overwrite an existing path. Omit `--public-url` for loopback-only use. `doctor` checks configuration, Compose, PostgreSQL, containers, and `/ready` without printing secrets.

Claim a new instance with `RIVUNE_SETUP_TOKEN`. Configure providers, quotas, transcoding, and Jellyfin compatibility in **Administration → Settings**, not `.env`. Provider changes apply live. Hardware acceleration, preferred codec, quality, and concurrency require a restart; confirm that `pending restart` clears before treating the requested value as active.

## Multilingual semantic search

Rivune always builds a persistent deterministic catalogue from TMDB's primary translations. The current request language is loaded immediately; the remaining official genre and country translations are refreshed progressively in the background. Common year, decade, recent-release, minimum-rating, runtime, and negative-genre wording is also compiled deterministically into TMDB filters. A temporary TMDB failure uses the retained catalogue and marks the search response as partial.

For title-like residual text, Rivune starts a TMDB exact-title probe ahead of and concurrently with eligible local inference. An exact title match wins without waiting for or applying model output, and canceling the now-unneeded model attempt does not mark the response partial. The probe can be reused for the title fallback when no model intent is selected. This fast route depends on TMDB responsiveness; the 3.5-second limit below bounds local model assistance, not upstream TMDB or network time.

Ollama is optional and is consulted only for residual ambiguous descriptions that the deterministic catalogue and constraint parser did not recognize. Set both `RIVUNE_SEMANTIC_OLLAMA_URL` and `RIVUNE_SEMANTIC_OLLAMA_MODEL`, then restart Rivune. The URL must be an exact local HTTP(S) origin with an explicit port. DNS is revalidated on every connection, and public, link-local, metadata-service, and reserved destinations are rejected. Each server-side model attempt has a hard 3.5-second budget. Concurrent identical requests share one attempt, and successful selections—including an empty selection—are cached; canceling one caller does not cancel another caller's shared work. Unknown, malformed, or timed-out model output is ignored as a partial extension failure rather than replacing deterministic results.

Residual text that no deterministic or local-model intent consumes is treated as a title. For example, `Alien` searches both TMDB movies and series, while `film Alien` searches movies only and `film d'horreur Alien` retains the movie and horror constraints. Requests such as `science fiction from the 1990s rated at least 7 under 2 hours but not horror` keep those deterministic filters whether the residual text is probed as a title or sent to discovery. A title is never converted into a genre merely because its wording resembles one.

All first-party clients publish each completed addon or semantic batch while the remaining sources continue in the background. Results are deduplicated by provider identity, stale batches are rejected when the query or profile changes, and the final state still carries source failures, pagination, and partial-result status. This is client-side orchestration over the existing bounded HTTP requests; it does not require WebSocket or SSE infrastructure.

No local classifier is selected or recommended for production. The committed [semantic benchmark report](../server/cmd/semantic-benchmark/report.v1.json) evaluates three pinned candidates against pre-registered gates: exact accuracy at least 85%, every language at least 75%, zero proper-title false positives, p95 at most 15 seconds, and deterministic repeated output. None passed. `qwen3:0.6b` reached 0% exact accuracy with 161 title false positives and 20.00-second p95; `qwen3:1.7b` reached 5% with 36 false positives and 2.49-second p95; `qwen3:4b` reached 8.33% with 30 false positives and 20.01-second p95. All three were nondeterministic.

Leave `RIVUNE_SEMANTIC_OLLAMA_URL` and `RIVUNE_SEMANTIC_OLLAMA_MODEL` empty for the supported default: deterministic constraints plus exact-title and title fallback search. Rivune never downloads a model or contacts a hosted inference service. The optional Ollama path remains available only for an operator who has explicitly selected and evaluated another local model; its output is still bounded, untrusted, and ignored on failure.

For that explicit experiment only, set a nonempty model before enabling the persistent Ollama overlay:

```sh
RIVUNE_SEMANTIC_OLLAMA_MODEL='<operator-evaluated-model>' \
  docker compose -f compose.yaml -f compose.ollama.yaml up -d
```

The overlay does not choose a production model and Compose exits with a required-variable error when `RIVUNE_SEMANTIC_OLLAMA_MODEL` is empty or unset. Its one-shot `ollama-model` service pulls only the explicit value, and the `rivune_ollama` volume survives container recreation. The separate `semantic-benchmark` profile exists for candidate evaluation, not normal deployment.

## Protocol 22 application behavior

The server advertises protocol 22 and the currently implemented optional capabilities from `GET /.well-known/rivune`; clients must use the returned `apiBaseUrl` and ignore unknown additive capability names. The normative paths and schemas are in [`protocol/openapi.yaml`](../protocol/openapi.yaml).

- Add-on candidates are verified with `POST /addons/verifications`; installed add-ons use `POST /addons/{addonId}/verifications`. `POST /addons` atomically consumes one unexpired successful snapshot without another network request. A candidate verification is bounded to 15 seconds, expires within 15 minutes, can be installed only once, and retains its private transport URL only as an encrypted envelope. Failed verifications retain no transport URL.
- Playback devices publish presence at `PUT /playback/device`, receive commands from `GET /playback/commands`, and report terminal results to `PUT /playback/commands/incoming/{operationId}/result`; senders inspect `GET /playback/commands/outgoing/{operationId}`. Sender-supplied UUID operation IDs make retries idempotent; terminal status and result codes come from closed enums.
- Profile queues use `GET /profiles/{profileId}/queue` plus the item, order, and consume routes. Mutations carry caller operation UUIDs, deduplicate identity-equal additions, and use optimistic revisions; the queue is durable and profile-scoped.
- Playback failover uses `/playback/failovers`. It stores two to eight opaque source references, advances only for the closed eligible failure classes, allows at most three attempts, and never returns provider URLs, headers, or credentials.
- Saved searches use `/saved-searches`. Smart collections use `/smart-collections` and evaluate a closed typed rule tree rather than expressions or SQL; updates and deletes use optimistic revisions.
- `/operations/extension-incidents` exposes a bounded profile-scoped timeline to profile managers. Records contain closed classifications and timestamps only—never upstream URLs, credentials, queries, bodies, or raw errors—and acknowledgement does not change recovery state.
- Device-session notifications are pending per authenticated session under `/auth/notifications`; targeted delivery uses `/profiles/{profileId}/sessions/{sessionId}/notifications`, while the administrator broadcast route snapshots sessions active at first acceptance and is idempotent. Clients poll by decimal cursor and explicitly acknowledge deliveries; there is no push or background-delivery promise.
- Media notifications are a separate durable profile inbox. `/media-notification-subscriptions/{titleId}` follows a library title with timezone, horizon, and lead time; existing seasons and episodes establish a silent baseline, and each profile may retain at most 4,096 followed titles. `/media-notifications` returns unread and read alerts newest first, excluding dismissed and expired records, and acknowledgement records either read or dismissed state idempotently.
- `/profiles/{profileId}/accessibility-preferences` persists reduced motion, contrast, text scale, captions, audio description, and focus-indicator choices with an optimistic revision and `ETag`. Each client applies only the controls its native surface implements; this profile document does not override unavailable operating-system or hardware support.

All first-party clients expose the queue, saved-search, smart-collection, media-inbox, incident, and accessibility surfaces appropriate to their form factor. Automatic application installation is intentionally platform-specific: Android and Windows can install a verified downloaded package; Apple clients only notify and open the verified release (tvOS shows a QR code); webOS and Tizen can replace the verified shared runtime in-app when persistent storage works, but full IPK/WGT installation still requires the local TV installer, developer mode, and—for Tizen—a local certificate profile.

## Network and HTTPS

For public access, terminate TLS at Pangolin/Newt or another reverse proxy. Join the proxy to `rivune-edge` and target `http://rivune:8080`. Set `RIVUNE_TRUSTED_PROXIES` to only the proxy's exact address or dedicated edge CIDR; never `0.0.0.0/0`, the LAN, or the database network. The proxy must replace, not append, client, host, and scheme headers. Rivune honors `X-Forwarded-Proto` and `X-Forwarded-Host` for browser authentication only when the direct peer is trusted and each header has exactly one value.

Browser login and device-code exchange use `/api/v1/auth/web/*`, not the native refresh route. JavaScript receives only a tab-local access token. The rotating refresh credential is a host-only `rivune_web_refresh` cookie with `HttpOnly`, `SameSite=Strict`, and `Path=/api/v1/auth/web/refresh`; it is `Secure` except on strict HTTP loopback origins and is never returned in JSON. Login, exchange, refresh, and logout require an exact `Origin`, `Sec-Fetch-Site: same-origin`, and `X-Rivune-CSRF: 1`. A proxy misconfigured as trusted can redefine the effective origin, so keep the trust range narrow and ensure the public origin exactly matches `RIVUNE_PUBLIC_URL`.

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

Before upgrading a protocol-21 deployment to a release that serves protocol 22, create and verify an authenticated backup, record its printed backup ID outside the backup repository, and preserve the complete encryption keyring. Then set an exact released `RIVUNE_VERSION` and recreate only Rivune. Startup applies embedded migrations transactionally before readiness; migrations `000082` through `000094` add add-on verification, playback command operations, web session separation, the reading queue, playback failover and bounded cleanup indexes, saved searches and smart collections, extension incidents, media notifications and its durable worker cursor, profile accessibility preferences, 24-hour queue-operation replay retention, and encrypted add-on verification transport URLs. Migration `000092` removes pending short-lived verification snapshots during the storage cutover; re-run verification after upgrade instead of reusing a pre-upgrade snapshot.

```sh
target_version=1.13.1
backup="backups/rivune-before-${target_version}.dump"
COMPOSE_FILE=compose.yaml ./scripts/postgres-backup.sh "${backup}"
./scripts/postgres-verify-backup.sh --expect-backup-id '<recorded ID>' "${backup}"
# set RIVUNE_VERSION=${target_version} in .env
docker compose --env-file .env -f compose.yaml pull rivune
docker compose --env-file .env -f compose.yaml up -d rivune
curl --fail --show-error https://media.example.com/ready
curl --fail --show-error https://media.example.com/.well-known/rivune | jq -e '.protocolVersion == 22 and (.apiBaseUrl | type == "string")'
docker compose --env-file .env -f compose.yaml exec -T postgres \
  psql -U rivune -d rivune -Atc \
  'SELECT version FROM schema_migrations WHERE version BETWEEN 82 AND 94 ORDER BY version;'
```

The final query must print every integer from `82` through `94`. If migration or readiness fails, do not regenerate keys, edit `schema_migrations`, or attempt to run individual migration files: keep the failed container logs, restore the verified pre-upgrade backup with the documented restore procedure, and run the previous exact image version until the failure is understood. Protocol 21 clients are incompatible with protocol 22; upgrade server and first-party clients together.

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
