#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/release-journey.compose.yaml"
IMAGE="${RIVUNE_RELEASE_IMAGE:-}"
RUN_ID="${GITHUB_RUN_ID:-local}-$(date +%s)-$$"
PROJECT="rivune-release-${RUN_ID//[^a-zA-Z0-9_-]/-}"
WORK_DIR=""
COMPOSE_STARTED=0

for command_name in docker node npm openssl timeout; do
  command -v "${command_name}" >/dev/null 2>&1 || { printf 'Required command not found: %s\n' "${command_name}" >&2; exit 1; }
done
docker compose version >/dev/null 2>&1 || { echo 'Docker Compose v2 is required' >&2; exit 1; }
if [[ ! "${IMAGE}" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]]; then
  echo 'RIVUNE_RELEASE_IMAGE must identify the exact published image by sha256 digest' >&2
  exit 1
fi

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if (( status != 0 )) && [[ -n "${WORK_DIR}" ]]; then
    mkdir -p "${ROOT_DIR}/web/test-results/release-journey"
    printf 'Release journey failed. Container and fixture diagnostics were redacted in the job log; no credentials or browser state were retained.\n' >"${ROOT_DIR}/web/test-results/release-journey/failure.txt"
    echo 'Release journey failed; redacted diagnostics follow.' >&2
    if (( COMPOSE_STARTED )); then
      docker compose --project-name "${PROJECT}" --env-file "${WORK_DIR}/secrets.env" -f "${COMPOSE_FILE}" logs --no-color --tail=200 2>&1 |
        sed -E 's#(token|password|secret|authorization)([=: ]+)[^[:space:]]+#\1\2[REDACTED]#Ig; s#(postgresql://[^:[:space:]]+:)[^@[:space:]]+#\1[REDACTED]#g' >&2 || true
    fi
  fi
  if (( COMPOSE_STARTED )); then
    docker compose --project-name "${PROJECT}" --env-file "${WORK_DIR}/secrets.env" -f "${COMPOSE_FILE}" down --volumes --remove-orphans --timeout 15 >/dev/null 2>&1 || true
  fi
  [[ -z "${WORK_DIR}" ]] || rm -rf -- "${WORK_DIR}"
  exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

umask 077
WORK_DIR="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/rivune-release-journey.XXXXXX")"
chmod 700 "${WORK_DIR}"
database_password="$(openssl rand -hex 32)"
setup_token="$(openssl rand -hex 32)"
encryption_key="$(openssl rand -hex 32)"
admin_password="$(openssl rand -hex 32)"
release_port="$(node -e 'const net = require("node:net"); const server = net.createServer(); server.listen(0, "127.0.0.1", () => { const address = server.address(); if (typeof address === "object" && address !== null) process.stdout.write(String(address.port)); server.close(); });')"
[[ "${release_port}" =~ ^[0-9]+$ ]] && (( release_port > 0 && release_port <= 65535 )) || { echo 'Could not reserve a valid loopback port for the release journey' >&2; exit 1; }
for secret in "${database_password}" "${setup_token}" "${encryption_key}" "${admin_password}"; do
  if [[ -n "${GITHUB_ACTIONS:-}" ]]; then printf '::add-mask::%s\n' "${secret}"; fi
done
cat >"${WORK_DIR}/secrets.env" <<EOF
RIVUNE_RELEASE_IMAGE=${IMAGE}
RIVUNE_DATABASE_PASSWORD=${database_password}
RIVUNE_SETUP_TOKEN=${setup_token}
RIVUNE_ENCRYPTION_KEYS=1:${encryption_key}
RIVUNE_SOURCE_ROOT=${ROOT_DIR}
RIVUNE_RELEASE_PORT=${release_port}
EOF
chmod 600 "${WORK_DIR}/secrets.env"
permissions="$(stat -c '%a' "${WORK_DIR}/secrets.env" 2>/dev/null || stat -f '%Lp' "${WORK_DIR}/secrets.env")"
[[ "${permissions: -3}" == 600 ]] || { echo 'Private environment file does not have mode 0600' >&2; exit 1; }

docker pull "${IMAGE}"
docker pull postgres:18-trixie@sha256:06cad38a5d9f5d24b4d83d86def30795d5e4b757fedbf5281172b576dedcd941
docker pull python:3.13-alpine@sha256:540c7d91f98ff6880174c40e99067bf5941eb54d818a7a5e094d188b196a934d
COMPOSE_STARTED=1
docker compose --project-name "${PROJECT}" --env-file "${WORK_DIR}/secrets.env" -f "${COMPOSE_FILE}" up --detach --wait --wait-timeout 120
rivune_port="${release_port}"
docker compose --project-name "${PROJECT}" --env-file "${WORK_DIR}/secrets.env" -f "${COMPOSE_FILE}" exec -T fixture python3 -c '
import http.client
connection = http.client.HTTPConnection("127.0.0.1", 8081, timeout=2)
connection.request("GET", "/media/%2e%2e/manifest.json")
traversal = connection.getresponse()
assert traversal.status == 400, f"fixture traversal returned {traversal.status}"
traversal.read()
connection.request("GET", "/media/demo-720p.mp4", headers={"Range": "bytes=0-1023"})
ranged = connection.getresponse()
body = ranged.read()
assert ranged.status == 206 and len(body) == 1024, f"fixture range returned {ranged.status}/{len(body)}"
'

export RIVUNE_RELEASE_BASE_URL="http://127.0.0.1:${rivune_port}"
export RIVUNE_RELEASE_SETUP_TOKEN="${setup_token}"
export RIVUNE_RELEASE_ADMIN_PASSWORD="${admin_password}"
export RIVUNE_RELEASE_ADDON_MANIFEST_URL="http://172.29.254.10:8081/manifest.json"
(
  cd "${ROOT_DIR}/web"
  timeout 180 npx playwright test --config=playwright.release-journey.config.ts
)
printf 'Release journey passed for %s\n' "${IMAGE}"
