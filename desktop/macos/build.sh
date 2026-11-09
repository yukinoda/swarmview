#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
# ARCH=universal builds both architectures and merges them with lipo.
arch="${ARCH:-$(uname -m)}"
case "$arch" in
  arm64) arches=(arm64) ;;
  amd64|x86_64) arches=(x86_64) ;;
  universal) arches=(arm64 x86_64) ;;
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
cp "$root/assets/aider-LICENSE.txt" "$app/Contents/Resources/aider-LICENSE.txt"
cp "$root/desktop/macos/Installation.txt" "$app/Contents/Resources/Installation.txt"
bridge="$app/Contents/Resources/integrations/opencode"
mkdir -p "$bridge"
cp -X "$root/integrations/opencode/"{package.json,tui.ts,observer.js,README.md} "$bridge/"
mkdir -p "$app/Contents/Resources/integrations/claude"
cp -X "$root/integrations/claude/README.md" "$app/Contents/Resources/integrations/claude/"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
mkdir "$temporary/Swarmview.iconset"
swift "$root/desktop/macos/Icon.swift" "$root/assets/logo.svg" "$temporary/Swarmview.iconset"
iconutil -c icns "$temporary/Swarmview.iconset" -o "$app/Contents/Resources/Swarmview.icns"
for arch in "${arches[@]}"; do
  goarch="$arch"
  if [[ "$arch" == x86_64 ]]; then
    goarch=amd64
  fi
  CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" go -C "$root" build -trimpath -ldflags='-s -w' -o "$temporary/swarmview-server-$arch" .
  swiftc -O -swift-version 5 -target "$arch-apple-macos13.3" \
    -framework Cocoa -framework WebKit "$root/desktop/macos/Swarmview.swift" "$root/desktop/macos/Installer.swift" \
    -o "$temporary/Swarmview-$arch"
done
lipo -create "$temporary"/swarmview-server-* -output "$app/Contents/MacOS/swarmview-server"
lipo -create "$temporary"/Swarmview-* -output "$app/Contents/MacOS/Swarmview"
codesign --force --sign - "$app/Contents/MacOS/swarmview-server"
codesign --force --sign - --entitlements "$root/desktop/macos/entitlements.plist" "$app"
printf '%s\n' "$app"
