#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview, preferring the
# binary that Swarmview recorded beside its socket, then the CLI on PATH, then
# the native app. Never fails the hook.
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
if [ "$(uname)" = Darwin ]; then
  cache="$HOME/Library/Caches"
fi
dir="$cache/swarmview"

# trusted succeeds when $1 (followed through symlinks) is owned by you or root
# and is writable by neither group nor others, so no other user can replace it.
trusted() {
  set -- $(ls -lndL "$1" 2>/dev/null)
  case "$3" in
    "$(id -u)" | 0) ;;
    *) return 1 ;;
  esac
  case "$1" in
    ?????w* | ????????w*) return 1 ;;
  esac
  return 0
}

# No live socket means no opted-in Swarmview: skip starting a binary at all.
if [ -S "$dir/live.sock" ]; then
  running=""
  # live.exe must be your own regular file in your own directory. Shutdown may
  # remove it at any moment, so read it quietly.
  if [ -O "$dir" ]; then
    if [ -O "$dir/live.exe" ]; then
      if [ -f "$dir/live.exe" ]; then
        if [ ! -h "$dir/live.exe" ]; then
          running="$(cat "$dir/live.exe" 2>/dev/null)"
        fi
      fi
    fi
  fi
  # The binary it names, and its folder, must be yours or root's alone: a stale
  # path in a shared place such as /tmp could otherwise be refilled by others.
  if [ -n "$running" ]; then
    if ! trusted "$running"; then
      running=""
    elif ! trusted "$(dirname "$running")"; then
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
