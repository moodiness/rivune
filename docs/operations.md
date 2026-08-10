# Production operations

The supported container deployment uses PostgreSQL 18 and puts Rivune behind Caddy. Run the commands in this document from the repository root. The supplied shell scripts explicitly target Linux with Bash, OpenSSL, Docker Engine, Docker Compose v2, and `set -euo pipefail`; CI runs the same scripts on Linux.

## HTTPS with Caddy

Create the private `.env` file before entering any secrets:

```sh
./scripts/create-env.sh
```

The helper refuses to overwrite an existing file or link and creates `.env`
with mode `0600`. For an existing deployment, run `chmod 600 .env` and stop if
it fails before continuing. Then set three independent random database secrets,
a separate setup secret, a public DNS name, and a pinned Rivune version.
`postgres` is bootstrap-only, `rivune` is the application login,
`rivune_owner` is a non-login owner, and `rivune_restore` is a non-superuser
login used only by the restore scripts.

```dotenv
RIVUNE_HOST=media.example.com
RIVUNE_VERSION=1.4.2
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=<output of: openssl rand -hex 32>
RIVUNE_DATABASE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_RESTORE_PASSWORD=<different output of: openssl rand -hex 32>
RIVUNE_SETUP_TOKEN=<different output of: openssl rand -hex 32>
TZ=UTC
```

Set optional TMDB, TVDB, Fanart.tv API-key, MDBList, Trakt, Simkl, and tracking-encryption credentials directly in the private `.env` file. Leave credentials for unused providers empty.

Collection and addon artwork is fetched through Rivune's same-origin artwork cache. Public artwork remains restricted to HTTPS on port 443 with public DNS results. If artwork is intentionally hosted on a trusted LAN server, set `RIVUNE_LAN_ARTWORK_ORIGINS` to a comma-separated list of exact origins such as `http://192.168.1.20:8080`. Entries require a private IP literal and explicit port; do not include a hostname, path, query, fragment, credential, loopback, link-local, metadata, documentation, or reserved address. Rivune revalidates redirects and does not expose source URLs or query credentials to browsers.

Point the `A`/`AAAA` records for `RIVUNE_HOST` at the host, allow inbound TCP 80 and TCP/UDP 443, then start the supported configuration:

```sh
docker compose --env-file .env -f deploy/caddy/compose.yaml pull
docker compose --env-file .env -f deploy/caddy/compose.yaml up -d
docker compose --env-file .env -f deploy/caddy/compose.yaml ps
curl --fail --show-error https://media.example.com/health
```

Caddy obtains and renews a publicly trusted certificate automatically. [`deploy/caddy/Caddyfile`](../deploy/caddy/Caddyfile) replaces inbound `X-Forwarded-For`, `X-Real-IP`, `X-Forwarded-Proto`, and `X-Forwarded-Host`; clients therefore cannot inject a trusted chain. The Compose network has the fixed `172.30.0.0/24` edge subnet, and Rivune trusts only that subnet through `RIVUNE_TRUSTED_PROXIES`. Do not change it to `0.0.0.0/0`, a host LAN, or another client-reachable range. If the edge subnet must change, update both the network subnet and `RIVUNE_TRUSTED_PROXIES` to the same dedicated range.

