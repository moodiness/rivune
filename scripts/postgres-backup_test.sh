#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(realpath -e -- "$(mktemp -d)")"
cleanup() {
  rm -rf -- "${TEST_DIR}"
}
trap cleanup EXIT

mkdir -p "${TEST_DIR}/bin" "${TEST_DIR}/secure" "${TEST_DIR}/keys" \
  "${TEST_DIR}/state" "${TEST_DIR}/staging"
chmod 700 "${TEST_DIR}/secure" "${TEST_DIR}/keys" "${TEST_DIR}/state" \
  "${TEST_DIR}/staging"
OBSERVATION_FILE="${TEST_DIR}/temporary-file.txt"
DOCKER_LOG="${TEST_DIR}/docker.log"
DD_LOG="${TEST_DIR}/dd.log"
DD_METADATA_LOG="${TEST_DIR}/dd-metadata.log"
FALLOCATE_LOG="${TEST_DIR}/fallocate.log"
export OBSERVATION_FILE DOCKER_LOG DD_LOG DD_METADATA_LOG FALLOCATE_LOG
REAL_DD="$(command -v dd)"
export REAL_DD
export RIVUNE_BACKUP_STAGING_DIR="${TEST_DIR}/staging"
cat > "${TEST_DIR}/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${DOCKER_LOG}"
invocation="$*"
if [[ " $* " == *" pg_dump "* ]]; then
  if [[ -e "/proc/$$/fd/1" ]]; then
    target="$(readlink "/proc/$$/fd/1")"
  elif command -v lsof >/dev/null 2>&1; then
    target="$(lsof -a -p "$$" -d 1 -Fn | sed -n 's/^n//p')"
  else
    echo 'Cannot resolve stdout descriptor target' >&2
    exit 1
  fi
  printf '%s\n%s\n%s\n' "${target}" "$(stat -c '%a' -- "${target}")" \
    "$(stat -c '%F' -- "${target}")" > "${OBSERVATION_FILE}"
  sleep "${FAKE_DUMP_DELAY:-0.05}"
  printf 'test-postgres-custom-archive-%s' "${target##*/}"
  exit 0
fi
if [[ " $* " == *" compose exec "*" pg_restore --list "* ]]; then
  test -n "$(cat)"
  exit 0
fi
if [[ "$1" == "run" || "$1" == "cp" || "$1" == "rm" ]]; then
  exit 0
fi
if [[ "$1" == "compose" && "$2" == "ps" ]]; then
  [[ "${FAKE_SERVER_RUNNING:-0}" != "1" ]] || printf 'rivune\n'
  exit 0
fi
if [[ "$1" == "compose" && "$2" == "stop" ]]; then
  exit 0
fi
if [[ "$1" == "compose" && "$2" == "up" ]]; then
  if [[ "${FAKE_START_FAIL:-0}" == 1 ]]; then
    exit 1
  fi
  if [[ "${FAKE_START_FAIL_ONCE:-0}" == 1 ]] &&
     [[ "$(grep -c '^compose up ' "${DOCKER_LOG}")" == 1 ]]; then
    exit 1
  fi
  exit 0
fi
if [[ " $* " == *" pg_isready "* ]]; then
  exit 0
fi
if [[ " $* " == *" psql "* ]]; then
  sql="$(cat)"
  if [[ -n "${sql}" ]]; then
    printf 'SQL %s\n' "$(printf '%s' "${sql}" | tr '\n' ' ')" >> "${DOCKER_LOG}"
  fi
  # rivune_owner owns the databases but is intentionally NOCREATEDB. Renames
  # must run as rivune_restore, whose CREATEDB plus inherited ownership permits
  # them; the mock rejects the production-invalid privilege combination.
  if [[ "${sql}" == *'SET ROLE rivune_owner;'* &&
        "${sql}" == *'ALTER DATABASE '* ]]; then
    exit 1
  fi
  if [[ "${FAKE_SWAP_FAIL:-0}" == 1 &&
        "${sql}" == *'ALTER DATABASE :"live_database" RENAME TO :"prior_database";'* ]]; then
    exit 1
  fi
  if [[ "${FAKE_ROLLBACK_FAIL:-0}" == 1 &&
        "${sql}" == *"format('ALTER DATABASE %I RENAME TO %I', :'live_database', :'staging_database')"* ]]; then
    exit 1
  fi
  printf 'verified\n'
  exit 0
fi
if [[ " $* " == *" pg_restore "* ]]; then
  [[ "${FAKE_RESTORE_FAIL:-0}" != "1" ]] || exit 1
  exit 0
