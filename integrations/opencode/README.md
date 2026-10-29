# Optional live communication bridge (OpenCode V2)

This CLI-only plugin observes the **existing TUI event connection**, filters
metadata locally, and sends live relationships and confirmed communication to
Swarmview. It never reads message history, retains prompts/results, creates a
session, changes model state, or starts/restarts the OpenCode service.

## Manual setup — nothing is installed automatically

Updated CLI archives include this directory beside `swarmview`; native app
downloads include it under `Contents/Resources/integrations/opencode`.
Both ship exactly `package.json`, `tui.ts`, `observer.js`, and this README,
without test fixtures, `node_modules`, or downloaded tool dependencies.
The files are inert until you explicitly configure OpenCode. Their project
license is [MIT](../../LICENSE), at the archive root or native `Resources/LICENSE`.

Supported contract: **OpenCode CLI and connected service 2.1.3 or 2.1.4**, macOS
or Linux. Other versions disable the observer rather than guessing a format.
Other providers retain normal monitoring/static hierarchy; no message support
is implied by executable detection.
The socket peer must itself match a detected OpenCode process row. Installations
whose launcher wrappers collapse that PID into a different shim row are not
mapped by guessing ancestry; they retain normal monitoring only.

1. For the **native macOS app**, download/update the matching app (or build
   locally with `bash desktop/macos/build.sh`), quit an existing Swarmview
   yourself. Communication is on by default, so a normal launch is enough. To
   turn it off, launch:

   ```sh
   "/Applications/Swarmview.app/Contents/MacOS/Swarmview" --no-communication
   ```

   For a local build, use `dist/Swarmview.app/Contents/MacOS/Swarmview` instead.
   The setting applies only to the app's owned backend on **4778**, with
   the same lifetime pipe and navigation restrictions. A second launch only
   activates the existing singleton; it cannot change its mode, so
   quit/relaunch to change it. If another Swarmview already owns the socket,
   the app still starts, with communication unavailable.
   No standalone CLI on 4777 is needed for native communication.

   For the separate CLI/browser workflow, extract the matching CLI archive
   (or build from source) and run `./swarmview -communication` (normally port
   4777). Keep the extracted `integrations` directory beside the binary.
   The app and
   `swarmview -communication` compete for one owner-only Unix socket:
   whichever starts first owns it. The app then still runs, without
   communication, and a later `-communication` CLI exits with "already owned".
   Quit one before starting the other.
   This opens an owner-only Unix socket at
   `~/Library/Caches/swarmview/live.sock` on macOS, or
   `${XDG_CACHE_HOME:-~/.cache}/swarmview/live.sock` on Linux. The directory must
   be owned by you with mode `0700`; the socket and empty `live.lock` file are
   `0600`. Only one Swarmview instance can own the socket. A live
   listener is never replaced. A crashed owner's private socket can be safely
   recovered on the next start while holding the owner lock.
2. **Review `package.json`, `tui.ts`, and `observer.js` first.** Keep all four
   packaged files together at a stable path. For a CLI archive, use the
   absolute extracted `integrations/opencode` directory. For an installed
   native app, use:

   `/Applications/Swarmview.app/Contents/Resources/integrations/opencode`

   A local build uses the same path inside `dist/Swarmview.app`. Review signed
   app resources without editing them; modifying the bundle invalidates its
   signature. In your **existing** global OpenCode `cli.json`, manually add
   the appropriate absolute directory to `plugins`, preserving other settings:

   ```json
   {
      "plugins": ["/Applications/Swarmview.app/Contents/Resources/integrations/opencode"]
   }
   ```

   This is a **TUI** plugin, not a server plugin: use `cli.json`, not
   `opencode.json`. The host supplies `@opencode/plugin/tui` and Solid; there
   is no dependency install or frontend build for Swarmview. Save your config
   and let the TUI reload the plugin. Do not restart the service to apply it.
   No settings were changed by the implementation/tests.
   If you move the extracted archive/app, update this path yourself. Updates
   may replace the reviewed code: review new files before keeping it enabled.
