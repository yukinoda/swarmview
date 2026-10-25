# Compatibility

Swarmview monitors local processes independently of your terminal. Selecting a
tmux pane and controlling a terminal application's OS window are separate
capabilities.

## Platform support

| Capability | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Agent discovery, CPU status, and process-tree RSS | Supported; checked locally | Covered by Ubuntu CI | Unsupported |
| Repository/worktree grouping | Supported with Git and `lsof` | Supported with Git and `lsof` | Unsupported |
| Select an attached tmux client's pane | Supported | Supported; live isolated-server check in CI | Unsupported |
| Bring a terminal OS window forward | Ghostty 1.3+ only | Not implemented | Not implemented |
| Open a terminal window for a detached tmux session | Ghostty 1.3+ only | Not implemented | Not implemented |
| System free RAM, memory pressure, and swap | macOS sysctls; missing pressure is explicitly unavailable and missing free/swap values are omitted | Not implemented; pressure displays unavailable, per-agent RSS still works | Unsupported |
| Dedicated system-WebView desktop app | Prototype; macOS 13.3+, local Apple Silicon check | Not implemented; use browser UI | Unsupported |
| `memory_check.py` terminal-overhead comparison | macOS only | Not supported as a comparison tool | Unsupported |

Native Windows builds are not supported: process execution uses Unix process
groups and signals. WSL should be treated as a Linux environment; native Windows
processes and terminal windows are outside the supported scope. WSL and other
Unix/BSD systems are not currently verified.

CI is configured for Ubuntu and macOS. macOS-specific code has regression checks, including
Ghostty AppleScript compilation when Ghostty is installed. The opt-in
`SWARMVIEW_GHOSTTY_TEST=1 go test -race -run TestGhosttyLive -v .` checks actual
existing-view keyboard focus, title restoration, unchanged view counts, and
client session selection using its own Ghostty views and isolated tmux server.
Automation permission must already be granted
for unattended execution; the test does not interact with permission prompts.

## Release targets

The release workflow builds `darwin-amd64`, `darwin-arm64`, `linux-amd64`, and
`linux-arm64` archives with the UI and logo embedded. All four targets are
cross-buildable; runtime CI coverage follows the hosted Ubuntu and macOS runner
architectures, rather than testing every target architecture separately.

Builds disable CGO; Linux binaries are statically linked. macOS binaries are not
signed or notarized by the release workflow. Archives include SHA-256 checksums
as a separate release asset.

Separate macOS `.dmg` images and `.app.zip` archives bundle a Swift/AppKit WKWebView launcher and
the Go backend for ARM/Intel. They are ad-hoc signed, not Developer ID signed or
notarized. Native app builds require Apple's SDK and are separate from CGO-free
CLI cross-builds. See [desktop app limitations and checks](desktop/macos/README.md).
DMG builds additionally use Python 3.10+ and a temporary pinned dmgbuild install;
users downloading the app do not need those tools. The artwork runs in Finder,
and optional eject/Trash cleanup is a one-shot native action after installation.

## Requirements

- **Build:** Go 1.27 or newer, as declared in `go.mod`. The binary embeds the UI;
  no Node.js, frontend build, or Go runtime installation is needed to run it.
- **Monitoring:** `ps` on `PATH`, with the fields used by
  `ps -axo pid=,ppid=,rss=,time=,etime=,args=`. Processes must be visible to the
  user running Swarmview.
- **Directory/repository metadata:** `lsof` for working directories and Git for
  repository/worktree grouping. Missing or inaccessible directories appear as
  unknown; failed Git lookups fall back to directory-based grouping.
- **Pane selection:** tmux on `PATH`. No minimum tmux version is currently
  enforced; discovery needs `list-panes`, `list-clients`, and their format fields,
  and selection needs `switch-client -t %pane`. Conversation labels recognize
  OpenCode's `OC | …`, Claude's status-symbol-prefixed task title, and Codex's
  `task | project` pane titles, then fall back to custom tmux window names;
  generic shell names are suppressed. These title formats were observed locally
  on macOS; parser regression checks cover them on both CI platforms.
- **Optional memory comparison script:** Python 3.8+ and macOS command-line tools.

## Terminal behavior when clicking

| Setup | Attached tmux client exists | No attached tmux client |
| --- | --- | --- |
| macOS with Ghostty detected | Reuse the Ghostty client attached to the target session; select its pane and focus its existing surface | Open a Ghostty view attached to the target session only if that session has no attached client |
| macOS without Ghostty detected | Select the pane in tmux; bring your terminal forward yourself | Row disabled; attach a client first |
| Linux, including Ghostty on Linux | Select the pane in tmux; bring your terminal forward yourself | Row disabled; attach a client first |
| Agent running outside tmux | Monitoring only | Monitoring only |

Ghostty detection runs at server startup. It checks `/Applications/Ghostty.app`,
`~/Applications/Ghostty.app`, then `ghostty` on `PATH`. Restart Swarmview after
installing or removing it.

**Detection is installation-based, not client-based.** If Ghostty is installed
on macOS, window control targets Ghostty even if your attached tmux client is
actually in Terminal.app, iTerm2, or another terminal. There is currently no
terminal selector or automatic owner detection.

