#!/usr/bin/env bash
set -euo pipefail

if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  export MSYS_NO_PATHCONV=1
  export MSYS2_ARG_CONV_EXCL='*'
fi

IMAGE="${RIVUNE_IMAGE:-rivune-ci:current}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-trixie}"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.13-alpine}"
RUN_ID="${GITHUB_RUN_ID:-local}-$$"
DATABASE_NETWORK="rivune-unraid-database-${RUN_ID}"
EDGE_NETWORK="rivune-unraid-edge-${RUN_ID}"
POSTGRES="rivune-unraid-postgres-${RUN_ID}"
RIVUNE="rivune-unraid-app-${RUN_ID}"
ARTWORK_VOLUME="rivune-unraid-artwork-${RUN_ID}"
TRANSCODE_VOLUME="rivune-unraid-transcode-${RUN_ID}"
TLS_VOLUME="rivune-unraid-postgres-tls-${RUN_ID}"
CA_VOLUME="rivune-unraid-ca-${RUN_ID}"
PASSWORD="unraid-smoke-database-password"
SETUP_TOKEN="unraid-smoke-setup-token"
ENCRYPTION_KEYS="1:1212121212121212121212121212121212121212121212121212121212121212"

cleanup() {
  docker rm -f "${RIVUNE}" "${POSTGRES}" >/dev/null 2>&1 || true
  docker volume rm "${ARTWORK_VOLUME}" "${TRANSCODE_VOLUME}" "${TLS_VOLUME}" "${CA_VOLUME}" >/dev/null 2>&1 || true
  docker network rm "${EDGE_NETWORK}" "${DATABASE_NETWORK}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_postgres() {
  for _ in $(seq 1 60); do
    if docker exec "${POSTGRES}" pg_isready -h 127.0.0.1 -U rivune -d rivune >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "${POSTGRES}"
  return 1
}

wait_for_rivune() {
  for _ in $(seq 1 60); do
    if docker run --rm --network "${EDGE_NETWORK}" curlimages/curl:8.12.1 \
      --fail --silent "http://rivune:8080/health" >/dev/null 2>&1; then
      return
    fi
    if ! docker inspect --format '{{.State.Running}}' "${RIVUNE}" 2>/dev/null | grep -qx true; then
      docker logs "${RIVUNE}"
      return 1
    fi
    sleep 1
  done
  docker logs "${RIVUNE}"
  return 1
}

start_rivune() {
  local ssl_mode="$1"
  local root_certificate="$2"
  docker run -d --name "${RIVUNE}" --network "${DATABASE_NETWORK}" --init \
    --security-opt no-new-privileges:true --cap-drop ALL \
    --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add SETGID --cap-add SETUID \
    -p 127.0.0.1::8080 \
    -e RIVUNE_DATABASE_HOST=postgres \
    -e RIVUNE_DATABASE_PORT= \
    -e RIVUNE_DATABASE_NAME=rivune \
    -e RIVUNE_DATABASE_USER=rivune \
    -e RIVUNE_DATABASE_PASSWORD="${PASSWORD}" \
    -e RIVUNE_DATABASE_SSLMODE="${ssl_mode}" \
    -e RIVUNE_DATABASE_SSLROOTCERT="${root_certificate}" \
    -e RIVUNE_SETUP_TOKEN="${SETUP_TOKEN}" \
    -e RIVUNE_ENCRYPTION_KEYS="${ENCRYPTION_KEYS}" \
    -e RIVUNE_PUBLIC_URL=https://rivune.example.invalid \
    -e RIVUNE_TRUSTED_PROXIES="${EDGE_CIDR}" \
    -e PUID=99 -e PGID=100 \
    -e RIVUNE_MEDIA_TEMP_DIR=/transcode \
    -v "${ARTWORK_VOLUME}:/var/lib/rivune/artwork" \
    -v "${TRANSCODE_VOLUME}:/transcode" \
    -v "${CA_VOLUME}:/run/rivune-postgres-tls:ro" \
    "${IMAGE}" >/dev/null
  docker network connect --alias rivune "${EDGE_NETWORK}" "${RIVUNE}"
  wait_for_rivune
}

docker network create --internal "${DATABASE_NETWORK}" >/dev/null
docker network create "${EDGE_NETWORK}" >/dev/null
EDGE_CIDR="$(docker network inspect "${EDGE_NETWORK}" --format '{{(index .IPAM.Config 0).Subnet}}')"
docker volume create "${ARTWORK_VOLUME}" >/dev/null
docker volume create "${TRANSCODE_VOLUME}" >/dev/null
docker volume create "${TLS_VOLUME}" >/dev/null
docker volume create "${CA_VOLUME}" >/dev/null

