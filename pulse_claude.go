package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Claude Code runs `swarmview -claude-hook` once per configured hook event. The hook
// forwards one identifier-only record over the private socket and exits; it never
// blocks or fails Claude.

// Claude names subagent transcripts agent-<id>.jsonl; current IDs are a + 7 or 16 hex digits.
var claudeAgentID = regexp.MustCompile(`^a[0-9a-f]{6,32}$`)

type claudePulse struct {
	Type  string `json:"type"`
	Agent string `json:"agent"`
}

// claudeHookRecord maps a hook payload to a record; ok is false for events that are not communication.
// Only the event name, agent ID, tool name and recipient are kept; prompts and results are discarded.
func claudeHookRecord(r io.Reader) (claudePulse, bool) {
	var in struct {
		Event   string `json:"hook_event_name"`
		AgentID string `json:"agent_id"`
		Tool    string `json:"tool_name"`
		Input   struct {
			To string `json:"to"`
		} `json:"tool_input"`
	}
	if json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(&in) != nil {
		return claudePulse{}, false
	}
	rec := claudePulse{Agent: in.AgentID}
	switch {
	case in.Event == "SubagentStart":
		rec.Type = "sent"
	case in.Event == "SubagentStop":
		rec.Type = "returned"
	case in.Event == "PreToolUse" && in.Tool == "SendMessage":
		// A follow-up to a running subagent; names of teammates or sessions are not mapped.
		rec.Type, rec.Agent = "message", in.Input.To
	default:
		return claudePulse{}, false
	}
	return rec, claudeAgentID.MatchString(rec.Agent)
}

func runClaudeHook(stdin io.Reader) {
	rec, ok := claudeHookRecord(stdin)
	if !ok {
		return
	}
	path, err := pulseSocketPath()
	if err != nil {
		return
	}
	c, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return // Swarmview is not running with -communication
	}
	defer c.Close()
	// Async hooks never delay Claude, so leave a busy bridge time to finish its check.
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write(append(marshal(rec), '\n')); err != nil {
		return
	}
	// Stay alive until the bridge closes, so it can check this process's ancestry.
	io.Copy(io.Discard, c)
}

// publishHookBinary records this binary's path beside the socket, so the Claude
// plugin's launcher runs the build that is listening, wherever it was started from.
// Best effort: without it the launcher falls back to PATH and the native app.
func publishHookBinary(dir string) {
	// Never leave an older bridge's path in front of the launcher's fallbacks.
	os.Remove(liveExePath(dir))
	exe, err := os.Executable()
	if err != nil {
		return
	}
	tmp := liveExePath(dir) + ".tmp"
	if os.WriteFile(tmp, []byte(exe+"\n"), 0600) != nil {
		os.Remove(tmp)
		return
	}
	if os.Rename(tmp, liveExePath(dir)) != nil {
		os.Remove(tmp)
	}
}

// unpublishHookBinary removes live.exe only while it still names this binary,
// so a bridge that replaced it keeps its own.
func unpublishHookBinary(dir string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	recorded, err := os.ReadFile(liveExePath(dir))
	if err != nil {
		return
	}
	if string(recorded) == exe+"\n" {
		os.Remove(liveExePath(dir))
	}
}

// liveExePath is where the bridge records its binary for the plugin launcher.
func liveExePath(dir string) string {
	return filepath.Join(dir, "live.exe")
}

func pulseSocketPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "swarmview", "live.sock"), nil
}

func decodeClaudePulse(b []byte) (claudePulse, error) {
	var m claudePulse
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, errors.New("invalid hook record")
	}
	if m.Type != "sent" && m.Type != "returned" && m.Type != "message" {
		return m, errors.New("invalid hook record")
	}
	if !claudeAgentID.MatchString(m.Agent) {
		return m, errors.New("invalid hook record")
	}
	return m, nil
}

// claudeHost returns the detected Claude process that pid is, or descends from within
// five parent hops (Claude runs hooks through a shell, the plugin through a launcher), or 0.
func claudeHost(s *server, sn snap, pid int) int {
	claude := map[int]bool{}
	for _, a := range sn.agents {
		if a.Kind == "claude" {
			claude[a.Pid] = true
		}
	}
	if claude[pid] {
		return pid
	}
	out, err := s.run("ps", "-axo", "pid=,ppid=")
	if err != nil {
		return 0
	}
	parent := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		child, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			parent[child] = ppid
		}
	}
	for hop := 0; hop < 5; hop++ {
		pid = parent[pid]
		if claude[pid] {
			return pid
		}
	}
	return 0
}

// acceptClaude reads one record from a hook connected while its process is still alive.
// The cue names the host PID so the viewer only marks a worker row owned by that Claude.
func (b *pulseBridge) acceptClaude(s *server, c net.Conn, sn snap, pid int) {
	b.mu.Lock()
	watched := len(b.viewers) > 0
	b.mu.Unlock()
	if !watched {
		return // nobody would see the cue: skip the ancestry walk
	}
	host := claudeHost(s, sn, pid)
	if host == 0 {
		return
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 129), 128)
	if !scan.Scan() {
		return
	}
	m, err := decodeClaudePulse(scan.Bytes())
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.viewers) == 0 {
		return
	}
	f := b.frameLocked()
	f.Key, f.Direction, f.Pid = "sub:"+m.Agent, m.Type, host
	// A cue changes no worker state, so a viewer with a full queue just misses it;
	// parallel subagent starts must not be reported as a lost bridge.
	for ch := range b.viewers {
		select {
		case ch <- f:
		default:
		}
	}
}
