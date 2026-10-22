package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const PS = `  100     1 1000  0:05.40 01:02:03 claude --dangerously-skip-permissions
  101   100 1000 0:00.10    00:10 claude -p summarize
  110     1 1000 0:00.20    00:10 node /usr/lib/node_modules/@openai/codex/bin/codex.js --yolo
  111   110 1000 1:00.00    00:10 /usr/lib/node_modules/@openai/codex/vendor/codex --yolo
  200     1 1000 0:00.00 1-02:03:04 /Users/x/.codex/bin/codex app-server --listen unix://
  201     1 1000 0:00.20    05:00 claude -p explain the mcp config
  202     1 1000 0:00.00    05:00 claude mcp serve
  300     1 1000 0:02.00    00:30 /opt/homebrew/bin/node --no-warnings /usr/local/bin/gemini
  400     1 1000 9:00.00    00:01 vim notes.md
  500     1 1000 1-02:00:00   00:01 opencode
  600     1 1000 0:00.00    00:01 /Applications/Claude.app/Contents/MacOS/Claude
`

func psAgents(text string, self int) []psRow { a, _ := parsePs(text, self); return a }

func pidKinds(rows []psRow) []string {
	var o []string
	for _, a := range rows {
		o = append(o, itoa(a.Pid)+":"+a.Kind)
	}
	return o
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func hasPid(rows []psRow, pid int) bool {
	for _, a := range rows {
		if a.Pid == pid {
			return true
		}
	}
	return false
}

func findPid(rows []psRow, pid int) psRow {
	for _, a := range rows {
		if a.Pid == pid {
			return a
		}
	}
	return psRow{}
}

func TestDetectsAgents(t *testing.T) {
	want := []string{"100:claude", "101:claude", "110:codex", "201:claude", "300:gemini", "500:opencode"}
	if got := pidKinds(psAgents(PS, 0)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestPsFilters(t *testing.T) {
	rows := psAgents(PS, 0)
	for _, tc := range []struct {
		name string
		pid  int
		want bool
	}{
		{"daemon app-server excluded", 200, false},
		{"daemon mcp excluded", 202, false},
		{"prompt containing mcp kept", 201, true},
		{"shim duplicate dropped", 111, false},
		{"child with different args kept", 101, true},
		{"case-sensitive Claude.app not an agent", 600, false},
	} {
		if hasPid(rows, tc.pid) != tc.want {
			t.Errorf("%s", tc.name)
		}
	}
	if findPid(rows, 300).Kind != "gemini" {
		t.Error("runtime-wrapped agent")
	}
}

func TestParsesFields(t *testing.T) {
	rows := psAgents(PS, 0)
	if rows[0] != (psRow{100, 1, 5.4, "01:02:03", "claude"}) {
		t.Errorf("got %+v", rows[0])
	}
	if findPid(rows, 500).CpuSec != 93600 {
		t.Error("day-prefixed time")
	}
}

func TestParsesMacTimeOver59Minutes(t *testing.T) {
	r := psAgents("  7 1 1000 1036:27.10 00:01 aider", 0)
	if math.Abs(r[0].CpuSec-62187.1) > 1e-6 {
		t.Errorf("got %v", r[0].CpuSec)
	}
}

func TestExcludesSelfPid(t *testing.T) {
	if hasPid(psAgents(PS, 100), 100) {
		t.Error("self included")
	}
}

func TestEmptyInput(t *testing.T) {
	if r := psAgents("", 0); r == nil || len(r) != 0 {
		t.Errorf("got %#v", r)
	}
}

func TestCpuSecondsNaN(t *testing.T) {
	for _, s := range []string{"garbage", "x-1:00", "1:xx", ""} {
		if !math.IsNaN(cpuSeconds(s)) {
			t.Errorf("%q should be NaN", s)
		}
	}
	if cpuSeconds("010-00:00:01") != 10*86400+1 { // base-10 day field
		t.Error("leading zero day")
	}
}

func TestParseLsof(t *testing.T) {
	m := parseLsof("p100\nfcwd\nn/Users/x/proj-a\np201\nfcwd\nn/tmp/b c\n")
	if m[100] != "/Users/x/proj-a" || m[201] != "/tmp/b c" {
		t.Errorf("got %v", m)
	}
	if _, ok := m[999]; ok {
		t.Error("unexpected 999")
	}
}

// fake runner helpers
type call struct{ cmd, args string }

func joined(args []string) string { return strings.Join(args, " ") }

func fakeRun(lsof func() (string, error)) Runner {
	return func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n  201 1 1000 0:00.00 00:10 codex\n", nil
		}
		return lsof()
	}
}

func srvWith(run Runner) *server { s := newServer(run); s.ghostty = false; return s }

func mustList(t *testing.T, s *server, now int64) []Agent {
	t.Helper()
	a, err := s.listAgents(now)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func agentByPid(as []Agent, pid int) Agent {
	for _, a := range as {
		if a.Pid == pid {
			return a
		}
	}
	return Agent{}
}

func TestListAgentsJoinsCwdAndProject(t *testing.T) {
	s := srvWith(fakeRun(func() (string, error) { return "p100\nn/Users/x/my-harness\np201\nn/tmp/proj\n", nil }))
	a := mustList(t, s, 1000)
	if a[0].Project != "my-harness" || a[1].Project != "proj" {
		t.Errorf("got %v %v", a[0].Project, a[1].Project)
	}
}

func TestListAgentsUsesPartialLsofStdout(t *testing.T) {
	s := srvWith(fakeRun(func() (string, error) {
		return "p100\nfcwd\nn/Users/x/my-harness\n", errors.New("exit 1")
	}))
	a := mustList(t, s, 1000)
	if agentByPid(a, 100).Project != "my-harness" || agentByPid(a, 201).Project != "unknown" {
		t.Errorf("got %+v", a)
	}
}

func TestListAgentsSurvivesTotalLsofFailure(t *testing.T) {
	s := srvWith(fakeRun(func() (string, error) { return "", errors.New("lsof: not found") }))
	a := mustList(t, s, 1000)
	if len(a) != 2 || a[0].Cwd != nil {
		t.Errorf("got %+v", a)
	}
}

func TestListAgentsPsFailure(t *testing.T) {
	s := srvWith(func(cmd string, args ...string) (string, error) { return "", errors.New("boom") })
	if _, err := s.listAgents(1000); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.snapshot(1000); err == nil || s.snap != nil {
		t.Fatal("snap must not update on ps failure")
	}
}

func E(o func(*cacheEntry)) *cacheEntry {
	e := &cacheEntry{cwd: "/x/a", etime: "00:05"}
	if o != nil {
		o(e)
	}
	return e
}

const PS2 = "  100 1 1000 0:01.00 00:10 claude\n  201 1 1000 0:00.00 00:10 codex\n"

func TestLsofOnlyForUncachedPids(t *testing.T) {
	var calls []call
	s := srvWith(func(cmd string, args ...string) (string, error) {
		calls = append(calls, call{cmd, joined(args)})
		if cmd == "ps" {
			return PS2, nil
		}
		return "p201\nn/tmp/proj\n", nil
	})
	s.cache[100] = E(func(e *cacheEntry) { e.cwd = "/Users/x/a" })
	a := mustList(t, s, 1000)
	for _, c := range calls {
		if c.cmd == "lsof" && !strings.HasSuffix(c.args, " 201") {
			t.Errorf("lsof args %q", c.args)
		}
	}
	if a[0].Project != "a" || a[1].Project != "proj" {
		t.Errorf("got %v %v", a[0].Project, a[1].Project)
	}
}

func TestCachedAgentsOnlyPollProcessesAndTmux(t *testing.T) {
	var calls []string
	s := srvWith(func(cmd string, args ...string) (string, error) {
		calls = append(calls, cmd)
		return "  100 1 1000 0:01.00 00:10 claude\n", nil
	})
	s.cache[100] = E(nil)
	mustList(t, s, 1000)
	if !reflect.DeepEqual(calls, []string{"ps", "tmux"}) {
		t.Errorf("got %v", calls)
	}
}

func TestCacheEvictsDeadPids(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "  100 1 1000 0:01.00 00:10 claude\n", nil })
	s.cache[100], s.cache[999] = E(nil), E(nil)
	mustList(t, s, 1000)
	if len(s.cache) != 1 || s.cache[100] == nil {
		t.Errorf("got %v", s.cache)
	}
}