### Ghostty requirements and limitations

- Requires **Ghostty 1.3.0+**, which introduced its AppleScript API. Installation
  detection does not check the version.
- AppleScript must be enabled in Ghostty (`macos-applescript = true`, the
  default). Allow the controlling app access to Ghostty when macOS requests
  Automation permission. Existing permissions are under **System Settings →
  Privacy & Security → Automation**.
- Clicks prefer a Ghostty client already attached to the target **session**, not
  an arbitrary client on the same server. That client's pane and exact Ghostty
  window/tab/split are focused. Other sessions' clients are not repurposed.
- Ghostty 1.3 does not expose terminal TTYs through AppleScript. Swarmview briefly
  writes a unique title marker to a candidate client's output TTY, matches the
  surface through AppleScript, and restores the captured original title. This
  does not send keyboard input or commands, read terminal contents or process
  environments, or change tmux configuration.
- Fixed Ghostty title overrides or concurrent title updates can prevent the
  marker from being observed. If an attached session cannot be identified,
  clicking reports an error instead of creating a duplicate view. A failed
  automation call during identification can leave the temporary title visible
  until the terminal's next title update.
- A new view is opened only when the target session has **no attached client**.
  A client attached to that session through another terminal or SSH prevents
  automatic creation. Clients of other sessions do not prevent reopening.
- Identification is repeated on each click, so restarting Swarmview does not
  cause existing tabs to be duplicated.
- Reopening attaches to the existing agent's pane; it does not restart the
  agent. The tmux session and agent must still be running after the window closes.
- If window control or tmux attachment fails, the row shows an error rather than
  raising an unrelated window or switching an arbitrary attached client.

### tmux server scope

Swarmview uses tmux's normal socket selection: the default server, or the server
inherited through `TMUX` when Swarmview starts inside tmux. It does not scan
additional custom `-L`/`-S` servers. Ghostty reopening uses the selected server's
exact socket and clears inherited `TMUX` to avoid nesting clients.

Agents are matched to panes through process ancestry, not by directory. Closing
an ordinary non-tmux terminal cannot be undone by Swarmview; exited agents must
be resumed manually using their CLI's own session-resume command.

## Agent coverage

Cursor CLI's `agent` executable is recognized when its command line includes
the bundled `cursor-agent/versions/.../index.js` runtime path. Unrelated programs
named `agent` are not included. OpenCode `serve` backends and Cursor `worker` /
`worker-server` helpers are excluded as separate agents, but their resources
still count toward their parent process trees.

Executable detection currently includes `claude`, `codex`, `opencode`, `gemini`,
`aider`, and `cursor-agent`. Detection does not imply access to a CLI's native
session status or conversation metadata.

- Working/quiet status is a CPU-based heuristic, including descendants.
  Network/I/O-bound work can still appear quiet; processes that start and
  exit between samples cannot be measured.
- Native human requests, permissions, completion, and send/reply events are not
  available. Recently quiet observations and notifications do not confirm that
  someone needs to act. Nesting indicates discovered parentage, not communication.
- Per-agent RAM is approximate process-tree RSS and remains neutral regardless of
  size. Missing measurements (including Claude workers) display a dash, not zero.
  Machine-wide warning colors require the macOS pressure signal; free RAM and
  existing swap alone do not establish pressure.
- Claude Code has additional read-only subagent discovery under
  `~/.claude/projects`. Matching is by working directory; an exact parent is
  assigned only when one Claude agent runs in that directory.
- When Claude's pane title is generic or unavailable, generated task titles are
  read from `ai-title` metadata records in the last 64 KiB of the session JSONL.
  This requires matching PID and working directory metadata in
  `~/.claude/sessions/<pid>.json`; older CLI versions without it retain the
  existing label. Renamed pane titles take precedence. Titles outside the
  bounded tail are unavailable; full conversation histories are not scanned.
- Other CLIs have process-level monitoring and tmux window titles, without
  native subagent/session adapters.
- Executable paths containing spaces, runtime flags taking separate values,
  and some daemon-subcommand-like prompts can confuse process-name detection.

## Browser support

Use a current Chrome/Chromium, Firefox, or Safari. The UI uses native JavaScript modules, modern CSS
(`:has()` and `color-mix()`) and `AbortSignal.timeout()`; older browser versions
are not tested. Automated UI checks use Chrome/Chromium with synthetic data at
compact and native-window widths, both shortcut platforms, light/dark themes,
and reduced motion. These are browser checks, not a claim of native WKWebView or
Firefox/Safari visual verification.

Desktop notifications are optional and require the browser's Notification API,
site permission, and OS notification settings. If the API is unavailable, the
Notify control is hidden; recent quiet observations, the tab title, and favicon still work.
Background-tab throttling can delay polling and notifications; Chrome may poll
about once per minute after the tab has been hidden for over five minutes.

The server listens on `127.0.0.1` only and accepts its configured
`127.0.0.1:PORT` or `localhost:PORT` host. Remote/LAN access, arbitrary reverse
proxy hostnames, and IPv6 loopback are not supported by the current listener and
host checks.
