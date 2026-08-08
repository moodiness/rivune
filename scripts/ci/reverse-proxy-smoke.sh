#!/usr/bin/env bash
set -euo pipefail

IMAGE="${RIVUNE_IMAGE:-rivune-ci:current}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2.10.2-alpine}"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.13-alpine}"
RUN_ID="${GITHUB_RUN_ID:-local}-$$"
NETWORK="rivune-proxy-${RUN_ID}"
POSTGRES="rivune-proxy-postgres-${RUN_ID}"
RIVUNE="rivune-proxy-app-${RUN_ID}"
CADDY="rivune-proxy-caddy-${RUN_ID}"
PROBE="rivune-proxy-probe-${RUN_ID}"
CADDY_DATA="rivune-proxy-caddy-data-${RUN_ID}"
PASSWORD="proxy-test-password"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

cleanup() {
  docker rm -f "${CADDY}" "${RIVUNE}" "${PROBE}" "${POSTGRES}" >/dev/null 2>&1 || true
  docker volume rm "${CADDY_DATA}" >/dev/null 2>&1 || true
  docker network rm "${NETWORK}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_postgres() {
  for _ in $(seq 1 60); do
    if docker exec "${POSTGRES}" pg_isready -U rivune -d rivune >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${POSTGRES}"
  return 1
}

wait_for_internal_health() {
  for _ in $(seq 1 60); do
    if docker run --rm --network "${NETWORK}" curlimages/curl:8.12.1 \
      --fail --silent "http://rivune:8080/health" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${RIVUNE}"
  return 1
}

https_request() {
  docker run --rm --user 0:0 --network "${NETWORK}" \
    -v "${CADDY_DATA}:/data:ro" \
    curlimages/curl:8.12.1 \
    --cacert /data/caddy/pki/authorities/local/root.crt \
    --connect-to "localhost:443:${CADDY}:443" \
    "$@"
}

start_caddy() {
  docker run -d --name "${CADDY}" --network "${NETWORK}" \
    -e RIVUNE_HOST=localhost \
    -v "${ROOT}/deploy/caddy/Caddyfile:/etc/caddy/Caddyfile:ro" \
    -v "${CADDY_DATA}:/data" \
    "${CADDY_IMAGE}" >/dev/null
  for _ in $(seq 1 60); do
    if docker exec "${CADDY}" test -s /data/caddy/pki/authorities/local/root.crt >/dev/null 2>&1 \
      && https_request --fail --silent "https://localhost/health" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${CADDY}"
  return 1
}

docker network create "${NETWORK}" >/dev/null
docker volume create "${CADDY_DATA}" >/dev/null
docker run -d --name "${POSTGRES}" --network "${NETWORK}" --network-alias postgres \
  -e POSTGRES_DB=rivune -e POSTGRES_USER=rivune -e POSTGRES_PASSWORD="${PASSWORD}" \
  "${POSTGRES_IMAGE}" >/dev/null
wait_for_postgres

docker run -d --name "${RIVUNE}" --network "${NETWORK}" --network-alias rivune \
  -e RIVUNE_DATABASE_URL="postgres://rivune:${PASSWORD}@postgres:5432/rivune?sslmode=disable" \
  -e RIVUNE_SETUP_TOKEN=proxy-test-setup-token \
  -e RIVUNE_PUBLIC_URL=https://localhost \
  -e RIVUNE_TRUSTED_PROXIES=172.16.0.0/12 \
  "${IMAGE}" >/dev/null
wait_for_internal_health
start_caddy

https_request --fail --silent --show-error \
  "https://localhost/.well-known/rivune" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["apiBaseUrl"] == "https://localhost/api/v1"; assert value["protocolVersion"] == 20; assert value["interfaceLanguage"] == "en"'

docker rm -f "${CADDY}" "${RIVUNE}" >/dev/null
docker run -d --name "${PROBE}" --network "${NETWORK}" --network-alias rivune \
  -v "${ROOT}/scripts/ci/proxy-header-probe.py:/probe.py:ro" \
  "${PYTHON_IMAGE}" python /probe.py >/dev/null
start_caddy

https_request --fail --silent --show-error \
  -H 'X-Forwarded-For: 198.51.100.99' \
  "https://localhost/proxy-probe" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["forwardedProto"] == "https"; assert value["forwardedHost"] == "localhost"; assert value["forwardedFor"] and "198.51.100.99" not in value["forwardedFor"]; assert value["realIP"] == value["forwardedFor"]'

echo "Caddy TLS, Rivune discovery, and sanitized forwarded headers passed."
