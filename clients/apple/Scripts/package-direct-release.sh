#!/bin/bash
set -euo pipefail

if (( $# != 3 )); then
    echo "Usage: $0 OUTPUT_DIRECTORY VERSION BUILD_NUMBER" >&2
    exit 64
fi

output_directory="$1"
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

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_directory="$(cd "${script_directory}/.." && pwd)"
project_path="${project_directory}/Rivune.xcodeproj"
work_directory="$(mktemp -d "${TMPDIR:-/tmp}/rivune-apple-release.XXXXXX")"
trap 'rm -rf -- "${work_directory}"' EXIT

mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd)"
if [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    echo "Apple direct-release output directory must be empty: ${output_directory}" >&2
    exit 1
fi

common_build_settings=(
    -project "${project_path}"
    -configuration Release
    CODE_SIGNING_ALLOWED=NO
    CODE_SIGNING_REQUIRED=NO
    CODE_SIGN_IDENTITY=
    DEVELOPMENT_TEAM=
    MARKETING_VERSION="${version}"
    CURRENT_PROJECT_VERSION="${build_number}"
)

build_target() {
    local scheme="$1"
    local destination="$2"
    local derived_data="$3"
    shift 3
    xcodebuild \
        "${common_build_settings[@]}" \
        -scheme "${scheme}" \
        -destination "${destination}" \
        -derivedDataPath "${derived_data}" \
        "$@" \
        build
}

build_target Rivune-iOS 'generic/platform=iOS' "${work_directory}/ios-derived"
build_target Rivune-tvOS 'generic/platform=tvOS' "${work_directory}/tvos-derived"
build_target Rivune-visionOS 'generic/platform=visionOS' "${work_directory}/visionos-derived"
build_target Rivune-macOS 'generic/platform=macOS' "${work_directory}/macos-derived" ARCHS='arm64 x86_64' ONLY_ACTIVE_ARCH=NO

ios_app="${work_directory}/ios-derived/Build/Products/Release-iphoneos/Rivune.app"
tvos_app="${work_directory}/tvos-derived/Build/Products/Release-appletvos/Rivune.app"
visionos_app="${work_directory}/visionos-derived/Build/Products/Release-xros/Rivune.app"
macos_app="${work_directory}/macos-derived/Build/Products/Release/Rivune.app"
legal_files=(
    Rivune-Apache-2.0.txt
    NOTICE.txt
    LGPL-3.0.txt
    LGPL-2.1.txt
    GPL-3.0.txt
    FFmpeg-LICENSE.txt
    mpv-Copyright.txt
)

validate_unsigned_app() {
    local app="$1"
    local legal_directory="$2"
    local expected_bundle_identifier="$3"
    local expected_platform="$4"
    local info_plist="${app}/Info.plist"
    if [[ "${app}" == *'/Contents/'* || -d "${app}/Contents" ]]; then
        info_plist="${app}/Contents/Info.plist"
    fi
    if [[ ! -f "${info_plist}" ]]; then
        echo "Built application has no Info.plist: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${info_plist}")" != "${expected_bundle_identifier}" ]]; then
        echo "Built application has an unexpected bundle identifier: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :DTPlatformName' "${info_plist}")" != "${expected_platform}" ]]; then
        echo "Built application has an unexpected platform: ${app}" >&2
        exit 1
    fi
    if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${info_plist}")" != "${version}" ]] ||
       [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "${info_plist}")" != "${build_number}" ]]; then
        echo "Built application has unexpected version metadata: ${app}" >&2
        exit 1
    fi
    if /usr/bin/codesign --verify --strict "${app}" >/dev/null 2>&1; then
        echo "Direct-release application must remain unsigned: ${app}" >&2
        exit 1
    fi
    if [[ -e "${app}/embedded.mobileprovision" || -e "${app}/_CodeSignature" ||
          -e "${app}/Contents/embedded.provisionprofile" || -e "${app}/Contents/_CodeSignature" ]]; then
        echo "Direct-release application contains signing or provisioning material: ${app}" >&2
        exit 1
    fi
    for legal_file in "${legal_files[@]}"; do
        if [[ ! -s "${legal_directory}/${legal_file}" ]]; then
            echo "Built application is missing legal resource ${legal_file}: ${app}" >&2
            exit 1
        fi
    done
}