3. Use the selected session's built-in `subagent` tool normally. Only future
   observations can pulse; starting the monitor mid-task cannot replay a send
   or reconstruct a foreground result whose correlation was missed.

Disable by removing the plugin entry and quitting/relaunching Swarmview
(the CLI without `-communication`, the native app with `--no-communication`). Plugin
cleanup closes the socket/listeners/timer. Neither path installs the plugin.

## What the UI means

- **Delegation attempted** is static feedback for the built-in tool's explicit
  target/parent metadata. It is never shown as a confirmed send: progress precedes
  admission, and inbox events have no parent-call ID, so a nearby inbox message
  cannot safely prove which call sent it. Inbox bodies/events are not read.
- **Sent to worker** requires a successful background launch (or a foreground
  call moved to the background), with the observed call/child ID and
  `status: "running"`. The version-matched implementation awaits prompt
  admission before that success. There is no immediate confirmed foreground
  send confirmation: its evidence arrives only with the eventual result, when
  reply feedback is shown instead.
- **Reply received** requires that same observed call's native tool-success
  metadata, child ID, `executed: false` (native **provider-executed** flag), and
  **`status: "completed"`**. This built-in tool executes locally; `executed`
  does not mean whether the call succeeded.
  Background `status: "running"`, stop/idle/CPU activity, and failures are
  never replies. Background result notification is not supported in this version.
- Worker rows describe **relationships observed live during the selected
  session**, not processes or current task liveness. CPU/RAM/process age stay
  unavailable; state stays unknown. Clicking, when available, opens the
  mapped **parent terminal**, not a nonexistent worker process.
- Changing the TUI session, plugin/service/socket reconnection, or disconnect
  clears correlation. Two live TUIs selecting the same service/session are
  ambiguous: their workers/feedback are suppressed until unambiguous. This also
  applies when one selects a worker in the other's observed session tree.
  A replacement native service feed pauses the plugin until it is reloaded:
  the host context captures its API client at setup, so reusing an old
  endpoint's identity for a new feed would be unsafe. Browser/viewer-socket
  reconnects resume future observations automatically, never history.
  After a native service reconnect, use `/plugins` to disable and re-enable
  `swarmview.live-communication`; do not restart the service for this.
- The observed worker shows plain colored **Sent from parent**, **Reply received
  by parent**, or **Attempted from parent · unconfirmed** in its existing secondary
  context line for 2.5 seconds, without row fill, outline, stripe or motion. Parent
  and worker labels are announced politely (bounded to 160 characters each)
  and available in the cue tooltip. Status/RAM remain unknown/unavailable in
  Details, without dummy unknown pills or RAM cells in the compact worker row.
  The cue is not CPU state. Normal and reduced motion use the same static
  implementation: no travel, Web Animations, frame loop, or viewport layer.
  At most four cue/timer entries exist; repeated events update the same
  span instead of allocating a history. Hidden/collapsed/offscreen rows do
  not queue feedback. Fold/details/search/resize/scroll, topology changes,
  loss/ambiguity, disconnect, stale/offline state, or removal clear it.

## Bounds, privacy, and failure behavior

No event journal, replay log, transcript tail, database, or retained event ID
list exists. The plugin retains at most 64 pending call correlations and 64
worker relationships (identifiers only), cleared on switch/disconnect. Native
event bodies remain in OpenCode's own connection; the bridge reads only event
type/time, IDs, tool name, executed/status metadata, and parent metadata. It
never accesses inbox items, prompt/result text, or requests history.
Child ownership can hydrate asynchronously after progress. The observer keeps
only the pending target/call IDs and, if terminal success arrives first, its
confirmation kind for at most a 2-second monotonic identity-resolution window. The
existing reactive session cache triggers revalidation; no sync/history request
is added. A verified terminal publishes the worker and signal atomically.
Wrong parents are rejected; unresolved/expired identity is discarded with a
gap, never guessed. The existing 3-second heartbeat sweeps expired entries.

