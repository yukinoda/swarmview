package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestTmuxDiscovery(t *testing.T) {
	title, available, attached := "Task one", true, true
	var calls []string
	s := srvWith(func(cmd string, args ...string) (string, error) {
		calls = append(calls, cmd+" "+joined(args))
		switch cmd {
		case "ps":
			if args[0] != "-axo" {
				t.Fatal("must not inspect process environments")
			}
			return "10 1 10 0:00 01:00 zsh\n20 1 10 0:00 01:00 zsh\n100 10 10 0:01 01:00 claude\n200 20 10 0:01 01:00 codex\n300 1 10 0:01 01:00 aider\n", nil
		case "tmux":
			if !available {
				return "", errors.New("no tmux server")
			}
			if args[0] == "list-clients" {
				if attached {
					return "/dev/ttys001\n", nil
				}
				return "", nil
			}
			return "%1\t10\t" + title + "\tdev\thost\n%2\t20\tTask two\tother\thost\nbad\t30\tx\tdev\thost\n%3\tbogus\tx\tdev\thost\n%4\t0\tx\tdev\thost\n", nil
		case "lsof":
			return "p100\nn/w/a\np200\nn/w/a\np300\nn/w/a\n", nil
		case "git":
			return "", errors.New("not git")
		default:
			t.Fatalf("unexpected command: %s", cmd)
			return "", nil
		}
	})
	check := func(now int64, wantTitle string) {
		t.Helper()
		as := mustList(t, s, now)
		for pid, pane := range map[int]string{100: "%1", 200: "%2", 300: ""} {
			a := agentByPid(as, pid)
			if a.pane != pane || a.CanFocus != (pane != "" && attached) {
				t.Fatalf("same cwd must not confuse pane matching: %+v", as)
			}
		}
		if a := agentByPid(as, 100); a.Title == nil || *a.Title != wantTitle {
			t.Fatalf("title not refreshed: %+v", a)
		}
		b, _ := json.Marshal(as)
		if !strings.Contains(string(b), `"tmuxTarget":"dev:%1"`) {
			t.Fatal("missing session/pane target")
		}
	}
	check(1000, title)
	title = "Renamed task"
	check(3000, title)
	attached = false
	check(4000, title)
	if ok, why := focusAgent(100, mustList(t, s, 4500), s.run, false); ok || why != "attach a tmux client first" {
		t.Fatalf("detached pane should explain recovery: %v %s", ok, why)
	}
	s.ghostty = true
	if a := agentByPid(mustList(t, s, 4600), 100); !a.CanFocus || a.TmuxAttached || a.TmuxTarget == "" {
		t.Fatalf("Ghostty can reopen a detached pane: %+v", a)
	}
	s.ghostty = false
	available = false
	for _, a := range mustList(t, s, 5000) {
		if a.CanFocus || a.Title != nil || a.pane != "" {
			t.Fatalf("unavailable tmux must fall back to monitoring: %+v", a)
		}
	}
}

func TestTmuxAncestry(t *testing.T) {
	tree := buildTree(rows([3]int{10, 1, 1}, [3]int{11, 10, 1}, [3]int{12, 11, 1}, [3]int{20, 21, 1}, [3]int{21, 20, 1}))
	panes := parseTmuxPanes("%1\t10\tOuter\tdev\thost\n%2\t11\tInner\tdev\thost\n")
	if p, ok := tree.paneFor(12, panes); !ok || p.id != "%2" {
		t.Fatal("nearest ancestor pane must win")
	}
	for _, pid := range []int{20, 99} {
		if _, ok := tree.paneFor(pid, panes); ok {
			t.Fatal("cycles and missing parents must not match")
		}
	}
}

func TestTmuxTaskTitles(t *testing.T) {
	for _, tc := range []struct{ window, pane, want string }{
		{"zsh", "OC | Next steps for swarmview", "Next steps for swarmview"},
		{"Named window", "OC | Actual conversation", "Actual conversation"},
		{"zsh", "✳ Audit task", "Audit task"},
		{"Named window", " ✻ Working task ", "Working task"},
		{"zsh", "✽ Working task", "Working task"},
		{"zsh", "✶ Working task", "Working task"},
		{"zsh", "✢ Working task", "Working task"},
		{"zsh", "Explain capabilities | demo", "Explain capabilities | demo"},
		{"Named window", " Task | demo ", "Task | demo"},
		{"Named window", "✳ ", "Named window"},
		{"zsh", "Task | ", ""},
		{"zsh", " | demo", ""},
		{"zsh", "user@host:~/demo", ""},
		{"zsh", "codex", ""},
		{"Named window", "MacBook.local", "Named window"},
		{"zsh", "MacBook.local", ""},
		{"/bin/bash", "OC | ", ""},
		{"opencode", "", ""},
	} {
		panes := parseTmuxPanes("%1\t10\t" + tc.window + "\tdev\t" + tc.pane + "\n")
		if got := panes[10].title; got != tc.want {
			t.Errorf("%q / %q: title %q, want %q", tc.window, tc.pane, got, tc.want)
		}
	}
}

func TestFocus(t *testing.T) {
	run := func(err error) Runner { return func(string, ...string) (string, error) { return "", err } }
	if _, e := focusAgent(7, nil, run(nil), false); e != "unknown pid" {
		t.Error(e)
	}
	for _, a := range []Agent{{Pid: 100}, {Pid: 100, CanFocus: true, pane: "-bad"}, {Pid: 100, CanFocus: true, pane: "%1; run-shell x"}} {
		if _, e := focusAgent(100, []Agent{a}, func(string, ...string) (string, error) {
			t.Fatal("unmatched or invalid pane must not execute a command")
			return "", nil
		}, false); e != "not in a tmux pane" {
			t.Error(e)
		}
	}
	ag := []Agent{{Pid: 100, CanFocus: true, pane: "%42"}}
	if _, e := focusAgent(100, ag, run(errors.New("no current client")), false); e != "tmux switch failed; attach a client first" {
		t.Error(e)
	}
	var calls []string
	ok := func(cmd string, args ...string) (string, error) {
		calls = append(calls, cmd+" "+joined(args))
		return "", nil
	}
	if o, e := focusAgent(100, ag, ok, false); !o || e != "" || !reflect.DeepEqual(calls, []string{"tmux switch-client -t %42"}) {
		t.Errorf("calls %v", calls)
	}
}
