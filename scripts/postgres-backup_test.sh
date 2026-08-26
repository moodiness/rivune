#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
TEST_DIR="$(cd "${TEST_DIR}" && pwd -P)"
cleanup() { rm -rf -- "${TEST_DIR}"; }
trap cleanup EXIT INT TERM

mkdir -p "${TEST_DIR}/bin" "${TEST_DIR}/backups" "${TEST_DIR}/keys" \
  "${TEST_DIR}/state" "${TEST_DIR}/staging"
chmod 700 "${TEST_DIR}"/{bin,backups,keys,state,staging}
export DOCKER_LOG="${TEST_DIR}/docker.log"
export AGE_LOG="${TEST_DIR}/age.log"
: > "${DOCKER_LOG}"
for utility in realpath stat dd truncate; do
  gutility="$(command -v "g${utility}" || true)"
  [[ -z "${gutility}" ]] || ln -s "${gutility}" "${TEST_DIR}/bin/${utility}"
done
: > "${AGE_LOG}"

cat > "${TEST_DIR}/bin/age-keygen" <<'FAKE_AGE_KEYGEN'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == -y && $# == 2 ]]
identity="$(sed -e '/^#/d' -e '/^$/d' "$2")"
case "${identity}" in
  AGE-SECRET-KEY-key-a) printf 'age1recipienta\n' ;;
  AGE-SECRET-KEY-key-b) printf 'age1recipientb\n' ;;
  *) exit 1 ;;
esac
FAKE_AGE_KEYGEN

cat > "${TEST_DIR}/bin/age" <<'FAKE_AGE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${AGE_LOG}"
if [[ "$1" == --encrypt && "$2" == --recipients-file && $# == 3 ]]; then
  recipient="$(sed -e '/^#/d' -e '/^$/d' "$3")"
  nonce="$(openssl rand -hex 16)"
  key="$(printf '%s:%s' "${recipient}" "${nonce}" | openssl dgst -sha256 -r | cut -d ' ' -f 1)"
  printf 'age-encryption.org/v1\nrecipient:%s\nnonce:%s\n' "${recipient}" "${nonce}"
  openssl enc -aes-256-ctr -K "${key}" -iv 00000000000000000000000000000000
  exit
fi
if [[ "$1" == --decrypt && "$2" == --identity && $# == 4 ]]; then
  identity="$(sed -e '/^#/d' -e '/^$/d' "$3")"
  case "${identity}" in
    AGE-SECRET-KEY-key-a) expected=age1recipienta ;;
    AGE-SECRET-KEY-key-b) expected=age1recipientb ;;
    *) exit 1 ;;
  esac
  exec 5< "$4"
  IFS= read -r header <&5
  if [[ -n "${FAKE_AGE_BLOCK_FILE:-}" ]]; then
    : > "${FAKE_AGE_BLOCK_FILE}"
    sleep 2
  fi
  IFS= read -r recipient_line <&5
  IFS= read -r nonce_line <&5
  [[ "${header}" == age-encryption.org/v1 ]]
  recipient="${recipient_line#recipient:}"
  nonce="${nonce_line#nonce:}"
  [[ "${recipient}" == "${expected}" && "${nonce}" =~ ^[0-9a-f]{32}$ ]]
  key="$(printf '%s:%s' "${recipient}" "${nonce}" | openssl dgst -sha256 -r | cut -d ' ' -f 1)"
  openssl enc -d -aes-256-ctr -K "${key}" -iv 00000000000000000000000000000000 <&5
  exit
fi
exit 64
FAKE_AGE

cat > "${TEST_DIR}/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${DOCKER_LOG}"
if [[ " $* " == *" pg_dump "* ]]; then
  printf 'PGDMP plaintext-ledger-fragment schema_migrations payload'
  exit
fi
if [[ "$1" == compose && "$2" == ps ]]; then
  [[ "${FAKE_SERVER_RUNNING:-0}" == 1 ]] && printf 'rivune\n'
  exit
fi
if [[ "$1" == compose && "$2" == stop ]]; then exit; fi
if [[ "$1" == compose && "$2" == up ]]; then
  count="$(grep -c '^compose up ' "${DOCKER_LOG}" || true)"
  if [[ "${FAKE_KEYRING_HEALTH_FAIL_ONCE:-0}" == 1 && "${count}" == 1 ]]; then exit 1; fi
  exit
fi
if [[ " $* " == *" pg_restore --list "* ]]; then
  if [[ "$1" == compose || "$1" == exec ]]; then grep -q '^PGDMP '; fi
  exit
fi
if [[ " $* " == *" pg_restore "* ]]; then
  if [[ "$1" == compose || "$1" == exec ]]; then
    grep -q '^PGDMP '
    [[ "${FAKE_RESTORE_FAIL:-0}" != 1 ]]
  fi
  exit
fi
if [[ " $* " == *" psql "* ]]; then
  sql="$(cat)"
  [[ -z "${sql}" ]] || printf 'SQL %s\n' "$(printf '%s' "${sql}" | tr '\n' ' ')" >> "${DOCKER_LOG}"
  printf 'verified\n'
  exit
fi
case "$1" in
  run|rm) exit 0 ;;
  exec)
    if [[ " $* " == *" pg_isready "* ]]; then exit; fi
    ;;
