#!/bin/bash
set -euo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
subject="${script_directory}/prepare-sourcekit.sh"
test_directory="$(mktemp -d "${TMPDIR:-/tmp}/rivune-sourcekit-test.XXXXXX")"
trap 'rm -rf -- "${test_directory}"' EXIT

fail() { echo "test-prepare-sourcekit: $*" >&2; exit 1; }
assert_equal() {
    [[ "$1" == "$2" ]] || fail "$3: expected '$1', found '$2'"
}
make_executable() {
    local path="$1"; shift
    printf '%s\n' "$@" >"${path}"
    chmod +x "${path}"
}

prepare_case() {
    local name="$1" case_directory="${test_directory}/$1 root"
    local root="${case_directory}/repository" fake_bin="${case_directory}/bin"
    mkdir -p "${root}/clients/apple/Scripts" "${root}/clients/apple/Apps/Shared" \
        "${root}/clients/apple/Rivune.xcodeproj/xcshareddata/xcschemes" "${fake_bin}"
    cp "${subject}" "${root}/clients/apple/Scripts/prepare-sourcekit.sh"
    cp "${script_directory}/../ToolVersions.env" "${root}/clients/apple/ToolVersions.env"
    printf 'name: Rivune\n' >"${root}/clients/apple/project.yml"
    printf 'project fixture\n' >"${root}/clients/apple/Rivune.xcodeproj/project.pbxproj"
    for scheme in Rivune-iOS Rivune-macOS Rivune-tvOS Rivune-visionOS; do
        printf '%s\n' "${scheme}" >"${root}/clients/apple/Rivune.xcodeproj/xcshareddata/xcschemes/${scheme}.xcscheme"
    done
    make_executable "${fake_bin}/uname" '#!/bin/bash' 'printf "Darwin\\n"'
    make_executable "${fake_bin}/xcrun" '#!/bin/bash' '[[ "$1" == --find && "$2" == xcodebuild ]]'
    make_executable "${fake_bin}/xcodegen" '#!/bin/bash' \
        'set -euo pipefail' \
        'if [[ "${1:-}" == --version ]]; then printf "Version: 2.46.0\\n"; exit 0; fi' \
        'destination=""; while (( $# > 0 )); do if [[ "$1" == --project ]]; then destination="$2"; shift 2; else shift; fi; done' \
        'mkdir -p "${destination}"; cp -R "${SOURCE_PROJECT}" "${destination}/Rivune.xcodeproj"'
    make_executable "${fake_bin}/xcodebuild" '#!/bin/bash' \
        'set -euo pipefail' \
        'if [[ "${1:-}" == -version ]]; then printf "Xcode 26.6\\nBuild version 17F113\\n"; exit 0; fi' \
        'if [[ " $* " == *" -list "* ]]; then' \
        '  if [[ "${MISSING_SCHEME:-}" == true ]]; then printf '\''{"project":{"schemes":["Rivune-iOS","Rivune-macOS","Rivune-tvOS"]}}\n'\''; else printf '\''{"project":{"schemes":["Rivune-iOS","Rivune-macOS","Rivune-tvOS","Rivune-visionOS"]}}\n'\''; fi; exit 0; fi' \
        'printf "%s\\n" "$*" >>"${COMMAND_LOG}"; printf "build output\\n"'
    make_executable "${fake_bin}/xcode-build-server" '#!/bin/bash' \
        'set -euo pipefail' \
        'if [[ "${1:-}" == --version ]]; then printf "xcode-build-server 1.3.0\\n"; exit 0; fi' \
        '[[ "$1" == parse && "$2" == -o ]]; output="$3"; mkdir -p "$(dirname "${output}")"' \
        'if [[ "${EMPTY_PLATFORM:-}" != "" && "${output}" == *"/${EMPTY_PLATFORM}.compile" ]]; then printf "[]\\n" >"${output}"; exit 0; fi' \
        'platform="$(basename "${output}" .compile)"; stage="$(cd "$(dirname "${output}")/.." && pwd -P)"' \
        'printf '\''[{"directory":"/shared","command":"swiftc shared","module_name":"Shared","files":[],"fileLists":[]},{"directory":"/src","command":"swiftc %s %s/%s/flags","module_name":"App-%s","files":[],"fileLists":["%s/%s/files"]}]\n'\'' "${platform}" "${stage}" "${platform}" "${platform}" "${stage}" "${platform}" >"${output}"'
    make_executable "${fake_bin}/mv" '#!/bin/bash' \
        'set -euo pipefail; destination="${!#}"; canonical_root="$(cd "${CASE_ROOT}" && pwd -P)"' \
        'if [[ "${FAIL_PUBLISH:-}" == true && "${destination}" == "${canonical_root}/buildServer.json" && ! -e "${FAIL_MARKER}" ]]; then touch "${FAIL_MARKER}"; exit 73; fi' \
        '/bin/mv "$@"'
    printf '%s|%s\n' "${root}" "${fake_bin}"
}