func TestCacheClearedWhenNoAgents(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	s.cache[100] = E(nil)
	mustList(t, s, 1000)
	if len(s.cache) != 0 {
		t.Error("cache not cleared")
	}
}

func TestUnresolvedCwdNotCached(t *testing.T) {
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n", nil
		}
		return "", nil
	})
	mustList(t, s, 1000)
	if s.cache[100] != nil {
		t.Error("cached")
	}
}

func TestUnresolvedCwdRetriedAfterBackoff(t *testing.T) {
	n := 0
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n", nil
		}
		if cmd == "lsof" {
			n++
		}
		return "", nil
	})
	for _, at := range []int64{1000, 2000, 1000 + cwdRetryMs - 1, 1000 + cwdRetryMs} {
		mustList(t, s, at)
	}
	if n != 2 {
		t.Errorf("lsof calls = %d", n)
	}
}

func TestReusedPidReResolved(t *testing.T) {
	var calls []string
	s := srvWith(func(cmd string, args ...string) (string, error) {
		calls = append(calls, cmd)
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:03 claude\n", nil
		}
		return "p100\nn/x/new\n", nil
	})
	s.cache[100] = E(func(e *cacheEntry) { e.cwd, e.etime = "/x/old", "05:00" })
	a := mustList(t, s, 1000)
	if !slices.Contains(calls, "lsof") || a[0].Project != "new" {
		t.Errorf("calls %v project %v", calls, a[0].Project)
	}
}

