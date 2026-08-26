#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=postgres-backup-auth.sh
source "${SCRIPT_DIR}/postgres-backup-auth.sh"

if (( $# != 1 )); then
  echo "Usage: $0 BACKUP_FILE" >&2
  exit 64
fi

BACKUP_FILE="$1"
BACKUP_DIR="$(dirname "${BACKUP_FILE}")"
BACKUP_NAME="$(basename "${BACKUP_FILE}")"
umask 077
mkdir -p -- "${BACKUP_DIR}"
BACKUP_DIR="$(realpath -e -- "${BACKUP_DIR}")"
require_trusted_directory_ancestors "${BACKUP_DIR}" "Backup directory"
BACKUP_FILE="${BACKUP_DIR}/${BACKUP_NAME}"
MANIFEST_FILE="${BACKUP_FILE}.manifest"
SIGNATURE_FILE="${BACKUP_FILE}.sig"

if [[ -L "${BACKUP_DIR}" || ! -d "${BACKUP_DIR}" ]]; then
  echo "Refusing unsafe backup directory" >&2
  exit 1
fi
DIRECTORY_OWNER="$(stat -c '%u' -- "${BACKUP_DIR}")"
DIRECTORY_MODE="$(stat -c '%a' -- "${BACKUP_DIR}")"
if [[ "${DIRECTORY_OWNER}" != "$(id -u)" ]] || (( (8#${DIRECTORY_MODE} & 0022) != 0 )); then
  echo "Refusing backup directory not exclusively writable by the current user" >&2
  exit 1
fi
if [[ ! "${BACKUP_NAME}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$ ]]; then
  echo "Backup filename is invalid" >&2
  exit 1
fi

if [[ -e "${BACKUP_FILE}" || -L "${BACKUP_FILE}" || \
      -e "${MANIFEST_FILE}" || -L "${MANIFEST_FILE}" || \
      -e "${SIGNATURE_FILE}" || -L "${SIGNATURE_FILE}" ]]; then
  echo "Refusing to overwrite existing backup authentication material" >&2
  exit 1
fi

require_backup_token "${RIVUNE_BACKUP_LINEAGE:-}" "RIVUNE_BACKUP_LINEAGE"
require_backup_key "${RIVUNE_BACKUP_SIGNING_KEY_FILE:-}" "${BACKUP_DIR}" "signing"
SECURE_BACKUP_SIGNING_KEY_FILE="${SECURE_BACKUP_KEY_FILE}"
require_backup_state_location "${RIVUNE_BACKUP_STATE_FILE:-}" "${BACKUP_DIR}" true
require_age_recipient_file "${RIVUNE_BACKUP_AGE_RECIPIENT_FILE:-}" "${BACKUP_DIR}"
if ! openssl pkey -in "${SECURE_BACKUP_SIGNING_KEY_FILE}" -check -noout >/dev/null 2>&1; then
  echo "Backup signing key is not a valid private key" >&2
  exit 1
fi
reserve_backup_sequence "${RIVUNE_BACKUP_LINEAGE}"

TMP_CIPHERTEXT=""
TMP_MANIFEST=""
TMP_SIGNATURE=""
TMP_PUBLIC_KEY=""
MANIFEST_PUBLISHED=false
SIGNATURE_PUBLISHED=false
ARCHIVE_PUBLISHED=false
cleanup() {
  [[ -z "${TMP_CIPHERTEXT}" ]] || rm -f -- "${TMP_CIPHERTEXT}"
  [[ -z "${TMP_MANIFEST}" ]] || rm -f -- "${TMP_MANIFEST}"
  [[ -z "${TMP_SIGNATURE}" ]] || rm -f -- "${TMP_SIGNATURE}"
  [[ -z "${TMP_PUBLIC_KEY}" ]] || rm -f -- "${TMP_PUBLIC_KEY}"
  if [[ "${ARCHIVE_PUBLISHED}" != true ]]; then
    [[ "${MANIFEST_PUBLISHED}" != true ]] || rm -f -- "${MANIFEST_FILE}"
    [[ "${SIGNATURE_PUBLISHED}" != true ]] || rm -f -- "${SIGNATURE_FILE}"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

TMP_CIPHERTEXT="$(mktemp --tmpdir="${BACKUP_DIR}" -- ".${BACKUP_NAME}.partial.XXXXXXXX")"
TMP_MANIFEST="$(mktemp --tmpdir="${BACKUP_DIR}" -- ".${BACKUP_NAME}.manifest.partial.XXXXXXXX")"
TMP_SIGNATURE="$(mktemp --tmpdir="${BACKUP_DIR}" -- ".${BACKUP_NAME}.sig.partial.XXXXXXXX")"
TMP_PUBLIC_KEY="$(mktemp "${TMPDIR:-/tmp}/rivune-backup-public-key.XXXXXXXX")"
for temporary_file in "${TMP_CIPHERTEXT}" "${TMP_MANIFEST}" "${TMP_SIGNATURE}" "${TMP_PUBLIC_KEY}"; do
  if [[ -L "${temporary_file}" || ! -f "${temporary_file}" || \
        "$(stat -c '%u' -- "${temporary_file}")" != "$(id -u)" ]]; then
    echo "Failed to create a secure backup temporary file" >&2
    exit 1
  fi
  chmod 600 "${temporary_file}"
done

CIPHERTEXT_LIMIT="$(backup_ciphertext_limit)"
FILE_BLOCK_LIMIT="$(( (CIPHERTEXT_LIMIT + 1023) / 1024 ))"
exec 3> "${TMP_CIPHERTEXT}"
(
  ulimit -f "${FILE_BLOCK_LIMIT}"
  docker compose exec -T postgres pg_dump \
    --username rivune \
    --dbname rivune \
    --format=custom \
    --compress=9 \
    --no-owner \
    --no-privileges \
    | age --encrypt --recipients-file "${SECURE_BACKUP_RECIPIENT_FILE}" >&3
)
exec 3>&-

require_repository_archive "${TMP_CIPHERTEXT}"
ARCHIVE_SIZE="$(stat -c '%s' -- "${TMP_CIPHERTEXT}")"
if (( ARCHIVE_SIZE > CIPHERTEXT_LIMIT )); then
  backup_error "Encrypted backup size is outside the configured safety limit"
  exit 1
fi
ARCHIVE_SHA256="$(openssl dgst -sha256 -r "${TMP_CIPHERTEXT}" | cut -d ' ' -f 1)"
BACKUP_ID="$(openssl rand -hex 16)"
CREATED_AT="$(date -u +%Y%m%dT%H%M%SZ)"
SIGNING_KEY_ID="$(backup_private_key_id "${SECURE_BACKUP_SIGNING_KEY_FILE}")"
RECIPIENT_KEY_ID="${BACKUP_AGE_RECIPIENT_KEY_ID}"
if [[ ! "${SIGNING_KEY_ID}" =~ ^[0-9a-f]{64}$ ]]; then
  echo "Failed to identify backup signing key" >&2
  exit 1
fi
printf '%s\n' \
  'format=rivune-backup-manifest-v3-age' \
  "lineage=${RIVUNE_BACKUP_LINEAGE}" \
  "sequence=${RESERVED_BACKUP_SEQUENCE}" \
  "backup_id=${BACKUP_ID}" \
  "created_at=${CREATED_AT}" \
  "archive_name=${BACKUP_NAME}" \
  "ciphertext_size=${ARCHIVE_SIZE}" \
  "ciphertext_sha256=${ARCHIVE_SHA256}" \
  "recipient_key_id=${RECIPIENT_KEY_ID}" \
  "signing_key_id=${SIGNING_KEY_ID}" > "${TMP_MANIFEST}"

openssl dgst -sha256 -sign "${SECURE_BACKUP_SIGNING_KEY_FILE}" \
  -out "${TMP_SIGNATURE}" "${TMP_MANIFEST}"
openssl pkey -in "${SECURE_BACKUP_SIGNING_KEY_FILE}" -pubout -out "${TMP_PUBLIC_KEY}"
openssl pkeyutl -verify -pubin -inkey "${TMP_PUBLIC_KEY}" \
  -sigfile "${TMP_SIGNATURE}" -rawin -digest sha256 \
  -in "${TMP_MANIFEST}" >/dev/null
parse_authenticated_manifest "${TMP_MANIFEST}"
[[ "${MANIFEST_ARCHIVE_SHA256}" == "${ARCHIVE_SHA256}" ]]
[[ "${MANIFEST_ARCHIVE_SIZE}" == "${ARCHIVE_SIZE}" ]]
[[ "${MANIFEST_RECIPIENT_KEY_ID}" == "${RECIPIENT_KEY_ID}" ]]
[[ "${MANIFEST_SIGNING_KEY_ID}" == "$(backup_public_key_id "${TMP_PUBLIC_KEY}")" ]]

# The producer validates the encrypted stream without ever materializing the
# PostgreSQL archive. Disposable restore verification exercises decryption.
[[ "$(dd if="${TMP_CIPHERTEXT}" bs=1 count=21 status=none)" == 'age-encryption.org/v1' ]]

if ! ln -- "${TMP_SIGNATURE}" "${SIGNATURE_FILE}"; then
  echo "Backup destination was claimed concurrently" >&2
  exit 1
fi
SIGNATURE_PUBLISHED=true
rm -f -- "${TMP_SIGNATURE}"
if ! ln -- "${TMP_MANIFEST}" "${MANIFEST_FILE}"; then
  echo "Backup destination was claimed concurrently" >&2
  exit 1
fi
MANIFEST_PUBLISHED=true
rm -f -- "${TMP_MANIFEST}"
if ! ln -- "${TMP_CIPHERTEXT}" "${BACKUP_FILE}"; then
  echo "Backup destination was claimed concurrently" >&2
  exit 1
fi
ARCHIVE_PUBLISHED=true
rm -f -- "${TMP_CIPHERTEXT}"
commit_backup_generation "${RIVUNE_BACKUP_LINEAGE}" "${RESERVED_BACKUP_SEQUENCE}" \
  "${BACKUP_ID}" "${ARCHIVE_SHA256}"
rm -f -- "${TMP_PUBLIC_KEY}"
trap - EXIT
printf 'Backup published: id=%s sequence=%s sha256=%s\n' \
  "${BACKUP_ID}" "${RESERVED_BACKUP_SEQUENCE}" "${ARCHIVE_SHA256}"
