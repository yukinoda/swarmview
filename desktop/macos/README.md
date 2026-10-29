# macOS app prototype

A small AppKit window loads the existing dashboard in the system WKWebView.
It bundles the standard-library Go server; no Chromium, Electron, frontend build,
or third-party Go modules are added. Requires macOS 13.3+; currently tested locally
on Apple Silicon with the installed macOS/WebKit version, not every supported OS.
The Linux CLI/browser workflow is still available; this prototype is macOS-only.

## Build and run

With Go and Xcode command-line tools installed:

```sh
bash desktop/macos/build.sh
open dist/Swarmview.app
```

Drag the app to Applications to launch it from Finder or the Dock. It includes
its backend and icon. `ARCH=amd64 bash desktop/macos/build.sh` cross-builds the
Intel app on a Mac. Release tags produce separate ARM/Intel `.dmg` and `.app.zip`
downloads in addition to CLI archives. Builds use ad-hoc signatures; they are not Developer
ID signed or notarized. This is a prototype, not a notarized installer.

## Drag-to-install disk image

```sh
bash desktop/macos/build.sh
bash desktop/macos/dmg.sh
open dist/Swarmview.dmg
```

Drag the app onto Applications in the branded Finder window, then launch the
installed copy. The selected light Figma artwork is stored as vector SVG with
outlined typography, rasterized at build time to a 2x-resolution sRGB background.
The installer uses one fixed light theme with either Finder chrome appearance;
it does not automatically switch with system appearance.
Finder owns the real file icons, filenames, and keyboard/drag interactions.
Artwork stays within a 420-point safe area for Finder's title/tab bars, with a
build-time pixel check for footer visibility and bottom clearance. The canvas
matches Finder's white resize fill. Native filename colors remain system-managed:
the tested dark image still had black filenames, even with a dark fallback fill.
The light artwork avoids relying on unsupported per-DMG filename-color control.
No installer process, font dependency, or runtime graphics are added to the app.
Installation Help in the app's Help menu covers first-launch approval.
The mounted installer uses the default macOS removable-disk icon with the
white-backed Swarmview logo replacing its download arrow. The logo is centered
on the top-down disk face and stays square, without perspective distortion.

Building the DMG requires Python 3.10+ and network access to install pinned
`dmgbuild==1.6.7` into a temporary virtual environment. This is a build-only tool:
no Python packages ship with the app. It writes Finder metadata without Finder
automation or Accessibility permission. The build verifies the image, icon
positions, Retina artwork/profile, white fallback fill, Applications link, and the
bundled app signature.

After the copy in `/Applications` or `~/Applications` successfully loads its
dashboard, it checks mounted disk images once. Moving the app alone does not
trigger cleanup; launch the installed copy first.
If one contains exactly matching app/backend binaries, a native sheet offers
**Keep Installer** (default) or **Eject & Move to Trash**. The offer is remembered
for that download path. Cleanup rechecks the download's file identity, ejects
the matched disk, and only then asks macOS to recycle the DMG. It never empties
Trash or removes the installed app. A failed eject does not trash the download.
The one-shot check is off the UI thread, with no polling or resident helper.

## Behavior

- The app owns its backend on `127.0.0.1:4778`; it does not attach to another
  server. The CLI retains port 4777. A port conflict reports an error.
- Reopening activates the existing app. Closing the last window or quitting
  stops the owned backend. A lifetime pipe also stops it after GUI force-quit.
  Monitored agents and tmux sessions are not terminated.
- Finder launches use system paths plus `/opt/homebrew/bin` and `/usr/local/bin`.
  Git, tmux, and lsof must be installed there. Nonstandard tool paths are not
  discovered by sourcing shell configuration. The app clears inherited `TMUX`
  and uses the default tmux server.
- Pins/folds use WebKit's persistent storage, separately from browser preferences.
  Command-K search and standard Edit shortcuts use the existing page.
- Page navigation stays within the dashboard; deliberate external HTTP(S) links
  open in the default browser. No general native JavaScript bridge is exposed.