esac
exit 2
FAKE_DOCKER
chmod 700 "${TEST_DIR}/bin/"*

PRIVATE_KEY="${TEST_DIR}/keys/signing.pem"
PUBLIC_KEY="${TEST_DIR}/keys/verify.pem"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${PRIVATE_KEY}" >/dev/null 2>&1
openssl pkey -in "${PRIVATE_KEY}" -pubout -out "${PUBLIC_KEY}" >/dev/null 2>&1
RECIPIENT_A="${TEST_DIR}/keys/recipient-a.txt"
IDENTITY_A="${TEST_DIR}/keys/identity-a.txt"
RECIPIENT_B="${TEST_DIR}/keys/recipient-b.txt"
IDENTITY_B="${TEST_DIR}/keys/identity-b.txt"
printf 'age1recipienta\n' > "${RECIPIENT_A}"
printf 'AGE-SECRET-KEY-key-a\n' > "${IDENTITY_A}"
printf 'age1recipientb\n' > "${RECIPIENT_B}"
printf 'AGE-SECRET-KEY-key-b\n' > "${IDENTITY_B}"
chmod 600 "${TEST_DIR}/keys/"*

export PATH="${TEST_DIR}/bin:${PATH}"
export RIVUNE_BACKUP_LINEAGE=deployment-encrypted
export RIVUNE_BACKUP_STATE_FILE="${TEST_DIR}/state/backup.state"
export RIVUNE_BACKUP_STAGING_DIR="${TEST_DIR}/staging"
export RIVUNE_BACKUP_SIGNING_KEY_FILE="${PRIVATE_KEY}"
export RIVUNE_BACKUP_VERIFY_KEY_FILE="${PUBLIC_KEY}"
export RIVUNE_BACKUP_AGE_RECIPIENT_FILE="${RECIPIENT_A}"
export RIVUNE_BACKUP_AGE_IDENTITY_FILE="${IDENTITY_A}"

manifest_value() {
  local file="$1" key="$2"
  sed -n "s/^${key}=//p" "${file}"
}
create_backup() { "${ROOT_DIR}/scripts/postgres-backup.sh" "$1" >/dev/null; }
verify_backup() {
  local file="$1" id
  id="$(manifest_value "${file}.manifest" backup_id)"
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${id}" "${file}" >/dev/null
}
reject_before_docker() {
  : > "${DOCKER_LOG}"
  if "$@" >/dev/null 2>&1; then
    echo "unexpected acceptance: $*" >&2
    exit 1
  fi
  [[ ! -s "${DOCKER_LOG}" ]] || { echo 'rejection reached Docker' >&2; exit 1; }
}

