#!/usr/bin/env bash
set -euo pipefail

: "${RIVUNE_DATABASE_PASSWORD:?RIVUNE_DATABASE_PASSWORD is required}"
: "${RIVUNE_RESTORE_PASSWORD:?RIVUNE_RESTORE_PASSWORD is required}"
if [[ "${RIVUNE_DATABASE_PASSWORD}" == "${RIVUNE_RESTORE_PASSWORD}" || \
      "${RIVUNE_DATABASE_PASSWORD}" == "${POSTGRES_PASSWORD}" || \
      "${RIVUNE_RESTORE_PASSWORD}" == "${POSTGRES_PASSWORD}" ]]; then
  echo "PostgreSQL bootstrap, application, and restore passwords must be distinct" >&2
  exit 1
fi

psql --set ON_ERROR_STOP=1 --username "${POSTGRES_USER}" --dbname postgres \
  --set=application_password="${RIVUNE_DATABASE_PASSWORD}" \
  --set=restore_password="${RIVUNE_RESTORE_PASSWORD}" <<'SQL'
SELECT 'CREATE ROLE rivune_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rivune_owner') \gexec
ALTER ROLE rivune_owner WITH NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

SELECT 'CREATE ROLE rivune LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rivune') \gexec
ALTER ROLE rivune WITH LOGIN PASSWORD :'application_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

SELECT 'CREATE ROLE rivune_restore LOGIN NOSUPERUSER CREATEDB NOCREATEROLE NOREPLICATION'
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rivune_restore') \gexec
ALTER ROLE rivune_restore WITH LOGIN PASSWORD :'restore_password'
  NOSUPERUSER CREATEDB NOCREATEROLE NOREPLICATION;

GRANT rivune_owner TO rivune, rivune_restore;

SELECT 'CREATE DATABASE rivune OWNER rivune_owner'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'rivune') \gexec
ALTER DATABASE rivune OWNER TO rivune_owner;
SQL
