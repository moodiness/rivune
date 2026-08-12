#!/usr/bin/env bash
set -euo pipefail

if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  export MSYS2_ARG_CONV_EXCL='/CN=;/proxy;/probe.py;/etc/nginx/test'
fi

IMAGE="${RIVUNE_IMAGE:-rivune-ci:current}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
NGINX_IMAGE="${NGINX_IMAGE:-nginx:1.28.0-alpine}"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.13-alpine}"
RUN_ID="${GITHUB_RUN_ID:-local}-$$"
NETWORK="rivune-proxy-${RUN_ID}"
POSTGRES="rivune-proxy-postgres-${RUN_ID}"
RIVUNE="rivune-proxy-app-${RUN_ID}"
NGINX="rivune-proxy-nginx-${RUN_ID}"
PROBE="rivune-proxy-probe-${RUN_ID}"
PASSWORD="proxy-test-password"
ENCRYPTION_KEYS="1:1212121212121212121212121212121212121212121212121212121212121212"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROXY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/rivune-proxy.XXXXXX")"
DOCKER_PROXY_DIR="${PROXY_DIR}"
if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  DOCKER_PROXY_DIR="$(cygpath -w "${PROXY_DIR}")"
fi

cleanup() {
  docker rm -f "${NGINX}" "${RIVUNE}" "${PROBE}" "${POSTGRES}" >/dev/null 2>&1 || true
  docker network rm "${NETWORK}" >/dev/null 2>&1 || true
  rm -rf "${PROXY_DIR}"
}
trap cleanup EXIT

openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 1 \
  -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost' \
  -keyout "${PROXY_DIR}/tls.key" \
  -out "${PROXY_DIR}/tls.crt" >/dev/null 2>&1

cat >"${PROXY_DIR}/nginx.conf" <<'EOF'
events {}

http {
  access_log off;

  server {
    listen 443 ssl;
    server_name localhost;

    ssl_certificate /etc/nginx/test/tls.crt;
    ssl_certificate_key /etc/nginx/test/tls.key;

    location / {
      proxy_pass http://rivune:8080;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto https;
      proxy_set_header X-Forwarded-Host $host;
    }
  }
}
EOF

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

wait_for_internal_ready() {
  for _ in $(seq 1 60); do
    if docker run --rm --network "${NETWORK}" curlimages/curl:8.12.1 \
      --fail --silent "http://rivune:8080/ready" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${RIVUNE}"
  return 1
}

https_request() {
  docker run --rm --user 0:0 --network "${NETWORK}" \
    -v "${DOCKER_PROXY_DIR}:/proxy:ro" \
    curlimages/curl:8.12.1 \
    --cacert /proxy/tls.crt \
    --connect-to "localhost:443:${NGINX}:443" \
    "$@"
}

start_nginx() {
  docker run -d --name "${NGINX}" --network "${NETWORK}" \
    -v "${DOCKER_PROXY_DIR}:/etc/nginx/test:ro" \
    "${NGINX_IMAGE}" nginx -c /etc/nginx/test/nginx.conf -g 'daemon off;' >/dev/null
  for _ in $(seq 1 60); do
    if https_request --fail --silent "https://localhost/ready" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${NGINX}"
  return 1
}

docker network create "${NETWORK}" >/dev/null
docker run -d --name "${POSTGRES}" --network "${NETWORK}" --network-alias postgres \
  -e POSTGRES_DB=rivune -e POSTGRES_USER=rivune -e POSTGRES_PASSWORD="${PASSWORD}" \
  "${POSTGRES_IMAGE}" >/dev/null
wait_for_postgres

docker run -d --name "${RIVUNE}" --network "${NETWORK}" --network-alias rivune \
  -e RIVUNE_DATABASE_URL="postgres://rivune:${PASSWORD}@postgres:5432/rivune?sslmode=disable" \
  -e RIVUNE_SETUP_TOKEN=proxy-test-setup-token \
  -e RIVUNE_ENCRYPTION_KEYS="${ENCRYPTION_KEYS}" \
  -e RIVUNE_PUBLIC_URL=https://localhost \
  -e RIVUNE_TRUSTED_PROXIES=172.16.0.0/12 \
  "${IMAGE}" >/dev/null
wait_for_internal_ready
docker restart "${RIVUNE}" >/dev/null
wait_for_internal_ready
start_nginx

https_request --fail --silent --show-error \
  "https://localhost/.well-known/rivune" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["apiBaseUrl"] == "https://localhost/api/v1"; assert value["protocolVersion"] == 20; assert value["interfaceLanguage"] == "en"'

https_request --fail --silent --show-error "https://localhost/health" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["status"] == "ok"; assert value["database"] == "ok"'
https_request --fail --silent --show-error "https://localhost/ready" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["status"] == "ok"; assert value["database"] == "ok"'
https_request --fail --silent --show-error "https://localhost/live" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["status"] == "ok"; assert "database" not in value'

docker rm -f "${NGINX}" "${RIVUNE}" >/dev/null
docker run -d --name "${PROBE}" --network "${NETWORK}" --network-alias rivune \
  -v "${ROOT}/scripts/ci/proxy-header-probe.py:/probe.py:ro" \
  "${PYTHON_IMAGE}" python /probe.py >/dev/null
start_nginx

https_request --fail --silent --show-error \
  -H 'X-Forwarded-For: 198.51.100.99' \
  -H 'X-Real-IP: 198.51.100.98' \
  -H 'X-Forwarded-Proto: http' \
  -H 'X-Forwarded-Host: attacker.example' \
  "https://localhost/proxy-probe" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["forwardedProto"] == "https"; assert value["forwardedHost"] == "localhost"; assert value["forwardedFor"] and "198.51.100.99" not in value["forwardedFor"]; assert value["realIP"] == value["forwardedFor"]'

echo "Nginx TLS, Rivune discovery, health probes, and sanitized forwarded headers passed."