func TestActivityUsesRate(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "  100 1 1000 0:01.10 00:10 claude\n", nil })
	s.cache[100] = E(nil)
	s.prevProcs = map[int]procRow{100: {pid: 100, cpuSec: 1, etime: "00:08"}}
	a := mustList(t, s, 2000)[0] // +0.1 cpu-s over 2 s = 0.05 cores
	if math.Abs(float64(a.RecentCpu)-0.05) > 1e-9 || a.IdleSec == nil || *a.IdleSec != 0 {
		t.Errorf("got %+v", a)
	}
	b := mustList(t, s, 2100)[0] // no new cpu: lastActiveAt not refreshed
	if b.RecentCpu != 0 || math.Abs(*b.IdleSec-0.1) > 1e-9 {
		t.Errorf("got %+v", b)
	}
}

func TestRateIgnoredAfterLongGap(t *testing.T) {
	cpu := "1:40.00"
	s := srvWith(func(string, ...string) (string, error) { return "  100 1 1000 " + cpu + " 1:00:10 claude\n", nil })
	s.cache[100] = E(func(e *cacheEntry) { e.active, e.lastActiveAt = true, 0 })
	s.prevProcs = map[int]procRow{100: {pid: 100, cpuSec: 0, etime: "00:10"}}
	s.procAt = 0
	a := mustList(t, s, 3600e3)[0] // 100 cpu-s over an hour would read as 0.028 cores
	if a.RecentCpu != 0 || a.IdleSec == nil || *a.IdleSec != 3600 {
		t.Errorf("got %+v", a)
	}
	cpu = "1:40.20"
	b := mustList(t, s, 3602e3)[0] // reseeded: next poll is a normal 2 s delta
	if math.Abs(float64(b.RecentCpu)-0.1) > 1e-9 || *b.IdleSec != 0 {
		t.Errorf("got %+v", b)
	}
}

func TestCollapsedShimActivity(t *testing.T) {
	childCPU := "0:01.00"
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" && args[0] == "-axo" {
			// Child-first order, two shim layers, and a real subagent with different args.
			return "  112 111 1000 " + childCPU + " 00:20 codex --yolo\n" +
				"  111 110 1000 0:00.20 00:20 codex --yolo\n" +
				"  110 1 1000 0:00.10 00:20 node codex.js --yolo\n" +
				"  113 112 1000 0:50.00 00:20 codex review\n", nil
		}
		if cmd == "lsof" {
			return "p110\nn/w/a\np113\nn/w/a\n", nil
		}
		return "", nil
	})
	first := mustList(t, s, 1000)
	if len(first) != 2 || math.Abs(float64(first[0].CpuSec)-1.3) > 1e-9 {
		t.Fatalf("collapsed CPU or distinct subagent: %+v", first)
	}
	if agentByPid(first, 113).Ppid != 110 {
		t.Fatalf("subagent must nest under retained shim: %+v", first)
	}
	childCPU = "0:01.10"
	a := agentByPid(mustList(t, s, 3000), 110)
	if math.Abs(float64(a.RecentCpu)-0.05) > 1e-9 || a.IdleSec == nil || *a.IdleSec != 0 {
		t.Fatalf("native child must make shim working: %+v", a)
	}
	a = agentByPid(mustList(t, s, 5000), 110)
	if a.RecentCpu != 0 || a.IdleSec == nil || *a.IdleSec != 2 {
		t.Fatalf("quiet child must stop activity: %+v", a)
	}
}

func TestDescendantActivityAndChurn(t *testing.T) {
	ps := ""
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return ps, nil
		}
		if cmd == "lsof" {
			return "p100\nn/w/a\np200\nn/w/a\n", nil
		}
		return "", nil
	})
	base := "100 1 1000 0:01 01:00 claude\n"
	child := func(cpu string) string { return "110 100 500 " + cpu + " 00:30 sh build\n" }
	for i, tc := range []struct {
		procs string
		rate  float64
	}{
		{child("0:10"), 0},
		{child("0:10.20"), 0.1}, // quiet agent, busy build
		{child("0:10.20") + "120 100 500 9:00 00:30 helper\n", 0}, // new child is a baseline, not a CPU spike
		{"120 100 500 9:00.20 00:32 helper\n", 0.1},               // old child disappearing must not subtract activity
		{"120 100 500 0:00 00:01 helper\n", 0},                    // reused PID
		{"200 100 500 0:00 00:30 codex\n210 200 500 0:10 00:30 compiler\n", 0},
		{"200 100 500 0:00 00:32 codex\n210 200 500 0:10.20 00:32 compiler\n", 0.1},
		{"", 0},
	} {
		ps = base + tc.procs
		as := mustList(t, s, int64(i+1)*2000)
		a := agentByPid(as, 100)
		if math.Abs(float64(a.RecentCpu)-tc.rate) > 1e-9 {
			t.Fatalf("step %d: got %+v, want rate %v", i, a, tc.rate)
		}
		if tc.rate > 0 && (a.IdleSec == nil || *a.IdleSec != 0) {
			t.Fatalf("child activity must reset parent waiting timer: %+v", a)
		}
		if i == 6 && math.Abs(float64(agentByPid(as, 200).RecentCpu)-0.1) > 1e-9 {
			t.Fatal("nested agent must also reflect its compiler's activity")
		}
	}
}