ONE="${TEST_DIR}/backups/one.age"
TWO="${TEST_DIR}/backups/two.age"
create_backup "${ONE}"
create_backup "${TWO}"
ID_ONE="$(manifest_value "${ONE}.manifest" backup_id)"
ID_TWO="$(manifest_value "${TWO}.manifest" backup_id)"
[[ "$(manifest_value "${ONE}.manifest" format)" == rivune-backup-manifest-v3-age ]]
[[ "$(manifest_value "${TWO}.manifest" sequence)" == 2 ]]
[[ "$(openssl dgst -sha256 -r "${ONE}" | cut -d ' ' -f 1)" == "$(manifest_value "${ONE}.manifest" ciphertext_sha256)" ]]
openssl dgst -sha256 -verify "${PUBLIC_KEY}" -signature "${ONE}.sig" "${ONE}.manifest" >/dev/null
if grep -Fq 'AGE-SECRET-KEY' "${ONE}.manifest" || \
   grep -Fq 'age1recipienta' "${ONE}.manifest"; then
  echo 'manifest disclosed age identity or recipient material' >&2
  exit 1
fi
cmp -s "${ONE}" "${TWO}" && { echo 'age ciphertext was deterministic' >&2; exit 1; }
if grep -aFq 'plaintext-ledger-fragment' "${ONE}"; then
  echo 'published ciphertext contains PostgreSQL plaintext' >&2
  exit 1
fi
if compgen -G "${TEST_DIR}/backups/*.dump" >/dev/null; then
  echo 'normal producer published a plaintext dump pathname' >&2
  exit 1
fi

mkdir "${TEST_DIR}/tamper"
chmod 700 "${TEST_DIR}/tamper"
TAMPER="${TEST_DIR}/tamper/$(basename "${TWO}")"
for suffix in '' .manifest .sig; do cp -- "${TWO}${suffix}" "${TAMPER}${suffix}"; done
printf x >> "${TAMPER}"
reject_before_docker "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TAMPER}"
cp -- "${TWO}" "${TAMPER}"
printf '\nforged=1\n' >> "${TAMPER}.manifest"
reject_before_docker "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TAMPER}"
cp -- "${TWO}.manifest" "${TAMPER}.manifest"
printf x >> "${TAMPER}.sig"
reject_before_docker "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TAMPER}"

# Wrong decryption identity and a replayed older generation fail closed.
reject_before_docker env RIVUNE_BACKUP_AGE_IDENTITY_FILE="${IDENTITY_B}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TWO}"
reject_before_docker env RIVUNE_RESTORE_PASSWORD=secret \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --expect-backup-id "${ID_ONE}" "${ONE}"

# Decrypted output is bounded and every private staging file is removed on failure.
if RIVUNE_BACKUP_MAX_BYTES=16 \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TWO}" \
  >/dev/null 2>&1; then
  echo 'streamed decrypt exceeded its plaintext cap' >&2
  exit 1
fi
if compgen -G "${TEST_DIR}/staging/*" >/dev/null; then
  echo 'failed verification left decrypted or ciphertext staging files' >&2
  exit 1
fi

# Even SIGKILL during a blocked decrypt stream cannot strand plaintext on disk.
KILL_STAGING="${TEST_DIR}/kill-staging"
KILL_MARKER="${TEST_DIR}/age-blocked"
mkdir "${KILL_STAGING}"
chmod 700 "${KILL_STAGING}"
RIVUNE_BACKUP_STAGING_DIR="${KILL_STAGING}" FAKE_AGE_BLOCK_FILE="${KILL_MARKER}" \
  "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ID_TWO}" "${TWO}" \
  >/dev/null 2>&1 &
blocked_pid=$!
for _ in {1..200}; do
  [[ -e "${KILL_MARKER}" ]] && break
  kill -0 "${blocked_pid}" 2>/dev/null || break
  sleep 0.01
