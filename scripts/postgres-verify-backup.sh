#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=postgres-backup-auth.sh
source "${SCRIPT_DIR}/postgres-backup-auth.sh"

usage() {
  echo "Usage: $0 --expect-backup-id ID BACKUP_FILE" >&2
  echo "       $0 --allow-legacy SHA256 BACKUP_FILE" >&2
  exit 64
}
if (( $# != 3 )); then
  usage
fi
VERIFY_MODE="$1"
OPERATOR_EXPECTATION="$2"
BACKUP_FILE="$3"
case "${VERIFY_MODE}" in
  --expect-backup-id) [[ "${OPERATOR_EXPECTATION}" =~ ^[0-9a-f]{32}$ ]] || usage ;;
  --allow-legacy) [[ "${OPERATOR_EXPECTATION}" =~ ^[0-9a-f]{64}$ ]] || usage ;;
  *) usage ;;
esac

require_repository_archive "${BACKUP_FILE}"
BACKUP_FILE="$(realpath -e -- "${BACKUP_FILE}")"
BACKUP_DIR="$(dirname "${BACKUP_FILE}")"
MANIFEST_FILE="${BACKUP_FILE}.manifest"
SIGNATURE_FILE="${BACKUP_FILE}.sig"
require_backup_key "${RIVUNE_BACKUP_VERIFY_KEY_FILE:-}" "${BACKUP_DIR}" "verification"
require_backup_signature "${SIGNATURE_FILE}"
AUTHENTICATED_BACKUP_FILE=""
AUTHENTICATED_MANIFEST_FILE=""
AUTHENTICATED_SIGNATURE_FILE=""
trap 'cleanup_authenticated_backup' EXIT
if [[ "${VERIFY_MODE}" == --allow-legacy ]]; then
  if [[ -e "${MANIFEST_FILE}" || -L "${MANIFEST_FILE}" ]]; then
    backup_error "Legacy authorization cannot be used for a manifested backup"
    exit 1
  fi
  stage_legacy_authenticated_backup "${BACKUP_FILE}" "${SECURE_BACKUP_SIGNATURE_FILE}" \
    "${SECURE_BACKUP_KEY_FILE}" "${OPERATOR_EXPECTATION}"
else
  require_backup_manifest "${MANIFEST_FILE}"
  stage_authenticated_manifest "${BACKUP_FILE}" "${SECURE_BACKUP_MANIFEST_FILE}" \
    "${SECURE_BACKUP_SIGNATURE_FILE}" "${SECURE_BACKUP_KEY_FILE}"
  if [[ "${OPERATOR_EXPECTATION}" != "${MANIFEST_BACKUP_ID}" ]]; then
    backup_error "Backup does not match the operator-selected backup ID"
    exit 1
  fi
  stage_authenticated_archive "${BACKUP_FILE}"
fi

POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
CONTAINER="rivune-backup-verify-${GITHUB_RUN_ID:-local}-$$"
BOOTSTRAP_PASSWORD="$(openssl rand -hex 32)"
APPLICATION_PASSWORD="$(openssl rand -hex 32)"
RESTORE_PASSWORD="$(openssl rand -hex 32)"
CONTAINER_ARCHIVE="/tmp/rivune-backup.dump"

cleanup() {
  cleanup_authenticated_backup
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

POSTGRES_PASSWORD="${BOOTSTRAP_PASSWORD}" \
RIVUNE_DATABASE_PASSWORD="${APPLICATION_PASSWORD}" \
RIVUNE_RESTORE_PASSWORD="${RESTORE_PASSWORD}" \
docker run -d --name "${CONTAINER}" \
  --network none \
  --read-only \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add DAC_OVERRIDE \
  --cap-add FOWNER \
  --cap-add SETGID \
  --cap-add SETUID \
  --pids-limit 256 \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,mode=1777 \
  --tmpfs /var/run/postgresql:rw,noexec,nosuid,nodev,mode=3775 \
  --tmpfs /var/lib/postgresql:rw,noexec,nosuid,nodev,mode=1777 \
  --volume "${SCRIPT_DIR}/postgres-init-rivune.sh:/docker-entrypoint-initdb.d/10-rivune.sh:ro" \
  -e POSTGRES_DB=postgres \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD \
  -e RIVUNE_DATABASE_PASSWORD \
  -e RIVUNE_RESTORE_PASSWORD \
  "${POSTGRES_IMAGE}" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" pg_isready --username rivune_restore --dbname rivune >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${CONTAINER}" pg_isready --username rivune_restore --dbname rivune >/dev/null
PGPASSWORD="${RESTORE_PASSWORD}" docker exec -e PGPASSWORD "${CONTAINER}" psql \
  --host 127.0.0.1 --username rivune_restore --dbname rivune \
  --set ON_ERROR_STOP=1 --tuples-only --no-align \
  --command "SELECT CASE WHEN NOT rolsuper AND rolcreatedb AND NOT rolcreaterole THEN 'verified' ELSE 'invalid' END FROM pg_roles WHERE rolname = current_user;" \
  | grep -qx verified

# Authentication and the external ID expectation completed before PostgreSQL sees bytes.
docker cp "${AUTHENTICATED_BACKUP_FILE}" "${CONTAINER}:${CONTAINER_ARCHIVE}"
docker exec "${CONTAINER}" pg_restore --list "${CONTAINER_ARCHIVE}" >/dev/null
PGPASSWORD="${RESTORE_PASSWORD}" docker exec -e PGPASSWORD "${CONTAINER}" pg_restore \
  --host 127.0.0.1 --username rivune_restore --role rivune_owner \
  --dbname rivune --exit-on-error --no-owner --no-privileges \
  "${CONTAINER_ARCHIVE}"
PGPASSWORD="${RESTORE_PASSWORD}" docker exec -e PGPASSWORD "${CONTAINER}" psql \
  --host 127.0.0.1 --username rivune_restore --dbname rivune \
  --set ON_ERROR_STOP=1 --tuples-only --no-align \
  --command "SELECT CASE WHEN EXISTS (SELECT FROM schema_migrations) THEN 'verified' ELSE 'invalid' END;" \
  | grep -qx verified

printf 'Authenticated operator-selected backup restored in the disposable PostgreSQL container\n'
