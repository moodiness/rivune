#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ENV_FILE="${JFCOMPAT_ENV_FILE:-${SCRIPT_DIR}/targets.env}"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yaml"
UPSTREAM_URL="http://127.0.0.1:18096"
compose_started=0

for command_name in docker go curl jq; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "${command_name}" >&2
    exit 1
  fi
done
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required" >&2
  exit 1
fi

if [[ ! -f "${ENV_FILE}" || -L "${ENV_FILE}" ]]; then
  printf 'Private environment file is missing or unsafe: %s\n' "${ENV_FILE}" >&2
  exit 1
fi
permissions="$(stat -c '%a' "${ENV_FILE}" 2>/dev/null || stat -f '%Lp' "${ENV_FILE}" 2>/dev/null || true)"
if [[ ! "${permissions}" =~ ^[0-7]{3,4}$ || "${permissions: -2}" != "00" ]]; then
  echo "Refusing environment file readable or writable by group/others; use chmod 600 or 400" >&2
  exit 1
fi

# The private file is trusted local configuration and is intentionally sourced
# so values need not be placed on any command line.
set -a
# shellcheck disable=SC1090
if ! source "${ENV_FILE}" >/dev/null 2>&1; then
  set +a
  echo "Failed to load the private environment file" >&2
  exit 1
fi
set +a

require_value() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    printf 'Required private environment variable is unset: %s\n' "${name}" >&2
    exit 1
  fi
}
require_value RIVUNE_TARGET_URL
require_value JFCOMPAT_UPSTREAM_USERNAME
require_value JFCOMPAT_UPSTREAM_PASSWORD
require_value JFCOMPAT_RIVUNE_USERNAME
require_value JFCOMPAT_RIVUNE_PASSWORD

if [[ ! "${RIVUNE_TARGET_URL}" =~ ^https?://[^/?#]+$ ]]; then
  echo "RIVUNE_TARGET_URL must be an HTTP(S) origin without credentials, path, query, or fragment" >&2
  exit 1
fi
export JFCOMPAT_UPSTREAM_URL="${UPSTREAM_URL}"

if [[ -n "${JFCOMPAT_OUT_DIR:-}" ]]; then
  if [[ "${JFCOMPAT_OUT_DIR}" == /* ]]; then
    OUT_DIR="${JFCOMPAT_OUT_DIR}"
  else
    OUT_DIR="${SCRIPT_DIR}/${JFCOMPAT_OUT_DIR}"
  fi
else
  OUT_DIR="${SCRIPT_DIR}/work/runs/$(date -u '+%Y%m%dT%H%M%SZ')-$$"
fi
if [[ -e "${OUT_DIR}" || -L "${OUT_DIR}" ]]; then
  printf 'Refusing to overwrite an existing output path: %s\n' "${OUT_DIR}" >&2
  exit 1
fi

cleanup() {
  local exit_status=$?
  trap - EXIT
  if (( compose_started )); then
    docker compose -f "${COMPOSE_FILE}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  exit "${exit_status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"${SCRIPT_DIR}/prepare-fixtures.sh"
compose_started=1
docker compose -f "${COMPOSE_FILE}" up --detach --wait
"${SCRIPT_DIR}/bootstrap-upstream.sh"

mkdir -p "$(dirname "${OUT_DIR}")"
(
  cd "${ROOT_DIR}/server"
  go run ./cmd/jellyfin-compat validate -manifest ../scripts/jellyfin-compat/requests.json
  go run ./cmd/jellyfin-compat run \
    -manifest ../scripts/jellyfin-compat/requests.json \
    -target "upstream=${UPSTREAM_URL}" \
    -target "rivune=${RIVUNE_TARGET_URL}" \
    -out "${OUT_DIR}"
  go run ./cmd/jellyfin-compat compare \
    -left "${OUT_DIR}/upstream" \
    -right "${OUT_DIR}/rivune" \
    -out "${OUT_DIR}/diff"
)

printf 'Differential artifacts written to %s\n' "${OUT_DIR}"