Rivune is not published directly to the host in this configuration. PostgreSQL is isolated on the private database network. Caddy is the only externally reachable service.

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
  export COMPOSE_FILE=deploy/caddy/compose.yaml

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
COMPOSE_FILE=deploy/caddy/compose.yaml ./scripts/postgres-backup.sh backups/rivune-before-1.5.0.dump
./scripts/postgres-verify-backup.sh --expect-backup-id '<recorded ID>' backups/rivune-before-1.5.0.dump
# edit RIVUNE_VERSION=1.5.0 in .env
docker compose --env-file .env -f deploy/caddy/compose.yaml pull rivune
docker compose --env-file .env -f deploy/caddy/compose.yaml up -d rivune
curl --fail --show-error https://media.example.com/health
```

## Playback processing and diagnostics

Rivune resolves `RIVUNE_FFMPEG_PATH` and `RIVUNE_FFPROBE_PATH` at startup. The defaults are `ffmpeg` and `ffprobe`. `RIVUNE_HARDWARE_ACCELERATION` accepts `auto`, `software`, `vaapi`, `qsv`, or `nvenc`; `RIVUNE_VIDEO_DEVICE` must be an absolute container path. `auto` probes the supported hardware encoders and falls back to software. The following existing limits are startup-validated:

The base Compose target exposes no GPU. Opt in with `compose.vaapi.yaml` for `/dev/dri/renderD128` or `compose.nvidia.yaml` for the NVIDIA runtime; do not combine overlays unless the host intentionally exposes both device families. In `auto` mode Rivune tests viable encoders in deterministic order (NVENC, then Intel QSV/VAAPI, or AMD VAAPI) and uses software when no probe succeeds. A hardware job that fails before publishing HLS output is retried once with a clean software command; published output is never mixed across encoders.

The startup log emits the same bounded executable versions, requested acceleration mode, selected encoder, tone-map capability, thread and pool limits, maximum video bitrate, and media-storage ceiling. It never emits executable paths, source URLs, provider data, or command output.

| Variable | Range | Default |
| --- | ---: | ---: |
| `RIVUNE_REMUX_CONCURRENCY` | `1`–`16` slots in each process, probe, and subtitle pool | `4` |
| `RIVUNE_TRANSCODE_THREADS` | `1`–`32` threads per transcode | `4` |
| `RIVUNE_TRANSCODE_MAX_BITRATE_KBPS` | `64`–`200000` kb/s | `12000` |
| `RIVUNE_TRANSCODE_MAX_READ_RATE` | `1.0`–`4.0` × real time; reduced automatically as the process pool fills | `1.5` |
| `RIVUNE_HLS_INITIAL_BUFFER_SECONDS` | `3`–`30` seconds, in whole seconds | `6` |
| `RIVUNE_MEDIA_MAX_STORAGE_MB` | `512`–`102400` MiB | `20480` for the binary; `4096` in Compose |

`RIVUNE_MEDIA_TEMP_DIR` selects the workspace parent; Rivune creates a private `rivune-media` child there. If it is empty, the system temporary directory is used. Keep this workspace on local storage with enough capacity for the configured limit. Reaching the processing pools returns a retryable `503`; reaching the workspace limit returns `507`. Increasing concurrency also increases CPU, memory, network, and temporary-storage pressure.

Both Compose targets mount `/tmp` as a `noexec,nosuid,nodev` tmpfs, defaulting to `4096` MiB, and set Rivune's media limit to the same value. Keep `RIVUNE_MEDIA_TMPFS_SIZE_MB` and `RIVUNE_MEDIA_MAX_STORAGE_MB` aligned. Rivune defaults to `8` CPUs / `6g`, PostgreSQL to `2` CPUs / `1g`, and Caddy to `1` CPU / `256m`; override these with the corresponding `RIVUNE_*_LIMIT` variables from `.env.example`. Root filesystems are read-only, capabilities are minimized, PID ceilings are enforced, the Rivune process drops to `PUID:PGID`, and Compose waits for Rivune's native `/health` check before starting Caddy.

A global administrator can inspect **Administration → Activity**. The response reports scrubbed, bounded FFmpeg and ffprobe versions, requested acceleration, selected encoder, thread/read-rate ceilings, exact active/limit values for each bounded pool, and cumulative started/succeeded/failed/software-fallback process totals. Active jobs expose bounded progress, speed, and estimated startup duration only when valid; no raw FFmpeg progress text is returned. Failed jobs expose one bounded class (`capacity`, `source`, `processing`, `storage`, `timeout`, `cancelled`, or `unknown`), never command output, source URLs, tokens, codec strings, or provider details. Activity returns at most 200 sessions and 200 physical jobs, deduplicating shared workers, and marks truncated arrays explicitly while preserving total counts.

The media safety limits below are fixed rather than environment settings:

- probe deadline/output: 15 seconds and 4 MiB; retained process diagnostic output: 2 KiB;
- subtitle conversion deadline/output: 30 seconds and 16 MiB;
- HLS readiness: 45 seconds; segment duration: 3 seconds; retained window: 120 segments;
- upstream playlist input: 8 MiB, 20,016 lines, and 10,000 references; rewritten output: 16 MiB;
- playback-session inactivity: 30 minutes; source references: 4 hours; media-job idle cleanup: 2 minutes;
- opaque child delivery capabilities: 5 minutes, 10,000 per profile and 40,000 globally.

Do not make these safety limits configurable to work around a failing source. Use the bounded activity diagnostic to distinguish capacity, storage, source, and processing failures, then adjust only the documented startup settings when appropriate.

## Jellyfin-compatible client access

Rivune includes a limited Jellyfin-compatible API adapter. It is not a complete Jellyfin server, does not implement every Jellyfin endpoint or client feature, and provides no LAN UDP discovery. Configure it manually with Rivune's public URL.

The adapter is off by default. An administrator can enable or disable it from Rivune's settings page without restarting the service. For unattended initial provisioning, set this non-secret default before first setup:

```dotenv
RIVUNE_JELLYFIN_ENABLED=true
```

Only `true` or `false` is accepted, after surrounding whitespace is trimmed and letter case is normalized. Any other non-empty value prevents Rivune from starting. Once an administrator saves the Jellyfin setting, that persisted value is authoritative over the environment default.

For temporary compatibility diagnostics, `RIVUNE_JELLYFIN_DEBUG=true` adds bounded route metadata to the existing completion log. It records a normalized client family, route and method, query parameter names with their cardinalities, selected-header presence, response status/duration/byte count/content type/range presence, and bounded top-level JSON shape when available. It does not buffer media and never records query or header values, body values or payload bytes, credentials, cookies, provider URLs, or item/session identifiers. The flag is strictly opt-in, defaults to `false`, accepts only `true` or `false` under the same normalization rules, and requires a service restart to change. Disable it after collecting the needed diagnostics.

On Unraid, **Jellyfin initial default** controls only the value used before the administrator setting is first saved. For every non-loopback deployment, keep Rivune behind the documented HTTPS reverse proxy, give the client the same `https://` origin configured by `RIVUNE_PUBLIC_URL`, and never publish Rivune's raw port `8080` to the LAN or internet.

