#!/bin/sh
set -eu

repository_root="${SRCROOT}/../.."
legal_resources="${TARGET_BUILD_DIR}/${UNLOCALIZED_RESOURCES_FOLDER_PATH}/Legal"

/usr/bin/install -d -m 0755 "${legal_resources}"

install_notice() {
    source_path="$1"
    destination_name="$2"
    if [ ! -s "${source_path}" ]; then
        echo "error: Required Apple distribution notice is missing or empty: ${source_path}" >&2
        exit 1
    fi
    /usr/bin/install -m 0644 "${source_path}" "${legal_resources}/${destination_name}"
}

install_notice "${repository_root}/LICENSE" "Rivune-Apache-2.0.txt"
install_notice "${repository_root}/NOTICE" "NOTICE.txt"
install_notice "${SRCROOT}/Legal/LGPL-3.0.txt" "LGPL-3.0.txt"
install_notice "${repository_root}/clients/android/app/src/main/assets/legal/licenses/LGPL-2.1.txt" "LGPL-2.1.txt"
install_notice "${repository_root}/clients/android/app/src/main/assets/legal/licenses/GPL-3.0.txt" "GPL-3.0.txt"
install_notice "${repository_root}/clients/android/app/src/main/assets/legal/licenses/FFmpeg-LICENSE.txt" "FFmpeg-LICENSE.txt"
install_notice "${repository_root}/clients/android/app/src/main/assets/legal/licenses/mpv-Copyright.txt" "mpv-Copyright.txt"
