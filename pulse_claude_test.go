package main

import (
	"errors"
	"os"
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
		s.snap = &snap{at: time.Now().UnixMilli(), agents: []Agent{{Pid: host, Kind: kind}}}
		s.mu.Unlock()
	}
	b := newPulseBridge()
	must(t, b.start(s))
	defer b.close()
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