fi
exit 2
FAKE_DOCKER
chmod 700 "${TEST_DIR}/bin/docker"
cat > "${TEST_DIR}/bin/dd" <<'FAKE_DD'
#!/usr/bin/env bash
set -euo pipefail
source_path=""
destination_path=""
for argument in "$@"; do
  case "${argument}" in
    if=*) source_path="${argument#if=}" ;;
    of=*) destination_path="${argument#of=}" ;;
  esac
done
if [[ -n "${FAKE_DD_TARGET:-}" && "${source_path}" == "${FAKE_DD_TARGET}" ]]; then
  mv -f -- "${FAKE_DD_REPLACEMENT}" "${source_path}"
fi
"${REAL_DD}" "$@"
if [[ "${destination_path}" == *rivune-authenticated-backup.* ]]; then
  printf 'source=%s destination=%s size=%s\n' "${source_path}" "${destination_path}" \
    "$(stat -c '%s' -- "${destination_path}")" >> "${DD_LOG}"
elif [[ "${destination_path}" == *rivune-authenticated-manifest.* || \
        "${destination_path}" == *rivune-authenticated-signature.* ]]; then
  printf 'source=%s destination=%s size=%s\n' "${source_path}" "${destination_path}" \
    "$(stat -c '%s' -- "${destination_path}")" >> "${DD_METADATA_LOG}"
fi
FAKE_DD
chmod 700 "${TEST_DIR}/bin/dd"

cat > "${TEST_DIR}/bin/fallocate" <<'FAKE_FALLOCATE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FALLOCATE_LOG}"
if [[ "${FAKE_FALLOCATE_UNAVAILABLE:-0}" == 1 ]]; then
  exit 127
fi
if [[ "${FAKE_FALLOCATE_FAIL:-0}" == 1 ]]; then
  exit 1
fi
exit 0
FAKE_FALLOCATE
chmod 700 "${TEST_DIR}/bin/fallocate"

PRIVATE_KEY="${TEST_DIR}/keys/backup-signing-2026.pem"
PUBLIC_KEY="${TEST_DIR}/keys/backup-verification-2026.pem"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
  -out "${PRIVATE_KEY}" >/dev/null 2>&1
openssl pkey -in "${PRIVATE_KEY}" -pubout -out "${PUBLIC_KEY}" >/dev/null 2>&1
chmod 600 "${PRIVATE_KEY}" "${PUBLIC_KEY}"

LINEAGE="deployment-7f0d8d66"
STATE_FILE="${TEST_DIR}/state/backup.state"
export RIVUNE_BACKUP_LINEAGE="${LINEAGE}"
export RIVUNE_BACKUP_STATE_FILE="${STATE_FILE}"

manifest_value() {
  local manifest="$1"
  local wanted="$2"
  local key value
  while IFS='=' read -r key value; do
    if [[ "${key}" == "${wanted}" ]]; then
      printf '%s' "${value}"
      return 0
    fi
  done < "${manifest}"
  return 1
}

create_backup() {
  PATH="${TEST_DIR}/bin:${PATH}" \
    RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
    "${ROOT_DIR}/scripts/postgres-backup.sh" "$1" >/dev/null
}

assert_rejected_before_docker() {
  : > "${DOCKER_LOG}"
  if PATH="${TEST_DIR}/bin:${PATH}" \
    RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
    RIVUNE_RESTORE_PASSWORD="not-visible" \
    "${ROOT_DIR}/scripts/postgres-restore.sh" "$@" >/dev/null 2>&1; then
    echo "restore accepted a rejected backup policy case: $*" >&2
    exit 1
  fi
  if [[ -s "${DOCKER_LOG}" ]]; then
    echo "restore reached Docker before authentication and freshness policy" >&2
    exit 1
  fi
}

BACKUP_ONE="${TEST_DIR}/secure/rivune-1.dump"
VICTIM_FILE="${TEST_DIR}/victim"
printf 'must-not-change' > "${VICTIM_FILE}"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${BACKUP_ONE}" >/dev/null &
backup_pid=$!
ln -s -- "${VICTIM_FILE}" "${TEST_DIR}/secure/.rivune-1.dump.partial.${backup_pid}"
wait "${backup_pid}"

if [[ "$(cat "${VICTIM_FILE}")" != 'must-not-change' ]]; then
  echo "backup followed a predictable temporary-file symlink" >&2
  exit 1
fi
mapfile -t observation < "${OBSERVATION_FILE}"
if [[ "${observation[0]}" != "${TEST_DIR}/secure/.rivune-1.dump.partial."???????? ]]; then
  echo "unexpected backup temporary path" >&2
  exit 1
fi
if [[ "${observation[1]}" != 600 || "${observation[2]}" != regular*file ]]; then
  echo "backup temporary file was not a private regular file" >&2
  exit 1
