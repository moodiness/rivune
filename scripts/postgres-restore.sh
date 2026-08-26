#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=postgres-backup-auth.sh
source "${SCRIPT_DIR}/postgres-backup-auth.sh"

usage() {
  echo "Usage: $0 --expect-backup-id ID BACKUP_FILE" >&2
  echo "       $0 --allow-rollback ID BACKUP_FILE" >&2
  echo "       $0 --initialize-state ID BACKUP_FILE" >&2
  echo "       $0 --allow-legacy SHA256 BACKUP_FILE" >&2
  echo "       $0 --allow-legacy-manifest ID BACKUP_FILE" >&2
  exit 64
}
if (( $# != 3 )); then
  usage
fi
RESTORE_MODE="$1"
OPERATOR_EXPECTATION="$2"
BACKUP_FILE="$3"
case "${RESTORE_MODE}" in
  --expect-backup-id|--allow-rollback|--initialize-state|--allow-legacy-manifest)
    [[ "${OPERATOR_EXPECTATION}" =~ ^[0-9a-f]{32}$ ]] || usage
    ;;
  --allow-legacy)
    [[ "${OPERATOR_EXPECTATION}" =~ ^[0-9a-f]{64}$ ]] || usage
    ;;
  *) usage ;;
esac

require_repository_archive "${BACKUP_FILE}"
BACKUP_FILE="$(realpath -e -- "${BACKUP_FILE}")"
BACKUP_DIR="$(dirname "${BACKUP_FILE}")"
MANIFEST_FILE="${BACKUP_FILE}.manifest"
SIGNATURE_FILE="${BACKUP_FILE}.sig"
: "${RIVUNE_RESTORE_PASSWORD:?RIVUNE_RESTORE_PASSWORD is required}"
require_backup_key "${RIVUNE_BACKUP_VERIFY_KEY_FILE:-}" "${BACKUP_DIR}" "verification"
require_backup_signature "${SIGNATURE_FILE}"
SECURE_BACKUP_VERIFY_KEY_FILE="${SECURE_BACKUP_KEY_FILE}"

AUTHENTICATED_BACKUP_FILE=""
# shellcheck disable=SC2034 # Assigned and consumed by postgres-backup-auth.sh.
declare AUTHENTICATED_MANIFEST_FILE AUTHENTICATED_SIGNATURE_FILE
STATE_LOCKED=false
server_was_running=false
service_stop_attempted=false
staging_created=false
database_swapped=false
activation_validated=false

RIVUNE_SERVICE="${RIVUNE_SERVICE:-rivune}"
LIVE_DATABASE=rivune
STAGING_DATABASE=rivune_restore_staging
PRIOR_DATABASE=rivune_restore_prior
RIVUNE_RESTORE_READY_TIMEOUT="${RIVUNE_RESTORE_READY_TIMEOUT:-90}"
if [[ ! "${RIVUNE_RESTORE_READY_TIMEOUT}" =~ ^[1-9][0-9]*$ ]] ||
   (( RIVUNE_RESTORE_READY_TIMEOUT > 600 )); then
  backup_error "RIVUNE_RESTORE_READY_TIMEOUT must be an integer from 1 through 600"
  exit 1
fi

restore_psql() {
  PGPASSWORD="${RIVUNE_RESTORE_PASSWORD}" docker compose exec -T -e PGPASSWORD postgres \
    psql --host 127.0.0.1 --username rivune_restore --dbname postgres \
    --set ON_ERROR_STOP=1 \
    --set=live_database="${LIVE_DATABASE}" \
    --set=staging_database="${STAGING_DATABASE}" \
    --set=prior_database="${PRIOR_DATABASE}"
}

start_rivune_and_wait() {
  docker compose up -d --wait --wait-timeout "${RIVUNE_RESTORE_READY_TIMEOUT}" \
    "${RIVUNE_SERVICE}" >/dev/null
}

drop_staging_database() {
  restore_psql <<'SQL'
SET ROLE rivune_owner;
DROP DATABASE IF EXISTS :"staging_database" WITH (FORCE);
SQL
}

