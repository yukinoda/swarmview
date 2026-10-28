#!/bin/sh
# Forwards one Claude Code hook event to a running Swarmview. Finds the CLI on
# PATH or the native app; does nothing when neither is installed, and never
# fails the hook.
for bin in \
  "$(command -v swarmview)" \
  /Applications/Swarmview.app/Contents/MacOS/swarmview-server \
  "$HOME/Applications/Swarmview.app/Contents/MacOS/swarmview-server"
do
  if [ -x "$bin" ]; then
    "$bin" -claude-hook 2>/dev/null
    exit 0
  fi
done
exit 0