fi
for private_file in "${BACKUP_ONE}" "${BACKUP_ONE}.manifest" "${BACKUP_ONE}.sig" "${STATE_FILE}"; do
  if [[ "$(stat -c '%a' -- "${private_file}")" != 600 ]]; then
    echo "backup authentication material was not mode 600" >&2
    exit 1
  fi
done
openssl dgst -sha256 -verify "${PUBLIC_KEY}" \
  -signature "${BACKUP_ONE}.sig" "${BACKUP_ONE}.manifest" >/dev/null
ID_ONE="$(manifest_value "${BACKUP_ONE}.manifest" backup_id)"
DIGEST_ONE="$(manifest_value "${BACKUP_ONE}.manifest" archive_sha256)"
[[ "$(manifest_value "${BACKUP_ONE}.manifest" sequence)" == 1 ]]
[[ "$(manifest_value "${BACKUP_ONE}.manifest" lineage)" == "${LINEAGE}" ]]
[[ "$(manifest_value "${BACKUP_ONE}.manifest" archive_name)" == "$(basename "${BACKUP_ONE}")" ]]
[[ "$(openssl dgst -sha256 -r "${BACKUP_ONE}" | cut -d ' ' -f 1)" == "${DIGEST_ONE}" ]]
[[ "$(manifest_value "${BACKUP_ONE}.manifest" format)" == rivune-backup-manifest-v2 ]]
[[ "$(manifest_value "${BACKUP_ONE}.manifest" archive_size)" == "$(stat -c '%s' -- "${BACKUP_ONE}")" ]]
[[ "$(wc -l < "${BACKUP_ONE}.manifest")" == 9 ]]

BACKUP_TWO="${TEST_DIR}/secure/rivune-2.dump"
create_backup "${BACKUP_TWO}"
ID_TWO="$(manifest_value "${BACKUP_TWO}.manifest" backup_id)"
[[ "$(manifest_value "${BACKUP_TWO}.manifest" sequence)" == 2 ]]
[[ "${ID_ONE}" != "${ID_TWO}" ]]

# No restore is possible without an operator expectation. N-1 is rejected even
# with its exact ID because trusted state records generation 2. Both the protected
# state replay decision and a wrong external ID precede archive staging.
assert_rejected_before_docker "${BACKUP_TWO}"
: > "${DD_LOG}"
assert_rejected_before_docker --expect-backup-id "${ID_ONE}" "${BACKUP_ONE}"
[[ ! -s "${DD_LOG}" ]]
: > "${DD_LOG}"
assert_rejected_before_docker --expect-backup-id "${ID_ONE}" "${BACKUP_TWO}"
[[ ! -s "${DD_LOG}" ]]
: > "${DD_LOG}"
assert_rejected_before_docker --allow-rollback "${ID_TWO}" "${BACKUP_TWO}"
[[ ! -s "${DD_LOG}" ]]
: > "${DD_LOG}"
assert_rejected_before_docker --initialize-state "${ID_TWO}" "${BACKUP_TWO}"
[[ ! -s "${DD_LOG}" && ! -e "${STATE_FILE}.audit" ]]
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" \
  --expect-backup-id "${ID_ONE}" "${BACKUP_TWO}" >/dev/null 2>&1; then
  echo "disposable verification ignored the external backup ID expectation" >&2
  exit 1
fi
if [[ -s "${DOCKER_LOG}" ]]; then
  echo "disposable verification reached Docker before checking the external backup ID" >&2
  exit 1
fi
if [[ -s "${DD_LOG}" ]]; then
  echo "disposable verification staged archive bytes before checking the external backup ID" >&2
  exit 1
fi

# Replaying the complete older authenticated triple at a different pathname also
# fails because the signed canonical archive name does not match.
REPLAY="${TEST_DIR}/secure/rivune-2-replayed.dump"
cp -- "${BACKUP_ONE}" "${REPLAY}"
cp -- "${BACKUP_ONE}.manifest" "${REPLAY}.manifest"
cp -- "${BACKUP_ONE}.sig" "${REPLAY}.sig"
assert_rejected_before_docker --expect-backup-id "${ID_ONE}" "${REPLAY}"

# Archive bytes, signed metadata, and detached signatures each fail closed before Docker.
mkdir "${TEST_DIR}/tamper"
chmod 700 "${TEST_DIR}/tamper"
TAMPERED="${TEST_DIR}/tamper/$(basename "${BACKUP_TWO}")"
cp -- "${BACKUP_TWO}" "${TAMPERED}"
cp -- "${BACKUP_TWO}.manifest" "${TAMPERED}.manifest"
cp -- "${BACKUP_TWO}.sig" "${TAMPERED}.sig"
# A metadata pathname race is bounded to the configured maximum plus one byte.
cp -- "${TAMPERED}.manifest" "${TAMPERED}.manifest.oversize"
truncate -s 5000 "${TAMPERED}.manifest.oversize"
: > "${DD_METADATA_LOG}"
FAKE_DD_TARGET="${TAMPERED}.manifest" \
FAKE_DD_REPLACEMENT="${TAMPERED}.manifest.oversize" \
  assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