rollback_database_names() {
  restore_psql <<'SQL'
SELECT EXISTS (SELECT FROM pg_database WHERE datname = :'live_database') AS live_exists,
       EXISTS (SELECT FROM pg_database WHERE datname = :'staging_database') AS staging_exists,
       EXISTS (SELECT FROM pg_database WHERE datname = :'prior_database') AS prior_exists \gset
\if :prior_exists
  \if :staging_exists
    \echo 'Cannot roll back while live, staging, and prior database names coexist.'
    \quit 1
  \endif
  BEGIN;
  SELECT format('ALTER DATABASE %I RENAME TO %I', :'live_database', :'staging_database')
  WHERE :'live_exists'::boolean \gexec
  ALTER DATABASE :"prior_database" RENAME TO :"live_database";
  COMMIT;
\else
  \if :live_exists
  \else
    \echo 'Cannot roll back because neither the live nor prior database exists.'
    \quit 1
  \endif
\endif
SQL
}

cleanup() {
  local status="$1"
  local rollback_status=0
  trap - EXIT
  set +e

  if (( status != 0 )) && [[ "${service_stop_attempted}" == true && \
                             "${activation_validated}" == false ]]; then
    docker compose stop "${RIVUNE_SERVICE}" >/dev/null 2>&1
    if rollback_database_names; then
      database_swapped=false
      staging_created=true
      if [[ "${server_was_running}" == true ]]; then
        if start_rivune_and_wait; then
          echo "Restore failed; the prior database was restored and Rivune is ready" >&2
        else
          rollback_status=1
          echo "Restore failed; the prior database was restored, but Rivune did not become ready" >&2
        fi
      else
        echo "Restore failed; the prior database was restored" >&2
      fi
    else
      rollback_status=1
      echo "Restore failed and automatic database rollback failed; Rivune remains stopped and the prior database is retained as ${PRIOR_DATABASE}" >&2
    fi
  fi

  if (( status != 0 )) && [[ "${staging_created}" == true && \
                             "${database_swapped}" == false ]]; then
    if ! drop_staging_database >/dev/null 2>&1; then
      rollback_status=1
      echo "Could not remove the inactive restore staging database ${STAGING_DATABASE}" >&2
    fi
  fi

  cleanup_authenticated_backup
  if [[ "${STATE_LOCKED}" == true ]]; then
    unlock_backup_state
    STATE_LOCKED=false
  fi
  if (( rollback_status != 0 )); then
    echo "Restore recovery requires operator attention" >&2
  fi
  exit "${status}"
}
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'cleanup "$?"' EXIT

if [[ "${RESTORE_MODE}" == --allow-legacy ]]; then
  if [[ -e "${MANIFEST_FILE}" || -L "${MANIFEST_FILE}" ]]; then
    backup_error "Legacy authorization cannot be used for a manifested backup"
    exit 1
  fi
  require_backup_token "${RIVUNE_BACKUP_LINEAGE:-}" "RIVUNE_BACKUP_LINEAGE"
  stage_legacy_authenticated_backup "${BACKUP_FILE}" "${SECURE_BACKUP_SIGNATURE_FILE}" \
    "${SECURE_BACKUP_VERIFY_KEY_FILE}" "${OPERATOR_EXPECTATION}"
  require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" true
  lock_backup_state
  STATE_LOCKED=true
  if [[ ! -e "${SECURE_BACKUP_STATE_FILE}" ]]; then
    initialize_backup_state "${RIVUNE_BACKUP_LINEAGE}"
  fi
  load_backup_state "${RIVUNE_BACKUP_LINEAGE}"
  record_restore_audit legacy-authorized "${RIVUNE_BACKUP_LINEAGE}" legacy legacy \
    "${LEGACY_ARCHIVE_SHA256}"
