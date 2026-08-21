#!/bin/bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  sign-and-install.sh --platform ios|tvos|visionos --team-id TEAM_ID \
    --bundle-id REVERSE_DNS_ID --device-id DEVICE_IDENTIFIER [--dry-run]

Builds Rivune from this checkout, asks Xcode to provision and sign it with the
selected local development team, verifies the resulting signature, then installs
it on the connected device. Configure the Apple account in Xcode first.

No Apple password, private key, certificate, or provisioning profile is accepted
by this script. Xcode and the macOS Keychain retain all signing credentials.
EOF
}

platform=""
team_id=""
bundle_id=""
device_id=""
dry_run=false

while (( $# > 0 )); do
    case "$1" in
        --platform|--team-id|--bundle-id|--device-id)
            if (( $# < 2 )) || [[ -z "$2" ]]; then
                echo "Missing value for $1" >&2
                usage >&2
                exit 64
            fi
            case "$1" in
                --platform) platform="$2" ;;
                --team-id) team_id="$2" ;;
                --bundle-id) bundle_id="$2" ;;
                --device-id) device_id="$2" ;;
            esac
            shift 2
            ;;
        --dry-run)
            dry_run=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage >&2
            exit 64
            ;;
    esac
done

case "${platform}" in
    ios)
        scheme="Rivune-iOS"
        product_directory="Release-iphoneos"
        ;;
    tvos)
        scheme="Rivune-tvOS"
        product_directory="Release-appletvos"
        ;;
    visionos)
        scheme="Rivune-visionOS"
        product_directory="Release-xros"
        ;;
    *)
        echo "--platform must be ios, tvos, or visionos" >&2
        exit 64
        ;;
esac

if [[ ! "${team_id}" =~ ^[A-Z0-9]{10}$ ]]; then
    echo "--team-id must contain exactly 10 uppercase letters or digits" >&2
    exit 64
fi
if (( ${#bundle_id} > 255 )) || [[ ! "${bundle_id}" =~ ^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$ ]]; then
    echo "--bundle-id must be a reverse-DNS identifier such as com.example.rivune" >&2
    exit 64
fi
if [[ ! "${device_id}" =~ ^[A-Za-z0-9-]{8,80}$ ]]; then
    echo "--device-id must be the 8-80 character identifier shown by 'xcrun devicectl list devices'" >&2
    exit 64
fi

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_directory="$(cd "${script_directory}/.." && pwd)"
project_path="${project_directory}/Rivune.xcodeproj"

if [[ ! -f "${project_path}/project.pbxproj" ]]; then
    echo "Rivune.xcodeproj is missing from ${project_directory}" >&2
    exit 1
fi

if [[ "${dry_run}" == true ]]; then
    cat <<EOF
Platform: ${platform}
Scheme: ${scheme}
Team: ${team_id}
Bundle identifier: ${bundle_id}
Device: ${device_id}
xcodebuild -project '${project_path}' -scheme '${scheme}' -configuration Release -destination 'id=${device_id}' -allowProvisioningUpdates -allowProvisioningDeviceRegistration DEVELOPMENT_TEAM='${team_id}' PRODUCT_BUNDLE_IDENTIFIER='${bundle_id}' CODE_SIGN_STYLE=Automatic build
xcrun devicectl device install app --device '${device_id}' '<temporary-derived-data>/Build/Products/${product_directory}/Rivune.app'
EOF
    exit 0
fi

for executable in /usr/bin/xcodebuild /usr/bin/xcrun /usr/bin/codesign /usr/bin/security /usr/libexec/PlistBuddy; do
    if [[ ! -x "${executable}" ]]; then
        echo "Required Apple developer tool is unavailable: ${executable}" >&2
        exit 1
    fi
done
if ! /usr/bin/xcrun --find devicectl >/dev/null 2>&1; then
    echo "Xcode 15 or later with devicectl is required" >&2
    exit 1
fi

work_directory="$(mktemp -d "${TMPDIR:-/tmp}/rivune-local-signing.XXXXXX")"
trap 'rm -rf -- "${work_directory}"' EXIT

/usr/bin/xcodebuild \
    -project "${project_path}" \
    -scheme "${scheme}" \
    -configuration Release \
    -destination "id=${device_id}" \
    -derivedDataPath "${work_directory}" \
    -allowProvisioningUpdates \
    -allowProvisioningDeviceRegistration \
    DEVELOPMENT_TEAM="${team_id}" \
    PRODUCT_BUNDLE_IDENTIFIER="${bundle_id}" \
    CODE_SIGN_STYLE=Automatic \
    CODE_SIGNING_ALLOWED=YES \
    CODE_SIGNING_REQUIRED=YES \
    COMPILER_INDEX_STORE_ENABLE=NO \
    build

app_path="${work_directory}/Build/Products/${product_directory}/Rivune.app"
info_plist="${app_path}/Info.plist"
profile_path="${app_path}/embedded.mobileprovision"
profile_plist="${work_directory}/embedded-profile.plist"
signed_entitlements_plist="${work_directory}/signed-entitlements.plist"

if [[ ! -d "${app_path}" || ! -f "${info_plist}" || ! -f "${profile_path}" ]]; then
    echo "Xcode did not produce a provisioned application at ${app_path}" >&2
    exit 1
fi
if [[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${info_plist}")" != "${bundle_id}" ]]; then
    echo "Signed application bundle identifier does not match --bundle-id" >&2
    exit 1
fi

/usr/bin/codesign --verify --deep --strict --verbose=2 "${app_path}"
/usr/bin/codesign -d --entitlements :- "${app_path}" >"${signed_entitlements_plist}" 2>/dev/null
signed_application_identifier="$(/usr/libexec/PlistBuddy -c 'Print :application-identifier' "${signed_entitlements_plist}")"
signature_details="$(/usr/bin/codesign -d --verbose=4 "${app_path}" 2>&1)"
signature_team=""
while IFS= read -r detail; do
    case "${detail}" in
        TeamIdentifier=*) signature_team="${detail#TeamIdentifier=}" ;;
    esac
done <<<"${signature_details}"
if [[ "${signature_team}" != "${team_id}" ]]; then
    echo "Signed application does not use the requested Apple development team" >&2
    exit 1
fi

/usr/bin/security cms -D -i "${profile_path}" >"${profile_plist}"
profile_team="$(/usr/libexec/PlistBuddy -c 'Print :TeamIdentifier:0' "${profile_plist}")"
profile_application_identifier="$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:application-identifier' "${profile_plist}")"
expected_application_identifier="${team_id}.${bundle_id}"
profile_matches=false
if [[ "${profile_application_identifier}" == "${expected_application_identifier}" ]]; then
    profile_matches=true
elif [[ "${profile_application_identifier}" =~ ^${team_id}\.([A-Za-z0-9-]+\.)*\*$ ]]; then
    profile_prefix="${profile_application_identifier%\*}"
    if [[ "${expected_application_identifier}" == "${profile_prefix}"* ]]; then
        profile_matches=true
    fi
fi
if [[ "${signed_application_identifier}" != "${expected_application_identifier}" || "${profile_team}" != "${team_id}" || "${profile_matches}" != true ]]; then
    echo "Code-signing entitlements or provisioning profile do not match the requested team and bundle identifier" >&2
    exit 1
fi

/usr/bin/xcrun devicectl device install app --device "${device_id}" "${app_path}"
printf 'Installed Rivune (%s) on device %s with bundle identifier %s.\n' "${platform}" "${device_id}" "${bundle_id}"