Open the profile's **Preferences → Connections** page and generate its Jellyfin credential. Enter the displayed UUID as the client username and the generated profile-only application password as the password. The password is shown once; copy it before closing the dialog, or rotate it to issue a replacement. Rivune account passwords, administrator credentials, profile names, and profile PINs are never accepted by the compatibility login.

Early development builds of the profile-credential cutover could remove the old Jellyfin mapping after its revoked session had already been purged while leaving the dedicated device row counted against the per-user quota. Migration 66 removes these rows whenever cutover-session evidence remains. If an instance ran one of those unreleased builds and a new application-password login still fails at the device limit, open **Administration → Devices**, remove only the stale duplicate compatibility-device entries for the affected profile, and retry once. Do not remove unrelated native devices.

Clients may use either the exact Jellyfin-style root paths or one lowercase `/emby` prefix. For example, public discovery is available at `/System/Info/Public` and `/emby/System/Info/Public`; nested prefixes, case variants, path normalization, and implicit method fallbacks are rejected. The bounded compatibility contract covers:

- public server identity and availability probes;
- credential login, the credential-bound user, session/capability projection, logout, and bounded WebSocket liveness;
- library views, items, movie/series hierarchy, enabled metadata and add-on catalog search, item artwork, and deterministic profile avatars;
- lazy multi-source `PlaybackInfo`, direct/remux/transcode delivery through Rivune's existing playback pipeline, byte ranges, seeking, opaque HLS child requests, and capability-scoped WebVTT subtitles;
- playing/progress/stopped events, played state, favorites, resume items, and next-up.

