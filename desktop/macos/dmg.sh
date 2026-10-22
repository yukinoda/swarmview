#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
app="${1:-$root/dist/Swarmview.app}"
image="${2:-$root/dist/Swarmview.dmg}"
app="$(cd "$app" && pwd)"
test -f "$app/Contents/MacOS/Swarmview"
test -f "$app/Contents/MacOS/swarmview-server"
mkdir -p "$(dirname "$image")"
image="$(cd "$(dirname "$image")" && pwd)/$(basename "$image")"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
swift "$root/desktop/macos/DMGBackground.swift" "$root/assets/installer-light.svg" "$temporary/background.png"
mkdir "$temporary/badge.iconset"
swift "$root/desktop/macos/Icon.swift" "$root/assets/logo.svg" "$temporary/badge.iconset" \
  /System/Library/Extensions/IOStorageFamily.kext/Contents/Resources/Removable.icns
iconutil -c icns "$temporary/badge.iconset" -o "$temporary/badge.icns"
python3 -m venv "$temporary/tools"
"$temporary/tools/bin/python" -m pip install --quiet --disable-pip-version-check 'dmgbuild==1.6.7'
"$temporary/tools/bin/python" -m dmgbuild -s "$root/desktop/macos/dmg-settings.py" \
  -D "app=$app" \
  -D "icon=$temporary/badge.icns" -D "background=$temporary/background.png" \
  Swarmview "$image"
hdiutil verify "$image"
"$temporary/tools/bin/python" "$root/desktop/macos/check-dmg.py" "$image"
printf '%s\n' "$image"
