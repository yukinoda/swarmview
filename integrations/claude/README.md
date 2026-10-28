# Optional live communication (Claude Code plugin)

The plugin's async hooks run a small launcher, which runs
`swarmview -claude-hook` for a few hook events. Each run reads the hook's JSON
from stdin, keeps only the event name and a subagent ID, sends one record to a
running opted-in Swarmview, and exits. Prompts, messages, and results are discarded in
that process and never reach Swarmview.

Tested with Claude Code 2.1.312 on macOS. Linux uses the same code paths but
has not been run by hand.

## Opt-in setup — nothing is installed automatically

1. Start Swarmview with communication enabled, as described in the
   [OpenCode bridge setup](../opencode/README.md#manual-opt-in--nothing-is-installed-automatically):
   `./swarmview -communication`, or the native app launched with
   `--communication`. Both bridges share the same owner-only socket.
2. Install the Swarmview plugin from inside Claude Code (2.1.275 or later):

   ```
   /plugin install swarmview --marketplace yukinoda/swarmview
   ```

   On older versions, add the marketplace first:

   ```
   /plugin marketplace add yukinoda/swarmview
   /plugin install swarmview@swarmview
   ```

   Review [`hooks/hooks.json`][hooks] and [`hooks/forward.sh`][launcher] first;
   they are the whole plugin. It registers async `SubagentStart`,
   `SubagentStop`, and `PreToolUse` (`SendMessage`) hooks, so Claude never
   waits for them. When `-communication` starts, Swarmview copies its own
   binary into its private cache directory as `live-hook`, and the launcher
   runs that copy, so the CLI is found wherever you started it and always
   matches the running build. Otherwise it tries `swarmview` in the absolute
   directories on your `PATH` (never a relative entry, which would resolve
   inside the project), then the native app in `/Applications` or
   `~/Applications`, skipping any binary too old to know `-claude-hook`. It
   does nothing when none is found and never fails a hook. New Claude sessions
   pick up the plugin.

   If you added these hooks to `~/.claude/settings.json` by hand, remove those
   entries when you install the plugin; otherwise every event shows twice.

   [hooks]: https://github.com/yukinoda/swarmview/blob/main/integrations/claude/hooks/hooks.json
   [launcher]: https://github.com/yukinoda/swarmview/blob/main/integrations/claude/hooks/forward.sh

Disable with `/plugin disable swarmview`, or remove it with
`/plugin uninstall swarmview`. When no Swarmview has opted in, the launcher
finds no socket and exits without starting a binary. After a crash leaves a
stale socket, the binary starts, finds nothing listening, and exits at once.
The launcher uses only an absolute cache directory, as Swarmview does, and runs
`live-hook` only while that directory is yours with mode `0700` and no ACL as
`ls` reports it (on macOS checked with `ls -le`, since an ACL can hide behind
the `@` column; BusyBox `ls` doesn't report ACLs), and the copy is your own
file, not a symlink. No recorded path is ever trusted: besides that copy, the
launcher only runs `swarmview` from absolute `PATH` entries and the native app,
as your shell would, and the launcher looks up its own helper commands in
`/usr/bin` and `/bin` first, then only in the absolute entries of your `PATH`.
A hook gives up after at most 5.5 seconds (a 0.5-second connect plus a 5-second
exchange). It never fails or delays a Claude tool call.

### Without the plugin

Add the same hooks to your **existing** `~/.claude/settings.json` yourself,
preserving other settings. Use the absolute path of the binary: the extracted
CLI `swarmview`, or
`/Applications/Swarmview.app/Contents/MacOS/swarmview-server` for the native
app, and keep `"async": true` so Claude never waits. If you move the binary or
app, update the path. Disable by removing the three entries.

```json
{
  "hooks": {
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/swarmview -claude-hook",
            "async": true
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/swarmview -claude-hook",
            "async": true
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "SendMessage",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/swarmview -claude-hook",
            "async": true
          }
        ]
      }
    ]
  }
}
```

## What the UI means

Cues mark the worker rows Swarmview already shows for Claude subagents. They
appear for 2.5 seconds, with the same limits as OpenCode cues (at most four,
no history or replay).

- **Sent from parent** — a subagent started (`SubagentStart`).
- **Reply received by parent** — a subagent finished a turn (`SubagentStop`),
  foreground or background.
- **Message sent to worker** — Claude called `SendMessage` addressed to a
  running subagent's ID. The sender isn't named: any agent can send one.
  This means the message was issued, not that the worker read it.

A cue that arrives before the next poll has rendered its worker row waits up
to 5 seconds for it, then is dropped.

Not shown yet: messages between teammates, messages addressed by name, and
messages to other Claude sessions. Those need a sideways display that the
parent/worker tree doesn't have.

## Identity

The socket checks the connecting process's kernel PID. A record is accepted
only if that process is a detected Claude process or descends from one within
five parent hops (Claude runs hooks through a shell, the plugin through its
launcher). The cue carries that Claude's PID, and the dashboard marks a worker
row only if it belongs to the same Claude. A hook stays connected while it is
checked, which keeps its PID in use. If the check outlasts the hook's 5-second
limit, the hook exits and the record is rejected because its process is gone.
Any program Claude runs is also its descendant, so it could send cues for that
Claude's own workers. It can't affect other sessions.

Starting `-communication` also copies the running binary into the same
directory as `live-hook` (mode `0700`, about the binary's size on disk), and
stopping it removes the copy. It is the same program, used only by the plugin's
launcher, and holds no event, PID, or session data.
