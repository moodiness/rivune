#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo "Usage: $0 BACKUP_FILE" >&2
  exit 64
fi

BACKUP_FILE="$1"
if [[ ! -r "${BACKUP_FILE}" ]]; then
  echo "Backup is not readable: ${BACKUP_FILE}" >&2
  exit 1
fi

POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
CONTAINER="rivune-backup-verify-${GITHUB_RUN_ID:-local}-$$"
PASSWORD="backup-verification-password"
CONTAINER_ARCHIVE="/tmp/rivune-backup.dump"

cleanup() {
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "${CONTAINER}" \
  -e POSTGRES_DB=rivune \
  -e POSTGRES_USER=rivune \
  -e POSTGRES_PASSWORD="${PASSWORD}" \
  "${POSTGRES_IMAGE}" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" pg_isready --username rivune --dbname rivune >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${CONTAINER}" pg_isready --username rivune --dbname rivune >/dev/null

docker cp "${BACKUP_FILE}" "${CONTAINER}:${CONTAINER_ARCHIVE}"
docker exec "${CONTAINER}" pg_restore --list "${CONTAINER_ARCHIVE}" >/dev/null
docker exec -e PGPASSWORD="${PASSWORD}" "${CONTAINER}" pg_restore \
  --username rivune --dbname rivune --exit-on-error --no-owner --no-privileges \
  "${CONTAINER_ARCHIVE}"
docker exec -e PGPASSWORD="${PASSWORD}" "${CONTAINER}" psql \
  --username rivune --dbname rivune --set ON_ERROR_STOP=1 --tuples-only --no-align \
  --command "SELECT CASE WHEN EXISTS (SELECT FROM schema_migrations) THEN 'verified' ELSE 'invalid' END;" \
  | grep -qx verified

printf 'Backup restored successfully into disposable PostgreSQL: %s\n' "${BACKUP_FILE}"
