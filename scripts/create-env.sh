#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_FILE="${ROOT_DIR}/.env.example"
DESTINATION_FILE="${ROOT_DIR}/.env"

if [[ -e "${DESTINATION_FILE}" || -L "${DESTINATION_FILE}" ]]; then
  echo "Refusing to overwrite existing .env path: ${DESTINATION_FILE}" >&2
  exit 1
fi

umask 077
created=0
cleanup() {
  if (( created )); then
    rm -f "${DESTINATION_FILE}"
  fi
}
trap cleanup EXIT

set -o noclobber
if ! : > "${DESTINATION_FILE}"; then
  echo "Refusing to overwrite existing .env path: ${DESTINATION_FILE}" >&2
  exit 1
fi
set +o noclobber
created=1

cp "${SOURCE_FILE}" "${DESTINATION_FILE}"
chmod 600 "${DESTINATION_FILE}"

created=0
trap - EXIT
printf 'Created private environment file: %s\n' "${DESTINATION_FILE}"