func TestIsNewFromEtime(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "  100 1 1000 0:00.00 00:03 claude\n", nil })
	s.cache[100] = E(func(e *cacheEntry) { e.etime = "00:01" })
	if !mustList(t, s, 1000)[0].IsNew {
		t.Error("want isNew")
	}
}

func TestUnparsableEtimeNotNewNoEviction(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "  100 1 1000 0:00.00 bogus claude\n", nil })
	s.cache[100] = E(nil)
	a := mustList(t, s, 1000)[0]
	if a.IsNew || s.cache[100] == nil {
		t.Error("NaN etime must be not-new and not evict")
	}
}

func TestPpidPassedThrough(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "  100 42 1000 0:01.00 00:10 claude\n", nil })
	s.cache[100] = E(nil)
	if mustList(t, s, 1000)[0].Ppid != 42 {
		t.Error("ppid")
	}
}

func sp(s string) *string { return &s }

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

func TestClaudeGeneratedTitle(t *testing.T) {
	root, cwd := filepath.Join(t.TempDir(), "projects"), "/work/demo"
	id := "11111111-1111-1111-1111-111111111111"
	metadata := filepath.Join(filepath.Dir(root), "sessions", "100.json")
	transcript := filepath.Join(root, claudeSlug(cwd), id+".jsonl")
	write := func(file, data string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(file), 0700))
		must(t, os.WriteFile(file, []byte(data), 0600))
	}
	meta := func(pid int, session, dir string) string {
		return string(marshal(map[string]any{"pid": pid, "sessionId": session, "cwd": dir}))
	}
	record := func(title, session string) string {
		return `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + session + `"}` + "\n"
	}
	write(metadata, meta(100, id, cwd))
	write(transcript, strings.Repeat("x", 70<<10)+"\n"+record("Old title", id)+record("Generated task", id)+record("Wrong session", "other")+`{"type":"ai-title",`)
	if got := claudeSessionTitle(root, 100, cwd, nowMs(), &sessionTitle{}); got != "Generated task" {
		t.Fatalf("generated title = %q", got)
	}
	paneTitle := "✳ Claude Code"
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "100 10 1000 0:01 01:00 claude\n", nil
		}
		if cmd == "tmux" && args[0] == "list-panes" {
			return "%1\t100\tzsh\tdev\t" + paneTitle + "\n", nil
		}
		return "", errors.New("unavailable")
	})
	s.root = root
	s.cache[100] = &cacheEntry{cwd: cwd, etime: "01:00"}
	if a := mustList(t, s, 1000)[0]; a.Title == nil || *a.Title != "Generated task" {
		t.Fatalf("generic pane must use generated title: %+v", a)
	}
	paneTitle = "✳ Renamed task"
	if a := mustList(t, s, 2000)[0]; a.Title == nil || *a.Title != "Renamed task" {
		t.Fatalf("rename must take precedence: %+v", a)
	}
	for _, data := range []string{meta(200, id, cwd), meta(100, id, "/other"), meta(100, "../invalid", cwd), "{"} {
		write(metadata, data)
		if got := claudeSessionTitle(root, 100, cwd, nowMs(), &sessionTitle{}); got != "" {
			t.Fatalf("invalid session metadata returned %q", got)
		}
	}
}

