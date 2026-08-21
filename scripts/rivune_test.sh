#!/usr/bin/env bash
set -euo pipefail
# Most cases exercise platform-neutral behavior and inject command doubles.
# The dedicated macOS case overrides this value explicitly.
export OSTYPE=linux-gnu

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
cleanup() {
  rm -rf -- "${TEST_DIR}"
}
trap cleanup EXIT

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

prepare_case() {
  local name="$1"
  local case_dir="${TEST_DIR}/${name}"
  mkdir -p "${case_dir}/root/scripts" "${case_dir}/bin" "${case_dir}/work"
  cp "${ROOT_DIR}/scripts/macos-discovery.sh" "${case_dir}/root/scripts/macos-discovery.sh"
  chmod +x "${case_dir}/root/scripts/macos-discovery.sh"
  cp "${ROOT_DIR}/rivune" "${case_dir}/root/rivune"
  cp "${ROOT_DIR}/.env.example" "${case_dir}/root/.env.example"
  chmod +x "${case_dir}/root/rivune"
  printf '%s\n' "${case_dir}"
}

write_environment() {
  local root="$1"
  cat > "${root}/.env" <<'ENV'
RIVUNE_POSTGRES_SUPERUSER_PASSWORD=superuser-secret
RIVUNE_DATABASE_PASSWORD=database-secret
RIVUNE_RESTORE_PASSWORD=restore-secret
RIVUNE_SETUP_TOKEN=setup-secret
RIVUNE_ENCRYPTION_KEYS=1:1212121212121212121212121212121212121212121212121212121212121212
RIVUNE_PUBLIC_URL=https://media.example.com
RIVUNE_DISCOVERY_URL=https://media.example.com
RIVUNE_DISCOVERY_NAME=Rivune
RIVUNE_VERSION=1.6.0
RIVUNE_PORT=8080
ENV
  chmod 600 "${root}/.env"
}

assert_file_equals() {
  local expected="$1" actual="$2" label="$3"
  if ! cmp -s "${expected}" "${actual}"; then
    printf '%s differs:\n' "${label}" >&2
    diff -u "${expected}" "${actual}" >&2 || true
    exit 1
  fi
}

make_fake_openssl() {
  local case_dir="$1"
  cat > "${case_dir}/bin/openssl" <<'FAKE_OPENSSL'
#!/usr/bin/env bash
set -euo pipefail
printf '%q ' "$@" >> "${OPENSSL_LOG}"
printf '\n' >> "${OPENSSL_LOG}"
[[ "$*" == 'rand -hex 32' ]] || exit 91
count=0
[[ ! -f "${OPENSSL_COUNT}" ]] || count="$(cat "${OPENSSL_COUNT}")"
count=$((count + 1))
printf '%s\n' "${count}" > "${OPENSSL_COUNT}"
if [[ -n "${OPENSSL_FAIL_AT:-}" && "${count}" == "${OPENSSL_FAIL_AT}" ]]; then
  exit 1
fi
printf '%064x\n' "${count}"
FAKE_OPENSSL
  chmod +x "${case_dir}/bin/openssl"
}

assert_no_setup_artifacts() {
  local root="$1" label="$2"
  [[ ! -e "${root}/.env" && ! -L "${root}/.env" ]] || fail "${label} left a .env path"
  if compgen -G "${root}/.rivune-env.*" >/dev/null; then
    fail "${label} left a temporary environment file"
  fi
}

