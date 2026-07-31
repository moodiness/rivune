#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo "Usage: $0 BACKUP_FILE" >&2
  exit 64
fi

BACKUP_FILE="$1"
BACKUP_DIR="$(dirname "${BACKUP_FILE}")"
BACKUP_NAME="$(basename "${BACKUP_FILE}")"
mkdir -p "${BACKUP_DIR}"
TMP_FILE="${BACKUP_DIR}/.${BACKUP_NAME}.partial.$$"
umask 077

cleanup() {
  rm -f "${TMP_FILE}"
}
trap cleanup EXIT

if [[ -e "${BACKUP_FILE}" ]]; then
  echo "Refusing to overwrite existing backup: ${BACKUP_FILE}" >&2
  exit 1
fi

docker compose exec -T postgres pg_dump \
  --username rivune \
  --dbname rivune \
  --format=custom \
  --compress=9 \
  --no-owner \
  --no-privileges > "${TMP_FILE}"

test -s "${TMP_FILE}"
docker compose exec -T postgres pg_restore --list < "${TMP_FILE}" >/dev/null
mv "${TMP_FILE}" "${BACKUP_FILE}"
trap - EXIT
printf 'Backup written and archive-checked: %s\n' "${BACKUP_FILE}"
