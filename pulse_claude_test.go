package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaudeHookRecord(t *testing.T) {
	cases := []struct {
		payload string
		want    claudePulse
		ok      bool
	}{
		{`{"hook_event_name":"SubagentStart","agent_id":"a0123456789abcdef","agent_type":"general-purpose"}`, claudePulse{"sent", "a0123456789abcdef"}, true},
		{`{"hook_event_name":"SubagentStop","agent_id":"a1234567","last_assistant_message":"private result"}`, claudePulse{"returned", "a1234567"}, true},
		{`{"hook_event_name":"PreToolUse","agent_id":"a1234567","tool_name":"SendMessage","tool_input":{"to":"a0123456789abcdef","message":"private"}}`, claudePulse{"message", "a0123456789abcdef"}, true},
		// Teammate and session names are not mapped to rows yet.
		{`{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_input":{"to":"researcher"}}`, claudePulse{}, false},
		{`{"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"prompt":"private"}}`, claudePulse{}, false},
		{`{"hook_event_name":"SubagentStart","agent_id":"../secret"}`, claudePulse{}, false},
		{`{"hook_event_name":"Stop"}`, claudePulse{}, false},
		{`not json`, claudePulse{}, false},
	}
	for _, c := range cases {
		got, ok := claudeHookRecord(strings.NewReader(c.payload))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: got %+v %v", c.payload, got, ok)
		}
	}
	for _, bad := range []string{
		`{"type":"sent","agent":"a1234567","prompt":"x"}`,
		`{"type":"attempted","agent":"a1234567"}`,
		`{"type":"sent","agent":"ses_root"}`,
		`{"type":"sent","agent":"a1234567"}{}`,
	} {
		if _, err := decodeClaudePulse([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestClaudeHookBridge(t *testing.T) {
	pulseHome(t)
	host := 4242
	self := strconv.Itoa(os.Getpid())
	// The hook (this test process) descends from the detected Claude through three shells:
	// four hops is the deepest accepted chain.
	fourHops := self + " 4300\n4300 4301\n4301 4302\n4302 4242\n"
	fiveHops := self + " 4299\n4299 4300\n4300 4301\n4301 4302\n4302 4242\n"
	var mu sync.Mutex
	ps := fourHops
	setPs := func(table string) {
		mu.Lock()
		ps = table
		mu.Unlock()
	}
	s := &server{run: func(cmd string, args ...string) (string, error) {
		if cmd != "ps" {
			return "", errors.New("unexpected command")
		}
		mu.Lock()
		defer mu.Unlock()
		return ps, nil
	}}
	setKind := func(kind string) {
		s.mu.Lock()
		// Dated an hour ahead so a slow run never refreshes it through the fake ps.
		at := time.Now().Add(time.Hour).UnixMilli()
		s.snap = &snap{at: at, agents: []Agent{{Pid: host, Kind: kind}}}
		s.mu.Unlock()
	}
	b := newPulseBridge()
	must(t, b.start(s))
	defer b.close()
	exe, err := os.Executable()
	must(t, err)
	published, err := os.ReadFile(filepath.Join(filepath.Dir(b.path), "live.exe"))
	if err != nil || string(published) != exe+"\n" {
		t.Fatalf("bridge did not publish its binary for the plugin launcher: %q %v", published, err)
	}
	ch, frame := pulseViewer(t, b)
	hook := func(payload string) { runClaudeHook(strings.NewReader(payload)) }

	setKind("claude")
	hook(`{"hook_event_name":"SubagentStart","agent_id":"a0123456789abcdef"}`)
	f := frame("subagent start")
	if f.Key != "sub:a0123456789abcdef" || f.Direction != "sent" || f.Pid != host || f.Workers == nil {
		t.Fatalf("start cue: %+v", f)
	}
	hook(`{"hook_event_name":"SubagentStop","agent_id":"a0123456789abcdef"}`)
	if f = frame("subagent stop"); f.Direction != "returned" || f.Pid != host {
		t.Fatalf("stop cue: %+v", f)
	}

	// A hook outside any detected Claude, too deep below one, or an unmapped event never pulses.
	setPs(fiveHops)
	hook(`{"hook_event_name":"SubagentStart","agent_id":"a0123456789abcdef"}`)
	setPs(fourHops)
	setKind("codex")
	hook(`{"hook_event_name":"SubagentStart","agent_id":"a0123456789abcdef"}`)
	setKind("claude")
	hook(`{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_input":{"to":"researcher"}}`)
	hook(`{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_input":{"to":"a1234567"}}`)
	if f = frame("follow-up message"); f.Key != "sub:a1234567" || f.Direction != "message" {
		t.Fatalf("only the mapped follow-up should arrive, got %+v", f)
	}
	if len(ch) != 0 {
		t.Fatal("unexpected extra cue")
	}

	// Parallel subagent starts all reach a viewer that has not read yet.
	var wg sync.WaitGroup
	for _, id := range []string{"a1111111", "a2222222", "a3333333"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hook(`{"hook_event_name":"SubagentStart","agent_id":"` + id + `"}`)
		}()
	}
	wg.Wait()
	if len(ch) != 3 {
		t.Fatalf("parallel starts queued %d of 3 cues", len(ch))
	}
}

// The plugin registers async hooks for events claudeHookRecord maps, through the launcher.
func TestClaudePluginHooks(t *testing.T) {
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile("integrations/claude/hooks/hooks.json")
	must(t, err)
	must(t, json.Unmarshal(b, &cfg))
	// One payload per registered event; each must map to a cue.
	samples := map[string]string{
		"SubagentStart": `{"hook_event_name":"SubagentStart","agent_id":"a1234567"}`,
		"SubagentStop":  `{"hook_event_name":"SubagentStop","agent_id":"a1234567"}`,
		"PreToolUse":    `{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_input":{"to":"a1234567"}}`,
	}
	if len(cfg.Hooks) != len(samples) {
		t.Fatalf("plugin registers %d events, want %d", len(cfg.Hooks), len(samples))
	}
	for event, payload := range samples {
		if _, ok := claudeHookRecord(strings.NewReader(payload)); !ok {
			t.Fatalf("%s is registered but claudeHookRecord does not map it", event)
		}
		groups := cfg.Hooks[event]
		if len(groups) != 1 {
			t.Fatalf("%s: want one hook group, got %d", event, len(groups))
		}
		if event == "PreToolUse" && groups[0].Matcher != "SendMessage" {
			t.Fatalf("PreToolUse matcher %q, want SendMessage", groups[0].Matcher)
		}
		if len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: want one hook, got %d", event, len(groups[0].Hooks))
		}
		hook := groups[0].Hooks[0]
		if hook.Type != "command" {
			t.Fatalf("%s: hook type %q", event, hook.Type)
		}
		if !hook.Async {
			t.Fatalf("%s: hook must be async so Claude never waits", event)
		}
		if hook.Command != `"${CLAUDE_PLUGIN_ROOT}/hooks/forward.sh"` {
			t.Fatalf("%s: hook command %q", event, hook.Command)
		}
	}
}

// launcherStub writes an executable that records its stdin to marker. An old stub
// instead rejects -claude-hook before reading stdin, like a pre-hook swarmview.
func launcherStub(t *testing.T, path, marker string, old bool) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\ncat > %q\necho noise >&2\n", marker)
	if old {
		script = fmt.Sprintf("#!/bin/sh\n: > %q\necho usage >&2\nexit 2\n", marker)
	}
	must(t, os.WriteFile(path, []byte(script), 0755))
}

