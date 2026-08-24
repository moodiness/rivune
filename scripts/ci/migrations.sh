#!/usr/bin/env bash
set -euo pipefail

IMAGE="${RIVUNE_IMAGE:-rivune-ci:current}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
RUN_ID="${GITHUB_RUN_ID:-local}-$$"
NETWORK="rivune-migrations-${RUN_ID}"
POSTGRES="rivune-migrations-postgres-${RUN_ID}"
CLEAN_APP="rivune-migrations-clean-${RUN_ID}"
UPGRADE_APP="rivune-migrations-upgrade-${RUN_ID}"
PASSWORD="migration-test-password"
SETUP_TOKEN="migration-test-setup-token"
DATABASE_URL="postgres://rivune:${PASSWORD}@postgres:5432/rivune?sslmode=disable"
ENCRYPTION_KEYS="1:1212121212121212121212121212121212121212121212121212121212121212"
LEGACY_ENCRYPTION_KEY="${ENCRYPTION_KEYS#1:}"

cleanup() {
  docker rm -f "${CLEAN_APP}" "${UPGRADE_APP}" "${POSTGRES}" >/dev/null 2>&1 || true
  docker network rm "${NETWORK}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_postgres() {
  for _ in $(seq 1 60); do
    if docker exec "${POSTGRES}" sh -ceu 'test "$(cat /proc/1/comm)" = postgres' >/dev/null 2>&1 &&
      docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" \
        psql --host postgres --username rivune --dbname rivune --tuples-only --no-align \
        --command 'SELECT 1;' | grep -qx 1; then
      return
    fi
    sleep 1
  done
  docker logs "${POSTGRES}"
  echo "PostgreSQL final server did not become queryable" >&2
  return 1
}

wait_for_rivune() {
  local container="$1"
  for _ in $(seq 1 60); do
    if docker run --rm --network "${NETWORK}" curlimages/curl:8.12.1 \
      --fail --silent --show-error "http://${container}:8080/health" >/dev/null 2>&1; then
      return
    fi
    if ! docker inspect --format '{{.State.Running}}' "${container}" 2>/dev/null | grep -qx true; then
      docker logs "${container}"
      echo "Rivune exited before becoming healthy" >&2
      return 1
    fi
    sleep 1
  done
  docker logs "${container}"
  echo "Rivune did not become healthy" >&2
  return 1
}

start_rivune() {
  local container="$1"
  local key_name="RIVUNE_ENCRYPTION_KEYS"
  local key_value="${ENCRYPTION_KEYS}"
  if [[ "${2:-versioned}" == "legacy" ]]; then
    key_name="RIVUNE_TRACKING_ENCRYPTION_KEY"
    key_value="${LEGACY_ENCRYPTION_KEY}"
  fi
  docker run -d --name "${container}" --network "${NETWORK}" \
    -e RIVUNE_DATABASE_URL="${DATABASE_URL}" \
    -e RIVUNE_SETUP_TOKEN="${SETUP_TOKEN}" \
    -e "${key_name}=${key_value}" \
    -e RIVUNE_PUBLIC_URL="http://127.0.0.1:8080" \
    "${IMAGE}" >/dev/null
  wait_for_rivune "${container}"
}

migration_count() {
  docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" \
    psql --username rivune --dbname rivune --tuples-only --no-align \
    --command 'SELECT count(*) FROM schema_migrations;'
}

migration_max() {
  docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" \
    psql --username rivune --dbname rivune --tuples-only --no-align \
    --command 'SELECT COALESCE(max(version), 0) FROM schema_migrations;'
}

mapfile -t MIGRATIONS < <(printf '%s\n' server/internal/database/migrations/*.sql | sort)
if (( ${#MIGRATIONS[@]} < 2 )); then
  echo "Expected at least two migration files" >&2
  exit 1
fi
EXPECTED_COUNT="${#MIGRATIONS[@]}"
LATEST_FILE="${MIGRATIONS[$((EXPECTED_COUNT - 1))]}"
LATEST_NAME="$(basename "${LATEST_FILE}")"
LATEST_VERSION="$((10#${LATEST_NAME%%_*}))"

docker network create "${NETWORK}" >/dev/null
docker run -d --name "${POSTGRES}" --network "${NETWORK}" --network-alias postgres \
  -e POSTGRES_DB=rivune -e POSTGRES_USER=rivune -e POSTGRES_PASSWORD="${PASSWORD}" \
  "${POSTGRES_IMAGE}" >/dev/null
wait_for_postgres

# Clean install: the current image must apply every embedded migration.
start_rivune "${CLEAN_APP}"
[[ "$(migration_count)" == "${EXPECTED_COUNT}" ]]
[[ "$(migration_max)" == "${LATEST_VERSION}" ]]
docker rm -f "${CLEAN_APP}" >/dev/null
start_rivune "${CLEAN_APP}"
[[ "$(migration_count)" == "${EXPECTED_COUNT}" ]]
[[ "$(migration_max)" == "${LATEST_VERSION}" ]]
docker rm -f "${CLEAN_APP}" >/dev/null

# Upgrade: construct the immediately previous schema and ledger, then boot current.
docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" psql --username rivune --dbname postgres \
  --set ON_ERROR_STOP=1 --command 'DROP DATABASE rivune WITH (FORCE);'
docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" psql --username rivune --dbname postgres \
  --set ON_ERROR_STOP=1 --command 'CREATE DATABASE rivune OWNER rivune;'
docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" psql --username rivune --dbname rivune \
  --set ON_ERROR_STOP=1 --command 'CREATE TABLE schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());'

for migration in "${MIGRATIONS[@]:0:$((EXPECTED_COUNT - 1))}"; do
  version_name="$(basename "${migration}")"
  version="$((10#${version_name%%_*}))"
  docker exec -i -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" psql --username rivune --dbname rivune \
    --single-transaction --set ON_ERROR_STOP=1 < "${migration}"
  docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" psql --username rivune --dbname rivune \
    --set ON_ERROR_STOP=1 --command "INSERT INTO schema_migrations (version) VALUES (${version});" >/dev/null
done

[[ "$(migration_count)" == "$((EXPECTED_COUNT - 1))" ]]
start_rivune "${UPGRADE_APP}" legacy
[[ "$(migration_count)" == "${EXPECTED_COUNT}" ]]
[[ "$(migration_max)" == "${LATEST_VERSION}" ]]
docker rm -f "${UPGRADE_APP}" >/dev/null
start_rivune "${UPGRADE_APP}"
[[ "$(migration_count)" == "${EXPECTED_COUNT}" ]]
[[ "$(migration_max)" == "${LATEST_VERSION}" ]]

echo "Clean install, one-version upgrade, and migration idempotency checks passed at version ${LATEST_VERSION}."
