package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTmuxLive(t *testing.T) {
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX") // Finder has no TMUX; even an empty value enables UTF-8 in tmux.
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(key, "C")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	run := func(_ string, args ...string) (string, error) {
		return execRun(tmux, append([]string{"-S", socket, "-f", "/dev/null"}, args...)...)
	}
	mustRun := func(args ...string) string {
		t.Helper()
		out, err := run("tmux", args...)
		must(t, err)
		return strings.TrimSpace(out)
	}
	mustRun("new-session", "-d", "-s", "first", "sleep 60")
	t.Cleanup(func() { run("tmux", "kill-server") })
	pane := mustRun("new-session", "-d", "-s", "second", "-P", "-F", "#{pane_id}", "sleep 60")
	mustRun("split-window", "-t", pane, "sleep 60")
	mustRun("new-window", "-t", "second", "sleep 60")
	panePID := mustRun("display-message", "-p", "-t", pane, "#{pane_pid}")
	mustRun("select-pane", "-t", pane, "-T", "OC | 日本語 task")
	s := newServer(func(cmd string, args ...string) (string, error) {
		if cmd == "tmux" {
			return run(cmd, args...)
		}
		if cmd == "ps" {
			return panePID + " 1 1000 0:01 01:00 codex\n", nil
		}
		return "", nil
	})
	s.ghostty = false
	checkDiscovery := func(attached bool) {
		t.Helper()
		as := mustList(t, s, time.Now().UnixMilli())
		if len(as) != 1 || as[0].TmuxTarget != "second:"+pane || as[0].CanFocus != attached {
			t.Fatalf("live tmux discovery (attached=%v): %+v", attached, as)
		}
		if as[0].Title == nil || *as[0].Title != "日本語 task" {
			t.Fatalf("tmux title was not preserved: %+v", as[0].Title)
		}
	}
	checkDiscovery(false)
	agents := []Agent{{Pid: 100, CanFocus: true, pane: pane}}
	if ok, _ := focusAgent(100, agents, run, false); ok {
		t.Fatal("detached sessions must require an attached client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := exec.CommandContext(ctx, tmux, "-S", socket, "-C", "attach-session", "-t", "first")
	stdin, err := client.StdinPipe()
	must(t, err)
	must(t, client.Start())
	t.Cleanup(func() { stdin.Close(); client.Process.Kill(); client.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for mustRun("list-clients", "-F", "#{session_name}") == "" {
		if time.Now().After(deadline) {
			t.Fatal("control client did not attach")
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkDiscovery(true)
	if ok, why := focusAgent(100, agents, run, false); !ok {
		t.Fatal(why)
	}
	if got := mustRun("list-clients", "-F", "#{session_name}"); got != "second" {
		t.Fatalf("client session = %q", got)
	}
	if got := mustRun("display-message", "-p", "-t", "second", "#{pane_id}"); got != pane {
		t.Fatalf("selected pane = %q, want %q", got, pane)
	}
}
