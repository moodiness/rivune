#!/bin/sh
set -eu

usage() {
  printf 'usage: build-windows-launcher.sh <output.exe> <x64-executable> <arm64-executable>\n' >&2
  exit 2
}

[ "$#" -eq 3 ] || usage
output=$1
x64_path=$2
arm64_path=$3
for path in "$x64_path" "$arm64_path"; do
  [ -f "$path" ] && [ -s "$path" ] || { printf 'Missing or empty launcher payload: %s\n' "$path" >&2; exit 1; }
done
[ ! -e "$output" ] || { printf 'Launcher output already exists: %s\n' "$output" >&2; exit 1; }

x64_target=$(basename "$x64_path")
arm64_target=$(basename "$arm64_path")
build_root=$(mktemp -d)
trap 'rm -rf "$build_root"' EXIT HUP INT TERM

CGO_ENABLED=0 GOOS=windows GOARCH=386 go build \
  -trimpath \
  -ldflags "-s -w -X main.x64Executable=$x64_target -X main.arm64Executable=$arm64_target" \
  -o "$build_root/launcher.exe" \
  ./cmd/windows-launcher
go run ./cmd/windows-bundle "$build_root/launcher.exe" "$output" "$x64_path" "$arm64_path"
