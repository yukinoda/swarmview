# Dashboard guide

[← Overview and quick start](README.md)

Use the dashboard to see activity across your coding agents, compare memory,
and return to the right terminal. Click a task's separate **Details** control to open
**Details** for CPU and process age; opening it does not open a terminal. The
metric help at the bottom explains the numbers without leaving your sessions.

## Reading the dashboard

| Example | Meaning |
| --- | --- |
| `2 agents · 2 working` | Detected agent processes and how many are active; excludes subagent rows |
| `2 working` | Working agents in this repository or worktree; quiet and worker counts appear separately |
| `~1.6 GB` in RAM | Approximate resident RAM of the agent and its child processes |
| `—` in a process RAM cell, or unavailable metrics in worker Details | Missing measurements, not zero; live metadata workers omit empty process-metric cells |
| `Process age: 1h` in Details | Process age, not duration of the current task |
| `CPU 36%` in Details | Recent process-tree CPU use; 100% is one core, and values can exceed 100%. **Not task progress.** |
| `OpenCode · dev:%42` | Agent identity and tmux session/pane target; not a metric |
| `swap 0 MB · RAM 89% free` | Machine-wide swap and macOS-reported available-memory percentage; macOS only |
| `Last updated 0s ago` | Snapshot age; retained data keeps aging while offline |

Per-agent RAM stays neutral regardless of size. Machine-wide warning/critical
styling uses the actual macOS memory-pressure signal, not low free RAM or swap
usage alone. Existing swap can remain occupied after pressure falls. Subagent
details show their model when available rather than inventing process age or CPU.

| Status | Meaning |
| --- | --- |
| Working | Agent and descendants use at least 0.025 CPU cores between samples |
| Quiet | Previously active within 30 minutes, now quiet; not proof of a human request |
| Idle | Not recently observed working |
| Unknown | Activity telemetry is unavailable, not quiet |
| New | Process is less than 10 seconds old |
| Exiting | Gone since the previous poll; shown briefly before removal |

**Quiet is an activity heuristic, not a request.** Network, tool, worker, and
I/O waits can all look quiet. **Recently quiet** shows at most three observations,
from 15 seconds to five minutes quiet, after work was observed in that page
session. It is not a permanent alert queue or proof that work is done. Optional
notifications and the tab badge describe quiet activity, not confirmed attention requests.
Notifications fire once per quiet episode for agents seen working in that tab.
Restarting Swarmview resets activity history.

Native questions, permission requests and task-completion signals are unavailable.
Communication is off by default. The optional [OpenCode V2 bridge](integrations/opencode/README.md)
can show confirmed background admission (**Sent from parent**, blue), a correlated
foreground return (**Reply received by parent**, teal), or an explicit
**Attempted from parent · unconfirmed** observation (amber). Text carries the
meaning as well as color. Cues clear after 2.5 seconds, with no history or replay;
they do not change worker activity, imply success, or animate the tree.
CPU activity and nesting never create communication observations. There is no
transcript ingestion, and other providers have no communication adapter here.

The main row shows the task title when known, with the agent name and tmux target
underneath. OpenCode's `OC | …` pane title supplies its conversation title;
otherwise a custom tmux window name is used. Generic names such as `zsh` are
not presented as task names. Use `tmux rename-window 'Task name'` to label others.

The `~` memory value sums RSS across the agent and its descendants, excluding
other listed agents to avoid double counting between rows. Shared pages still
count once per process: it is a comparison tool, not a promise of reclaimable
RAM. Swarmview does not infer reclaimable memory or recommend closing an agent
merely because it is large or quiet. System RAM/swap indicators are macOS-only.

Pin repositories and fold worktrees in the overview. Open **Preferences** for
System/Light/Dark appearance and opt-in quiet notifications. Opening Preferences
does not request notification permission; enabling notifications is an explicit
action. App preferences are stored in this page's browser profile, not agent CLI
configuration. **Last updated** shows snapshot age even
when the server is offline; the page retries failed or timed-out polls. After 15
seconds, retained rows are labeled **stale**; failed polls label them **offline**.
These states suppress quiet observations and badges instead of presenting old
measurements as live activity.

Details also lets you hide/show a worker branch without folding its siblings.
Hidden-worker counts include every worker descendant hidden by that branch,
not just its direct children. Mixed process/worker branches are labeled nested
agents rather than workers. Repository/worktree headers count each worker once
and keep those totals when collapsed. Only task identity is indented. Measured
process activity and RAM stay aligned; live metadata workers use that space for
identity, communication text and a visible Details control instead of empty
metric cells. Their names open the mapped main agent's terminal, while
communication refers to the immediate parent shown by the hierarchy. Preferences
shows the actual communication capability and manual setup guidance; it does not
install or enable the bridge. On narrow screens, worker rows reserve extra cue
space so the unconfirmed qualifier can wrap without cue-induced row movement.

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
status:quiet
task:"review authentication"
path:/feature pane:dev:%42
kind:codex worktree:/feature
pid:30394
```

Fields are `task`, `agent`, `kind`, `repo`, `path`, `worktree`, `status`, `pid`, and
`pane`. `kind:` uses the normalized agent kind; full live-worker identities are
also searchable without shortening. Use `path:`, `worktree:`, or `repo:` to search
full paths, and `pid:` for process IDs. PID
matching is exact; other fields use substrings. The palette displays at most
**20 results**; refine the query to narrow larger sets. **↑/↓** move between
selectable results and **Enter** opens the selected task (or the first match
from the search box). Click **Done**, press **Esc**, or click outside to close
and return keyboard focus. **Clear** resets the query while keeping search open.
An activation error stays visible in the palette instead of silently closing it.
`status:waiting` remains an alias for the visible quiet state.

The tree, saved folds, recent observations, tab badge, and notifications
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

Clicking an enabled task identity selects its session, window, and pane;
the adjacent Details control remains usable for monitoring-only agents. On macOS
with Ghostty detected, Swarmview focuses an existing Ghostty view attached to that session;
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
throttle background polling further. Provider icons and hierarchy lines are
static; there is no continuously animated swarm or communication animation.

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
