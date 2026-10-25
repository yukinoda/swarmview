package main

import (
	"strings"
	"testing"
)

func joined(args []string) string { return strings.Join(args, " ") }

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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func rows(r ...[3]int) []procRow {
	var o []procRow
	for _, x := range r {
		o = append(o, procRow{pid: x[0], ppid: x[1], rssKB: int64(x[2])})
	}
	return o
}
