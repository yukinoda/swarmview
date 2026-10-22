"""Check a DMG without Finder automation (uses dmgbuild's ds_store dependency)."""

import json
from pathlib import Path
import plistlib
import subprocess
import sys

from ds_store import DSStore


image = Path(sys.argv[1]).resolve()
attached = plistlib.loads(subprocess.check_output([
    "hdiutil", "attach", "-readonly", "-nobrowse", "-plist", str(image)]))
volume = Path(next(e["mount-point"] for e in attached["system-entities"] if "mount-point" in e))
try:
    app = volume / "Swarmview.app"
    assert app.is_dir()
    info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
    assert info["CFBundleName"] == info["CFBundleDisplayName"] == "Swarmview"
    assert (volume / "Applications").is_symlink()
    assert (volume / "Applications").readlink() == Path("/Applications")
    assert (app / "Contents/Resources/LICENSE").is_file()
    assert (app / "Contents/Resources/Installation.txt").is_file()
    volume_icon = (volume / ".VolumeIcon.icns").read_bytes()
    assert volume_icon.startswith(b"icns")
    assert volume_icon != (app / "Contents/Resources/Swarmview.icns").read_bytes(), "Installer volume must look like a disk, not the app"
    background = volume / ".background.png"
    assert background.is_file()
    dimensions = subprocess.check_output(["sips", "-g", "pixelWidth", "-g", "pixelHeight", "-g", "dpiWidth", "-g", "profile", str(background)], text=True)
    assert "pixelWidth: 1520" in dimensions and "pixelHeight: 960" in dimensions
    assert "dpiWidth: 144" in dimensions
    assert "sRGB" in dimensions
    with DSStore.open(str(volume / ".DS_Store"), "r") as store:
        window = store["."]["bwsp"]
        assert "{760, 508}" in window["WindowBounds"], window
        assert not window["ShowToolbar"] and not window["ShowSidebar"]
        assert not window["ShowTabView"]
        view = store["."]["icvp"]
        assert view["iconSize"] == 116 and view["textSize"] == 14
        assert view["backgroundType"] == 2
        assert all(view["backgroundColor" + channel] == 1.0 for channel in ["Red", "Green", "Blue"])
        assert store["Swarmview.app"]["Iloc"][:2] == (190, 256)
        assert store["Applications"]["Iloc"][:2] == (570, 256)
    subprocess.run(["codesign", "--verify", "--deep", "--strict", str(app)], check=True)
    print(json.dumps({"dmgChecks": "passed", "retinaBackground": "1520x960 at 144 dpi",
                      "windowPoints": [760, 508], "appSignature": "valid", "distinctVolumeIcon": True,
                      "resizeFill": "white", "nativeFilenameColor": "system-managed"}))
finally:
    subprocess.run(["hdiutil", "detach", str(volume)], check=True, stdout=subprocess.DEVNULL)
