package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	// The hook (this test process) descends from the detected Claude through four
	// processes: five hops is the deepest accepted chain.
	fiveHops := self + " 4299\n4299 4300\n4300 4301\n4301 4302\n4302 4242\n"
	sixHops := self + " 4298\n4298 4299\n4299 4300\n4300 4301\n4301 4302\n4302 4242\n"
	var mu sync.Mutex
	ps := fiveHops
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
	own, err := os.Stat(exe)
	must(t, err)
	copied, err := os.Stat(liveHookPath(filepath.Dir(b.path)))
	if err != nil {
		t.Fatalf("bridge did not copy its binary for the plugin launcher: %v", err)
	}
	if copied.Size() != own.Size() {
		t.Fatalf("live-hook is %d bytes, want a copy of this %d-byte binary", copied.Size(), own.Size())
	}
	if copied.Mode().Perm() != 0700 {
		t.Fatalf("live-hook mode %v, want 0700", copied.Mode().Perm())
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
	setPs(sixHops)
	hook(`{"hook_event_name":"SubagentStart","agent_id":"a0123456789abcdef"}`)
	setPs(fiveHops)
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

	// A stopped bridge leaves no path for the launcher to prefer.
	b.close()
	if _, err := os.Stat(liveHookPath(filepath.Dir(b.path))); !os.IsNotExist(err) {
		t.Fatalf("live-hook outlived the bridge: %v", err)
	}
}

func TestClaudeHookBinaryFile(t *testing.T) {
	_, socket := pulseHome(t)
	dir := filepath.Dir(socket)

	// Publishing replaces planted symlinks instead of writing through them.
	victim := filepath.Join(t.TempDir(), "victim")
	must(t, os.WriteFile(victim, []byte("keep\n"), 0600))
	must(t, os.Symlink(victim, liveHookPath(dir)))
	must(t, os.Symlink(victim, liveHookPath(dir)+".tmp"))
	publishHookBinary(dir)
	kept, err := os.ReadFile(victim)
	must(t, err)
	if string(kept) != "keep\n" {
		t.Fatalf("publishing wrote through a symlink: %q", kept)
	}
	fi, err := os.Lstat(liveHookPath(dir))
	must(t, err)
	if !fi.Mode().IsRegular() {
		t.Fatalf("live-hook is %v, want a regular file", fi.Mode())
	}

	// A bridge whose socket was replaced leaves the replacement's live-hook alone.
	s := &server{snap: pulseSnap("claude")}
	b := newPulseBridge()
	must(t, b.start(s))
	defer b.close()
	must(t, os.Remove(socket))
	replacement, err := net.Listen("unix", socket)
	must(t, err)
	defer replacement.Close()
	other := []byte("replacement bridge\n")
	must(t, os.WriteFile(liveHookPath(dir), other, 0700))
	b.close()
	kept, err = os.ReadFile(liveHookPath(dir))
	if err != nil {
		t.Fatalf("shutdown removed the replacement bridge's live-hook: %v", err)
	}
	if string(kept) != string(other) {
		t.Fatalf("live-hook changed to %q", kept)
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
		// Run through sh, so a lost exec bit on install can't break the hook.
		if hook.Command != `sh "${CLAUDE_PLUGIN_ROOT}/hooks/forward.sh"` {
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
	// pulseHome sets HOME and XDG_CACHE_HOME, which the launcher inherits.
	home, socket := pulseHome(t)
	dir := filepath.Dir(socket)
	// run invokes the launcher, which must always exit 0 without output.
	run := func(path string) {
		t.Helper()
		cmd := exec.Command("sh", "integrations/claude/hooks/forward.sh")
		cmd.Env = append(os.Environ(), "PATH="+path+":/usr/bin:/bin")
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("launcher must exit 0: %v", err)
		}
		if len(out) != 0 {
			t.Fatalf("launcher must be silent, got %q", out)
		}
	}
	received := func(marker string) string {
		t.Helper()
		got, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("%s did not run: %v", filepath.Base(marker), err)
		}
		return string(got)
	}
	ran := func(marker string) bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	bin := filepath.Join(home, "bin")
	must(t, os.Mkdir(bin, 0700))
	hookGot := filepath.Join(home, "hook.got")
	pathGot := filepath.Join(home, "path.got")
	// reset clears both markers and installs fresh stubs for the next step.
	reset := func(oldHook bool) {
		t.Helper()
		os.Remove(hookGot)
		os.Remove(pathGot)
		os.Remove(liveHookPath(dir))
		launcherStub(t, liveHookPath(dir), hookGot, oldHook)
		launcherStub(t, filepath.Join(bin, "swarmview"), pathGot, false)
	}
	ln, err := net.Listen("unix", socket)
	must(t, err)
	t.Cleanup(func() { ln.Close() })

	// The private copy of the listening build wins over PATH.
	reset(false)
	run(bin)
	if got := received(hookGot); got != payload {
		t.Fatalf("live-hook got %q, want the event", got)
	}
	if ran(pathGot) {
		t.Fatal("PATH binary ran although live-hook succeeded")
	}

	// A copy too old for -claude-hook fails before reading; PATH gets the event.
	reset(true)
	run(bin)
	if got := received(hookGot); got != "" {
		t.Fatalf("an old binary must fail before reading the event, got %q", got)
	}
	if got := received(pathGot); got != payload {
		t.Fatalf("PATH binary got %q after the old one failed, want the event", got)
	}

	// A symlinked live-hook is not trusted.
	reset(false)
	target := filepath.Join(home, "target")
	must(t, os.Rename(liveHookPath(dir), target))
	must(t, os.Symlink(target, liveHookPath(dir)))
	run(bin)
	if ran(hookGot) {
		t.Fatal("launcher ran a symlinked live-hook")
	}
	if got := received(pathGot); got != payload {
		t.Fatalf("PATH binary got %q, want the event", got)
	}

	// A directory others can enter or write is not private, so live-hook is ignored.
	reset(false)
	must(t, os.Chmod(dir, 0770))
	run(bin)
	must(t, os.Chmod(dir, 0700))
	if ran(hookGot) {
		t.Fatal("launcher ran live-hook from a group-accessible directory")
	}
	if got := received(pathGot); got != payload {
		t.Fatalf("PATH binary got %q, want the event", got)
	}

	// Without a live socket no Swarmview is listening, so no binary starts.
	reset(false)
	must(t, ln.Close())
	os.Remove(socket)
	run(bin)
	if ran(hookGot) {
		t.Fatal("live-hook ran with no Swarmview listening")
	}
	if ran(pathGot) {
		t.Fatal("PATH binary ran with no Swarmview listening")
	}
}
