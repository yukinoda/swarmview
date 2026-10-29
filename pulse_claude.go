package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
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

// stageHookBinary copies this binary beside the socket for the Claude plugin's
// launcher, which then runs the build that is listening without trusting any
// path outside the owner-only directory. It returns the staged file, or "" if
// copying failed; publishHookBinary moves it into place.
func stageHookBinary(dir string) string {
	tmp := liveHookPath(dir) + ".tmp"
	// The owner lock excludes other writers, so delete-then-exclusive-create
	// can't follow a planted symlink or race another bridge.
	os.Remove(tmp)
	exe, err := os.Executable()
	if err != nil {
		log.Printf("claude plugin: cannot find this binary to copy: %v", err)
		return ""
	}
	src, err := os.Open(exe)
	if err != nil {
		log.Printf("claude plugin: cannot copy this binary: %v", err)
		return ""
	}
	defer src.Close()
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		log.Printf("claude plugin: cannot copy this binary: %v", err)
		return ""
	}
	// The umask may have stripped the exec bit; the launcher needs it.
	err = dst.Chmod(0700)
	if err == nil {
		_, err = io.Copy(dst, src)
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		log.Printf("claude plugin: cannot copy this binary: %v", err)
		os.Remove(tmp)
		return ""
	}
	return tmp
}

// publishHookBinary moves a staged copy into place as live-hook. Without one,
// it removes any older copy, so the launcher falls back to PATH and the app
// instead of running a crashed bridge's build.
func publishHookBinary(dir, staged string) {
	if staged == "" {
		os.Remove(liveHookPath(dir))
		return
	}
	if err := os.Rename(staged, liveHookPath(dir)); err != nil {
		log.Printf("claude plugin: cannot publish this binary: %v", err)
		os.Remove(staged)
		os.Remove(liveHookPath(dir))
	}
}

// liveHookPath is the bridge's own binary, copied for the plugin launcher.
func liveHookPath(dir string) string {
	return filepath.Join(dir, "live-hook")
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

// maxWorkerHosts bounds the SubagentStart records kept for nesting doubly claimed sessions.
const maxWorkerHosts = 256

// recordWorkerHost remembers which Claude started a worker. The first record wins,
// and when full the oldest record is dropped. The next snapshot refresh uses it.
func (s *server) recordWorkerHost(agent string, host workerHost) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hosts == nil {
		s.hosts = map[string]workerHost{}
	}
	if _, ok := s.hosts[agent]; ok {
		return
	}
	if len(s.hosts) >= maxWorkerHosts {
		oldestID := ""
		oldestAt := int64(math.MaxInt64)
		for id, h := range s.hosts {
			if h.at < oldestAt {
				oldestID = id
				oldestAt = h.at
			}
		}
		delete(s.hosts, oldestID)
	}
	s.hosts[agent] = host
}

// record stores a SubagentStart record unless the bridge is closing. It never
// holds b.mu and s.mu together: close marks the bridge closed before clearing,
// so a record that lands after that clear sees closed and removes itself.
func (b *pulseBridge) record(s *server, agent string, host workerHost) {
	s.recordWorkerHost(agent, host)
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		s.forgetWorkerHost(agent)
	}
}

// forgetWorkerHost drops one SubagentStart record.
func (s *server) forgetWorkerHost(agent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.hosts, agent)
}

// clearWorkerHosts forgets every SubagentStart record.
func (s *server) clearWorkerHosts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.hosts)
}

// hostRecord is the record for a SubagentStart reported by Claude process pid in sn.
func hostRecord(sn snap, pid int, now int64) workerHost {
	h := workerHost{pid: pid, start: math.NaN(), at: now}
	if a, ok := findClaude(sn.agents, pid); ok {
		h.start = processStart(a, sn.at)
	}
	return h
}

// acceptClaude reads one record from a hook connected while its process is still alive.
// The cue names the host PID so the viewer only marks a worker row owned by that Claude.
// A SubagentStart record is kept even with no viewer, to nest a resumed session's workers.
func (b *pulseBridge) acceptClaude(s *server, c net.Conn, sn snap, pid int) {
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
	watched := len(b.viewers) > 0
	b.mu.Unlock()
	if !watched && m.Type != "sent" {
		return // nobody would see the cue and nothing to record: skip the ancestry walk
	}
	host := claudeHost(s, sn, pid)
	if host == 0 {
		return
	}
	if m.Type == "sent" {
		b.record(s, m.Agent, hostRecord(sn, host, time.Now().UnixMilli()))
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