done
[[ -e "${KILL_MARKER}" ]] || { echo 'decrypt stream did not reach the blocking marker' >&2; exit 1; }
if compgen -G "${KILL_STAGING}/*dump*" >/dev/null || \
   compgen -G "${KILL_STAGING}/*authenticated-backup*" >/dev/null; then
  echo 'blocked decrypt stream materialized plaintext on disk' >&2
  exit 1
fi
kill -KILL "${blocked_pid}"
wait "${blocked_pid}" 2>/dev/null || true
if compgen -G "${KILL_STAGING}/*dump*" >/dev/null || \
   compgen -G "${KILL_STAGING}/*authenticated-backup*" >/dev/null; then
  echo 'SIGKILL left a plaintext backup file behind' >&2
  exit 1
fi
sleep 2
rm -rf -- "${KILL_STAGING}"

# Planned verification decrypts and restores into a fresh disposable PostgreSQL.
: > "${DOCKER_LOG}"
verify_backup "${TWO}"
grep -q '^run .*--network none' "${DOCKER_LOG}"
[[ "$(grep -c '^exec -i .* pg_restore' "${DOCKER_LOG}")" == 2 ]]
grep -q '^exec -i .*pg_restore .*--username rivune_restore --role rivune_owner' "${DOCKER_LOG}"
if grep -Fq 'AGE-SECRET-KEY-key-a' "${AGE_LOG}"; then
  echo 'age identity secret appeared in process arguments' >&2
  exit 1
fi

# A rotated recipient requires its matching identity; older ciphertext stays usable.
ROTATED="${TEST_DIR}/backups/rotated.age"
RIVUNE_BACKUP_AGE_RECIPIENT_FILE="${RECIPIENT_B}" create_backup "${ROTATED}"
ROTATED_ID="$(manifest_value "${ROTATED}.manifest" backup_id)"
reject_before_docker "${ROOT_DIR}/scripts/postgres-verify-backup.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}"
RIVUNE_BACKUP_AGE_IDENTITY_FILE="${IDENTITY_B}" verify_backup "${ROTATED}"
verify_backup "${TWO}"

# Current restore uses a private decrypted dump, swaps only after restore validation,
# and rolls back the late swap when application keyring health fails.
: > "${DOCKER_LOG}"
RIVUNE_BACKUP_AGE_IDENTITY_FILE="${IDENTITY_B}" RIVUNE_RESTORE_PASSWORD=restore-secret \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}" >/dev/null
restore_line="$(grep -n -m1 'pg_restore .*--dbname rivune_restore_staging' "${DOCKER_LOG}" | cut -d: -f1)"
swap_line="$(grep -n -m1 'RENAME TO :"prior_database"' "${DOCKER_LOG}" | cut -d: -f1)"
(( restore_line < swap_line ))

: > "${DOCKER_LOG}"
if RIVUNE_BACKUP_AGE_IDENTITY_FILE="${IDENTITY_B}" RIVUNE_RESTORE_PASSWORD=restore-secret \
  FAKE_SERVER_RUNNING=1 FAKE_KEYRING_HEALTH_FAIL_ONCE=1 \
  "${ROOT_DIR}/scripts/postgres-restore.sh" --expect-backup-id "${ROTATED_ID}" "${ROTATED}" >/dev/null 2>&1; then
  echo 'restore accepted unhealthy application keyring after swap' >&2
  exit 1
fi
grep -Fq "ALTER DATABASE :\"prior_database\" RENAME TO :\"live_database\"" "${DOCKER_LOG}"
[[ "$(grep -c '^compose up ' "${DOCKER_LOG}")" == 2 ]]
if compgen -G "${TEST_DIR}/staging/*" >/dev/null; then
  echo 'restore left private decrypted staging files' >&2
  exit 1
fi

printf 'encrypted PostgreSQL backup, rotation, verification, restore, and rollback tests passed\n'
