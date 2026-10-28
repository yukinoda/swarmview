# Optional live communication (Claude Code hooks)

Claude Code runs `swarmview -claude-hook` for a few hook events. Each run reads
the hook's JSON from stdin, keeps only the event name and a subagent ID, sends
one record to a running opted-in Swarmview, and exits. Prompts, messages, and
results are discarded in that process and never reach Swarmview.

Tested with Claude Code 2.1.312 on macOS and Linux.

## Manual opt-in — nothing is installed automatically

1. Start Swarmview with communication enabled, as described in the
   [OpenCode bridge setup](../opencode/README.md#manual-opt-in--nothing-is-installed-automatically):
   `./swarmview -communication`, or the native app launched with
   `--communication`. Both bridges share the same owner-only socket.
2. Add hooks to your **existing** `~/.claude/settings.json`, preserving
   other settings. Use the absolute path of the binary: the extracted CLI
   `swarmview`, or
   `/Applications/Swarmview.app/Contents/MacOS/swarmview-server` for the
   native app.

   ```json
   {
     "hooks": {
       "SubagentStart": [
         { "hooks": [{ "type": "command", "command": "/path/to/swarmview -claude-hook" }] }
       ],
       "SubagentStop": [
         { "hooks": [{ "type": "command", "command": "/path/to/swarmview -claude-hook" }] }
       ],
       "PreToolUse": [
         {
           "matcher": "SendMessage",
           "hooks": [{ "type": "command", "command": "/path/to/swarmview -claude-hook" }]
         }
       ]
     }
   }
   ```

   New Claude sessions pick up the hooks. If you move the binary or app,
   update the path.

Disable by removing the three entries. When Swarmview isn't running with
communication enabled, the hook finds no socket and exits at once; it never
fails or delays a Claude tool call beyond its one-second limit.

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
to 2.5 seconds for it, then is dropped.

Not shown yet: messages between teammates, messages addressed by name, and
messages to other Claude sessions. Those need a sideways display that the
parent/worker tree doesn't have.

## Identity

The socket checks the connecting process's kernel PID. A record is accepted
only if that process is a detected Claude process or descends from one within
four parent hops (Claude runs hooks through a shell). The cue carries that
Claude's PID, and the dashboard marks a worker row only if it belongs to the
same Claude. A hook is connected while it is checked, so its PID can't be
reused mid-check. Any program Claude runs is also its descendant, so it could
send cues for that Claude's own workers. It can't affect other sessions.
