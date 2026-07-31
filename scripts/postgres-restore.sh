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

RIVUNE_SERVICE="${RIVUNE_SERVICE:-server}"

server_was_running=false
if docker compose ps --status running --services | grep -qx "${RIVUNE_SERVICE}"; then
  server_was_running=true
fi

restart_server() {
  if [[ "${server_was_running}" == true ]]; then
    docker compose up -d "${RIVUNE_SERVICE}" >/dev/null
  fi
}
trap restart_server EXIT

docker compose exec -T postgres pg_restore --list < "${BACKUP_FILE}" >/dev/null
docker compose stop "${RIVUNE_SERVICE}" >/dev/null

docker compose exec -T postgres psql --username rivune --dbname postgres \
  --set ON_ERROR_STOP=1 \
  --command 'DROP DATABASE rivune WITH (FORCE);'
docker compose exec -T postgres psql --username rivune --dbname postgres \
  --set ON_ERROR_STOP=1 \
  --command 'CREATE DATABASE rivune OWNER rivune;'
docker compose exec -T postgres pg_restore \
  --username rivune \
  --dbname rivune \
  --exit-on-error \
  --no-owner \
  --no-privileges < "${BACKUP_FILE}"

docker compose exec -T postgres psql --username rivune --dbname rivune \
  --set ON_ERROR_STOP=1 --tuples-only --no-align \
  --command "SELECT CASE WHEN to_regclass('public.schema_migrations') IS NOT NULL AND EXISTS (SELECT FROM schema_migrations) THEN 'verified' ELSE 'invalid' END;" \
  | grep -qx verified

restart_server
server_was_running=false
trap - EXIT
printf 'Restore completed and migration ledger verified from: %s\n' "${BACKUP_FILE}"