if [[ "$(cut -d= -f4 "${DD_METADATA_LOG}")" != 4097 ]]; then
  echo "oversize manifest staging did not stop at the metadata limit plus one" >&2
  exit 1
fi
# A forged signed-size field fails manifest authentication before archive dd.
cp -- "${BACKUP_TWO}.manifest" "${TAMPERED}.manifest"
: > "${DD_LOG}"
sed 's/^archive_size=.*/archive_size=1/' "${TAMPERED}.manifest" > "${TAMPERED}.manifest.new"
mv -f -- "${TAMPERED}.manifest.new" "${TAMPERED}.manifest"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
[[ ! -s "${DD_LOG}" ]]
cp -- "${BACKUP_TWO}.manifest" "${TAMPERED}.manifest"
cp -- "${BACKUP_TWO}.sig" "${TAMPERED}.sig"
printf 'tampered' >> "${TAMPERED}"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
cp -- "${BACKUP_TWO}" "${TAMPERED}"
truncate -s "$(( $(stat -c '%s' -- "${BACKUP_TWO}") - 1 ))" "${TAMPERED}"
: > "${DD_LOG}"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
[[ ! -s "${DD_LOG}" ]]
cp -- "${BACKUP_TWO}" "${TAMPERED}"
printf '\narchive_name=forged.dump\n' >> "${TAMPERED}.manifest"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
cp -- "${BACKUP_TWO}.manifest" "${TAMPERED}.manifest"
printf 'tampered' >> "${TAMPERED}.sig"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"

# Same-size replacement reaches only the bounded snapshot and fails its signed digest.
cp -- "${BACKUP_TWO}" "${TAMPERED}"
cp -- "${BACKUP_TWO}.manifest" "${TAMPERED}.manifest"
cp -- "${BACKUP_TWO}.sig" "${TAMPERED}.sig"
printf X | "${REAL_DD}" of="${TAMPERED}" bs=1 seek=0 conv=notrunc status=none
: > "${DD_LOG}"
assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${TAMPERED}"
if [[ "$(cut -d= -f4 "${DD_LOG}")" != "$(stat -c '%s' -- "${BACKUP_TWO}")" ]]; then
  echo "same-size tamper was not confined to the authenticated-size snapshot" >&2
  exit 1
fi
TAMPER_RECOVERY_STATE="${TEST_DIR}/state/tampered-recovery.state"
RIVUNE_BACKUP_STATE_FILE="${TAMPER_RECOVERY_STATE}" \
  assert_rejected_before_docker --initialize-state "${ID_TWO}" "${TAMPERED}"
if [[ -e "${TAMPER_RECOVERY_STATE}" || -e "${TAMPER_RECOVERY_STATE}.audit" ]]; then
  echo "state initialization mutated trusted state before archive authentication" >&2
  exit 1
fi

# A pathname swap after the optimistic stat can expose a longer regular or sparse
# file, but byte-count dd writes exactly authenticated-size+1 and then rejects it.
RACE_DIR="${TEST_DIR}/race"
mkdir "${RACE_DIR}"
chmod 700 "${RACE_DIR}"
RACE="${RACE_DIR}/$(basename "${BACKUP_TWO}")"
for suffix in "" .manifest .sig; do
  cp -- "${BACKUP_TWO}${suffix}" "${RACE}${suffix}"
done
EXPECTED_SIZE="$(stat -c '%s' -- "${BACKUP_TWO}")"
cp -- "${BACKUP_TWO}" "${RACE}.long"
printf x >> "${RACE}.long"
: > "${DD_LOG}"
FAKE_DD_TARGET="${RACE}" FAKE_DD_REPLACEMENT="${RACE}.long" \
  assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${RACE}"
if [[ "$(cut -d= -f4 "${DD_LOG}")" != "$(( EXPECTED_SIZE + 1 ))" ]]; then
  echo "oversize race wrote more or less than authenticated-size+1" >&2
  exit 1
fi
cp -- "${BACKUP_TWO}" "${RACE}"
truncate -s 1099511627776 "${RACE}.sparse"
: > "${DD_LOG}"
FAKE_DD_TARGET="${RACE}" FAKE_DD_REPLACEMENT="${RACE}.sparse" \
  assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${RACE}"
if [[ "$(cut -d= -f4 "${DD_LOG}")" != "$(( EXPECTED_SIZE + 1 ))" ]]; then
  echo "sparse TOCTOU source escaped the authenticated-size+1 bound" >&2
  exit 1