setup_case="$(prepare_case setup)"
make_fake_openssl "${setup_case}"
: > "${setup_case}/openssl.log"
export OPENSSL_LOG="${setup_case}/openssl.log" OPENSSL_COUNT="${setup_case}/openssl.count"
setup_output="${setup_case}/setup.output"
(
  cd "${setup_case}/work"
  PATH="${setup_case}/bin:${PATH}" "${setup_case}/root/rivune" setup \
    --public-url https://media.example.com --discovery-name 'Living room' --version 1.6.0
) >"${setup_output}" 2>&1
[[ "$(stat -c '%a' "${setup_case}/root/.env")" == 600 ]] || fail 'setup did not apply mode 0600'
[[ "$(wc -l < "${setup_case}/openssl.log")" == 5 ]] || fail 'setup did not invoke OpenSSL independently five times'
mapfile -t generated < <(sed -n -E 's/^(RIVUNE_POSTGRES_SUPERUSER_PASSWORD|RIVUNE_DATABASE_PASSWORD|RIVUNE_RESTORE_PASSWORD|RIVUNE_SETUP_TOKEN)=([0-9a-f]+)$/\2/p; s/^RIVUNE_ENCRYPTION_KEYS=1:([0-9a-f]+)$/\1/p' "${setup_case}/root/.env")
[[ "${#generated[@]}" == 5 ]] || fail 'setup did not populate all five generated secrets'
[[ "$(printf '%s\n' "${generated[@]}" | sort -u | wc -l)" == 5 ]] || fail 'setup reused generated secret material'
grep -qx 'RIVUNE_PUBLIC_URL=https://media.example.com' "${setup_case}/root/.env" || fail 'setup did not set the public origin'
grep -qx 'RIVUNE_DISCOVERY_URL=https://media.example.com' "${setup_case}/root/.env" || fail 'setup did not configure the announced origin'
grep -qx 'RIVUNE_DISCOVERY_NAME=Living room' "${setup_case}/root/.env" || fail 'setup did not configure the selected discovery name'
for secret in "${generated[@]}"; do
  if grep -Fq "${secret}" "${setup_output}"; then
    fail 'setup echoed a generated secret'
  fi
done

lan_setup_case="$(prepare_case setup-lan-http)"
make_fake_openssl "${lan_setup_case}"
export OPENSSL_LOG="${lan_setup_case}/openssl.log" OPENSSL_COUNT="${lan_setup_case}/openssl.count"
(
  cd "${lan_setup_case}/work"
  PATH="${lan_setup_case}/bin:${PATH}" "${lan_setup_case}/root/rivune" setup \
    --public-url http://192.168.1.20:8080 --version 1.9.0
) >/dev/null 2>&1
grep -qx 'RIVUNE_PUBLIC_URL=http://192.168.1.20:8080' "${lan_setup_case}/root/.env" ||
  fail 'setup did not accept the private-LAN HTTP origin'
grep -qx 'RIVUNE_BIND_ADDRESS=127.0.0.1' "${lan_setup_case}/root/.env" ||
  fail 'setup exposed the LAN port without the separate binding opt-in'

failed_generation_case="$(prepare_case failed-generation)"
make_fake_openssl "${failed_generation_case}"
export OPENSSL_LOG="${failed_generation_case}/openssl.log" OPENSSL_COUNT="${failed_generation_case}/openssl.count"
if OPENSSL_FAIL_AT=3 PATH="${failed_generation_case}/bin:${PATH}" \
  "${failed_generation_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup succeeded after secret generation failed'
fi
assert_no_setup_artifacts "${failed_generation_case}/root" 'failed secret generation'

interrupted_case="$(prepare_case interrupted-generation)"
make_fake_openssl "${interrupted_case}"
cat > "${interrupted_case}/bin/chmod" <<'INTERRUPTING_CHMOD'
#!/usr/bin/env bash
set -euo pipefail
kill -TERM "${PPID}"
exit 143
INTERRUPTING_CHMOD
chmod +x "${interrupted_case}/bin/chmod"
export OPENSSL_LOG="${interrupted_case}/openssl.log" OPENSSL_COUNT="${interrupted_case}/openssl.count"
if PATH="${interrupted_case}/bin:${PATH}" \
  "${interrupted_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup succeeded after interruption before publication'
fi
assert_no_setup_artifacts "${interrupted_case}/root" 'interrupted setup'

failed_publication_case="$(prepare_case failed-publication)"
make_fake_openssl "${failed_publication_case}"
cat > "${failed_publication_case}/bin/ln" <<'FAILING_LN'
#!/usr/bin/env bash
exit 1
FAILING_LN
chmod +x "${failed_publication_case}/bin/ln"
export OPENSSL_LOG="${failed_publication_case}/openssl.log" OPENSSL_COUNT="${failed_publication_case}/openssl.count"
if PATH="${failed_publication_case}/bin:${PATH}" \
  "${failed_publication_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup succeeded after atomic publication failed'