- Ghostty Automation permission belongs to the packaged app's execution context;
  existing terminal permission may not carry over. Allow access when prompted,
  then retry a click if the initial prompt outlasted the backend's timeout.
- The native window follows the page's waiting-count title. Browser notification
  availability and delivery depend on WebKit; no native notification or Dock
  badge adapter is provided. Minimized/background timers may be throttled.

## Checks and memory

Live communication is on by default in the native app, for Finder/Dock and
direct launches alike. It only opens an owner-only local socket that carries no
conversation content; nothing is installed automatically. To turn it off, quit
the app yourself and launch
`"/Applications/Swarmview.app/Contents/MacOS/Swarmview" --no-communication` (or
the executable under `dist` for a local build). The Swift app forwards this
to its own bundled backend on **4778**; no CLI server on 4777 is required.
The app and `swarmview -communication` compete for one owner-only socket:
whichever starts first owns it. If the CLI owns it, the app still runs, with
communication unavailable (any other failure to open the bridge is logged
the same way and never stops the app launching); if the app owns it, a later `-communication` CLI
exits with "already owned". Quit one before starting the other. A
second launch activates the existing singleton without changing its mode;
quit/relaunch to change it.
The [OpenCode TUI bridge setup](../../integrations/opencode/README.md) is still
manual. The app ships the four optional files in
`Contents/Resources/integrations/opencode`, not `Contents/MacOS`. Review them
first, then manually point OpenCode's existing `cli.json` plugin entry at the
stable installed directory (for example,
`/Applications/Swarmview.app/Contents/Resources/integrations/opencode`). No
package dependencies are bundled or automatically installed, and the plugin
stays inactive until you add it. Closing/force-quitting the GUI retains the
same lifetime-pipe cleanup.

```sh
python3 desktop/macos/check.py dist/Swarmview.app
python3 desktop/macos/check.py dist/Swarmview.app --no-communication
python3 desktop/macos/check.py dist/Swarmview.app --seconds 300
# With a CLI dashboard on port 4777 using the same default tmux server:
python3 desktop/macos/check.py dist/Swarmview.app --compare-cli
```

The check exercises the real WKWebView (HTTP polling, focus-request boundary,
search, native Escape key dismissal/focus restoration, CSS support, storage, and blocked navigation),
second launch, normal quit, force-quit cleanup, a Finder-like PATH, and unrelated
port conflicts. Installer self-checks use temporary fixtures to reject unrelated
or mismatched builds and replaced downloads; they do not recycle user files.
Ghostty consent/focus still needs an interactive packaged-app
check; the automated smoke test never selects a real agent.
The default variant verifies the actual owned backend has communication on and
refuses an existing app/socket (so it never displaces another viewer's listener); both variants check that inverse-mode second launches
do not reconfigure the singleton. It does not install/enable a TUI plugin.

The optional measurement includes the app, backend, and newly created WebKit
helpers even when their parent is launchd. Keep other WebKit apps quiet during
the run; this attribution is a process-difference estimate, not a system-wide
reclaimable-RAM measurement. Physical footprint includes compressed/accounted
memory; summed RSS counts shared pages repeatedly. Compare warmed, equivalent
workloads and include the browser runtime in browser comparisons. Native
packaging alone does not establish a memory saving.

One local five-minute run on a 32 GiB Apple Silicon Mac running macOS 27.0.1
sampled 147 times: median physical footprints were 32.2 MiB for the Swift app,
9.1 MiB for the backend, and 48.6 MiB for new WebKit helpers. The median total
was **90.0 MiB**, peaking at **97.5 MiB**, with no missing footprint samples.
This was an existing-agent monitoring workload, not a new agent-concurrency
stress test or a simultaneous browser comparison. Reproduce on your machine
before generalizing these prototype numbers.

A later 120-second run of the installer-enabled app from Applications measured
84.7 MiB median / 96.0 MiB peak total physical footprint across 59 samples, with
no missing samples. This includes the one-shot startup detection path with no
matching installer mounted; it is not a long-term leak test or a controlled
before/after comparison. DMG artwork and Python packaging tools are not loaded
by the installed app.
