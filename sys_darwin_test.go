package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGhosttyFocus(t *testing.T) {
	for _, attached := range []bool{true, false} {
		created, focused := 0, ""
		var switched []string
		run := func(cmd string, args ...string) (string, error) {
			if cmd == "osascript" {
				if args[0] == "-l" {
					name := "original title"
					if args[4] != "" {
						name = args[4]
					}
					b, _ := json.Marshal([]map[string]string{{"id": "existing-surface", "name": name}})
					return string(b), nil
				}
				if args[2] != "" {
					focused = args[2]
					return args[2], nil
				}
				created++
				command := args[3]
				if !strings.Contains(command, "-S '/tmp/a socket'\"'\"'s name' attach-session -t '%42'") || !strings.HasPrefix(command, "/usr/bin/env -u TMUX ") {
					t.Fatalf("reopen must use exact socket, shell quoting and clear TMUX: %s", command)
				}
				return "new-surface", nil
			}
			switch args[0] {
			case "display-message":
				return "/tmp/a socket's name\t$0\n", nil
			case "list-clients":
				clients := "other\t/dev/ttys001\t$1\txterm-ghostty\n"
				if attached {
					clients += "existing\t/dev/null\t$0\txterm-ghostty\n"
				}
				return clients, nil
			case "switch-client":
				switched = append(switched, joined(args))
			}
			return "", nil
		}
		if !attached {
			if _, err := exec.LookPath("tmux"); err != nil {
				continue
			}
		}
		if ok, why := focusGhostty("%42", run); !ok {
			t.Fatal(why)
		}
		if attached {
			if created != 0 || focused != "existing-surface" || len(switched) != 1 || switched[0] != "switch-client -c existing -t %42" {
				t.Fatalf("must reuse the existing session's tab/client, without creating a view: created=%d focused=%s switches=%v", created, focused, switched)
			}
		} else if created != 1 || len(switched) != 0 {
			t.Fatalf("only a detached target session may create a view: created=%d switches=%v", created, switched)
		}
	}
	if ghosttyInstalled() {
		out, err := execRun("osacompile", "-o", filepath.Join(t.TempDir(), "focus.scpt"), "-e", ghosttyScript)
		if err != nil {
			t.Fatalf("Ghostty script does not compile: %s %v", out, err)
		}
	}
}

func TestGhosttyUnidentifiableSessionNeverCreatesTab(t *testing.T) {
	regular := filepath.Join(t.TempDir(), "not-a-tty")
	must(t, os.WriteFile(regular, []byte("unchanged"), 0o600))
	for _, tty := range []string{"/dev/missing-swarmview-tty", regular} {
		var created, switched bool
		run := func(cmd string, args ...string) (string, error) {
			if cmd == "osascript" {
				if args[0] == "-l" {
					return "[]", nil
				}
				created = true
				return "", errors.New("must not create a view")
			}
			switch args[0] {
			case "display-message":
				return "socket\t$0", nil
			case "list-clients":
				return "client\t" + tty + "\t$0\txterm-ghostty", nil
			case "switch-client":
				switched = true
			}
			return "", nil
		}
		if ok, why := focusGhostty("%42", run); ok || !strings.Contains(why, "no new tab opened") || created || switched {
			t.Fatalf("identification failure must not duplicate or switch another tab: %v %s", ok, why)
		}
	}
	b, err := os.ReadFile(regular)
	must(t, err)
	if string(b) != "unchanged" {
		t.Fatal("must not write title sequences to a regular file")
	}
}