fi
assert_no_setup_artifacts "${failed_publication_case}/root" 'failed publication'

existing_case="$(prepare_case existing)"
make_fake_openssl "${existing_case}"
printf 'do-not-change\n' > "${existing_case}/root/.env"
chmod 644 "${existing_case}/root/.env"
export OPENSSL_LOG="${existing_case}/openssl.log" OPENSSL_COUNT="${existing_case}/openssl.count"
if PATH="${existing_case}/bin:${PATH}" "${existing_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup overwrote an existing .env'
fi
[[ "$(cat "${existing_case}/root/.env")" == 'do-not-change' ]] || fail 'setup changed existing .env contents'
[[ "$(stat -c '%a' "${existing_case}/root/.env")" == 644 ]] || fail 'setup changed existing .env permissions'

publication_race_case="$(prepare_case publication-race)"
make_fake_openssl "${publication_race_case}"
cat > "${publication_race_case}/bin/ln" <<'RACING_LN'
#!/usr/bin/env bash
set -euo pipefail
printf 'racing-path\n' > .env
exit 1
RACING_LN
chmod +x "${publication_race_case}/bin/ln"
export OPENSSL_LOG="${publication_race_case}/openssl.log" OPENSSL_COUNT="${publication_race_case}/openssl.count"
if PATH="${publication_race_case}/bin:${PATH}" \
  "${publication_race_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup overwrote a path created during publication'
fi
[[ "$(cat "${publication_race_case}/root/.env")" == 'racing-path' ]] || \
  fail 'setup changed a path created during publication'
if compgen -G "${publication_race_case}/root/.rivune-env.*" >/dev/null; then
  fail 'failed racing publication left a temporary environment file'
fi

symlink_case="$(prepare_case symlink)"
make_fake_openssl "${symlink_case}"
printf 'symlink-target\n' > "${symlink_case}/target"
ln -s "${symlink_case}/target" "${symlink_case}/root/.env"
export OPENSSL_LOG="${symlink_case}/openssl.log" OPENSSL_COUNT="${symlink_case}/openssl.count"
if PATH="${symlink_case}/bin:${PATH}" "${symlink_case}/root/rivune" setup --version 1.6.0 >/dev/null 2>&1; then
  fail 'setup accepted a symlink .env'
fi
[[ "$(cat "${symlink_case}/target")" == 'symlink-target' ]] || fail 'setup changed a symlink target'

missing_version_case="$(prepare_case missing-version)"
make_fake_openssl "${missing_version_case}"
export OPENSSL_LOG="${missing_version_case}/openssl.log" OPENSSL_COUNT="${missing_version_case}/openssl.count"
missing_version_output="${missing_version_case}/setup.output"
if PATH="${missing_version_case}/bin:${PATH}" \
  "${missing_version_case}/root/rivune" setup >"${missing_version_output}" 2>&1; then
  fail 'setup accepted an omitted version'
fi
grep -Fq -- '--version is required' "${missing_version_output}" || fail 'setup did not explain the required version'
[[ ! -s "${missing_version_case}/openssl.log" ]] || fail 'setup checked an omitted version after invoking OpenSSL'
assert_no_setup_artifacts "${missing_version_case}/root" 'setup without a version'

validation_index=0
for hostile in \
  $'https://media.example.com\nRIVUNE_VERSION=evil' \
  'https://user@media.example.com' \
  'https://media.example.com/path' \
  'https://media.example.com?query' \
  'http://media.example.com' \
  'http://198.51.100.20:8080' \
  'http://169.254.1.20:8080' \
  'https://media.example.com:70000'; do
  validation_case="$(prepare_case "validation-${validation_index}")"
  make_fake_openssl "${validation_case}"
  export OPENSSL_LOG="${validation_case}/openssl.log" OPENSSL_COUNT="${validation_case}/openssl.count"
  if PATH="${validation_case}/bin:${PATH}" "${validation_case}/root/rivune" setup --version 1.6.0 --public-url "${hostile}" >/dev/null 2>&1; then
    fail "setup accepted hostile public URL: ${hostile}"
  fi
  [[ ! -s "${validation_case}/openssl.log" ]] || fail 'setup validated a hostile URL after invoking OpenSSL'
  [[ ! -e "${validation_case}/root/.env" ]] || fail 'setup created .env for a hostile URL'
  validation_index=$((validation_index + 1))
