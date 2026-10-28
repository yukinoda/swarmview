#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview, preferring the
# binary that Swarmview recorded beside its socket, then the CLI on PATH, then
# the native app. Never fails the hook.
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
if [ "$(uname)" = Darwin ]; then
  cache="$HOME/Library/Caches"
fi
running="$(cat "$cache/swarmview/live.exe" 2>/dev/null)"
for bin in \
  "$running" \
  "$(command -v swarmview)" \
  /Applications/Swarmview.app/Contents/MacOS/swarmview-server \
  "$HOME/Applications/Swarmview.app/Contents/MacOS/swarmview-server"
do
  if [ -x "$bin" ]; then
    # Hook mode always exits 0; a binary too old for the flag fails before
    # reading stdin, so the next candidate still gets the event.
    if "$bin" -claude-hook 2>/dev/null; then
      break
    fi
  fi
done
# Drain anything left unread so Claude never writes into a closed pipe.
cat >/dev/null
exit 0
