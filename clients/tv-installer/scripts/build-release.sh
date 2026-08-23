#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
output="${2:-dist}"
if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'Usage: build-release.sh VERSION [OUTPUT_DIRECTORY]' >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rm -rf "${output}"
mkdir -p "${output}"
output="$(cd "${output}" && pwd)"
build_root="$(mktemp -d)"
trap 'rm -rf "${build_root}"' EXIT

build() {
  local goos="$1" goarch="$2" name="$3"
  (
    cd "${root}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${version}" \
      -o "${build_root}/${name}" .
  )
  chmod 0755 "${build_root}/${name}"
}

build windows amd64 Rivune-TV-Installer-Windows-x64.exe
build windows arm64 Rivune-TV-Installer-Windows-arm64.exe
build darwin amd64 Rivune-TV-Installer-macOS-x64
build darwin arm64 Rivune-TV-Installer-macOS-arm64
build linux amd64 Rivune-TV-Installer-Linux-x64
build linux arm64 Rivune-TV-Installer-Linux-arm64

cp "${build_root}/Rivune-TV-Installer-Windows-x64.exe" "${output}/"
cp "${build_root}/Rivune-TV-Installer-Windows-arm64.exe" "${output}/"
for architecture in x64 arm64; do
  binary="Rivune-TV-Installer-macOS-${architecture}"
  touch -t 198001010000 "${build_root}/${binary}"
  (cd "${build_root}" && zip -X -q "${output}/${binary}.zip" "${binary}")
  binary="Rivune-TV-Installer-Linux-${architecture}"
  touch -t 198001010000 "${build_root}/${binary}"
  (cd "${build_root}" && zip -X -q "${output}/${binary}.zip" "${binary}")
done
