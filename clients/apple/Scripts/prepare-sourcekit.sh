#!/bin/bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: prepare-sourcekit.sh [--check] [--root PATH] [--derived-data PATH]

Build all Apple schemes and atomically publish SourceKit-LSP metadata.
Use --check to verify every input and build without publishing artifacts.
Relative DerivedData paths are resolved from the repository root.
USAGE
}

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
default_root="$(cd "${script_directory}/../../.." && pwd)"
root_argument="${default_root}"
derived_data_argument=""
check_only=false

while (( $# > 0 )); do
    case "$1" in
        --check)
            check_only=true
            shift
            ;;
        --root)
            (( $# >= 2 )) || { echo "--root requires a path" >&2; exit 64; }
            root_argument="$2"
            shift 2
            ;;
        --derived-data)
            (( $# >= 2 )) || { echo "--derived-data requires a path" >&2; exit 64; }
            derived_data_argument="$2"
            shift 2
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

if [[ "$(uname -s)" != Darwin ]]; then
    echo "Apple SourceKit preparation requires macOS." >&2
    exit 1
fi
if [[ ! -d "${root_argument}" ]]; then
    echo "Repository root does not exist: ${root_argument}" >&2
    exit 1
fi
root="$(cd "${root_argument}" && pwd -P)"
apple_directory="${root}/clients/apple"
project="${apple_directory}/Rivune.xcodeproj"
versions_file="${apple_directory}/ToolVersions.env"

if [[ ! -f "${versions_file}" ]]; then
    echo "Missing Apple tool version contract: ${versions_file}" >&2
    exit 1
fi
# This repository-owned file contains portable version assignments only.
# shellcheck disable=SC1090
source "${versions_file}"
: "${XCODEGEN_VERSION:?ToolVersions.env must define XCODEGEN_VERSION}"
: "${XCODE_BUILD_SERVER_VERSION:?ToolVersions.env must define XCODE_BUILD_SERVER_VERSION}"

require_command() {
    local name="$1"
    if ! command -v "${name}" >/dev/null 2>&1; then
        echo "Required command is not installed: ${name}" >&2
        exit 1
    fi
}

require_command xcrun
require_command xcodebuild
require_command xcodegen
require_command jq
require_command xcode-build-server
require_command diff

if ! xcrun --find xcodebuild >/dev/null 2>&1; then
    echo "Xcode is not selected. Select a complete Xcode installation with xcode-select." >&2
    exit 1
fi
if ! xcodebuild -version >/dev/null 2>&1; then
    echo "The selected Xcode installation is not usable." >&2
    exit 1
fi

xcodegen_version="$(xcodegen --version 2>/dev/null | sed -E -n 's/.*([0-9]+\.[0-9]+\.[0-9]+).*/\1/p' | sed -n '1p')"
if [[ "${xcodegen_version}" != "${XCODEGEN_VERSION}" ]]; then
    echo "Expected XcodeGen ${XCODEGEN_VERSION}, found ${xcodegen_version:-unknown}." >&2
    exit 1
fi

build_server="$(command -v xcode-build-server)"
build_server="$(cd "$(dirname "${build_server}")" && pwd -P)/$(basename "${build_server}")"
build_server_version="$("${build_server}" --version 2>&1 || true)"
build_server_version="$(printf '%s\n' "${build_server_version}" | sed -E -n 's/.*([0-9]+\.[0-9]+\.[0-9]+).*/\1/p' | sed -n '1p')"
if [[ -z "${build_server_version}" ]]; then
    resolved_build_server="$(realpath "${build_server}" 2>/dev/null || printf '%s\n' "${build_server}")"
    build_server_version="$(printf '%s\n' "${resolved_build_server}" | sed -E -n 's#.*xcode-build-server/([0-9]+\.[0-9]+\.[0-9]+)/.*#\1#p' | sed -n '1p')"
fi
if [[ "${build_server_version}" != "${XCODE_BUILD_SERVER_VERSION}" ]]; then
    echo "Expected xcode-build-server ${XCODE_BUILD_SERVER_VERSION}, found ${build_server_version:-unknown}." >&2
    exit 1
fi

if [[ -z "${derived_data_argument}" ]]; then
    derived_data="${apple_directory}/.build/sourcekit-lsp"
elif [[ "${derived_data_argument}" == /* ]]; then
    derived_data="${derived_data_argument}"
else
    derived_data="${root}/${derived_data_argument}"
fi
derived_parent="$(dirname "${derived_data}")"
mkdir -p "${derived_parent}"
derived_parent="$(cd "${derived_parent}" && pwd -P)"
derived_data="${derived_parent}/$(basename "${derived_data}")"

if [[ ! -f "${apple_directory}/project.yml" ]]; then
    echo "Missing XcodeGen project specification: ${apple_directory}/project.yml" >&2
    exit 1
fi
if [[ ! -d "${project}" ]]; then
    echo "Missing versioned Xcode project: ${project}" >&2
    exit 1
fi

umask 077
stage=""
compile_temporary=""
build_server_temporary=""
derived_backup=""
publish_backup=""
new_derived_published=false
new_compile_published=false
new_build_server_published=false
publication_complete=false

rollback_publication() {
    if [[ "${publication_complete}" == true ]]; then
        return
    fi
    if [[ "${new_build_server_published}" == true ]]; then
        rm -f -- "${root}/buildServer.json"
    fi
    if [[ "${new_compile_published}" == true ]]; then
        rm -f -- "${root}/.compile"
    fi
    if [[ "${new_derived_published}" == true ]]; then
        rm -rf -- "${derived_data}"
    fi
    if [[ -n "${publish_backup}" && -e "${publish_backup}/buildServer.json" ]]; then
        mv -- "${publish_backup}/buildServer.json" "${root}/buildServer.json"
    fi
    if [[ -n "${publish_backup}" && -e "${publish_backup}/.compile" ]]; then
        mv -- "${publish_backup}/.compile" "${root}/.compile"
    fi
    if [[ -n "${derived_backup}" && -e "${derived_backup}" ]]; then
        mv -- "${derived_backup}" "${derived_data}"
    fi
}

cleanup() {
    local status=$?
    set +e
    rollback_publication
    [[ -z "${stage}" ]] || rm -rf -- "${stage}"
    [[ -z "${compile_temporary}" ]] || rm -f -- "${compile_temporary}"
    [[ -z "${build_server_temporary}" ]] || rm -f -- "${build_server_temporary}"
    [[ -z "${derived_backup}" ]] || rm -rf -- "${derived_backup}"
    [[ -z "${publish_backup}" ]] || rm -rf -- "${publish_backup}"
    exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

stage="$(mktemp -d "${derived_parent}/.$(basename "${derived_data}").stage.XXXXXX")"
generated_directory="${stage}/generated/apple"
mkdir -p "${generated_directory}"
cp -- "${apple_directory}/project.yml" "${generated_directory}/project.yml"
cp -R -- "${apple_directory}/Apps" "${generated_directory}/Apps"
xcodegen generate \
    --spec "${generated_directory}/project.yml" \
    --project "${generated_directory}" >/dev/null
if ! diff -qr --exclude=xcuserdata --exclude=xcshareddata "${generated_directory}/Rivune.xcodeproj" "${project}" >/dev/null; then
    echo "The versioned Xcode project has drifted from project.yml." >&2
    echo "Regenerate it with XcodeGen ${XCODEGEN_VERSION} before preparing SourceKit." >&2
    exit 1
fi
rm -rf -- "${generated_directory}"

schemes_json="$(xcodebuild -project "${project}" -list -json)"
schemes=(Rivune-iOS Rivune-macOS Rivune-tvOS Rivune-visionOS)
for scheme in "${schemes[@]}"; do
    if ! jq -e --arg scheme "${scheme}" '(.project.schemes // []) | index($scheme) != null' <<<"${schemes_json}" >/dev/null; then
        echo "Required Xcode scheme is missing: ${scheme}" >&2
        exit 1
    fi
done

mkdir -p "${stage}/logs" "${stage}/compilation"
platforms=(ios macos tvos visionos)
destinations=(
    'generic/platform=iOS Simulator'
    'generic/platform=macOS'
    'generic/platform=tvOS Simulator'
    'generic/platform=visionOS Simulator'
)

for index in "${!platforms[@]}"; do
    platform="${platforms[${index}]}"
    scheme="${schemes[${index}]}"
    destination="${destinations[${index}]}"
    platform_derived_data="${stage}/${platform}"
    log="${stage}/logs/${platform}.log"
    database="${stage}/compilation/${platform}.compile"
    mkdir -p "${platform_derived_data}"
    xcodebuild_arguments=(
        -project "${project}"
        -scheme "${scheme}"
        -configuration Debug
        -destination "${destination}"
        -derivedDataPath "${platform_derived_data}"
        CODE_SIGNING_ALLOWED=NO
        CODE_SIGNING_REQUIRED=NO
    )
    if [[ "${platform}" == visionos ]]; then
        xcodebuild_arguments+=(EXCLUDED_ARCHS=x86_64)
    fi
    xcodebuild_arguments+=(build)

    xcodebuild "${xcodebuild_arguments[@]}" >"${log}" 2>&1

    "${build_server}" parse -o "${database}" "${log}"
    if ! jq -e '
        type == "array" and length > 0 and
        all(.[]; type == "object" and (.command | type == "string" and length > 0))
    ' "${database}" >/dev/null; then
        echo "xcode-build-server produced an empty or invalid database for ${scheme}." >&2
        exit 1
    fi

    normalized="${database}.normalized"
    jq --arg stage "${stage}" --arg final "${derived_data}" '
        walk(if type == "string" then split($stage) | join($final) else . end)
    ' "${database}" >"${normalized}"
    mv -- "${normalized}" "${database}"
done

compile_temporary="$(mktemp "${root}/.compile.stage.XXXXXX")"
jq -s '
    reduce .[] as $database ([];
        reduce $database[] as $entry (.;
            if any(.[]; . == $entry) then . else . + [$entry] end
        )
    )
' \
    "${stage}/compilation/ios.compile" \
    "${stage}/compilation/macos.compile" \
    "${stage}/compilation/tvos.compile" \
    "${stage}/compilation/visionos.compile" >"${compile_temporary}"
if ! jq -e 'type == "array" and length > 0' "${compile_temporary}" >/dev/null; then
    echo "The merged compilation database is empty." >&2
    exit 1
fi
if [[ "${check_only}" == true ]]; then
    printf 'Verified SourceKit metadata for iOS, macOS, tvOS, and visionOS.\n'
    exit 0
fi

build_server_temporary="$(mktemp "${root}/buildServer.json.stage.XXXXXX")"
jq -n \
    --arg version "${XCODE_BUILD_SERVER_VERSION}" \
    --arg executable "${build_server}" \
    --arg workspace "${project}/project.xcworkspace" \
    --arg build_root "${derived_data}/ios" \
    --arg index_store "${derived_data}/ios/Index.noindex/DataStore" \
    '{
        name: "xcode build server",
        version: $version,
        bspVersion: "2.2.0",
        languages: ["c", "cpp", "objective-c", "objective-cpp", "swift"],
        argv: [$executable],
        workspace: $workspace,
        build_root: $build_root,
        scheme: "Rivune-iOS",
        kind: "manual",
        indexStorePath: $index_store
    }' >"${build_server_temporary}"

publish_backup="$(mktemp -d "${root}/.sourcekit-publish-backup.XXXXXX")"
derived_backup="${stage}.previous"
if [[ -e "${derived_data}" || -L "${derived_data}" ]]; then
    mv -- "${derived_data}" "${derived_backup}"
fi
if [[ -e "${root}/.compile" || -L "${root}/.compile" ]]; then
    mv -- "${root}/.compile" "${publish_backup}/.compile"
fi
if [[ -e "${root}/buildServer.json" || -L "${root}/buildServer.json" ]]; then
    mv -- "${root}/buildServer.json" "${publish_backup}/buildServer.json"
fi

mv -- "${stage}" "${derived_data}"
stage=""
new_derived_published=true
mv -- "${compile_temporary}" "${root}/.compile"
compile_temporary=""
new_compile_published=true
mv -- "${build_server_temporary}" "${root}/buildServer.json"
build_server_temporary=""
new_build_server_published=true
publication_complete=true

rm -rf -- "${derived_backup}" "${publish_backup}"
derived_backup=""
publish_backup=""
trap - EXIT INT TERM
printf 'Prepared SourceKit metadata for iOS, macOS, tvOS, and visionOS.\n'