func TestClaudeTitleCachedByFileKey(t *testing.T) {
	root, cwd, now := filepath.Join(t.TempDir(), "projects"), "/work/demo", nowMs()
	id, id2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	metadata := filepath.Join(filepath.Dir(root), "sessions", "100.json")
	log := func(session string) string { return filepath.Join(root, claudeSlug(cwd), session+".jsonl") }
	write := func(file, data string, ageMs int64) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(file), 0700))
		must(t, os.WriteFile(file, []byte(data), 0600))
		tm := time.UnixMilli(now - ageMs)
		must(t, os.Chtimes(file, tm, tm))
	}
	meta := func(session string) string {
		return string(marshal(map[string]any{"pid": 100, "sessionId": session, "cwd": cwd}))
	}
	record := func(title, session string) string {
		return `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + session + `"}` + "\n"
	}
	c := &sessionTitle{}
	title := func() string { return claudeSessionTitle(root, 100, cwd, now, c) }
	write(metadata, meta(id), 10e3)
	write(log(id), record("Title A", id), 10e3)
	if got := title(); got != "Title A" {
		t.Fatalf("initial = %q", got)
	}
	write(log(id), record("Title Z", id), 10e3) // same size and mtime: must not be re-read
	if got := title(); got != "Title A" {
		t.Errorf("unchanged key re-read: %q", got)
	}
	write(log(id), record("Title A", id)+record("Title B", id), 10e3) // appended
	if got := title(); got != "Title B" {
		t.Errorf("after append = %q", got)
	}
	write(log(id), record("Title C", id)+record("Title B", id), 0) // same size, mtime in this tick
	if got := title(); got != "Title B" {
		t.Errorf("after same-tick write = %q", got)
	}
	write(log(id), record("Title B", id)+record("Title C", id), 0) // same size and mtime again
	if got := title(); got != "Title C" {
		t.Errorf("unsettled file must be re-read: %q", got)
	}
	write(log(id2), record("Title D", id2), 10e3)
	write(metadata, meta(id2), 5e3) // same size, new session
	if got := title(); got != "Title D" {
		t.Errorf("after session switch = %q", got)
	}
	write(log(id2), record("Title D", id2), 20e3) // new stable mtime forces a read
	must(t, os.Chmod(log(id2), 0))                // unreadable (I/O error) must not be cached
	if got := title(); got != "" && os.Geteuid() != 0 {
		t.Errorf("unreadable transcript = %q", got)
	}
	must(t, os.Chmod(log(id2), 0600))
	if got := title(); got != "Title D" {
		t.Errorf("after transcript readable again = %q", got)
	}
	must(t, os.Remove(metadata))
	if got := title(); got != "" || *c != (sessionTitle{}) {
		t.Errorf("after metadata removal = %q, cache %+v", got, *c)
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

// subagent fixtures
func mk(t *testing.T, root, cwd, session, id string, meta any, ageSec int64, now int64) {
	t.Helper()
	dir := filepath.Join(root, claudeSlug(cwd))
	sub := filepath.Join(dir, session, "subagents")
	must(t, os.MkdirAll(sub, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, session+".jsonl"), nil, 0o644))
	var mb []byte
	if s, ok := meta.(string); ok {
		mb = []byte(s)
	} else {
		mb, _ = json.Marshal(meta)
	}
	must(t, os.WriteFile(filepath.Join(sub, "agent-"+id+".meta.json"), mb, 0o644))
	j := filepath.Join(sub, "agent-"+id+".jsonl")
	must(t, os.WriteFile(j, nil, 0o644))
	tm := time.UnixMilli(now - ageSec*1000)
	must(t, os.Chtimes(j, tm, tm))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }

func claude(pid int, cwd string) Agent { return Agent{Pid: pid, Kind: "claude", Cwd: &cwd} }

func subs(root string, now int64, as ...Agent) []Subagent {
	return listClaudeSubagents(as, root, now, map[string]metaEntry{}, map[string]sessionScan{})
}

func TestClaudeSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/y/.claude/x_y": "-Users-y--claude-x-y",
		"/w/日本語":               "-w----",
		"/w/😀":                 "-w---", // astral rune = 2 UTF-16 units
	} {
		if got := claudeSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRunningSubagentAttachedToSoleClaude(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "Review", "model": "sonnet"}, 3, now)
	got := subs(root, now, claude(100, "/w/a"))
	pid := 100
	want := []Subagent{{"a1", &pid, nil, "/w/a", "Review", "sonnet", true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFinishedSubagentExcluded(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m", "finished": true}, 3, now)
	if got := subs(root, now, claude(100, "/w/a")); len(got) != 0 {
		t.Error(got)
	}
}

func TestSubagentAgeWindow(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a9", map[string]any{"description": "R", "model": "m"}, 9*60, now)
	mk(t, root, "/w/a", "s1", "a31", map[string]any{"description": "R", "model": "m"}, 31*60, now)
	got := subs(root, now, claude(100, "/w/a"))
	if len(got) != 1 || got[0].Id != "a9" || got[0].Working {
		t.Errorf("got %+v", got)
	}
}

func TestTwoClaudeInCwdParentPidNull(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m"}, 3, now)
	if got := subs(root, now, claude(100, "/w/a"), claude(101, "/w/a")); got[0].ParentPid != nil {
		t.Error("parentPid should be nil")
	}
}

func TestSubagentEdgeCases(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	for _, meta := range []string{"{", "null", "[]", "true", "42", `"text"`, `{"description":"partial","model":}`} {
		mk(t, root, "/w/a", "bad", "bad", meta, 3, now)
		if got := subs(root, now, claude(100, "/w/a")); len(got) != 0 {
			t.Errorf("invalid metadata %q: %+v", meta, got)
		}
	}
	if got := subs(root, now, Agent{Pid: 5, Kind: "codex", Cwd: sp("/w/a")}, claude(6, "/nope")); got == nil || len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}

func TestSubagentMissingFieldsAreNull(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	b, _ := json.Marshal(subs(root, now, claude(100, "/w/a")))
	if !strings.Contains(string(b), `"description":null,"model":null`) {
		t.Errorf("got %s", b)
	}
}

func TestSubagentsExposeParentAgentId(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "p1", map[string]any{"description": "Parent", "model": "m", "spawnDepth": 1}, 3, now)
	mk(t, root, "/w/a", "s1", "c1", map[string]any{"description": "Child", "model": "m", "spawnDepth": 2, "parentAgentId": "p1"}, 3, now)
	got := subs(root, now, claude(100, "/w/a")) // ReadDir order is sorted: c1, p1
	if len(got) != 2 || got[0].Id != "c1" || got[0].ParentAgentId != "p1" || got[1].Id != "p1" || got[1].ParentAgentId != nil {
		t.Errorf("got %+v", got)
	}
}

func TestMetaCacheKeyedByMtimeAndPruned(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m"}, 3, now)
	cache := map[string]metaEntry{}
	listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now, cache, map[string]sessionScan{})
	if len(cache) != 1 {
		t.Fatal("not cached")
	}
	listClaudeSubagents(nil, root, now, cache, map[string]sessionScan{})
	if len(cache) != 0 {
		t.Error("not pruned")
	}
}

