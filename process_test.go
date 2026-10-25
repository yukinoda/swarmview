package main

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
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

func TestCursorCLIAndOpenCodeServices(t *testing.T) {
	const script = "/home/dev/.local/share/cursor-agent/versions/2026.10.22-example/index.js"
	for _, tc := range []struct {
		command string
		kind    string
	}{
		{"/home/dev/.local/bin/agent --use-system-ca " + script, "cursor-agent"},
		{"/home/dev/.local/bin/cursor-agent --use-system-ca " + script, "cursor-agent"},
		{"cursor-agent", "cursor-agent"},
		{"cursor-agent --resume example", "cursor-agent"},
		{"agent --use-system-ca /other/index.js", ""},
		{"agent", ""},
		{"agent --use-system-ca " + script + " worker start", ""},
		{"cursor-agent worker start", ""},
		{"cursor-agent --use-system-ca " + script + " worker-server", ""},
		{"node " + script + " worker-server", ""},
		{"opencode serve --service", ""},
		{"opencode serve", ""},
		{"opencode -s example", "opencode"},
		{"claude -p serve", "claude"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			agents, procs := parsePs("100 1 1000 0:01.00 00:10 "+tc.command, 0)
			if len(procs) != 1 {
				t.Fatal("filtered services must remain in process resource accounting")
			}
			if tc.kind == "" {
				if len(agents) != 0 {
					t.Fatalf("unexpected agent: %+v", agents)
				}
			} else if len(agents) != 1 || agents[0].Kind != tc.kind {
				t.Fatalf("want %s, got %+v", tc.kind, agents)
			}
		})
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

func fakeRun(lsof func() (string, error)) Runner {
	return func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n  201 1 1000 0:00.00 00:10 codex\n", nil
		}
		return lsof()
	}
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

func TestDefaultRunnerReturnsStdoutOnError(t *testing.T) {
	out, err := execRun("sh", "-c", "echo hi; exit 1")
	if err == nil || out != "hi\n" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestParsePsProcs(t *testing.T) {
	_, procs := parsePs(PS, 0)
	if len(procs) != 11 || procs[8] != (procRow{400, 1, 1000, 540, "00:01"}) {
		t.Errorf("got %d procs, %+v", len(procs), procs[8])
	}
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