// Opt-in: creates two pre-existing Ghostty views on different sessions of one
// isolated tmux server, then checks that clicking reuses them without duplicates.
func TestGhosttyLive(t *testing.T) {
	if os.Getenv("SWARMVIEW_GHOSTTY_TEST") != "1" {
		t.Skip("set SWARMVIEW_GHOSTTY_TEST=1 for existing-tab focus verification")
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	run := func(cmd string, args ...string) (string, error) {
		if cmd == "tmux" {
			args = append([]string{"-S", socket, "-f", "/dev/null"}, args...)
		}
		return execRun(cmd, args...)
	}
	var ids []string
	t.Cleanup(func() {
		for _, id := range ids {
			execRun("osascript", "-e", `on run argv
	tell application "Ghostty"
		set matches to every terminal whose id is item 1 of argv
		if (count of matches) > 0 then close (item 1 of matches)
	end tell
end run`, id)
		}
		run("tmux", "kill-server")
	})
	var panes []string
	for _, name := range []string{"swarmview-test-a", "swarmview-test-b"} {
		pane, err := run("tmux", "new-session", "-d", "-s", name, "-P", "-F", "#{pane_id}", "sleep 120")
		must(t, err)
		panes = append(panes, strings.TrimSpace(pane))
		// Create an ordinary, unmanaged tab before invoking the focus function.
		// This is the case the old controlled-terminal test failed to exercise.
		out, err := run("osascript", "-e", `on run argv
	tell application "Ghostty"
		set config to new surface configuration
		set command of config to item 1 of argv
		set w to new window with configuration config
		set term to terminal 1 of selected tab of w
		focus term
		return id of term
	end tell
end run`, "/usr/bin/env -u TMUX tmux -S '"+socket+"' attach-session -t "+strings.TrimSpace(pane))
		must(t, err)
		ids = append(ids, strings.TrimSpace(out))
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := run("tmux", "list-clients", "-F", "#{client_name}")
		must(t, err)
		if len(strings.Fields(out)) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test clients did not attach")
		}
		time.Sleep(25 * time.Millisecond)
	}
	readTerminals := func() map[string]string {
		t.Helper()
		out, err := run("osascript", "-l", "JavaScript", "-e", ghosttyTerminalsScript, "")
		must(t, err)
		var terms []struct{ ID, Name string }
		must(t, json.Unmarshal([]byte(out), &terms))
		result := map[string]string{}
		for _, term := range terms {
			result[term.ID] = term.Name
		}
		return result
	}
	// Give both unmanaged terminals the same stable title. This verifies that
	// identification is a TTY challenge, not a pre-existing title heuristic.
	ttys, err := run("tmux", "list-clients", "-F", "#{client_tty}")
	must(t, err)
	for _, tty := range strings.Fields(ttys) {
		f, err := os.OpenFile(tty, os.O_WRONLY, 0)
		must(t, err)
		_, err = fmt.Fprint(f, "\x1b]2;swarmview-fixture\x07")
		f.Close()
		must(t, err)
	}
	before := readTerminals()
	deadline = time.Now().Add(2 * time.Second)
	for before[ids[0]] != "swarmview-fixture" || before[ids[1]] != "swarmview-fixture" {
		if time.Now().After(deadline) {
			t.Fatal("fixture titles did not settle")
		}
		time.Sleep(25 * time.Millisecond)
		before = readTerminals()
	}
	for _, i := range []int{0, 1, 0} {
		if ok, why := focusGhostty(panes[i], run); !ok {
			t.Fatal(why)
		}
		front, err := execRun("osascript", "-e", `tell application "Ghostty" to get id of focused terminal of selected tab of front window`)
		must(t, err)
		if strings.TrimSpace(front) != ids[i] {
			t.Fatalf("wrong existing surface: %s, want %s", front, ids[i])
		}
		clients, err := run("tmux", "list-clients", "-F", "#{client_session}")
		must(t, err)
		if len(strings.Fields(clients)) != 2 || !strings.Contains(clients, "swarmview-test-a") || !strings.Contains(clients, "swarmview-test-b") {
			t.Fatalf("must not create a new client or repurpose another session's tab: %s", clients)
		}
		after := readTerminals()
		if len(after) != len(before) {
			t.Fatal("click must not create a Ghostty tab/window")
		}
		for _, id := range ids {
			if after[id] != before[id] {
				t.Fatalf("original title must be restored: %q, want %q", after[id], before[id])
			}
		}
	}
}