func TestSessionScanCached(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessions := map[string]sessionScan{}
	list := func(at int64) int {
		return len(listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, at, map[string]metaEntry{}, sessions))
	}
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	mk(t, root, "/w/a", "old", "a2", map[string]any{}, 3, now)
	dir := filepath.Join(root, claudeSlug("/w/a"))
	old := time.UnixMilli(now - 7*3600e3)
	must(t, os.Chtimes(filepath.Join(dir, "old.jsonl"), old, old))
	settle := func(ago int64) { tm := time.UnixMilli(now - ago); must(t, os.Chtimes(dir, tm, tm)) }
	settle(10e3)
	if got := list(now); got != 1 {
		t.Fatalf("initial = %d", got)
	}
	mk(t, root, "/w/a", "s2", "a3", map[string]any{}, 3, now)
	settle(5e3) // a new file changes the dir mtime
	if got := list(now + 1); got != 2 {
		t.Errorf("after new session = %d", got)
	}
	tm := time.UnixMilli(now)
	must(t, os.Chtimes(filepath.Join(dir, "old.jsonl"), tm, tm)) // resumed: no dir change
	if got := list(now + 2); got != 2 {
		t.Errorf("before rescan = %d", got)
	}
	if got := list(now + 1 + sessionRescanMs); got != 3 {
		t.Errorf("after rescan = %d", got)
	}
}

func TestSessionScanRepeatedWhileDirUnsettled(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessions := map[string]sessionScan{}
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	dir := filepath.Join(root, claudeSlug("/w/a"))
	tm := time.UnixMilli(now)
	must(t, os.Chtimes(dir, tm, tm)) // dir changed in the same tick as the scan
	listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now, map[string]metaEntry{}, sessions)
	mk(t, root, "/w/a", "s2", "a2", map[string]any{}, 3, now)
	must(t, os.Chtimes(dir, tm, tm)) // second create lands in that same tick
	if got := listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now+1, map[string]metaEntry{}, sessions); len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
}

func TestParseGit(t *testing.T) {
	for _, tc := range []struct {
		text, cwd string
		want      gitInfo
	}{
		{"/r/sample-app/.git\n/o/ws/sample-app/feat-x\n", "/o/ws/sample-app/feat-x/sub", gitInfo{"/r/sample-app", "sample-app", "/o/ws/sample-app/feat-x"}},
		{"/r/app/.git\n/r/app\n", "/r/app", gitInfo{"/r/app", "app", "/r/app"}},
		{"", "/x/y", gitInfo{"/x/y", "y", "/x/y"}},
		{"  100 1 0:01.00 00:10 claude\n", "/x/y", gitInfo{"/x/y", "y", "/x/y"}},
	} {
		if got := parseGit(tc.text, tc.cwd); got != tc.want {
			t.Errorf("%q: got %+v", tc.text, got)
		}
	}
}

func TestListAgentsAddsRepoOneGitPerNewPid(t *testing.T) {
	var git int
	s := srvWith(func(cmd string, args ...string) (string, error) {
		switch {
		case cmd == "ps" && args[0] == "-axo":
			return "  100 1 1000 0:01.00 00:10 claude\n", nil
		case cmd == "lsof":
			return "p100\nn/o/ws/app/wt1\n", nil
		case cmd == "git":
			git++
			return "/r/app/.git\n/o/ws/app/wt1\n", nil
		}
		return "", nil
	})
	a := mustList(t, s, 1000)[0]
	mustList(t, s, 3000)
	if *a.Repo != "/r/app" || a.RepoName != "app" || *a.Worktree != "/o/ws/app/wt1" || git != 1 {
		t.Errorf("got %+v git=%d", a, git)
	}
}