run_subject() {
    local root="$1" fake_bin="$2"; shift 2
    PATH="${fake_bin}:${PATH}" SOURCE_PROJECT="${root}/clients/apple/Rivune.xcodeproj" COMMAND_LOG="${root}/commands.log" \
        CASE_ROOT="${root}" FAIL_MARKER="${root}/publish-failed" "$@" \
        /bin/bash "${root}/clients/apple/Scripts/prepare-sourcekit.sh" --root "${root}" \
        --derived-data "clients/apple/.build/custom sourcekit" >/dev/null
}
seed_old_state() {
    local root="$1"
    printf 'old compile\n' >"${root}/.compile"; printf 'old server\n' >"${root}/buildServer.json"
    mkdir -p "${root}/clients/apple/.build/custom sourcekit"
    printf 'old derived\n' >"${root}/clients/apple/.build/custom sourcekit/marker"
}
assert_old_state() {
    local root="$1" reason="$2"
    assert_equal 'old compile' "$(cat "${root}/.compile")" "compile after ${reason}"
    assert_equal 'old server' "$(cat "${root}/buildServer.json")" "server after ${reason}"
    assert_equal 'old derived' "$(cat "${root}/clients/apple/.build/custom sourcekit/marker")" "DerivedData after ${reason}"
}

case_value="$(prepare_case success)"; root="${case_value%%|*}"; fake_bin="${case_value#*|}"
(cd "${test_directory}" && run_subject "${root}" "${fake_bin}" env)
assert_equal 4 "$(wc -l <"${root}/commands.log" | tr -d ' ')" 'build count'
for contract in 'Rivune-iOS|generic/platform=iOS Simulator' 'Rivune-macOS|generic/platform=macOS' 'Rivune-tvOS|generic/platform=tvOS Simulator' 'Rivune-visionOS|generic/platform=visionOS Simulator'; do
    scheme="${contract%%|*}"; destination="${contract#*|}"
    line="$(grep -- "-scheme ${scheme}" "${root}/commands.log" || true)"
    [[ "${line}" == *"-destination ${destination}"* ]] || fail "missing build contract for ${scheme}"
done
vision_line="$(grep -- '-scheme Rivune-visionOS' "${root}/commands.log")"
[[ "${vision_line}" == *'EXCLUDED_ARCHS=x86_64'* ]] || fail 'visionOS build did not exclude unsupported x86_64 linkage'
for platform in Rivune-iOS Rivune-macOS Rivune-tvOS; do
    line="$(grep -- "-scheme ${platform}" "${root}/commands.log")"
    [[ "${line}" != *'EXCLUDED_ARCHS='* ]] || fail "${platform} unexpectedly constrained architectures"
done
assert_equal 5 "$(jq 'length' "${root}/.compile")" 'deduplicated entry count'
assert_equal 'Shared,App-ios,App-macos,App-tvos,App-visionos' "$(jq -r 'map(.module_name) | join(",")' "${root}/.compile")" 'merge order'
for platform in ios macos tvos visionos; do jq -e --arg p "App-${platform}" 'any(.[]; .module_name == $p)' "${root}/.compile" >/dev/null || fail "lost ${platform}"; done
if grep -q -- '.stage.' "${root}/.compile"; then fail 'published database retained staging path'; fi
[[ -d "${root}/clients/apple/.build/custom sourcekit/ios" ]] || fail 'published DerivedData missing'
expected_build_root="$(cd "${root}/clients/apple/.build/custom sourcekit/ios" && pwd -P)"
assert_equal "${expected_build_root}" "$(jq -r '.build_root' "${root}/buildServer.json")" 'build root'
[[ "$(jq -r '.argv[0]' "${root}/buildServer.json")" == /* ]] || fail 'build server path is not absolute'

for failure in missing empty atomic; do
    case_value="$(prepare_case "${failure}")"; root="${case_value%%|*}"; fake_bin="${case_value#*|}"; seed_old_state "${root}"
    case "${failure}" in
        missing) arguments=(env MISSING_SCHEME=true) ;;
        empty) arguments=(env EMPTY_PLATFORM=tvos) ;;
        atomic) arguments=(env FAIL_PUBLISH=true) ;;
    esac
    if run_subject "${root}" "${fake_bin}" "${arguments[@]}"; then fail "${failure} failure was accepted"; fi
    assert_old_state "${root}" "${failure} failure"
    if find "${root}" \( -name '*.stage.*' -o -name '.sourcekit-publish-backup.*' \) -print | grep -q .; then fail "${failure} temporaries were not cleaned"; fi
done

printf 'SourceKit preparation success, failure, merge, and atomic publication contracts passed.\n'
