#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TEST_DIR}"
}
trap cleanup EXIT

prepare_case() {
  local case_dir="$1"
  mkdir -p "${case_dir}/scripts"
  cp "${ROOT_DIR}/scripts/create-env.sh" "${case_dir}/scripts/create-env.sh"
  cp "${ROOT_DIR}/.env.example" "${case_dir}/.env.example"
}

assert_private_copy() {
  local requested_umask="$1"
  local case_dir="${TEST_DIR}/umask-${requested_umask}"
  prepare_case "${case_dir}"

  (umask "${requested_umask}"; bash "${case_dir}/scripts/create-env.sh" >/dev/null)

  if [[ "$(stat -c '%a' "${case_dir}/.env")" != 600 ]]; then
    echo "create-env.sh did not create .env with mode 600 under umask ${requested_umask}" >&2
    exit 1
  fi
  if ! cmp -s "${case_dir}/.env.example" "${case_dir}/.env"; then
    echo "create-env.sh did not copy .env.example under umask ${requested_umask}" >&2
    exit 1
  fi
}

assert_private_copy 000
assert_private_copy 022

existing_dir="${TEST_DIR}/existing"
prepare_case "${existing_dir}"
printf 'existing-secret\n' > "${existing_dir}/.env"
chmod 644 "${existing_dir}/.env"
if bash "${existing_dir}/scripts/create-env.sh" >/dev/null 2>&1; then
  echo "create-env.sh overwrote an existing .env" >&2
  exit 1
fi
if [[ "$(cat "${existing_dir}/.env")" != 'existing-secret' ]] || \
   [[ "$(stat -c '%a' "${existing_dir}/.env")" != 644 ]]; then
  echo "create-env.sh changed an existing .env" >&2
  exit 1
fi

symlink_dir="${TEST_DIR}/symlink"
prepare_case "${symlink_dir}"
printf 'symlink-target-secret\n' > "${symlink_dir}/target"
ln -s "${symlink_dir}/target" "${symlink_dir}/.env"
if bash "${symlink_dir}/scripts/create-env.sh" >/dev/null 2>&1; then
  echo "create-env.sh accepted a symlink destination" >&2
  exit 1
fi
if [[ ! -L "${symlink_dir}/.env" ]] || \
   [[ "$(cat "${symlink_dir}/target")" != 'symlink-target-secret' ]]; then
  echo "create-env.sh changed a symlink destination or its target" >&2
  exit 1
fi
