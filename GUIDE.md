# Dashboard guide

[← Overview and quick start](README.md)

Use the dashboard to see activity across your coding agents, compare memory,
and return to the right terminal. Expand **What do these numbers mean?** in the
app for a quick explanation without leaving your sessions.

## Reading the dashboard

| Example | Meaning |
| --- | --- |
| `2 agents · 2 working` | Detected agent processes and how many are active; excludes subagent rows |
| `● 2 working` | Working agents in this repository or worktree; waiting counts appear separately |
| `RAM ~1.6 GB` | Approximate resident RAM of the agent and its child processes |
| `up 1h` | Process age, not duration of the current task |
| `CPU 36%` | Recent process-tree CPU use; 100% is one core, and values can exceed 100%. **Not task progress.** Only shown while working |
| `OpenCode · dev:%42` | Agent identity and tmux session/pane target; not a metric |
| `swap 0 MB · RAM 89% free` | Machine-wide swap and macOS-reported available-memory percentage; macOS only |
| `Last updated 0s ago` | Snapshot age; retained data keeps aging while offline |

Memory is amber at **500 MB** and red at **1.5 GB** per agent. These size
thresholds are hints, not errors or a measure of machine-wide memory pressure.
Subagent rows show their model in place of process age.

| Status | Meaning |
| --- | --- |
| Working | Agent and descendants use at least 0.025 CPU cores between samples |
| Waiting | Previously active within 30 minutes, now quiet |
| Idle | Not recently observed working |
| New | Process is less than 10 seconds old |
| Exiting | Gone since the previous poll; shown briefly before removal |

**Waiting is a heuristic.** Quiet network/I/O-bound work can still look like an
agent waiting for you. After 15 seconds of waiting, a task enters **Needs you**.
Notifications fire once per waiting episode for agents seen working in that tab.
Restarting Swarmview resets activity history.

The main row shows the task title when known, with the agent name and tmux target
underneath. OpenCode's `OC | …` pane title supplies its conversation title;
otherwise a custom tmux window name is used. Generic names such as `zsh` are
not presented as task names. Use `tmux rename-window 'Task name'` to label others.

The `~` memory value sums RSS across the agent and its descendants, excluding
other listed agents to avoid double counting between rows. Shared pages still
count once per process: it is a comparison tool, not a promise of reclaimable
RAM. **Reclaim memory** suggests up to three idle agents holding 300 MB or more;
closing them stays your call. System RAM/swap indicators are macOS-only.

Pin repositories, fold worktrees, and enable notifications with the bell. These
preferences are stored in your browser. **Last updated** shows snapshot age even
when the server is offline; the page retries failed or timed-out polls.

## Fast search

Search opens a compact native command-palette dialog when you press **⌘ K on
macOS** or **Ctrl K elsewhere**, or click the small **Search** shortcut button.
Its hint follows your platform. Opening it does not shift or filter the dashboard
behind it. Plain terms search visible task, agent, repository/worktree, status,
and pane labels—not hidden absolute paths or process IDs. It searches metadata
already loaded in the browser. Terms are
case-insensitive and combined with AND; quote a phrase to keep its words together.
For example:

```text
cache invalidation
agent:opencode repo:swarmview
status:waiting
task:"review authentication"
path:/feature pane:dev:%42
pid:30394
```

Fields are `task`, `agent`, `repo`, `path`, `status`, `pid`, and `pane`. Use `path:`
or `repo:` to search full paths, and `pid:` for process IDs. PID
matching is exact; other fields use substrings. The palette displays at most
**20 results**; refine the query to narrow larger sets. **↑/↓** move between
selectable results and **Enter** opens the selected task (or the first match
from the search box). Click **Done**, press **Esc**, or click outside to close
and return keyboard focus. **Clear** resets the query while keeping search open.
An activation error stays visible in the palette instead of silently closing it.

The tree, saved folds, needs-you/memory sections, tab badge, and notifications
remain unchanged. Typing makes no requests: it scans cached strings and reuses
the bounded result cards. The index is built lazily only for an active query
and released when the query is cleared or search closes. Temporary result cards
are removed too. Exited rows are discarded, with no history or background indexing.

## A tmux workflow

Ghostty provides the terminal window; tmux keeps shells and agents alive when
that window closes. From a checkout:

```sh
tmux new-session -A -s swarmview -c "$PWD"
```

Run `./swarmview` in one tmux window and agents in others. `Ctrl-b c` creates a
window, `Ctrl-b ,` renames it, `Ctrl-b w` chooses one, and `Ctrl-b d` detaches.
Reattach with `tmux attach -t swarmview`. Use ordinary Git worktrees for parallel
tasks; checkouts do not need to move.

Clicking a row selects its session, window, and pane. On macOS with Ghostty
detected, Swarmview focuses an existing Ghostty view attached to that session;
it opens a new view only if the session has no attached client. Ghostty 1.3+
and macOS Automation permission are required. Other terminals use tmux-only
selection. Read [terminal compatibility](COMPATIBILITY.md#terminal-behavior-when-clicking)
for the title-based identification, fixed-title limitations, and socket scope.

To migrate an agent into tmux, finish its current turn, exit normally, then use
the CLI's resume command inside tmux (for example,
`opencode --session SESSION_ID`). Existing non-tmux processes cannot simply be
moved into a pane. Do not open the same writable session in two terminals.

## Resource use

The server samples at most once per second regardless of viewer count. The page
polls every 2 seconds when visible and every 5 seconds when hidden; browsers can
throttle background polling further. Working icons use a CSS opacity animation
that respects reduced-motion preferences.

Sampling is request-driven: closing all dashboard tabs stops recurring sampling;
hidden tabs keep polling for notifications. Idle agents still own their session
state and caches—Swarmview cannot safely free another process's heap. Exiting an
agent releases its private memory, but surviving helper processes can retain RAM.

Historical macOS arm64 measurements before the current tmux/Ghostty integration:
about 17 MB server RSS and 0.09% CPU over 120 seconds. These are not a benchmark
of the current version. For terminal-overhead comparisons on macOS, use
`python3 memory_check.py --samples 30` with equivalent warmed-up workloads.
It compares Orca, Ghostty, and tmux, excluding hosted agents; summed RSS is
approximate and existing swap can remain occupied after pressure falls.