func TestListAgentsSurvivesGitFailure(t *testing.T) {
	s := srvWith(func(cmd string, args ...string) (string, error) {
		switch cmd {
		case "git":
			return "ignored", errors.New("not a repo")
		case "lsof":
			return "p100\nn/x/y\n", nil
		}
		return "  100 1 1000 0:01.00 00:10 claude\n", nil
	})
	a := mustList(t, s, 1000)[0]
	if *a.Repo != "/x/y" || a.RepoName != "y" || *a.Worktree != "/x/y" {
		t.Errorf("got %+v", a)
	}
}

func TestPayloadNulls(t *testing.T) {
	b, err := json.Marshal(toPayload(snap{
		agents:    []Agent{{Pid: 1, Kind: "claude", CpuSec: num(math.NaN()), pane: "secret:pane"}},
		subagents: []Subagent{{Id: "a", Cwd: "/x"}},
		at:        5,
	}))
	must(t, err)
	s := string(b)
	for _, want := range []string{`"cwd":null`, `"idleSec":null`, `"parentPid":null`, `"parentAgentId":null`, `"cpuSec":null`, `"repo":null`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "pane") {
		t.Error("pane leaked")
	}
	e, _ := json.Marshal(toPayload(snap{}))
	if string(e) != `{"agents":[],"subagents":[],"at":0,"system":{"freePct":null,"pressure":null,"swapUsedBytes":null}}` {
		t.Errorf("got %s", e)
	}
}

func TestDefaultRunnerReturnsStdoutOnError(t *testing.T) {
	out, err := execRun("sh", "-c", "echo hi; exit 1")
	if err == nil || out != "hi\n" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestSnapshotMemoized(t *testing.T) {
	var n int
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" && args[0] == "-axo" {
			n++
		}
		return "", nil
	})
	for _, at := range []int64{1000, 1500, 1999} {
		s.snapshot(at)
	}
	s.snapshot(2000)
	if n != 2 {
		t.Errorf("ps spawns = %d", n)
	}
}

func do(s *server, method, target, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestHandlers(t *testing.T) {
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n", nil
		}
		return "", nil
	})
	const h = "127.0.0.1:4777"
	js := map[string]string{"Content-Type": "application/json"}
	with := func(k, v string) map[string]string {
		return map[string]string{"Content-Type": "application/json", k: v}
	}
	for _, tc := range []struct {
		name, method, target, host string
		hdr                        map[string]string
		body                       string
		code                       int
		bodyHas                    string
	}{
		{"bad host", "GET", "/", "evil.com", nil, "", 403, ""},
		{"index", "GET", "/", h, nil, "", 200, "<!doctype html>"},
		{"logo", "GET", "/logo.svg", h, nil, "", 200, "<svg"},
		{"localhost host", "GET", "/", "localhost:4777", nil, "", 200, ""},
		{"agents", "GET", "/agents", h, nil, "", 200, `"agents":[`},
		{"agents with query", "GET", "/agents?x=1", h, nil, "", 404, ""},
		{"unknown route", "GET", "/nope", h, nil, "", 404, ""},
		{"focus bad origin", "POST", "/focus", h, with("Origin", "http://evil.com"), `{"pid":100}`, 403, ""},
		{"focus null origin", "POST", "/focus", h, with("Origin", "null"), `{"pid":100}`, 403, ""},
		{"focus cross-site", "POST", "/focus", h, with("Sec-Fetch-Site", "cross-site"), `{"pid":100}`, 403, ""},
		{"focus wrong content type", "POST", "/focus", h, map[string]string{"Content-Type": "text/plain"}, `{}`, 415, ""},
		{"focus no content type", "POST", "/focus", h, nil, `{}`, 415, ""},
		{"focus too large", "POST", "/focus", h, js, strings.Repeat("a", 1025), 413, "too large"},
		{"focus bad json", "POST", "/focus", h, js, `{`, 400, "bad json"},
		{"focus null", "POST", "/focus", h, js, `null`, 400, "bad json"},
		{"focus unknown pid", "POST", "/focus", h, js, `{"pid":7}`, 404, "unknown pid"},
		{"focus string pid", "POST", "/focus", h, js, `{"pid":"100"}`, 404, "unknown pid"},
		{"focus array", "POST", "/focus", h, js, `[]`, 404, "unknown pid"},
		{"focus number", "POST", "/focus", h, js, `5`, 404, "unknown pid"},
	} {
		w := do(s, tc.method, tc.target, tc.host, tc.hdr, tc.body)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.bodyHas) {
			t.Errorf("%s: %d %q", tc.name, w.Code, w.Body.String())
		}
		if (tc.code == 403 || tc.code == 415 || tc.code == 404 && tc.bodyHas == "") && w.Body.Len() != 0 {
			t.Errorf("%s: body should be empty", tc.name)
		}
	}
	w := do(s, "GET", "/agents", h, nil, "")
	if w.Header().Get("content-type") != "application/json" || w.Header().Get("cache-control") != "no-store" {
		t.Error("agents headers")
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	w := do(s, "GET", "/", "127.0.0.1:4777", nil, "")
	for k, v := range map[string]string{
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestAgentsPsFailure500(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", errors.New("boom") })
	w := do(s, "GET", "/agents", "127.0.0.1:4777", nil, "")
	if w.Code != 500 || w.Header().Get("content-type") != "application/json" || !strings.Contains(w.Body.String(), `{"error":`) || s.snap != nil {
		t.Errorf("%d %q", w.Code, w.Body.String())
	}
}

func TestConcurrentSnapshotsOnePs(t *testing.T) {
	var n atomic.Int32
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" && args[0] == "-axo" {
			n.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
		return "", nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.snapshot(5000) }()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Errorf("ps spawns = %d", n.Load())
	}
}

