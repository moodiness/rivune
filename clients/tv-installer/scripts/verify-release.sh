#!/usr/bin/env bash
set -euo pipefail

asset_directory="${1:-}"
version="${2:-}"
if [[ ! -d "${asset_directory}" ]] ||
   [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
   [[ "$(uname -s)" != Darwin ]]; then
  echo 'Usage: verify-release.sh ASSET_DIRECTORY VERSION (on macOS)' >&2
  exit 1
fi
asset_directory="$(cd "${asset_directory}" && pwd)"

expected_assets="$(printf '%s\n' Rivune-TV-Installer-Windows.exe Rivune-TV-Installer-macOS.dmg | sort)"
actual_assets="$(find "${asset_directory}" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | sort)"
if [[ "${actual_assets}" != "${expected_assets}" ]]; then
  echo 'TV installer release has an unexpected asset set.' >&2
  exit 1
fi
for asset in ${expected_assets}; do
  size="$(stat -f %z "${asset_directory}/${asset}")"
  if (( size <= 0 || size > 67108864 )); then
    echo "${asset} size is invalid: ${size} bytes." >&2
    exit 1
  fi
done

windows_asset="${asset_directory}/Rivune-TV-Installer-Windows.exe"
/usr/bin/unzip -tq "${windows_asset}" >/dev/null
expected_windows_entries="$(printf '%s\n' Rivune-TV-Installer-arm64.exe Rivune-TV-Installer-x64.exe | sort)"
actual_windows_entries="$(/usr/bin/unzip -Z1 "${windows_asset}" | sort)"
if [[ "${actual_windows_entries}" != "${expected_windows_entries}" ]]; then
  echo 'Windows TV installer has an unexpected embedded payload set.' >&2
  exit 1
fi

macos_asset="${asset_directory}/Rivune-TV-Installer-macOS.dmg"
hdiutil verify "${macos_asset}" >/dev/null
mount_point="$(mktemp -d)"
mounted=false
cleanup() {
  if [[ "${mounted}" == true ]]; then
    hdiutil detach -quiet "${mount_point}" || true
  fi
  rmdir "${mount_point}" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM
hdiutil attach -quiet -readonly -nobrowse -mountpoint "${mount_point}" "${macos_asset}"
mounted=true
app="${mount_point}/Rivune TV Installer.app"
if [[ ! -d "${app}" ]] || [[ ! -L "${mount_point}/Applications" ]]; then
  echo 'macOS TV installer DMG must contain its application and Applications link.' >&2
  exit 1
fi
root_entries="$(find "${mount_point}" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort)"
if [[ "${root_entries}" != $'Applications\nRivune TV Installer.app' ]]; then
  echo 'macOS TV installer DMG contains unexpected root entries.' >&2
  exit 1
fi
plist="${app}/Contents/Info.plist"
binary="${app}/Contents/MacOS/Rivune-TV-Installer"
if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${plist}")" != io.rivune.tv-installer ]] ||
   [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${plist}")" != "${version}" ]] ||
   [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "${plist}")" != "${version}" ]] ||
   [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "${plist}")" != Rivune-TV-Installer ]]; then
  echo 'macOS TV installer bundle metadata is invalid.' >&2
  exit 1
fi
architectures="$(/usr/bin/lipo -archs "${binary}")"
if [[ " ${architectures} " != *' arm64 '* || " ${architectures} " != *' x86_64 '* ]] ||
   (( $(wc -w <<<"${architectures}") != 2 )); then
  echo "macOS TV installer must contain exactly arm64 and x86_64; found: ${architectures}" >&2
  exit 1
fi
if [[ "$("${binary}" --version)" != "${version}" ]]; then
  echo 'macOS TV installer executable version is invalid.' >&2
  exit 1
fi
if /usr/bin/codesign --verify --deep --strict "${app}" >/dev/null 2>&1; then
  echo 'macOS TV installer must remain unsigned.' >&2
  exit 1
fi