else
  require_backup_manifest "${MANIFEST_FILE}"
  stage_authenticated_manifest "${BACKUP_FILE}" "${SECURE_BACKUP_MANIFEST_FILE}" \
    "${SECURE_BACKUP_SIGNATURE_FILE}" "${SECURE_BACKUP_VERIFY_KEY_FILE}"
  if [[ "${MANIFEST_FORMAT}" == v3-age ]]; then
    require_age_identity_file "${RIVUNE_BACKUP_AGE_IDENTITY_FILE:-}" "${BACKUP_DIR}"
  fi
  if [[ "${MANIFEST_FORMAT}" == v3-age && "${RESTORE_MODE}" == --allow-legacy-manifest ]]; then
    backup_error "Legacy manifest authorization cannot be used for an encrypted backup"
    exit 1
  fi
  if [[ "${MANIFEST_FORMAT}" != v3-age && "${RESTORE_MODE}" != --allow-legacy-manifest ]]; then
    backup_error "Plaintext manifested backups require explicit legacy authorization"
    exit 1
  fi
  if [[ "${OPERATOR_EXPECTATION}" != "${MANIFEST_BACKUP_ID}" ]]; then
    backup_error "Backup does not match the operator-selected backup ID"
    exit 1
  fi

  case "${RESTORE_MODE}" in
    --expect-backup-id)
      require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" false
      lock_backup_state
      STATE_LOCKED=true
      enforce_current_restore_policy "${OPERATOR_EXPECTATION}"
      ;;
    --allow-legacy-manifest)
      require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" false
      lock_backup_state
      STATE_LOCKED=true
      enforce_legacy_manifest_restore_policy "${OPERATOR_EXPECTATION}"
      ;;
    --allow-rollback)
      require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" false
      lock_backup_state
      STATE_LOCKED=true
      load_backup_state "${MANIFEST_LINEAGE}"
      if (( MANIFEST_SEQUENCE >= STATE_LATEST_SEQUENCE )); then
        backup_error "Rollback authorization only accepts an older trusted-lineage generation"
        exit 1
      fi
      ;;
    --initialize-state)
      require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" true
      lock_backup_state
      STATE_LOCKED=true
      if [[ -e "${SECURE_BACKUP_STATE_FILE}" ]]; then
        backup_error "Backup state initialization requires an absent state file"
        exit 1
      fi
      if (( MANIFEST_SEQUENCE == 999999999999999999 )); then
        backup_error "Backup generation counter is exhausted"
        exit 1
      fi
      ;;
  esac

  # No archive bytes are read until the signed metadata, external expectation,
  # and protected current/rollback/initialization policy have all been accepted.
  stage_authenticated_archive "${BACKUP_FILE}"

  # Exceptional authorization is recorded only after the private snapshot has
  # the authenticated exact size and digest.
  if [[ "${RESTORE_MODE}" == --allow-rollback ]]; then
    record_restore_audit rollback-authorized "${MANIFEST_LINEAGE}" "${MANIFEST_SEQUENCE}" \
      "${MANIFEST_BACKUP_ID}" "${MANIFEST_ARCHIVE_SHA256}"
  elif [[ "${RESTORE_MODE}" == --initialize-state ]]; then
    if [[ -e "${SECURE_BACKUP_STATE_FILE}" ]]; then
      backup_error "Backup state initialization requires an absent state file"
      exit 1
    fi
    write_backup_state "${MANIFEST_LINEAGE}" "$(( MANIFEST_SEQUENCE + 1 ))" \
      "${MANIFEST_SEQUENCE}" "${MANIFEST_BACKUP_ID}" "${MANIFEST_ARCHIVE_SHA256}"
    record_restore_audit state-initialized "${MANIFEST_LINEAGE}" "${MANIFEST_SEQUENCE}" \
      "${MANIFEST_BACKUP_ID}" "${MANIFEST_ARCHIVE_SHA256}"
  fi
fi

if docker compose ps --status running --services | grep -qx "${RIVUNE_SERVICE}"; then
  server_was_running=true
fi

# PostgreSQL parses only the private snapshot, after authentication, expectation,
# lineage, freshness policy, and any exceptional authorization have succeeded.
stream_authenticated_backup \
  | docker compose exec -T postgres pg_restore --list >/dev/null

# The state lock remains held across parsing. Re-read the trusted state immediately
# before creating an isolated database so an inconsistent replacement fails closed.
if [[ "${RESTORE_MODE}" == --expect-backup-id ]]; then
  enforce_current_restore_policy "${OPERATOR_EXPECTATION}"
