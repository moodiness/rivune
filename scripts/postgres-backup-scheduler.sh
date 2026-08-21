#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: $0 [--once] BACKUP_DIRECTORY" >&2
  exit 64
}

RUN_ONCE=false
if (( $# > 0 )) && [[ "$1" == --once ]]; then
  RUN_ONCE=true
  shift
fi
(( $# == 1 )) || usage

BACKUP_DIRECTORY="$1"
INTERVAL_SECONDS="${RIVUNE_BACKUP_INTERVAL_SECONDS:-86400}"
RETENTION="${RIVUNE_BACKUP_RETENTION:-30}"
if [[ ! "${INTERVAL_SECONDS}" =~ ^[0-9]+$ ]] ||
   (( 10#${INTERVAL_SECONDS} < 3600 || 10#${INTERVAL_SECONDS} > 604800 )); then
  echo "RIVUNE_BACKUP_INTERVAL_SECONDS must be an integer from 3600 through 604800" >&2
  exit 1
fi
if [[ ! "${RETENTION}" =~ ^[0-9]+$ ]] ||
   (( 10#${RETENTION} < 1 || 10#${RETENTION} > 365 )); then
  echo "RIVUNE_BACKUP_RETENTION must be an integer from 1 through 365" >&2
  exit 1
fi

umask 077
if [[ -L "${BACKUP_DIRECTORY}" ]]; then
  echo "Refusing unsafe scheduled-backup directory" >&2
  exit 1
fi
mkdir -p -- "${BACKUP_DIRECTORY}"
if [[ -L "${BACKUP_DIRECTORY}" || ! -d "${BACKUP_DIRECTORY}" ]]; then
  echo "Refusing unsafe scheduled-backup directory" >&2
  exit 1
fi
BACKUP_DIRECTORY="$(cd -P -- "${BACKUP_DIRECTORY}" && pwd)"

LOCK_FILE="${BACKUP_DIRECTORY}/.rivune-backup-scheduler.lock"
if [[ -L "${LOCK_FILE}" ]]; then
  echo "Refusing unsafe scheduler lock ${LOCK_FILE}" >&2
  exit 1
fi
if command -v flock >/dev/null 2>&1; then
  exec 9>> "${LOCK_FILE}"
  chmod 600 -- "${LOCK_FILE}"
  if [[ ! -f "${LOCK_FILE}" ]] || ! flock -n -x 9; then
    echo "Another Rivune backup scheduler owns ${LOCK_FILE}" >&2
    exit 1
  fi
  printf '%s\n' "$$" > "${LOCK_FILE}"
  SCHEDULER_LOCK_KIND=flock
elif command -v lockf >/dev/null 2>&1; then
  exec 9>> "${LOCK_FILE}"
  chmod 600 -- "${LOCK_FILE}"
  if [[ ! -f "${LOCK_FILE}" ]] || ! lockf -s -t 0 9; then
    echo "Another Rivune backup scheduler owns ${LOCK_FILE}" >&2
    exit 1
  fi
  printf '%s\n' "$$" > "${LOCK_FILE}"
  SCHEDULER_LOCK_KIND=lockf
else
  echo "A supported file-lock utility (flock or lockf) is required" >&2
  exit 1
fi
cleanup() {
  case "${SCHEDULER_LOCK_KIND}" in
    flock) flock -u 9; exec 9>&- ;;
    lockf) exec 9>&- ;;
  esac
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 131' QUIT
trap 'exit 143' TERM

prune_backups() {
  local -a archives=()
  local excess index archive
  shopt -s nullglob
  archives=("${BACKUP_DIRECTORY}"/rivune-scheduled-????????T??????Z-*.dump)
  shopt -u nullglob
  if (( ${#archives[@]} > 1 )); then
    mapfile -t archives < <({ printf '%s\n' "${archives[@]}" | sort; } 9>&-)
  fi
  excess=$(( ${#archives[@]} - 10#${RETENTION} ))
  (( excess > 0 )) || return 0
  for (( index = 0; index < excess; index++ )); do
    archive="${archives[index]}"
    if [[ -L "${archive}" || -L "${archive}.manifest" || -L "${archive}.sig" ||
          ! -f "${archive}" || ! -f "${archive}.manifest" || ! -f "${archive}.sig" ]]; then
      echo "Refusing to prune incomplete or unsafe backup set: ${archive}" >&2
      return 1
    fi
    rm -f -- "${archive}.sig" "${archive}.manifest" "${archive}" 9>&-
  done
}

run_backup() {
  local timestamp archive result backup_id
  timestamp="$(date -u +%Y%m%dT%H%M%SZ 9>&-)"
  archive="${BACKUP_DIRECTORY}/rivune-scheduled-${timestamp}-$$.dump"
  result="$("${SCRIPT_DIR}/postgres-backup.sh" "${archive}" 9>&-)"
  printf '%s\n' "${result}"
  if [[ ! "${result}" =~ id=([0-9a-f]{32})([[:space:]]|$) ]]; then
    echo "Scheduled backup did not return a valid backup ID" >&2
    return 1
  fi
  backup_id="${BASH_REMATCH[1]}"
  "${SCRIPT_DIR}/postgres-verify-backup.sh" --expect-backup-id "${backup_id}" "${archive}" 9>&-
  prune_backups
}

while true; do
  run_backup
  [[ "${RUN_ONCE}" == false ]] || break
  sleep "${INTERVAL_SECONDS}" 9>&-
done