done
version_index=0
for hostile_version in latest '../latest' '1.6' 'v1.6.0' '1.6.0;touch-pwned' $'1.6.0\nevil'; do
  validation_case="$(prepare_case "version-${version_index}")"
  make_fake_openssl "${validation_case}"
  export OPENSSL_LOG="${validation_case}/openssl.log" OPENSSL_COUNT="${validation_case}/openssl.count"
  version_output="${validation_case}/setup.output"
  if PATH="${validation_case}/bin:${PATH}" "${validation_case}/root/rivune" setup --version "${hostile_version}" >"${version_output}" 2>&1; then
    fail "setup accepted hostile version: ${hostile_version}"
  fi
  [[ ! -s "${validation_case}/openssl.log" ]] || fail 'setup validated a hostile version after invoking OpenSSL'
  grep -Fq -- '--version must be a stable numeric version' "${version_output}" || \
    fail 'setup did not explain the stable numeric version requirement'
  if [[ "${hostile_version}" == latest ]]; then
    grep -Fq 'mutable tags such as latest are not supported' "${version_output}" || \
      fail 'setup did not explain why latest is rejected'
  fi
  [[ ! -e "${validation_case}/root/.env" ]] || fail 'setup created .env for an invalid version'
  if compgen -G "${validation_case}/root/.rivune-env.*" >/dev/null; then
    fail 'invalid version left a temporary environment file'
  fi
  version_index=$((version_index + 1))
done

macos_case="$(prepare_case macos-discovery)"
write_environment "${macos_case}/root"
export ARGV_LOG="${macos_case}/argv.log"
cat > "${macos_case}/bin/docker" <<'FAKE_MACOS_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" >> "${ARGV_LOG}"
FAKE_MACOS_DOCKER
cat > "${macos_case}/root/scripts/macos-discovery.sh" <<'FAKE_MACOS_DISCOVERY'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" >> "${ARGV_LOG}"
FAKE_MACOS_DISCOVERY
chmod +x "${macos_case}/bin/docker" "${macos_case}/root/scripts/macos-discovery.sh"
OSTYPE=darwin25 PATH="${macos_case}/bin:${PATH}" "${macos_case}/root/rivune" up
cat > "${macos_case}/expected" <<'EXPECTED_MACOS_UP'
<compose>
<--env-file>
<.env>
<-f>
<compose.yaml>
<up>
<-d>
<start>
<https://media.example.com>
<Rivune>
<1.6.0>
EXPECTED_MACOS_UP
assert_file_equals "${macos_case}/expected" "${ARGV_LOG}" 'macOS up and discovery argv'
: > "${ARGV_LOG}"
OSTYPE=darwin25 PATH="${macos_case}/bin:${PATH}" "${macos_case}/root/rivune" down
cat > "${macos_case}/expected" <<'EXPECTED_MACOS_DOWN'
<compose>
<--env-file>
<.env>
<-f>
<compose.yaml>
<down>
<stop>
EXPECTED_MACOS_DOWN
assert_file_equals "${macos_case}/expected" "${ARGV_LOG}" 'macOS down and discovery argv'
: > "${ARGV_LOG}"
OSTYPE=darwin25 PATH="${macos_case}/bin:${PATH}" "${macos_case}/root/rivune" logs discovery
printf '<logs>\n' > "${macos_case}/expected"
assert_file_equals "${macos_case}/expected" "${ARGV_LOG}" 'macOS discovery logs argv'

