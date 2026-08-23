#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
output="${2:-dist}"
if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'Usage: build-release.sh VERSION [OUTPUT_DIRECTORY]' >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rm -rf "${output}"
mkdir -p "${output}"
output="$(cd "${output}" && pwd)"
build_root="$(mktemp -d)"
trap 'rm -rf "${build_root}"' EXIT

if [[ "$(uname -s)" != Darwin ]]; then
  echo 'TV installer release packaging requires macOS to create and verify the DMG.' >&2
  exit 1
fi

build() {
  local goos="$1" goarch="$2" name="$3"
  (
    cd "${root}"
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${version}" \
      -o "${build_root}/${name}" .
  )
  chmod 0755 "${build_root}/${name}"
}

build windows amd64 Rivune-TV-Installer-x64.exe
build windows arm64 Rivune-TV-Installer-arm64.exe
build darwin amd64 Rivune-TV-Installer-macOS-x64
build darwin arm64 Rivune-TV-Installer-macOS-arm64

(
  cd "${root}"
  scripts/build-windows-launcher.sh \
    "${output}/Rivune-TV-Installer-Windows.exe" \
    "${build_root}/Rivune-TV-Installer-x64.exe" \
    "${build_root}/Rivune-TV-Installer-arm64.exe"
)

/usr/bin/lipo -create \
  -output "${build_root}/Rivune-TV-Installer-macOS" \
  "${build_root}/Rivune-TV-Installer-macOS-x64" \
  "${build_root}/Rivune-TV-Installer-macOS-arm64"

app="${build_root}/Rivune TV Installer.app"
mkdir -p "${app}/Contents/MacOS" "${app}/Contents/Resources"
cp "${build_root}/Rivune-TV-Installer-macOS" "${app}/Contents/MacOS/Rivune-TV-Installer"
chmod 0755 "${app}/Contents/MacOS/Rivune-TV-Installer"

iconset="${build_root}/Rivune.iconset"
mkdir -p "${iconset}"
icons="${root}/../apple/Apps/macOS/Assets.xcassets/AppIcon.appiconset"
cp "${icons}/AppIcon-16.png" "${iconset}/icon_16x16.png"
cp "${icons}/AppIcon-16@2x.png" "${iconset}/icon_16x16@2x.png"
cp "${icons}/AppIcon-32.png" "${iconset}/icon_32x32.png"
cp "${icons}/AppIcon-32@2x.png" "${iconset}/icon_32x32@2x.png"
cp "${icons}/AppIcon-128.png" "${iconset}/icon_128x128.png"
cp "${icons}/AppIcon-128@2x.png" "${iconset}/icon_128x128@2x.png"
cp "${icons}/AppIcon-256.png" "${iconset}/icon_256x256.png"
cp "${icons}/AppIcon-256@2x.png" "${iconset}/icon_256x256@2x.png"
cp "${icons}/AppIcon-512.png" "${iconset}/icon_512x512.png"
cp "${icons}/AppIcon-512@2x.png" "${iconset}/icon_512x512@2x.png"
/usr/bin/iconutil -c icns "${iconset}" -o "${app}/Contents/Resources/Rivune.icns"

cat > "${app}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDisplayName</key><string>Rivune TV Installer</string>
  <key>CFBundleExecutable</key><string>Rivune-TV-Installer</string>
  <key>CFBundleIconFile</key><string>Rivune.icns</string>
  <key>CFBundleIdentifier</key><string>io.rivune.tv-installer</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Rivune TV Installer</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
/usr/bin/plutil -lint "${app}/Contents/Info.plist" >/dev/null

architectures="$(/usr/bin/lipo -archs "${app}/Contents/MacOS/Rivune-TV-Installer")"
if [[ " ${architectures} " != *' arm64 '* || " ${architectures} " != *' x86_64 '* ]] ||
   (( $(wc -w <<<"${architectures}") != 2 )); then
  echo "macOS TV installer must contain exactly arm64 and x86_64; found: ${architectures}" >&2
  exit 1
fi

dmg_stage="${build_root}/macOS-dmg"
mkdir -p "${dmg_stage}"
/usr/bin/ditto "${app}" "${dmg_stage}/Rivune TV Installer.app"
ln -s /Applications "${dmg_stage}/Applications"
hdiutil create \
  -quiet \
  -fs HFS+ \
  -format UDZO \
  -imagekey zlib-level=9 \
  -srcfolder "${dmg_stage}" \
  -volname "Rivune TV Installer" \
  "${output}/Rivune-TV-Installer-macOS.dmg"
hdiutil verify "${output}/Rivune-TV-Installer-macOS.dmg" >/dev/null
"${root}/scripts/verify-release.sh" "${output}" "${version}"
