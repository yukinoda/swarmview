package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func sysctlU32(name string) (uint32, bool) {
	v, err := syscall.SysctlUint32(name)
	return v, err == nil
}

func ghosttyInstalled() bool {
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/Applications/Ghostty.app", filepath.Join(home, "Applications/Ghostty.app")} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("ghostty")
	return err == nil
}

var ghosttyMu sync.Mutex

const ghosttyScript = `on run argv
	tell application "Ghostty"
		if item 1 of argv is "" then
			set config to new surface configuration
			set command of config to item 2 of argv
			set targetWindow to new window with configuration config
			set targetTerminal to terminal 1 of selected tab of targetWindow
		else
			set matches to every terminal whose id is item 1 of argv
			if (count of matches) is 0 then return ""
			set targetTerminal to item 1 of matches
		end if
		focus targetTerminal
		activate
		return id of targetTerminal
	end tell
end run`

const ghosttyTerminalsScript = `function run(argv) {
  return JSON.stringify(Application('Ghostty').terminals().map(t => ({id: t.id(), name: t.name()}))
    .filter(t => !argv[0] || t.name === argv[0]));
}`

// Ghostty 1.3 doesn't expose TTYs. Challenge the client's output with a unique
// title instead of guessing by cwd/title or relying on process-local mappings.
func ghosttySurface(tty string, run Runner) (string, error) {
	type terminal struct{ ID, Name string }
	query := func(marker string) ([]terminal, error) {
		out, err := run("osascript", "-l", "JavaScript", "-e", ghosttyTerminalsScript, marker)
		var terms []terminal
		if err == nil {
			err = json.Unmarshal([]byte(out), &terms)
		}
		return terms, err
	}
	before, err := query("")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "", fmt.Errorf("client TTY is not a character device")
	}
	marker := "swarmview-" + rand.Text()
	if _, err := fmt.Fprintf(f, "\x1b]2;%s\x07", marker); err != nil {
		return "", err
	}
	deadline := time.Now().Add(time.Second)
	for i := 0; i < 10 && time.Now().Before(deadline); i++ {
		matches, err := query(marker)
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue // Retry a transient automation failure so the title can be restored.
		}
		if len(matches) == 1 {
			for _, original := range before {
				if original.ID == matches[0].ID {
					// Do not allow a title to inject terminal control sequences.
					title := strings.Map(func(r rune) rune {
						if r < 32 || (r >= 127 && r < 160) {
							return -1
						}
						return r
					}, original.Name)
					_, err := fmt.Fprintf(f, "\x1b]2;%s\x07", title)
					return original.ID, err
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", fmt.Errorf("no Ghostty terminal accepted the client title marker")
}

func focusGhostty(pane string, run Runner) (bool, string) {
	// Serialize clicks so concurrent requests cannot create duplicate windows.
	ghosttyMu.Lock()
	defer ghosttyMu.Unlock()
	out, err := run("tmux", "display-message", "-p", "-t", pane, "#{socket_path}\t#{session_id}")
	socket, session, valid := strings.Cut(strings.TrimSpace(out), "\t")
	if err != nil || !valid || socket == "" || session == "" {
		return false, "cannot resolve tmux socket/session"
	}
	clients, err := run("tmux", "list-clients", "-F", "#{client_name}\t#{client_tty}\t#{session_id}\t#{client_termname}")
	if err != nil {
		return false, "cannot check tmux clients"
	}
	attached := false
	for _, line := range strings.Split(clients, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || fields[2] != session {
			continue
		}
		attached = true
		if fields[3] != "xterm-ghostty" {
			continue
		}
		id, err := ghosttySurface(fields[1], run)
		if err != nil {
			continue
		}
		if _, err := run("tmux", "switch-client", "-c", fields[0], "-t", pane); err != nil {
			return false, "tmux switch failed; try again"
		}
		focused, err := run("osascript", "-e", ghosttyScript, id, "")
		if err != nil || strings.TrimSpace(focused) != id {
			return false, "cannot focus existing Ghostty terminal; check Automation access"
		}
		return true, ""
	}
	if attached {
		return false, "session is attached but its Ghostty tab could not be identified; no new tab opened"
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return false, "tmux executable not found"
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := "/usr/bin/env -u TMUX " + quote(tmux) + " -S " + quote(socket) + " attach-session -t " + quote(pane)
	id, err := run("osascript", "-e", ghosttyScript, "", command)
	if err != nil || strings.TrimSpace(id) == "" {
		return false, "cannot open Ghostty; check Automation access and macos-applescript"
	}
	return true, ""
}