wrapper_case="$(prepare_case wrappers)"
write_environment "${wrapper_case}/root"
export ARGV_LOG="${wrapper_case}/argv.log"
cat > "${wrapper_case}/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" > "${ARGV_LOG}"
FAKE_DOCKER
chmod +x "${wrapper_case}/bin/docker"
assert_compose() {
  local command="$1"
  shift
  local expected="${wrapper_case}/expected"
  : > "${ARGV_LOG}"
  (
    cd "${wrapper_case}/work"
    PATH="${wrapper_case}/bin:${PATH}" "${wrapper_case}/root/rivune" "${command}" "$@"
  )
  printf '<%s>\n' compose --env-file .env -f compose.yaml > "${expected}"
  case "${command}" in
    up) printf '<%s>\n' up -d >> "${expected}" ;;
    down|restart|pull) printf '<%s>\n' "${command}" >> "${expected}" ;;
    status) printf '<%s>\n' ps >> "${expected}" ;;
    logs) printf '<%s>\n' logs "$@" >> "${expected}" ;;
  esac
  assert_file_equals "${expected}" "${ARGV_LOG}" "${command} argv"
}
assert_compose up
assert_compose down
assert_compose restart
assert_compose pull
assert_compose status
assert_compose logs
assert_compose logs postgres
assert_compose logs discovery
if PATH="${wrapper_case}/bin:${PATH}" "${wrapper_case}/root/rivune" logs 'postgres;touch-pwned' >/dev/null 2>&1; then
  fail 'logs accepted an unrecognized service'
fi

for delegated in backup backup-scheduler verify-backup restore; do
  case "${delegated}" in
    backup) helper=postgres-backup.sh; args=(archive.dump) ;;
    backup-scheduler) helper=postgres-backup-scheduler.sh; args=(--once backups) ;;
    verify-backup) helper=postgres-verify-backup.sh; args=(--expect-backup-id 0123456789abcdef0123456789abcdef archive.dump) ;;
    restore) helper=postgres-restore.sh; args=(--allow-rollback fedcba9876543210fedcba9876543210 archive.dump) ;;
  esac
  cat > "${wrapper_case}/root/scripts/${helper}" <<'FAKE_HELPER'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" > "${ARGV_LOG}"
{
  printf 'COMPOSE_FILE=%s\n' "${COMPOSE_FILE:-}"
  printf 'COMPOSE_ENV_FILES=%s\n' "${COMPOSE_ENV_FILES:-}"
  printf 'COMPOSE_PROJECT_NAME=%s\n' "${COMPOSE_PROJECT_NAME:-}"
  printf 'COMPOSE_PROFILES=%s\n' "${COMPOSE_PROFILES:-}"
  printf 'COMPOSE_PATH_SEPARATOR=%s\n' "${COMPOSE_PATH_SEPARATOR:-}"
} > "${ENV_LOG}"
FAKE_HELPER
  chmod +x "${wrapper_case}/root/scripts/${helper}"
  : > "${ARGV_LOG}"
  export ENV_LOG="${wrapper_case}/${delegated}.env.log"
  COMPOSE_FILE=/tmp/attacker-compose.yaml \
    COMPOSE_ENV_FILES=/tmp/attacker.env \
    COMPOSE_PROJECT_NAME=attacker \
    COMPOSE_PROFILES=attacker \
    COMPOSE_PATH_SEPARATOR=: \
    "${wrapper_case}/root/rivune" "${delegated}" "${args[@]}"
  printf '<%s>\n' "${args[@]}" > "${wrapper_case}/expected"
  assert_file_equals "${wrapper_case}/expected" "${ARGV_LOG}" "${delegated} argv"
  cat > "${wrapper_case}/expected-env" <<EXPECTED_ENV
COMPOSE_FILE=${wrapper_case}/root/compose.yaml
COMPOSE_ENV_FILES=${wrapper_case}/root/.env
COMPOSE_PROJECT_NAME=
COMPOSE_PROFILES=discovery
COMPOSE_PATH_SEPARATOR=
EXPECTED_ENV
done