func TestClaudePluginLauncher(t *testing.T) {
	payload := `{"hook_event_name":"SubagentStart","agent_id":"a1234567"}`
	run := func(home, path string) string {
		t.Helper()
		cmd := exec.Command("integrations/claude/hooks/forward.sh")
		cmd.Env = append(os.Environ(), "PATH="+path+":/usr/bin:/bin", "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"))
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("launcher must exit 0: %v", err)
		}
		return string(out)
	}
	home := t.TempDir()
	cache := filepath.Join(home, ".cache")
	if runtime.GOOS == "darwin" {
		cache = filepath.Join(home, "Library", "Caches")
	}
	must(t, os.MkdirAll(filepath.Join(cache, "swarmview"), 0700))
	bin := filepath.Join(home, "bin")
	must(t, os.Mkdir(bin, 0700))

	// The binary Swarmview recorded beside its socket wins over PATH.
	running := filepath.Join(home, "running")
	launcherStub(t, running, filepath.Join(home, "running.got"), false)
	launcherStub(t, filepath.Join(bin, "swarmview"), filepath.Join(home, "path.got"), false)
	must(t, os.WriteFile(filepath.Join(cache, "swarmview", "live.exe"), []byte(running+"\n"), 0600))
	if out := run(home, bin); out != "" {
		t.Fatalf("launcher must be silent, got %q", out)
	}
	got, err := os.ReadFile(filepath.Join(home, "running.got"))
	if err != nil || string(got) != payload {
		t.Fatalf("recorded binary did not receive the event: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "path.got")); err == nil {
		t.Fatal("PATH binary ran although the recorded one succeeded")
	}

	// A recorded binary too old for -claude-hook fails; the PATH one still gets the event.
	launcherStub(t, running, filepath.Join(home, "running.got"), true)
	if out := run(home, bin); out != "" {
		t.Fatalf("launcher must be silent, got %q", out)
	}
	got, err = os.ReadFile(filepath.Join(home, "path.got"))
	if err != nil || string(got) != payload {
		t.Fatalf("PATH binary did not get the event after the old one failed: %q %v", got, err)
	}
}
