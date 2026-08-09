#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ASSET_DIR="${ROOT_DIR}/server/internal/demo/assets"
MEDIA_DIR="${SCRIPT_DIR}/work/media"
MOVIE_DIR="${MEDIA_DIR}/Rivune Demo (2026)"

if command -v sha256sum >/dev/null 2>&1; then
  hash_file() {
    local result
    result="$(sha256sum "$1")"
    printf '%s\n' "${result%% *}"
  }
elif command -v shasum >/dev/null 2>&1; then
  hash_file() {
    local result
    result="$(shasum -a 256 "$1")"
    printf '%s\n' "${result%% *}"
  }
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

verify_asset() {
  local expected="$1"
  local name="$2"
  local path="${ASSET_DIR}/${name}"
  local actual

  if [[ ! -f "${path}" || -L "${path}" ]]; then
    printf 'Missing or unsafe synthetic asset: %s\n' "${path}" >&2
    exit 1
  fi
  actual="$(hash_file "${path}")"
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'SHA-256 mismatch for synthetic asset %s\n' "${name}" >&2
    exit 1
  fi
}

# These are the hashes recorded in the repository NOTICE. Keep this list in sync
# with that provenance record; a mismatch aborts before anything is copied.
verify_asset ec7cbf6fae17c35b166df5b651ce5ebf37138c52d54372cd886773173c5274ec demo-720p.mp4
verify_asset e99c55a611c28bc0c8b2e1bc061cbd0b86a4180cbc3f42155bf3fc73fcf96c45 demo-360p.mp4
verify_asset 213f23304649d6a91f5312e1981aaf6633269ed674e49991c7e7f0bc725f6407 demo.en.vtt
verify_asset bcb1b8b9f6b012397eb9091f655f1a129dd1becedfc2182d5100b849751f8951 demo.fr.vtt
verify_asset 42a2e34b6439b84d267b6bf8bfad83ce52c17051d4e2860f8f1925f31045ff7f artwork.svg

if [[ -L "${MEDIA_DIR}" || -L "${MOVIE_DIR}" ]]; then
  echo "Refusing to prepare media through a symbolic link" >&2
  exit 1
fi

mkdir -p "${MEDIA_DIR}"
if [[ -e "${MOVIE_DIR}" ]]; then
  rm -rf "${MOVIE_DIR}"
fi
mkdir -p "${MOVIE_DIR}/extras"
cp "${ASSET_DIR}/demo-720p.mp4" "${MOVIE_DIR}/Rivune Demo (2026).mp4"
cp "${ASSET_DIR}/demo-360p.mp4" "${MOVIE_DIR}/extras/Rivune Demo Sample.mp4"
cp "${ASSET_DIR}/demo.en.vtt" "${MOVIE_DIR}/Rivune Demo (2026).en.vtt"
cp "${ASSET_DIR}/demo.fr.vtt" "${MOVIE_DIR}/Rivune Demo (2026).fr.vtt"
cp "${ASSET_DIR}/artwork.svg" "${MOVIE_DIR}/poster.svg"

printf 'Prepared verified synthetic fixture in %s\n' "${MOVIE_DIR}"