func TestParsePsProcs(t *testing.T) {
	_, procs := parsePs(PS, 0)
	if len(procs) != 11 || procs[8] != (procRow{400, 1, 1000, 540, "00:01"}) {
		t.Errorf("got %d procs, %+v", len(procs), procs[8])
	}
}

func rows(r ...[3]int) []procRow {
	var o []procRow
	for _, x := range r {
		o = append(o, procRow{pid: x[0], ppid: x[1], rssKB: int64(x[2])})
	}
	return o
}

func TestTreeRSS(t *testing.T) {
	// 1 -> 10(agent) -> 11 -> 12 ; 10 -> 13 ; 13 -> 20(agent) -> 21 ; 99 unrelated
	tr := buildTree(rows([3]int{1, 0, 5}, [3]int{10, 1, 100}, [3]int{11, 10, 10}, [3]int{12, 11, 1}, [3]int{13, 10, 7}, [3]int{20, 13, 1000}, [3]int{21, 20, 2000}, [3]int{99, 1, 9}))
	stop := map[int]bool{10: true, 20: true}
	if got := tr.treeRSS(10, stop); got != (100+10+1+7)*1024 {
		t.Errorf("agent descendant subtree must be excluded: %d", got)
	}
	if got := tr.treeRSS(20, stop); got != 3000*1024 {
		t.Errorf("got %d", got)
	}
	// shim child (not listed, so not in stop) counts toward its parent
	if got := tr.treeRSS(13, map[int]bool{}); got != (7+1000+2000)*1024 {
		t.Errorf("got %d", got)
	}
	cyc := buildTree(rows([3]int{1, 2, 1}, [3]int{2, 1, 2}, [3]int{3, 3, 4}))
	if got := cyc.treeRSS(1, nil); got != 3*1024 {
		t.Errorf("cycle: %d", got)
	}
	if got := cyc.treeRSS(3, nil); got != 4*1024 {
		t.Errorf("self parent: %d", got)
	}
}

func TestParseSwap(t *testing.T) {
	for in, want := range map[string]int64{
		"total = 5120.00M  used = 3749.75M  free = 1370.25M  (encrypted)": int64(3749.75 * 1024 * 1024),
		"total = 6.00G  used = 2.00G  free = 4.00G":                       2 << 30,
		"used = 512.00K free = 1M":                                        512 * 1024,
	} {
		if got, ok := parseSwap(in); !ok || got != want {
			t.Errorf("%q: %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "garbage", "used = xM", "used = 5X"} {
		if _, ok := parseSwap(bad); ok {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestMemBytesAndSystem(t *testing.T) {
	var swaps int
	s := srvWith(func(cmd string, args ...string) (string, error) {
		switch {
		case cmd == "sysctl":
			swaps++
			return "total = 5120.00M  used = 3749.75M  free = 1370.25M  (encrypted)", nil
		case cmd == "ps" && args[0] == "-axo":
			return "  100 1 500 0:01.00 00:10 claude\n  101 100 300 0:01.00 00:10 node mcp-thing\n  102 101 200 0:00.00 00:10 helper\n  201 100 9000 0:01.00 00:10 codex\n", nil
		}
		return "", nil
	})
	s.sysU32 = func(name string) (uint32, bool) {
		if name == "kern.memorystatus_level" {
			return 73, true
		}
		return 2, true
	}
	sn, err := s.snapshot(1000)
	must(t, err)
	if got := agentByPid(sn.agents, 100).MemBytes; got != (500+300+200)*1024 {
		t.Errorf("claude memBytes=%d", got)
	}
	if got := agentByPid(sn.agents, 201).MemBytes; got != 9000*1024 {
		t.Errorf("codex memBytes=%d", got)
	}
	b, _ := json.Marshal(toPayload(sn))
	if !strings.Contains(string(b), `"memBytes":1024000`) || !strings.Contains(string(b), `"system":{"freePct":73,"pressure":2,"swapUsedBytes":3931897856}`) {
		t.Errorf("got %s", b)
	}
	s.snapshot(5000)
	if swaps != 1 {
		t.Errorf("swap exec within 10s: %d", swaps)
	}
	s.snapshot(11000)
	if swaps != 2 {
		t.Errorf("swap exec after 10s: %d", swaps)
	}
}