docker run --rm --user 0:0 -v "${TLS_VOLUME}:/tls" "${POSTGRES_IMAGE}" bash -ceu '
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tls/ca.key
  openssl req -x509 -new -sha256 -days 1 -key /tls/ca.key -subj "/CN=Rivune smoke CA" -out /tls/ca.crt
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tls/server.key
  openssl req -new -sha256 -key /tls/server.key -subj "/CN=postgres" -out /tls/server.csr
  printf "%s\n" "subjectAltName=DNS:postgres" "extendedKeyUsage=serverAuth" > /tls/server.ext
  openssl x509 -req -sha256 -days 1 -in /tls/server.csr -CA /tls/ca.crt -CAkey /tls/ca.key -CAcreateserial -extfile /tls/server.ext -out /tls/server.crt
  chown postgres:postgres /tls/server.crt /tls/server.key
  chmod 600 /tls/server.key
  chmod 644 /tls/server.crt /tls/ca.crt
' >/dev/null
docker run --rm --user 0:0 -v "${TLS_VOLUME}:/source:ro" -v "${CA_VOLUME}:/dest" "${POSTGRES_IMAGE}" \
  bash -ceu 'cp /source/ca.crt /dest/ca.crt && chmod 644 /dest/ca.crt' >/dev/null

docker run -d --name "${POSTGRES}" --network "${DATABASE_NETWORK}" --network-alias postgres \
  -e POSTGRES_DB=rivune -e POSTGRES_USER=rivune -e POSTGRES_PASSWORD="${PASSWORD}" \
  -v "${TLS_VOLUME}:/tls:ro" \
  "${POSTGRES_IMAGE}" -c ssl=on -c ssl_cert_file=/tls/server.crt -c ssl_key_file=/tls/server.key >/dev/null
wait_for_postgres

start_rivune disable ""
docker exec --user 99:100 "${RIVUNE}" sh -ceu \
  'printf "%s\n" persisted > /var/lib/rivune/artwork/unraid-persistence-smoke'

docker run --rm --network "${EDGE_NETWORK}" curlimages/curl:8.12.1 \
  --fail --silent --show-error "http://rivune:8080/.well-known/rivune" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; value=json.load(sys.stdin); assert value["apiBaseUrl"] == "https://rivune.example.invalid/api/v1"'

HOST_BINDING="$(docker port "${RIVUNE}" 8080/tcp)"
HOST_PORT="${HOST_BINDING##*:}"
curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/health" >/dev/null
curl --silent --show-error \
  --request POST \
  --header 'Host: 192.168.1.20:18080' \
  --header 'Origin: http://192.168.1.20:18080' \
  --header 'X-Rivune-CSRF: 1' \
  --write-out '\n%{http_code}' \
  "http://127.0.0.1:${HOST_PORT}/api/v1/auth/web/refresh" \
  | docker run --rm -i "${PYTHON_IMAGE}" python -c 'import json,sys; body,status=sys.stdin.read().rsplit("\n",1); value=json.loads(body); assert status == "401", (status, value); assert value["error"]["code"] == "invalid_refresh_token", value'


test "$(docker inspect "${RIVUNE}" --format '{{len .HostConfig.Devices}}')" = 0
docker exec -e PGPASSWORD="${PASSWORD}" "${POSTGRES}" \
  psql --username rivune --dbname rivune --tuples-only --no-align \
  --command 'SELECT bool_and(NOT ssl) FROM pg_stat_ssl;' \
  | grep -qx t

docker rm -f "${RIVUNE}" >/dev/null
docker run --rm --user 0:0 --entrypoint sh -v "${TRANSCODE_VOLUME}:/transcode" "${IMAGE}" -ceu '
  mkdir -p /transcode/rivune-media/orphaned-session
  printf stale >/transcode/rivune-media/orphaned-session/segment.m4s
  chown -R 12345:12346 /transcode/rivune-media
  chmod 700 /transcode/rivune-media /transcode/rivune-media/orphaned-session
' >/dev/null
start_rivune disable ""
docker exec "${RIVUNE}" test ! -e /transcode/rivune-media/orphaned-session/segment.m4s
docker exec --user 99:100 "${RIVUNE}" \
  grep -qx persisted /var/lib/rivune/artwork/unraid-persistence-smoke

docker restart "${RIVUNE}" >/dev/null
wait_for_rivune

docker rm -f "${RIVUNE}" >/dev/null
start_rivune verify-full /run/rivune-postgres-tls/ca.crt
TLS_CONNECTIONS="$(docker exec -e PGPASSWORD="${PASSWORD}" -e PGSSLMODE=disable "${POSTGRES}" \
  psql --username rivune --dbname rivune --tuples-only --no-align \
  --command "SELECT count(*) FROM pg_stat_activity AS a JOIN pg_stat_ssl AS s USING (pid) WHERE a.usename = 'rivune' AND a.pid <> pg_backend_pid() AND s.ssl;")"
[[ "${TLS_CONNECTIONS}" -ge 1 ]]
docker restart "${RIVUNE}" >/dev/null
wait_for_rivune

echo "Unraid-style clean install, direct private-IP browser auth without Fetch Metadata, isolated plaintext and verify-full PostgreSQL modes, CPU-only startup, edge target, optional host port, and restart passed."
