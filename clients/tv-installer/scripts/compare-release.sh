#!/usr/bin/env bash
set -euo pipefail

candidate_directory="${1:-}"
trusted_directory="${2:-}"
version="${3:-}"
if [[ ! -d "${candidate_directory}" ]] || [[ ! -d "${trusted_directory}" ]] || [[ "$(uname -s)" != Darwin ]]; then
  echo 'Usage: compare-release.sh CANDIDATE_DIRECTORY TRUSTED_DIRECTORY VERSION (on macOS)' >&2
  exit 1
fi
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"${script_directory}/verify-release.sh" "${candidate_directory}" "${version}"
"${script_directory}/verify-release.sh" "${trusted_directory}" "${version}"
cmp \
  "${candidate_directory}/Rivune-TV-Installer-Windows.exe" \
  "${trusted_directory}/Rivune-TV-Installer-Windows.exe"

candidate_mount="$(mktemp -d)"
trusted_mount="$(mktemp -d)"
candidate_mounted=false
trusted_mounted=false
cleanup() {
  if [[ "${candidate_mounted}" == true ]]; then hdiutil detach -quiet "${candidate_mount}" || true; fi
  if [[ "${trusted_mounted}" == true ]]; then hdiutil detach -quiet "${trusted_mount}" || true; fi
  rmdir "${candidate_mount}" "${trusted_mount}" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM
hdiutil attach -quiet -readonly -nobrowse -mountpoint "${candidate_mount}" \
  "${candidate_directory}/Rivune-TV-Installer-macOS.dmg"
candidate_mounted=true
hdiutil attach -quiet -readonly -nobrowse -mountpoint "${trusted_mount}" \
  "${trusted_directory}/Rivune-TV-Installer-macOS.dmg"
trusted_mounted=true
/usr/bin/diff -r \
  "${candidate_mount}/Rivune TV Installer.app" \
  "${trusted_mount}/Rivune TV Installer.app"