fi

if command -v mkfile >/dev/null 2>&1; then
  : > "${DD_LOG}"
  PATH="${TEST_DIR}/bin:${PATH}" \
    FAKE_FALLOCATE_UNAVAILABLE=1 \
    RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
    "${ROOT_DIR}/scripts/postgres-verify-backup.sh" \
    --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null
  if [[ "$(cut -d= -f4 "${DD_LOG}")" != "$(( EXPECTED_SIZE + 1 ))" ]]; then
    echo "macOS mkfile fallback did not stage the authenticated archive" >&2
    exit 1
  fi
fi

# Physical reservation is fail-closed and happens before archive dd or Docker.
: > "${DD_LOG}"
: > "${FALLOCATE_LOG}"
FAKE_FALLOCATE_FAIL=1 \
  assert_rejected_before_docker --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}"
if ! grep -q -- "--length $(( EXPECTED_SIZE + 1 )) --" "${FALLOCATE_LOG}" || \
   [[ -s "${DD_LOG}" ]]; then
  echo "archive staging did not fail before dd when fallocate failed" >&2
  exit 1
fi
UNSAFE_STAGING_PARENT="${TEST_DIR}/unsafe-staging-parent"
UNSAFE_STAGING="${UNSAFE_STAGING_PARENT}/private"
mkdir -p "${UNSAFE_STAGING}"
chmod 777 "${UNSAFE_STAGING_PARENT}"
chmod 700 "${UNSAFE_STAGING}"
: > "${DOCKER_LOG}"
: > "${DD_LOG}"
: > "${DD_METADATA_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_STAGING_DIR='' \
  TMPDIR="${UNSAFE_STAGING}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="not-visible" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" \
  --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null 2>&1; then
  echo "restore accepted TMPDIR below an unsafe ancestor" >&2
  exit 1
fi
if [[ -s "${DOCKER_LOG}" || -s "${DD_LOG}" || -s "${DD_METADATA_LOG}" ]]; then
  echo "TMPDIR below an unsafe ancestor was used for authenticated staging" >&2
  exit 1
fi

# The exact current generation succeeds. Restore secrets are inherited by name,
# never expanded into Docker argv or the fake Docker audit log. The archive is
# restored and ledger-checked in staging before Rivune stops; only then are the
# database names swapped, readiness checked, and the retained prior copy dropped.
: > "${DOCKER_LOG}"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="secret-must-not-appear" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null
if grep -q -- 'secret-must-not-appear' "${DOCKER_LOG}"; then
  echo "restore secret appeared in Docker argv" >&2
  exit 1
fi
if ! grep -q -- 'pg_restore .*--username rivune_restore .*--role rivune_owner .*--dbname rivune_restore_staging' "${DOCKER_LOG}" || \
   grep -q -- 'pg_restore .*--username postgres' "${DOCKER_LOG}"; then
  echo "production restore did not stage with the non-superuser restore role" >&2
  exit 1
fi
staging_restore_line="$(grep -n -m1 -- 'pg_restore .*--dbname rivune_restore_staging' "${DOCKER_LOG}" | cut -d: -f1)"
ledger_line="$(grep -n -m1 -- "psql .*--dbname rivune_restore_staging .*schema_migrations" "${DOCKER_LOG}" | cut -d: -f1)"
stop_line="$(grep -n -m1 -- 'compose stop rivune' "${DOCKER_LOG}" | cut -d: -f1)"
swap_line="$(grep -n -m1 -- 'SQL .*ALTER DATABASE :"live_database" RENAME TO :"prior_database"' "${DOCKER_LOG}" | cut -d: -f1)"
drop_prior_line="$(grep -n -m1 -- 'SQL .*DROP DATABASE :"prior_database"' "${DOCKER_LOG}" | cut -d: -f1)"
if [[ -z "${staging_restore_line}" || -z "${ledger_line}" || -z "${stop_line}" || \
      -z "${swap_line}" || -z "${drop_prior_line}" ]] ||
   ! (( staging_restore_line < ledger_line && ledger_line < stop_line &&
        stop_line < swap_line && swap_line < drop_prior_line )); then
  echo "successful restore did not validate, swap, and retire the prior database in order" >&2
  exit 1
fi

# A restore failure before the swap leaves the running service and live database
# untouched and removes only the inactive staging database.
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" \
  FAKE_SERVER_RUNNING=1 \
  FAKE_RESTORE_FAIL=1 \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="failed-restore-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" \
  --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null 2>&1; then
  echo "production restore ignored a controlled staging failure" >&2
  exit 1
