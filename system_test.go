package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