The namespace hashes the connected service's descriptor (version/PID/URLs/tmp
identity) without transmitting those paths/URLs. Linux explicitly checks the
kernel peer UID and PID. macOS checks the kernel peer PID; its cross-user
boundary is the user-owned private directory/socket, not a separate kernel
peer-UID check. No JSON PID claim is accepted. A live connection identifies its
process instance; closed/expired sockets lose all identity state, so PID reuse
cannot inherit a mapping. The source is a user-reviewed local plugin, not an
audit/security attestation of a remote OpenCode server.

Ctrl-C/SIGTERM unwind owned listener cleanup. SIGKILL cannot run defers, so the
next start acquires the exclusive lock and reclaims only an owned,
single-link `0600` socket whose connection probe reports `ECONNREFUSED` and
whose inode still matches. Symlinks, regular files, lax modes, unexpected lock
files, live listeners, timeouts, and other probe errors are refused rather than
unlinked. Cleanup also checks inode identity and disables Go's unconditional
Unix unlink-on-close so an old closer leaves a replacement path untouched.
The empty lock file deliberately remains: deleting/recreating that inode while
an instance is active would split the ownership lock. It stores no event/PID/
session data. This serializes cooperating Swarmview instances; it is not a
guarantee against a same-account program deliberately replacing private paths.

A normal shutdown of a TUI with a selected session sends a final `bye` record
and then closes the socket. `bye` ends the stream (later records are never
read), and the viewer drops that TUI's workers without a missing-observation
indication. Observations are session-scoped, so a TUI with no selected session
(home screen, or quitting before the connection opens) sends no `bye` and its
disconnect is never a loss. Any other disconnect of a TUI with a session
is reported as lost: a crash or SIGKILL, a timeout, an invalid record, `bye` for
the wrong session, a deliberate drop (the TUI closing the connection because its
write queue backed up or a line was too long), or OpenCode switching to a
replacement server (`server.connected`), where the bridge stops observing a
still-running TUI. When Swarmview itself stops the bridge, no lost frame is
sent, because the viewers are being torn down.

Maximums: 16 TUI socket connections, 64 workers per TUI, 1024-byte protocol
records, 64 records/second per TUI, 10-second missing-heartbeat expiry, four
browser viewers, four transient frames per viewer, four simultaneous UI
pulses. Slow viewers drop/coalesce frames and explicitly report missing
observations; they never replay. Socket writing does not block tool execution;
backpressure disconnects rather than growing a queue; connection attempts time
out after 2 seconds. A 3-second heartbeat
retries the optional viewer socket. No extra provider event connection opens.
Native records more than 5 seconds from the TUI clock are discarded with a
gap indication; remote clock skew or a long client pause can suppress them.

The usual browser snapshot polling remains 2s/5s. Disabled Swarmview opens no
bridge listener or browser stream. A configured TUI plugin with no viewer
discards observations instead of accumulating them. Memory and CPU costs need
measurement with the bridge active; no physical-memory savings are claimed.

## Checks

`go test -race ./...` includes standard-library socket/metadata checks, the real
Chrome UI harness with synthetic live frames, and `node --test` parser checks
when Node is present. Tests never install this plugin, invoke an agent tool,
or inspect private messages. Actual host-plugin activation requires the
explicit user setup above and is not claimed by synthetic fixtures.
The actual TUI entrypoint also runs against a synthetic host and real private
socket when Node has built-in TypeScript/module hooks (22.15+); older Node
versions still run the plain-JavaScript event-observer checks.
Native default-on and `--no-communication` forwarding are checked against the genuine
owned backend, including unchanged singleton mode and socket/pipe cleanup:

```sh
python3 desktop/macos/check.py dist/Swarmview.app
python3 desktop/macos/check.py dist/Swarmview.app --no-communication
```

These checks refuse an existing app/listener rather than stopping user apps.