fi
if grep -q -- 'compose stop rivune\|RENAME TO :"prior_database"\|compose up ' "${DOCKER_LOG}" ||
   ! grep -q -- 'DROP DATABASE IF EXISTS :"staging_database"' "${DOCKER_LOG}"; then
  echo "pre-swap restore failure disturbed the live database or service" >&2
  exit 1
fi

# Readiness failure after the swap automatically restores the retained database,
# removes the failed staging copy, and waits for the prior service to become ready.
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" \
  FAKE_SERVER_RUNNING=1 \
  FAKE_START_FAIL_ONCE=1 \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="failed-start-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" \
  --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null 2>&1; then
  echo "production restore ignored a controlled readiness failure" >&2
  exit 1
fi
if [[ "$(grep -c '^compose up -d --wait --wait-timeout 90 rivune$' "${DOCKER_LOG}")" != 2 ]]; then
  echo "readiness rollback did not perform the failed and recovery starts" >&2
  exit 1
fi
for expected_command in \
  "format('ALTER DATABASE %I RENAME TO %I', :'live_database', :'staging_database')" \
  'ALTER DATABASE :"prior_database" RENAME TO :"live_database"' \
  'DROP DATABASE IF EXISTS :"staging_database"'; do
  if ! grep -Fq -- "${expected_command}" "${DOCKER_LOG}"; then
    echo "readiness rollback omitted: ${expected_command}" >&2
    exit 1
  fi
done
if grep -Fq -- 'DROP DATABASE :"prior_database"' "${DOCKER_LOG}"; then
  echo "readiness rollback dropped the retained prior database" >&2
  exit 1
fi

# If the database-name rollback itself fails, Rivune remains stopped, the prior
# database is retained under its reserved name, and no destructive cleanup runs.
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" \
  FAKE_SERVER_RUNNING=1 \
  FAKE_START_FAIL_ONCE=1 \
  FAKE_ROLLBACK_FAIL=1 \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="failed-rollback-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" \
  --expect-backup-id "${ID_TWO}" "${BACKUP_TWO}" >/dev/null 2>&1; then
  echo "production restore ignored a controlled rollback failure" >&2
  exit 1
fi
if [[ "$(grep -c '^compose up -d --wait --wait-timeout 90 rivune$' "${DOCKER_LOG}")" != 1 ]] ||
   [[ "$(grep -Fc -- 'DROP DATABASE IF EXISTS :"staging_database"' "${DOCKER_LOG}")" != 1 ]] ||
   grep -Fq -- 'DROP DATABASE :"prior_database"' "${DOCKER_LOG}"; then
  echo "rollback failure did not retain the prior database with Rivune stopped" >&2
  exit 1
fi

# Rollback uses a separate explicit verb plus the exact old ID and records both
# authorization and completion without lowering the current trusted generation.
: > "${DOCKER_LOG}"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="rollback-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --allow-rollback "${ID_ONE}" "${BACKUP_ONE}" >/dev/null
if ! grep -q -- "action=rollback-authorized .*backup_id=${ID_ONE}" "${STATE_FILE}.audit" || \
   ! grep -q -- "action=rollback-completed .*backup_id=${ID_ONE}" "${STATE_FILE}.audit" || \
   [[ "$(manifest_value "${STATE_FILE}" latest_backup_id)" != "${ID_TWO}" ]]; then
  echo "explicit rollback was not audited without lowering trusted state" >&2
  exit 1
fi
if [[ "$(stat -c '%a' -- "${STATE_FILE}.audit")" != 600 ]]; then
  echo "restore audit log was not mode 600" >&2
  exit 1
fi
assert_rejected_before_docker --expect-backup-id "${ID_ONE}" "${BACKUP_ONE}"

# Detached legacy and manifested v1 archives have no authenticated size. They
# fail before archive dd unless an explicit conservative cap is supplied.
LEGACY="${TEST_DIR}/secure/legacy.dump"
printf 'legacy-postgres-custom-archive' > "${LEGACY}"
openssl dgst -sha256 -sign "${PRIVATE_KEY}" -out "${LEGACY}.sig" "${LEGACY}"
LEGACY_DIGEST="$(openssl dgst -sha256 -r "${LEGACY}" | cut -d ' ' -f 1)"
LEGACY_SIZE="$(stat -c '%s' -- "${LEGACY}")"
chmod 600 "${LEGACY}" "${LEGACY}.sig"
: > "${DD_LOG}"
assert_rejected_before_docker --allow-legacy "${LEGACY_DIGEST}" "${LEGACY}"
[[ ! -s "${DD_LOG}" ]]
RIVUNE_BACKUP_LEGACY_MAX_BYTES="${LEGACY_SIZE}" \
  assert_rejected_before_docker --allow-legacy "$(printf '0%.0s' {1..64})" "${LEGACY}"

