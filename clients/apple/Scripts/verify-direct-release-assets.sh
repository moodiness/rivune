#!/bin/bash
set -euo pipefail

if (( $# != 3 )); then
    echo "Usage: $0 ASSET_DIRECTORY VERSION BUILD_NUMBER" >&2
    exit 64
fi

asset_directory="$1"
version="$2"
build_number="$3"

if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "Apple direct-release version must contain exactly three numeric components: ${version}" >&2
    exit 64
fi
if [[ ! "${build_number}" =~ ^[1-9][0-9]*$ ]]; then
    echo "Apple direct-release build number must be a positive decimal integer: ${build_number}" >&2
    exit 64
fi
if [[ ! -d "${asset_directory}" ]]; then
    echo "Apple direct-release asset directory does not exist: ${asset_directory}" >&2
    exit 1
fi
asset_directory="$(cd "${asset_directory}" && pwd)"

expected_assets=$'Rivune-iOS-unsigned.ipa\nRivune-macOS.dmg\nRivune-tvOS-unsigned.ipa\nRivune-visionOS-unsigned.ipa'
actual_assets="$(find "${asset_directory}" -mindepth 1 -maxdepth 1 -type f -print | sed 's#.*/##' | sort)"
if [[ "${actual_assets}" != "${expected_assets}" ]]; then
    echo "Apple direct-release artifact contains an unexpected asset set." >&2
    exit 1
fi

work_directory="$(mktemp -d "${TMPDIR:-/tmp}/rivune-apple-verify.XXXXXX")"
mounted_volume=""
dmg_attached=false
cleanup() {
    local status=$?
    if [[ "${dmg_attached}" == true ]]; then
        hdiutil detach -quiet "${mounted_volume}" || hdiutil detach -quiet -force "${mounted_volume}" || true
    fi
    rm -rf -- "${work_directory}"
    exit "${status}"
}
trap cleanup EXIT

legal_files=(
    Rivune-Apache-2.0.txt
    NOTICE.txt
    LGPL-3.0.txt
    LGPL-2.1.txt
    GPL-3.0.txt
    FFmpeg-LICENSE.txt
    mpv-Copyright.txt
)

validate_app() {
    local app="$1"
    local legal_directory="$2"
    local expected_bundle_identifier="$3"
    local expected_architectures="$4"
    local executable="$5"
    local expected_platform="$6"
    local info_plist="${app}/Info.plist"
    if [[ -d "${app}/Contents" ]]; then
        info_plist="${app}/Contents/Info.plist"
    fi
    if [[ ! -f "${info_plist}" || ! -f "${executable}" ]]; then
        echo "Packaged Apple application is incomplete: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${info_plist}")" != "${expected_bundle_identifier}" ]]; then
        echo "Packaged Apple application has an unexpected bundle identifier: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :DTPlatformName' "${info_plist}")" != "${expected_platform}" ]]; then
        echo "Packaged Apple application has an unexpected platform: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${info_plist}")" != "${version}" ]] ||
       [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "${info_plist}")" != "${build_number}" ]]; then
        echo "Packaged Apple application has unexpected version metadata: ${app}" >&2
        exit 1
    fi
    if /usr/bin/codesign --verify --strict "${app}" >/dev/null 2>&1; then
        echo "Apple direct-release application unexpectedly has a valid code signature: ${app}" >&2
        exit 1
    fi
    if [[ -e "${app}/embedded.mobileprovision" || -e "${app}/_CodeSignature" ||
          -e "${app}/Contents/embedded.provisionprofile" || -e "${app}/Contents/_CodeSignature" ]]; then
        echo "Apple direct-release application contains signing or provisioning material: ${app}" >&2
        exit 1
    fi
    for legal_file in "${legal_files[@]}"; do
        if [[ ! -s "${legal_directory}/${legal_file}" ]]; then
            echo "Packaged Apple application is missing legal resource ${legal_file}: ${app}" >&2
            exit 1
        fi
    done
    actual_architectures="$(/usr/bin/lipo -archs "${executable}" | tr ' ' '\n' | sort | tr '\n' ' ' | sed 's/ $//')"
    if [[ "${actual_architectures}" != "${expected_architectures}" ]]; then
        echo "Packaged Apple application has unexpected architectures: ${actual_architectures}; expected ${expected_architectures}." >&2
        exit 1
    fi
}

verify_ipa() {
    local asset_name="$1"
    local expected_bundle_identifier="$2"
    local expected_platform="$3"
    local stage="${work_directory}/${asset_name}"
    local archive="${asset_directory}/${asset_name}"
    if [[ ! -s "${archive}" ]]; then
        echo "Apple IPA is missing or empty: ${archive}" >&2
        exit 1
    fi
    if /usr/bin/zipinfo -1 "${archive}" | grep -Ev '^Payload/?$|^Payload/Rivune\.app(/|$)' | grep -q .; then
        echo "Apple IPA contains content outside Payload/Rivune.app: ${asset_name}" >&2
        exit 1
    fi
    mkdir -p "${stage}"
    /usr/bin/unzip -q "${archive}" -d "${stage}"
    validate_app \
        "${stage}/Payload/Rivune.app" \
        "${stage}/Payload/Rivune.app/Legal" \
        "${expected_bundle_identifier}" \
        arm64 \
        "${stage}/Payload/Rivune.app/Rivune" \
        "${expected_platform}"
}

verify_ipa Rivune-iOS-unsigned.ipa io.rivune.app iphoneos
verify_ipa Rivune-tvOS-unsigned.ipa io.rivune.app.tv appletvos
verify_ipa Rivune-visionOS-unsigned.ipa io.rivune.app.vision xros

dmg="${asset_directory}/Rivune-macOS.dmg"
if [[ ! -s "${dmg}" ]]; then
    echo "macOS DMG is missing or empty: ${dmg}" >&2
    exit 1
fi
hdiutil verify "${dmg}" >/dev/null
mounted_volume="${work_directory}/mounted-dmg"
mkdir -p "${mounted_volume}"
hdiutil attach -quiet -readonly -nobrowse -mountpoint "${mounted_volume}" "${dmg}"
dmg_attached=true
if [[ ! -L "${mounted_volume}/Applications" || "$(readlink "${mounted_volume}/Applications")" != /Applications ]]; then
    echo "macOS DMG is missing its Applications link." >&2
    exit 1
fi
validate_app \
    "${mounted_volume}/Rivune.app" \
    "${mounted_volume}/Rivune.app/Contents/Resources/Legal" \
    io.rivune.app.mac \
    'arm64 x86_64' \
    "${mounted_volume}/Rivune.app/Contents/MacOS/Rivune" \
    macosx
