#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview. Prefers the copy
# of its own binary that Swarmview keeps in its private cache directory, then
# the CLI on PATH, then the native app. Never fails the hook.

# Helpers come from the system directories first, then only the absolute
# entries of your PATH (for systems like NixOS); a relative entry would resolve
# inside the project Claude has open. Your PATH is also searched for swarmview.
search_path="$PATH"
absolute=""
saved_ifs="$IFS"
IFS=:
set -f
for entry in $search_path; do
  case "$entry" in
    /*) absolute="$absolute:$entry" ;;
  esac
done
set +f
IFS="$saved_ifs"
PATH="/usr/bin:/bin$absolute"
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

# run_hook runs $1 in hook mode when it is an executable regular file, and
# succeeds only if it delivered (hook mode exits 0 unless the binary itself
# fails; a binary too old for the flag fails before reading stdin, so the next
# candidate still gets the event).
run_hook() {
  if [ -f "$1" ]; then
    if [ -x "$1" ]; then
      "$1" -claude-hook 2>/dev/null
      return
    fi
  fi
  return 1
}

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
  delivered=""
  if run_hook "$running"; then
    delivered=1
  fi
  if [ -z "$delivered" ]; then
    IFS=:
    set -f
    for entry in $absolute; do
      if [ -n "$entry" ]; then
        if run_hook "$entry/swarmview"; then
          delivered=1
          break
        fi
      fi
    done
    set +f
    IFS="$saved_ifs"
  fi
  if [ -z "$delivered" ]; then
    if run_hook /Applications/Swarmview.app/Contents/MacOS/swarmview-server; then
      delivered=1
    fi
  fi
  if [ -z "$delivered" ]; then
    run_hook "$HOME/Applications/Swarmview.app/Contents/MacOS/swarmview-server"
  fi
fi
# Drain anything left unread so Claude never writes into a closed pipe.
cat >/dev/null
exit 0
