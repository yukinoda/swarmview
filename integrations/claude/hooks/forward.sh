#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview. Prefers the copy
# of its own binary that Swarmview keeps in its private cache directory, then
# the CLI on PATH, then the native app. Never fails the hook.

# Helpers come from the system directories first, then only the absolute
# entries of your PATH (for systems like NixOS); a relative entry would resolve
# inside the project Claude has open. Your PATH is also searched for swarmview.
absolute=""
saved_ifs="$IFS"
IFS=:
set -f
for entry in $PATH; do
  case "$entry" in
    /*) absolute="$absolute:$entry" ;;
  esac
done
set +f
IFS="$saved_ifs"
PATH="/usr/bin:/bin$absolute"
# Like Go's os.UserCacheDir, an empty HOME means there is no cache directory.
if [ -z "$HOME" ]; then
  cat >/dev/null
  exit 0
fi
cache="${XDG_CACHE_HOME:-$HOME/.cache}"
darwin=""
if [ "$(uname)" = Darwin ]; then
  cache="$HOME/Library/Caches"
  darwin=1
fi
dir="$cache/swarmview"
# Like Go's os.UserCacheDir, accept only an absolute cache directory: a
# relative one would resolve inside the project Claude has open.
case "$dir" in
  /*) ;;
  *)
    cat >/dev/null
    exit 0
    ;;
esac

# deliver runs $1 in hook mode when it is an executable regular file and, if
# it exits 0, ends the launcher. Hook mode exits 0 unless the binary itself
# fails (it may still have found nothing listening); a binary too old for the
# flag fails before reading stdin, so the next candidate still gets the event.
deliver() {
  if [ -f "$1" ]; then
    if [ -x "$1" ]; then
      if "$1" -claude-hook 2>/dev/null; then
        cat >/dev/null
        exit 0
      fi
    fi
  fi
}

# No live socket means no Swarmview with the bridge on: skip starting a binary at all.
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
  # setgid (S) may be inherited from the parent; it grants no access.
  case "$mode" in
    drwx--[-S]---[\ @.])
      if [ -O "$dir" ]; then
        if [ -O "$dir/live-hook" ]; then
          if [ ! -h "$dir/live-hook" ]; then
            running="$dir/live-hook"
          fi
        fi
      fi
      ;;
  esac
  # Candidates in order: the private copy, every swarmview in an absolute PATH
  # entry, then the native app. Only the PATH entries are split on colons;
  # other paths, built from $HOME, may contain one.
  deliver "$running"
  IFS=:
  set -f
  for entry in $absolute; do
    if [ -n "$entry" ]; then
      deliver "$entry/swarmview"
    fi
  done
  set +f
  IFS="$saved_ifs"
  deliver /Applications/Swarmview.app/Contents/MacOS/swarmview-server
  deliver "$HOME/Applications/Swarmview.app/Contents/MacOS/swarmview-server"
fi
# Drain anything left unread so Claude never writes into a closed pipe.
cat >/dev/null
exit 0