elif [[ "${RESTORE_MODE}" == --allow-legacy-manifest ]]; then
  enforce_legacy_manifest_restore_policy "${OPERATOR_EXPECTATION}"
elif [[ "${RESTORE_MODE}" != --allow-legacy ]]; then
  load_backup_state "${MANIFEST_LINEAGE}"
fi

# Recover the only safe interrupted pre-swap state (the live name is absent and
# the retained prior name exists), reject ambiguous prior state, and remove only
# the inactive, fixed-purpose staging database. format(%I) quotes every generated
# database identifier before \gexec executes it.
restore_psql <<'SQL'
SELECT format('ALTER DATABASE %I RENAME TO %I', :'prior_database', :'live_database')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'live_database')
  AND EXISTS (SELECT FROM pg_database WHERE datname = :'prior_database') \gexec
SELECT EXISTS (SELECT FROM pg_database WHERE datname = :'live_database')
   AND NOT EXISTS (SELECT FROM pg_database WHERE datname = :'prior_database') AS database_state_valid \gset
\if :database_state_valid
\else
  \echo 'The live/prior database names are ambiguous; no database was changed.'
  \quit 1
\endif
DROP DATABASE IF EXISTS :"staging_database" WITH (FORCE);
CREATE DATABASE :"staging_database" OWNER rivune_owner;
SQL
staging_created=true
stream_authenticated_backup \
  | PGPASSWORD="${RIVUNE_RESTORE_PASSWORD}" docker compose exec -T -e PGPASSWORD postgres \
      pg_restore --host 127.0.0.1 \
      --username rivune_restore \
      --role rivune_owner \
      --dbname "${STAGING_DATABASE}" \
      --exit-on-error \
      --no-owner \
      --no-privileges

PGPASSWORD="${RIVUNE_RESTORE_PASSWORD}" docker compose exec -T -e PGPASSWORD postgres \
  psql --host 127.0.0.1 --username rivune_restore --dbname "${STAGING_DATABASE}" \
  --set ON_ERROR_STOP=1 --tuples-only --no-align \
  --command "SELECT CASE WHEN to_regclass('public.schema_migrations') IS NOT NULL AND EXISTS (SELECT FROM schema_migrations) THEN 'verified' ELSE 'invalid' END;" \
  | grep -qx verified

# The live database remains untouched until the archive has restored and passed
# its ledger check. The two renames commit atomically while Rivune is stopped.
service_stop_attempted=true
docker compose stop "${RIVUNE_SERVICE}" >/dev/null
restore_psql <<'SQL'
BEGIN;
ALTER DATABASE :"live_database" RENAME TO :"prior_database";
ALTER DATABASE :"staging_database" RENAME TO :"live_database";
COMMIT;
SQL
database_swapped=true
staging_created=false

if [[ "${server_was_running}" == true ]]; then
  start_rivune_and_wait
fi

if [[ "${RESTORE_MODE}" == --allow-rollback ]]; then
  record_restore_audit rollback-completed "${MANIFEST_LINEAGE}" "${MANIFEST_SEQUENCE}" \
    "${MANIFEST_BACKUP_ID}" "${MANIFEST_ARCHIVE_SHA256}"
elif [[ "${RESTORE_MODE}" == --allow-legacy ]]; then
  record_restore_audit legacy-completed "${RIVUNE_BACKUP_LINEAGE}" legacy legacy \
    "${LEGACY_ARCHIVE_SHA256}"
fi
activation_validated=true

# Retire temporary authentication material and release protected state before the
# final destructive commit. Any failure here still leaves the prior database.
cleanup_authenticated_backup
unlock_backup_state
STATE_LOCKED=false

# The retained database is discarded only after the replacement is validated and,
# when Rivune was previously running, Compose has reported the service healthy.
restore_psql <<'SQL'
SET ROLE rivune_owner;
DROP DATABASE :"prior_database" WITH (FORCE);
SQL
database_swapped=false
trap - EXIT
printf 'Authenticated restore completed for the operator-selected backup\n'
