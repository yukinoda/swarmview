#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview, preferring the
# binary that Swarmview recorded beside its socket, then the CLI on PATH, then
# the native app. Never fails the hook.
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
if [ "$(uname)" = Darwin ]; then
  cache="$HOME/Library/Caches"
fi
dir="$cache/swarmview"
# No live socket means no opted-in Swarmview: skip starting a binary at all.
if [ -S "$dir/live.sock" ]; then
  running=""
  # Trust the recorded path only from your own directory, as your own regular
  # file, naming a binary you own.
  if [ -O "$dir" ]; then
    if [ -O "$dir/live.exe" ]; then
      if [ ! -h "$dir/live.exe" ]; then
        running="$(cat "$dir/live.exe")"
      fi
    fi
  fi
  if [ -n "$running" ]; then
    if [ ! -O "$running" ]; then
      running=""
    fi
  fi
  for bin in \
    "$running" \
    "$(command -v swarmview)" \
    /Applications/Swarmview.app/Contents/MacOS/swarmview-server \
    "$HOME/Applications/Swarmview.app/Contents/MacOS/swarmview-server"
  do
    if [ -x "$bin" ]; then
      # Hook mode exits 0 unless the binary itself fails; a binary too old for
      # the flag fails before reading stdin, so the next one gets the event.
      if "$bin" -claude-hook 2>/dev/null; then
        break
      fi
    fi
  done
fi
# Drain anything left unread so Claude never writes into a closed pipe.
cat >/dev/null
exit 0