printf x >> "${LEGACY}"
: > "${DD_LOG}"
RIVUNE_BACKUP_LEGACY_MAX_BYTES="${LEGACY_SIZE}" \
  assert_rejected_before_docker --allow-legacy "${LEGACY_DIGEST}" "${LEGACY}"
if [[ -s "${DD_LOG}" ]]; then
  echo "legacy oversize archive was opened after its explicit cap was exceeded" >&2
  exit 1
fi
truncate -s "${LEGACY_SIZE}" "${LEGACY}"
printf 'legacy-postgres-custom-archive' > "${LEGACY}"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_LEGACY_MAX_BYTES="${LEGACY_SIZE}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="legacy-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --allow-legacy "${LEGACY_DIGEST}" "${LEGACY}" >/dev/null
if ! grep -q -- "action=legacy-completed .*sha256=${LEGACY_DIGEST}" "${STATE_FILE}.audit"; then
  echo "legacy restore authorization was not audited" >&2
  exit 1
fi

V1="${TEST_DIR}/secure/v1.dump"
cp -- "${BACKUP_TWO}" "${V1}"
cp -- "${BACKUP_TWO}.manifest" "${V1}.manifest"
sed \
  -e 's/^format=rivune-backup-manifest-v2$/format=rivune-backup-manifest-v1/' \
  -e "s/^archive_name=.*/archive_name=$(basename "${V1}")/" \
  -e '/^archive_size=/d' "${V1}.manifest" > "${V1}.manifest.new"
mv -f -- "${V1}.manifest.new" "${V1}.manifest"
openssl dgst -sha256 -sign "${PRIVATE_KEY}" -out "${V1}.sig" "${V1}.manifest"
chmod 600 "${V1}" "${V1}.manifest" "${V1}.sig"
: > "${DOCKER_LOG}"
: > "${DD_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" \
  --expect-backup-id "${ID_TWO}" "${V1}" >/dev/null 2>&1; then
  echo "v1 manifest was accepted without an explicit legacy cap" >&2
  exit 1
fi
[[ ! -s "${DOCKER_LOG}" && ! -s "${DD_LOG}" ]]
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_LEGACY_MAX_BYTES="$(stat -c '%s' -- "${V1}")" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" \
  --expect-backup-id "${ID_TWO}" "${V1}" >/dev/null

# Concurrent reservations are unique and the highest completed sequence remains
# the only normally restorable generation.
CONCURRENT_A="${TEST_DIR}/secure/concurrent-a.dump"
CONCURRENT_B="${TEST_DIR}/secure/concurrent-b.dump"
PATH="${TEST_DIR}/bin:${PATH}" FAKE_DUMP_DELAY=0.2 \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${CONCURRENT_A}" >/dev/null &
pid_a=$!
PATH="${TEST_DIR}/bin:${PATH}" FAKE_DUMP_DELAY=0.2 \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${CONCURRENT_B}" >/dev/null &
pid_b=$!
wait "${pid_a}"
wait "${pid_b}"
SEQ_A="$(manifest_value "${CONCURRENT_A}.manifest" sequence)"
SEQ_B="$(manifest_value "${CONCURRENT_B}.manifest" sequence)"
ID_A="$(manifest_value "${CONCURRENT_A}.manifest" backup_id)"
ID_B="$(manifest_value "${CONCURRENT_B}.manifest" backup_id)"
[[ "${SEQ_A}" != "${SEQ_B}" && "${ID_A}" != "${ID_B}" ]]
if (( SEQ_A > SEQ_B )); then
  CURRENT_FILE="${CONCURRENT_A}"
  CURRENT_ID="${ID_A}"
  OLD_FILE="${CONCURRENT_B}"
  OLD_ID="${ID_B}"
else
  CURRENT_FILE="${CONCURRENT_B}"
  CURRENT_ID="${ID_B}"
  OLD_FILE="${CONCURRENT_A}"
  OLD_ID="${ID_A}"
fi
assert_rejected_before_docker --expect-backup-id "${OLD_ID}" "${OLD_FILE}"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  RIVUNE_RESTORE_PASSWORD="concurrent-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --expect-backup-id "${CURRENT_ID}" "${CURRENT_FILE}" >/dev/null

# Same-target publishers elect one winner with no-replace links. The loser fails,
# and the winner's archive, manifest, signature, and trusted state remain coherent.
SAME_TARGET="${TEST_DIR}/secure/concurrent-same.dump"
PATH="${TEST_DIR}/bin:${PATH}" FAKE_DUMP_DELAY=0.2 \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${SAME_TARGET}" >/dev/null 2>&1 &
same_pid_a=$!
PATH="${TEST_DIR}/bin:${PATH}" FAKE_DUMP_DELAY=0.2 \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${SAME_TARGET}" >/dev/null 2>&1 &
same_pid_b=$!
if wait "${same_pid_a}"; then same_status_a=0; else same_status_a=$?; fi
if wait "${same_pid_b}"; then same_status_b=0; else same_status_b=$?; fi
if ! { [[ "${same_status_a}" == 0 && "${same_status_b}" != 0 ]] || \
       [[ "${same_status_a}" != 0 && "${same_status_b}" == 0 ]]; }; then
  echo "same-target concurrent backup did not produce exactly one winner" >&2
  exit 1
