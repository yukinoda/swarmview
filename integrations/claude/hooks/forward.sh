#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview. Prefers the copy
# of its own binary that Swarmview keeps in its private cache directory, then
# the CLI on PATH, then the native app. Never fails the hook.
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
darwin=""
if [ "$(uname)" = Darwin ]; then
  cache="$HOME/Library/Caches"
  darwin=1
fi
dir="$cache/swarmview"

# No live socket means no opted-in Swarmview: skip starting a binary at all.
if [ -S "$dir/live.sock" ]; then
  running=""
  # Run the private copy only while the directory is yours alone (the 0700
  # mode Swarmview requires, with no ACL) and the copy is your own file. The
  # 11th column is a space, @ (macOS xattrs) or . (SELinux); + is a Linux ACL.
  mode="$(ls -ld "$dir" 2>/dev/null | cut -c1-11)"
  # macOS shows @ rather than + when xattrs and an ACL coexist, but ls -le
  # lists each ACL entry on its own line.
  if [ -n "$darwin" ]; then
    if [ "$(ls -lde "$dir" 2>/dev/null | wc -l)" -ne 1 ]; then
      mode=""
    fi
  fi
  case "$mode" in
    "drwx------ " | drwx------@ | drwx------.)
      if [ -O "$dir" ]; then
        if [ -O "$dir/live-hook" ]; then
          if [ ! -h "$dir/live-hook" ]; then
            running="$dir/live-hook"
          fi
        fi
      fi
      ;;
  esac
  # swarmview from PATH, skipping relative entries: sh would resolve them in
  # the project Claude has open.
  onpath=""
  saved_ifs="$IFS"
  IFS=:
  for entry in $PATH; do
    case "$entry" in
      /*)
        if [ -x "$entry/swarmview" ]; then
          onpath="$entry/swarmview"
          break
        fi
        ;;
    esac
  done
  IFS="$saved_ifs"
  for bin in \
    "$running" \
    "$onpath" \
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