Private provider URLs, headers, native playback tokens, and source references remain server-side. Query authentication is accepted for Jellyfin protocol compatibility, but Rivune-generated playback URLs contain only an owner/item/source/TTL-bound capability and never the profile credential. Use HTTPS for every non-loopback deployment. Quick Connect is explicitly disabled, plugin and package lists are empty, and unknown paths or methods return `404`. The exact request/response schemas, limits, and status codes are in [`protocol/jellyfin-compat-openapi.yaml`](../protocol/jellyfin-compat-openapi.yaml).

Jellyfin Media Player/Desktop is incompatible with this adapter because it loads the server-hosted Jellyfin Web application from `/` after discovery, while Rivune intentionally serves its own web application there. A successful API probe in that desktop shell therefore does not validate the standalone application flows covered by the adapter.

To roll back, disable Jellyfin from the administrator settings page. Confirm normal Rivune access through the HTTPS origin afterward.

### Playback evidence and real-client validation

The compatibility route smoke is not a media certification. A route match, discovery response, `PlaybackInfo` response, or successful `2xx` proves only that exchange. Claiming a playback workflow requires the emitted URL to be consumed, media bytes to be decoded/rendered, seek and selected tracks to be observed, and the session to stop cleanly.

The optional FFmpeg byte-path tests create only short lavfi video/audio and local text in temporary directories. They do not download or retain media. Run them on a host with `ffmpeg` and `ffprobe` on `PATH`, or set `RIVUNE_FFMPEG_PATH` and `RIVUNE_FFPROBE_PATH` to trusted local binaries:

```sh
cd server
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/playback \
  -run '^TestExternalMedia' -count=1
RIVUNE_TEST_EXTERNAL_MEDIA=1 go test ./internal/jellyfin \
  -run '^TestPlaybackGatewayReadsPlaylistAndChildBytesAndRejectsOutOfOrderChild$' -count=1
```

The first command covers generated MP4/MKV, direct ranges, TS/fMP4 HLS remux/transcode, overlapping AAC track selection, synthetic 5.1 AAC downmix to decoded stereo, UTF-8 subtitle conversion/seek, embedded ASS burn-in verified on a decoded frame, and HLS child delivery. The second generates a one-second H.264/AAC MPEG-TS child and exercises the Jellyfin adapter gateway. A skip is not a pass: record it as `ABSENT` when FFmpeg is unavailable. Dolby Vision, HDR pixels, bitmap subtitles, DTS bytes, and named-client rendering are not supplied by these tests.

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
2. Enable `RIVUNE_JELLYFIN_DEBUG=true`, restart, and confirm logs expose only normalized client family/version, route/method, query **names and cardinalities**, selected-header presence, bounded response metadata and bounded JSON shape. Stop if any value, ID, URL, query, token, provider name or codec appears as a label.
3. In the actual client UI, perform discovery/manual connection, login, catalogue navigation, item detail and artwork. Then play long enough to observe rendered video and audible audio; select another audio track and subtitle where the licensed local fixture permits it; seek forward and backward; pause/resume; and stop. Record unsupported steps as `ABSENT`, not skipped success.
4. Correlate the bounded server trace with `PlaybackInfo`, the chosen mode (`direct`, `remux`, `transcode_audio`, or `transcode`), the exact same-origin stream/master/child sequence, byte ranges or decoded segments, Playing/Progress/Stopped, and FFmpeg teardown. Status codes alone are insufficient: retain a human observation of rendering and client controls plus bounded server-side media/lifecycle evidence.
5. Scrub a copy of the trace before it leaves the validation host. Remove or replace all credentials, cookies, authorization headers, user/profile/item/session/source/device IDs, IPs/hostnames, full URLs, query values, provider data, filesystem paths and media titles. Preserve the ordered methods, normalized route templates, status, content type, range presence, byte counts, timing buckets, decision mode and failure enum. Review the scrubbed copy manually; never publish the raw trace.
6. Store the scrubbed trace, environment manifest, command outputs and a result sheet together in the controlled release evidence store under client/version/date. Give each step `PASSED`, `ABSENT` or `BLOCKED` and link its artefact. Revoke the temporary credential, disable debug, restart, and verify no playback process remains.

