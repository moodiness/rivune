#!/bin/sh
set -eu

frameworks_dir="${TARGET_BUILD_DIR}/${FRAMEWORKS_FOLDER_PATH}"
app_executable="${TARGET_BUILD_DIR}/${EXECUTABLE_PATH}"

if [ ! -d "${frameworks_dir}" ] || [ ! -f "${app_executable}" ]; then
    exit 0
fi

linked_dylibs="$(/usr/bin/otool -L "${app_executable}")"

for framework in "${frameworks_dir}"/*.framework; do
    [ -d "${framework}" ] || continue

    info_plist="${framework}/Info.plist"
    if [ ! -f "${info_plist}" ]; then
        info_plist="${framework}/Resources/Info.plist"
    fi
    if [ ! -f "${info_plist}" ]; then
        echo "error: Embedded framework ${framework} has no Info.plist." >&2
        exit 1
    fi
    executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "${info_plist}")"
    binary="${framework}/${executable}"
    if [ ! -f "${binary}" ]; then
        binary="${framework}/Versions/A/${executable}"
    fi
    if [ ! -f "${binary}" ]; then
        echo "error: Embedded framework ${framework} has no executable." >&2
        exit 1
    fi

    framework_reference="/$(basename "${framework}")/${executable}"
    case "${linked_dylibs}" in
        *"${framework_reference}"*) continue ;;
    esac

    if xcrun dyld_info -exports "${binary}" | /usr/bin/grep -Eq '0x[0-9A-Fa-f]+'; then
        echo "error: Refusing to remove unlinked framework with exported symbols: ${framework}" >&2
        exit 1
    fi

    /bin/rm -rf "${framework}"
done
