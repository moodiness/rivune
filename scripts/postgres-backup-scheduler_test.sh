#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
cleanup() { rm -rf -- "${TEST_DIR}"; }
trap cleanup EXIT

mkdir -p "${TEST_DIR}/scripts" "${TEST_DIR}/backups"
cp "${ROOT_DIR}/scripts/postgres-backup-scheduler.sh" "${TEST_DIR}/scripts/"
cat > "${TEST_DIR}/scripts/postgres-backup.sh" <<'FAKE_BACKUP'
#!/usr/bin/env bash
set -euo pipefail
archive="$1"
printf 'archive\n' > "${archive}"
printf 'manifest\n' > "${archive}.manifest"
printf 'signature\n' > "${archive}.sig"
printf 'Backup published: id=0123456789abcdef0123456789abcdef sequence=3 sha256=%064d\n' 0
FAKE_BACKUP
cat > "${TEST_DIR}/scripts/postgres-verify-backup.sh" <<'FAKE_VERIFY'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" > "${VERIFY_LOG}"
[[ "${FAIL_VERIFY:-false}" == false ]]
FAKE_VERIFY
chmod +x "${TEST_DIR}/scripts/"*.sh

create_set() {
  local stem="$1"
  printf ciphertext > "${TEST_DIR}/backups/${stem}.age"
  printf manifest > "${TEST_DIR}/backups/${stem}.age.manifest"
  printf signature > "${TEST_DIR}/backups/${stem}.age.sig"
}
create_set rivune-scheduled-20260101T010101Z-1
create_set rivune-scheduled-20260102T010101Z-2

export VERIFY_LOG="${TEST_DIR}/verify.log"
RIVUNE_BACKUP_RETENTION=2 \
  "${TEST_DIR}/scripts/postgres-backup-scheduler.sh" --once "${TEST_DIR}/backups" >/dev/null

[[ ! -e "${TEST_DIR}/backups/rivune-scheduled-20260101T010101Z-1.age" ]]
[[ -f "${TEST_DIR}/backups/rivune-scheduled-20260102T010101Z-2.age" ]]
[[ "$(find "${TEST_DIR}/backups" -maxdepth 1 -type f -name '*.age' | wc -l | tr -d ' ')" == 2 ]]
[[ "$(cat "${VERIFY_LOG}")" == *'<--expect-backup-id>'* ]]
[[ "$(cat "${VERIFY_LOG}")" == *'<0123456789abcdef0123456789abcdef>'* ]]

before_count="$(find "${TEST_DIR}/backups" -maxdepth 1 -type f -name '*.age' | wc -l | tr -d ' ')"
if FAIL_VERIFY=true RIVUNE_BACKUP_RETENTION=1 \
    "${TEST_DIR}/scripts/postgres-backup-scheduler.sh" --once "${TEST_DIR}/backups" >/dev/null 2>&1; then
  echo 'scheduler accepted a failed verification' >&2
  exit 1
fi
after_count="$(find "${TEST_DIR}/backups" -maxdepth 1 -type f -name '*.age' | wc -l | tr -d ' ')"
(( after_count == before_count + 1 ))

if RIVUNE_BACKUP_INTERVAL_SECONDS=59 \
    "${TEST_DIR}/scripts/postgres-backup-scheduler.sh" --once "${TEST_DIR}/backups" >/dev/null 2>&1; then
  echo 'scheduler accepted an unsafe interval' >&2
  exit 1
fi

RIVUNE_BACKUP_INTERVAL_SECONDS=3600 \
  "${TEST_DIR}/scripts/postgres-backup-scheduler.sh" "${TEST_DIR}/backups" >/dev/null &
scheduler_pid=$!
lock_file="${TEST_DIR}/backups/.rivune-backup-scheduler.lock"
for _ in {1..100}; do
  if [[ -f "${lock_file}" ]] && [[ "$(cat "${lock_file}")" == "${scheduler_pid}" ]]; then
    break
  fi
  if ! kill -0 "${scheduler_pid}" 2>/dev/null; then
    wait "${scheduler_pid}" 2>/dev/null || true
    echo 'persistent scheduler exited before acquiring its lock' >&2
    exit 1
  fi
  sleep 0.01
done
[[ -f "${lock_file}" ]] && [[ "$(cat "${lock_file}")" == "${scheduler_pid}" ]] || {
  echo 'persistent scheduler did not acquire its lock' >&2
  kill -TERM "${scheduler_pid}" 2>/dev/null || true
  wait "${scheduler_pid}" 2>/dev/null || true
  exit 1
}
if "${TEST_DIR}/scripts/postgres-backup-scheduler.sh" --once "${TEST_DIR}/backups" >/dev/null 2>&1; then
  echo 'scheduler accepted a concurrent owner' >&2
  kill -TERM "${scheduler_pid}" 2>/dev/null || true
  wait "${scheduler_pid}" 2>/dev/null || true
  exit 1
fi
lock_owner="$(cat "${TEST_DIR}/backups/.rivune-backup-scheduler.lock")"
kill -KILL "${lock_owner}"
[[ "${scheduler_pid}" == "${lock_owner}" ]] || kill -KILL "${scheduler_pid}" 2>/dev/null || true
wait "${scheduler_pid}" 2>/dev/null || true
"${TEST_DIR}/scripts/postgres-backup-scheduler.sh" --once "${TEST_DIR}/backups" >/dev/null

printf 'postgres backup scheduler tests passed\n'