validate_unsigned_app "${ios_app}" "${ios_app}/Legal" io.rivune.app iphoneos
validate_unsigned_app "${tvos_app}" "${tvos_app}/Legal" io.rivune.app.tv appletvos
validate_unsigned_app "${visionos_app}" "${visionos_app}/Legal" io.rivune.app.vision xros
validate_unsigned_app "${macos_app}" "${macos_app}/Contents/Resources/Legal" io.rivune.app.mac macosx

if [[ "$(/usr/bin/lipo -archs "${ios_app}/Rivune")" != 'arm64' ]]; then
    echo "Unsigned iOS application must contain exactly arm64." >&2
    exit 1
fi
if [[ "$(/usr/bin/lipo -archs "${tvos_app}/Rivune")" != 'arm64' ]]; then
    echo "Unsigned tvOS application must contain exactly arm64." >&2
    exit 1
fi
if [[ "$(/usr/bin/lipo -archs "${visionos_app}/Rivune")" != 'arm64' ]]; then
    echo "Unsigned visionOS application must contain exactly arm64." >&2
    exit 1
fi
macos_architectures="$(/usr/bin/lipo -archs "${macos_app}/Contents/MacOS/Rivune")"
if [[ " ${macos_architectures} " != *' arm64 '* || " ${macos_architectures} " != *' x86_64 '* ]] ||
   (( $(wc -w <<<"${macos_architectures}") != 2 )); then
    echo "macOS application must contain exactly arm64 and x86_64; found: ${macos_architectures}" >&2
    exit 1
fi

package_ipa() {
    local app="$1"
    local asset_name="$2"
    local stage="${work_directory}/${asset_name}.stage"
    mkdir -p "${stage}/Payload"
    /usr/bin/ditto "${app}" "${stage}/Payload/Rivune.app"
    (
        cd "${stage}"
        /usr/bin/zip -q -r -X "${output_directory}/${asset_name}" Payload
    )
    /usr/bin/unzip -tq "${output_directory}/${asset_name}" >/dev/null
}

package_ipa "${ios_app}" Rivune-iOS-unsigned.ipa
package_ipa "${tvos_app}" Rivune-tvOS-unsigned.ipa
package_ipa "${visionos_app}" Rivune-visionOS-unsigned.ipa

macos_stage="${work_directory}/macos-dmg"
mkdir -p "${macos_stage}"
/usr/bin/ditto "${macos_app}" "${macos_stage}/Rivune.app"
ln -s /Applications "${macos_stage}/Applications"
hdiutil create \
    -quiet \
    -fs HFS+ \
    -format UDZO \
    -imagekey zlib-level=9 \
    -srcfolder "${macos_stage}" \
    -volname Rivune \
    "${output_directory}/Rivune-macOS.dmg"
hdiutil verify "${output_directory}/Rivune-macOS.dmg" >/dev/null

expected_assets=$'Rivune-iOS-unsigned.ipa\nRivune-macOS.dmg\nRivune-tvOS-unsigned.ipa\nRivune-visionOS-unsigned.ipa'
actual_assets="$(find "${output_directory}" -mindepth 1 -maxdepth 1 -type f -print | sed 's#.*/##' | sort)"
if [[ "${actual_assets}" != "${expected_assets}" ]]; then
    echo "Apple direct-release packager produced an unexpected asset set." >&2
    exit 1
fi

"${script_directory}/verify-direct-release-assets.sh" "${output_directory}" "${version}" "${build_number}"
