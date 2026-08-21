#!/bin/bash
set -euo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
subject="${script_directory}/sign-and-install.sh"
team_id=ABCDE12345
device_id=00008110-001234567890001E

fail() {
    echo "test-sign-and-install: $*" >&2
    exit 1
}

assert_contains() {
    local output="$1"
    local expected="$2"
    [[ "${output}" == *"${expected}"* ]] || fail "output does not contain: ${expected}"
}

for platform in ios tvos visionos; do
    output="$("${subject}" \
        --platform "${platform}" \
        --team-id "${team_id}" \
        --bundle-id "com.example.rivune.${platform}" \
        --device-id "${device_id}" \
        --dry-run)"
    assert_contains "${output}" "Platform: ${platform}"
    assert_contains "${output}" "Team: ${team_id}"
    assert_contains "${output}" "Bundle identifier: com.example.rivune.${platform}"
    assert_contains "${output}" "-allowProvisioningUpdates"
    assert_contains "${output}" "devicectl device install app"
done

assert_rejected() {
    local expected="$1"
    shift
    local output
    if output="$("${subject}" "$@" 2>&1)"; then
        fail "invalid invocation was accepted: $*"
    fi
    assert_contains "${output}" "${expected}"
}

assert_rejected "--platform must be ios, tvos, or visionos" \
    --platform macos --team-id "${team_id}" --bundle-id com.example.rivune --device-id "${device_id}" --dry-run
assert_rejected "--team-id must contain exactly 10 uppercase letters or digits" \
    --platform ios --team-id secret --bundle-id com.example.rivune --device-id "${device_id}" --dry-run
assert_rejected "--bundle-id must be a reverse-DNS identifier" \
    --platform ios --team-id "${team_id}" --bundle-id ../unsafe --device-id "${device_id}" --dry-run
assert_rejected "--device-id must be the 8-80 character identifier" \
    --platform ios --team-id "${team_id}" --bundle-id com.example.rivune --device-id bad --dry-run
assert_rejected "Unknown argument: --password" \
    --platform ios --team-id "${team_id}" --bundle-id com.example.rivune --device-id "${device_id}" --password secret --dry-run

printf 'sign-and-install argument and command contract passed for iOS, tvOS, and visionOS.\n'
