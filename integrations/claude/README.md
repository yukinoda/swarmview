# Optional live communication (Claude Code plugin)

Claude Code runs `swarmview -claude-hook` for a few hook events. Each run reads
the hook's JSON from stdin, keeps only the event name and a subagent ID, sends
one record to a running opted-in Swarmview, and exits. Prompts, messages, and
results are discarded in that process and never reach Swarmview.

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

   Review [`hooks/hooks.json`][hooks] and [`hooks/forward.sh`][launcher]
   first; they are the whole plugin. It registers async `SubagentStart`,
   `SubagentStop`, and `PreToolUse` (`SendMessage`) hooks, so Claude never
   waits for them. The launcher runs the binary Swarmview recorded beside its
   socket (`live.exe`, written when `-communication` starts), so it finds a CLI
   started from any folder. Otherwise it tries `swarmview` on your `PATH`, then
   the native app in `/Applications` or `~/Applications`, skipping any binary
   too old to know `-claude-hook`. It does nothing when none is found and never
   fails a hook. New Claude sessions pick up the plugin.

   If you added these hooks to `~/.claude/settings.json` by hand, remove those
   entries when you install the plugin; otherwise every event shows twice.

   [hooks]: https://github.com/yukinoda/swarmview/blob/main/integrations/claude/hooks/hooks.json
   [launcher]: https://github.com/yukinoda/swarmview/blob/main/integrations/claude/hooks/forward.sh

Disable with `/plugin disable swarmview`, or remove it with
`/plugin uninstall swarmview`. When Swarmview isn't running with communication
enabled, the hook finds no socket and exits at once. Otherwise it gives up
after at most 5.5 seconds (a 0.5-second connect plus a 5-second exchange). It
never fails or delays a Claude tool call.

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

Starting `-communication` also writes `live.exe` (mode `0600`) beside the
socket, and stopping it removes the file. It holds only the running binary's
path, for the plugin's launcher, and no event, PID, or session data.