Current evidence must be summarized narrowly: Infuse 8 has a partially observed hierarchy scan but no validated player playback; Streamyfin 0.31.0 has a reported HTTP-level HLS profile replay on NAS but no validated application rendering; VidHub has no audited trace or validation. None may be advertised as generally compatible until the versioned, scrubbed real-client bundle above exists.

## Unraid PostgreSQL TLS

The Unraid template has no plaintext database mode: it supplies
`sslmode=verify-full` and a read-only `ca.crt`. PostgreSQL must be ready before
Rivune starts. Use two user-defined Docker networks: connect Rivune to both the
edge and database networks, the reverse proxy only to edge, and PostgreSQL only
to database. Do not attach the proxy to the database network.

Create the private CA and server key on a protected administration machine, not
in a container or repository. Set `DB_HOST` to the exact value entered as
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

For an existing plaintext Unraid installation, use a planned maintenance
window and migrate without any permissive fallback:

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

3. Apply the current Rivune template, including the required CA mount,
   `RIVUNE_DATABASE_SSLROOTCERT`, and `verify-full`, then restart Rivune. Do
   not temporarily select `require`, `prefer`, `allow`, or `disable`: a bad
   chain, SAN, path, or expired certificate must make startup fail closed.
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
COMPOSE_FILE=deploy/caddy/compose.yaml ./scripts/postgres-backup.sh "${BACKUP}"
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

A restore is destructive: it replaces the current `rivune` database. Take and
verify a new authenticated backup of the current database first. Export the
restore password from the protected deployment secret; it is independent from
the application and bootstrap passwords. Keep the signing/verification key,
lineage, and state exports from the backup section. Select the backup ID from the
separate operator-controlled record, never from the adjacent manifest:

```sh
export COMPOSE_FILE=deploy/caddy/compose.yaml
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

After policy authorization, the restore stops Rivune, connects as non-superuser
`rivune_restore`, assumes non-login `rivune_owner` for restored objects, recreates
the database, restores with `--exit-on-error`, checks the migration ledger, and
restarts Rivune only if it was running before. The restore password is passed to
Compose by environment-variable name, not as an argument or log value. Missing,
unsafe, oversized, inconsistent, replayed, or incorrectly selected material
fails before the application is stopped.

If authentication, policy, archive parsing, or service shutdown fails before
database replacement starts, the script returns the error and restores the
prior service state. If a drop, create, restore, or ledger check fails after
replacement starts, Rivune deliberately remains stopped so it cannot serve a
partial database. Repair the database or restore a known-good authenticated
archive before starting it again. Inspect both services before accepting traffic:

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

Keep the original archive, manifest, signature, applicable trusted public key,
external ID record, protected generation state, and audit log until application
login, profiles, collections, and playback history have been checked.

## Migration and proxy validation

CI builds the current image and runs both scripts below. They create uniquely named disposable Docker networks, containers, and volumes and clean them on every exit.

```sh
docker build --build-arg VERSION=ci-local -t rivune-ci:current -f server/Dockerfile .
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/migrations.sh
RIVUNE_IMAGE=rivune-ci:current ./scripts/ci/reverse-proxy-smoke.sh
```

The migration check performs a clean install, checks the migration count and current version, restarts to prove idempotency, constructs the immediately previous schema, upgrades it with the current image, and checks idempotency again. The proxy smoke test serves a real Rivune discovery response over Caddy's locally trusted TLS, then verifies that the same supported Caddy configuration overwrites spoofed forwarding headers and forwards HTTPS scheme and host values.