help_output="$("${ROOT_DIR}/rivune" help)"
for command in setup up down restart pull status logs doctor backup backup-scheduler verify-backup restore help; do
  [[ "${help_output}" == *"${command}"* ]] || fail "help omitted ${command}"
done
[[ "${help_output}" == *'setup --version X.Y.Z [--public-url HTTPS_OR_LOCAL_HTTP_ORIGIN] [--discovery-name NAME]'* ]] || \
  fail 'help did not describe the supported setup origins and discovery name'

doctor_case="$(prepare_case doctor)"
write_environment "${doctor_case}/root"
export DOCTOR_LOG="${doctor_case}/doctor.log"
cat > "${doctor_case}/bin/openssl" <<'FAKE_OPENSSL_DOCTOR'
#!/usr/bin/env bash
exit 0
FAKE_OPENSSL_DOCTOR
cat > "${doctor_case}/bin/docker" <<'FAKE_DOCKER_DOCTOR'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" >> "${DOCTOR_LOG}"
case "$*" in
  'compose --env-file .env -f compose.yaml version') exit 0 ;;
  'compose --env-file .env -f compose.yaml config --quiet') exit 0 ;;
  'compose --env-file .env -f compose.yaml ps -q postgres')
    [[ "${DOCTOR_SERVICE_FAIL:-}" != postgres ]] || exit 0
    printf 'postgres-id\n'
    ;;
  'compose --env-file .env -f compose.yaml ps -q rivune')
    [[ "${DOCTOR_SERVICE_FAIL:-}" != rivune ]] || exit 0
    printf 'rivune-id\n'
    ;;
  'compose --env-file .env -f compose.yaml ps -q discovery')
    [[ "${DOCTOR_SERVICE_FAIL:-}" != discovery ]] || exit 0
    printf 'discovery-id\n'
    ;;
  'inspect --format {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}} postgres-id')
    printf 'running healthy\n'
    ;;
  'inspect --format {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}} rivune-id')
    printf 'running healthy\n'
    ;;
  'inspect --format {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}} discovery-id')
    printf 'running \n'
    ;;
  'compose --env-file .env -f compose.yaml exec -T postgres pg_isready --username rivune --dbname rivune')
    [[ "${DOCTOR_DB_FAIL:-0}" != 1 ]]
    ;;
  *) exit 93 ;;
esac
FAKE_DOCKER_DOCTOR
cat > "${doctor_case}/bin/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail
printf '<%s>\n' "$@" >> "${DOCTOR_LOG}"
url="${*: -1}"
if [[ "${DOCTOR_HTTP_FAIL:-}" == local && "${url}" == http://127.0.0.1:* ]]; then
  exit 22
fi
if [[ "${DOCTOR_HTTP_FAIL:-}" == public && "${url}" == https://* ]]; then
  exit 22
fi
FAKE_CURL
chmod +x "${doctor_case}/bin/openssl" "${doctor_case}/bin/docker" "${doctor_case}/bin/curl"
run_doctor() {
  (
    cd "${doctor_case}/work"
    PATH="${doctor_case}/bin:${PATH}" "${doctor_case}/root/rivune" doctor
  )
}
: > "${DOCTOR_LOG}"
run_doctor >/dev/null
for failure in service database local-http public-http; do
  case "${failure}" in
    service) failing_environment=(DOCTOR_SERVICE_FAIL=postgres) ;;
    database) failing_environment=(DOCTOR_DB_FAIL=1) ;;
    local-http) failing_environment=(DOCTOR_HTTP_FAIL=local) ;;
    public-http) failing_environment=(DOCTOR_HTTP_FAIL=public) ;;
  esac
  # The child shell expands positional parameters supplied below.
  # shellcheck disable=SC2016
  if env "${failing_environment[@]}" bash -c '
    cd "$1"
    PATH="$2:$PATH" "$3" doctor
  ' bash "${doctor_case}/work" "${doctor_case}/bin" "${doctor_case}/root/rivune" >/dev/null 2>&1; then
    fail "doctor did not detect ${failure} failure"
  fi
done

printf 'rivune CLI tests passed.\n'
