#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
arch="${ARCH:-$(uname -m)}"
case "$arch" in
  arm64) goarch=arm64 ;;
  amd64|x86_64) arch=x86_64; goarch=amd64 ;;
  *) exit 1 ;;
esac
app="${1:-$root/dist/Swarmview.app}"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
app="$(cd "$app" && pwd)"
cp "$root/desktop/macos/Info.plist" "$app/Contents/Info.plist"
version="${VERSION:-0.1.0}"
version="${version%%-*}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$app/Contents/Info.plist"
cp "$root/LICENSE" "$app/Contents/Resources/LICENSE"
cp "$root/desktop/macos/Installation.txt" "$app/Contents/Resources/Installation.txt"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
mkdir "$temporary/Swarmview.iconset"
swift "$root/desktop/macos/Icon.swift" "$root/assets/logo.svg" "$temporary/Swarmview.iconset"
iconutil -c icns "$temporary/Swarmview.iconset" -o "$app/Contents/Resources/Swarmview.icns"
CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" go -C "$root" build -trimpath -ldflags='-s -w' -o "$app/Contents/MacOS/swarmview-server" .
swiftc -O -swift-version 5 -target "$arch-apple-macos13.3" \
  -framework Cocoa -framework WebKit "$root/desktop/macos/Swarmview.swift" "$root/desktop/macos/Installer.swift" \
  -o "$app/Contents/MacOS/Swarmview"
codesign --force --sign - "$app/Contents/MacOS/swarmview-server"
codesign --force --sign - --entitlements "$root/desktop/macos/entitlements.plist" "$app"
printf '%s\n' "$app"