fi
openssl dgst -sha256 -verify "${PUBLIC_KEY}" \
  -signature "${SAME_TARGET}.sig" "${SAME_TARGET}.manifest" >/dev/null
SAME_ID="$(manifest_value "${SAME_TARGET}.manifest" backup_id)"
SAME_DIGEST="$(manifest_value "${SAME_TARGET}.manifest" archive_sha256)"
if [[ "$(openssl dgst -sha256 -r "${SAME_TARGET}" | cut -d ' ' -f 1)" != "${SAME_DIGEST}" || \
      "$(manifest_value "${STATE_FILE}" latest_backup_id)" != "${SAME_ID}" ]]; then
  echo "same-target concurrent publication left incoherent trusted material" >&2
  exit 1
fi

# Key rotation is explicit: a new manifest identifies and verifies only under the
# selected new public key, while older signed archives remain usable via rollback.
PRIVATE_KEY_2="${TEST_DIR}/keys/backup-signing-2027.pem"
PUBLIC_KEY_2="${TEST_DIR}/keys/backup-verification-2027.pem"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${PRIVATE_KEY_2}" >/dev/null 2>&1
openssl pkey -in "${PRIVATE_KEY_2}" -pubout -out "${PUBLIC_KEY_2}" >/dev/null 2>&1
chmod 600 "${PRIVATE_KEY_2}" "${PUBLIC_KEY_2}"
ROTATED="${TEST_DIR}/secure/rotated.dump"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY_2}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${ROTATED}" >/dev/null
ROTATED_ID="$(manifest_value "${ROTATED}.manifest" backup_id)"
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}" >/dev/null 2>&1; then
  echo "rotated backup verified with the retired public key" >&2
  exit 1
fi
[[ ! -s "${DOCKER_LOG}" ]]
PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY_2}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}" >/dev/null
if grep -Eq -- '-e (POSTGRES_PASSWORD|RIVUNE_DATABASE_PASSWORD|RIVUNE_RESTORE_PASSWORD|PGPASSWORD)=' \
  "${DOCKER_LOG}"; then
  echo "disposable verification exposed a generated password in Docker argv" >&2
  exit 1
fi
if ! grep -q -- '--network none' "${DOCKER_LOG}" || \
   ! grep -q -- 'pg_restore .*--username rivune_restore --role rivune_owner' "${DOCKER_LOG}" || \
   grep -q -- 'pg_restore .*--username postgres' "${DOCKER_LOG}"; then
  echo "disposable verification lost its egress or non-superuser constraints" >&2
  exit 1
fi

# State bootstrap on a recovery host is explicit and binds the exact external ID.
RECOVERY_STATE="${TEST_DIR}/state/recovery.state"
PATH="${TEST_DIR}/bin:${PATH}" \
  RIVUNE_BACKUP_STATE_FILE="${RECOVERY_STATE}" \
  RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY_2}" \
  RIVUNE_RESTORE_PASSWORD="recovery-secret" \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --initialize-state "${ROTATED_ID}" "${ROTATED}" >/dev/null
if ! grep -q -- "action=state-initialized .*backup_id=${ROTATED_ID}" "${RECOVERY_STATE}.audit"; then
  echo "recovery state bootstrap was not audited" >&2
  exit 1
fi

chmod 644 "${PUBLIC_KEY_2}"
: > "${DOCKER_LOG}"
if PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY_2}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}" >/dev/null 2>&1; then
  echo "verification accepted an overly permissive key" >&2
  exit 1
fi
[[ ! -s "${DOCKER_LOG}" ]]

mkdir "${TEST_DIR}/shared"
chmod 777 "${TEST_DIR}/shared"
if PATH="${TEST_DIR}/bin:${PATH}" RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}" \
  "${ROOT_DIR}/scripts/postgres-backup.sh" "${TEST_DIR}/shared/unsafe.dump" >/dev/null 2>&1; then
  echo "backup accepted an attacker-writable destination directory" >&2
  exit 1
fi
if compgen -G "${RIVUNE_BACKUP_STAGING_DIR}/*" >/dev/null; then
  echo "authenticated staging files were not cleaned up" >&2
  exit 1
fi

printf 'postgres backup v2, bounded staging, compatibility, and replay checks passed\n'
