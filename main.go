package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	kinds            = []string{"claude", "codex", "opencode", "gemini", "aider", "cursor-agent"}
	runtimes         = []string{"node", "bun", "deno", "python", "python3"}
	daemonSubcommand = []string{"app-server", "mcp", "mcp-server", "daemon"}
)

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Runner returns (stdout, err). Only the lsof caller uses stdout when err != nil.
type Runner func(cmd string, args ...string) (string, error)

func execRun(name string, args ...string) (string, error) {
	if filepath.Base(name) == "tmux" {
		args = append([]string{"-u"}, args...) // Preserve tabs and Unicode without a terminal/UTF-8 locale.
	}
	if os.Getenv("DEBUG") != "" && name == "ps" && args[0] == "-axo" {
		log.Println("ps spawn")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 500 * time.Millisecond
	c.Stderr = io.Discard
	out, err := c.Output()
	return string(out), err
}

// ps time= is [[dd-]hh:]mm:ss[.ss]; macOS minutes are unbounded (1036:27.10). NaN on bad input.
func cpuSeconds(t string) float64 {
	days, hms := 0.0, t
	if d, rest, ok := strings.Cut(t, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return math.NaN()
		}
		days, hms = float64(n), rest
	}
	parts := strings.Split(hms, ":")
	sum, mul := 0.0, 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		v, err := strconv.ParseFloat(parts[i], 64)
		if err != nil {
			return math.NaN()
		}
		sum += v * mul
		mul *= 60
	}
	return sum + days*86400
}

var etimeSec = cpuSeconds // same grammar

// tunable: CPU cores used (0.025 = 0.05 CPU-s per 2 s poll) that counts as "working"
const workingRate = 0.025

// CPU deltas across a longer gap (no tab polling) are long averages, not recent activity.
// Above the 1/min timer throttling browsers apply to hidden tabs.
const staleCpuMs = 120e3

type psRow struct {
	Pid, Ppid int
	CpuSec    float64
	Etime     string
	Kind      string
}

type procRow struct {
	pid, ppid int
	rssKB     int64
	cpuSec    float64
	etime     string
}

func parsePs(text string, selfPid int) ([]psRow, []procRow) {
	type cand struct {
		psRow
		key string
	}
	var cands []cand
	procs := []procRow{}
	for _, line := range strings.Split(text, "\n") {
		// limitation: whitespace tokenizing, so paths containing spaces misparse
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseInt(f[2], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		if e3 == nil {
			procs = append(procs, procRow{pid, ppid, rss, cpuSeconds(f[3]), f[4]})
		}
		if len(f) < 6 {
			continue
		}
		tokens := f[5:]
		i := 0
		// limitation: runtime flags that take a value (`node --require x.js agent`) are missed
		if has(runtimes, path.Base(tokens[0])) {
			i = -1
			for j := 1; j < len(tokens); j++ {
				if !strings.HasPrefix(tokens[j], "-") && tokens[j] != "run" {
					i = j
					break
				}
			}
			if i < 0 {
				continue
			}
		}
		kind := path.Base(tokens[i])
		for _, ext := range []string{".js", ".mjs", ".cjs", ".py"} {
			if strings.HasSuffix(kind, ext) {
				kind = strings.TrimSuffix(kind, ext)
				break
			}
		}
		if !has(kinds, kind) {
			continue
		}
		rest := tokens[i+1:]
		// limitation: ps drops quoting, so a one-word prompt equal to a daemon subcommand (`claude -p mcp`) is dropped
		sub := ""
		for _, t := range rest {
			if !strings.HasPrefix(t, "-") {
				sub = t
				break
			}
		}
		if has(daemonSubcommand, sub) || pid == selfPid {
			continue
		}
		cands = append(cands, cand{psRow{pid, ppid, cpuSeconds(f[3]), f[4], kind}, strings.Join(rest, " ")})
	}
	// collapse npm shim + the native binary it execs (same kind, same args, parent/child)
	byPid := map[int]cand{}
	for _, c := range cands {
		byPid[c.Pid] = c
	}
	cpu := map[int]float64{}
	roots := map[int]int{}
	for _, c := range cands {
		root := c
		seen := map[int]bool{c.Pid: true}
		for {
			p, ok := byPid[root.Ppid]
			if !ok || p.Kind != c.Kind || p.key != c.key || seen[p.Pid] {
				break
			}
			seen[p.Pid] = true
			root = p
		}
		cpu[root.Pid] += c.CpuSec
		roots[c.Pid] = root.Pid
	}
	agents := []psRow{}
	for _, c := range cands {
		if p, ok := byPid[c.Ppid]; ok && p.Kind == c.Kind && p.key == c.key {
			continue
		}
		c.CpuSec = cpu[c.Pid]
		if parent, ok := roots[c.Ppid]; ok {
			c.Ppid = parent
		}
		agents = append(agents, c.psRow)
	}
	return agents, procs
}

func parseLsof(text string) map[int]string {
	out := map[int]string{}
	pid, ok := 0, false
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(line[1:])
			pid, ok = n, err == nil
		case 'n':
			if ok {
				out[pid] = line[1:]
			}
		}
	}
	return out
}

type gitInfo struct{ repo, repoName, worktree string }

// `git rev-parse --path-format=absolute --git-common-dir --show-toplevel` -> repo (main checkout) + worktree; non-git cwd is its own repo
func parseGit(text, cwd string) gitInfo {
	common, top, _ := strings.Cut(text, "\n")
	top, _, _ = strings.Cut(top, "\n")
	if !strings.HasPrefix(common, "/") || !strings.HasPrefix(top, "/") {
		return gitInfo{cwd, path.Base(cwd), cwd}
	}
	repo := common // bare repo: the common dir itself
	if strings.HasSuffix(common, "/.git") {
		repo = path.Dir(common)
	}
	return gitInfo{repo, path.Base(repo), top}
}

// num marshals NaN/Inf as null, like JS JSON.stringify
type num float64

func (n num) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
		return []byte("null"), nil
	}
	return json.Marshal(float64(n))
}

type Agent struct {
	Pid          int      `json:"pid"`
	Ppid         int      `json:"ppid"`
	Kind         string   `json:"kind"`
	CpuSec       num      `json:"cpuSec"`
	Etime        string   `json:"etime"`
	Cwd          *string  `json:"cwd"`
	Project      string   `json:"project"`
	Repo         *string  `json:"repo"`
	RepoName     string   `json:"repoName"`
	Worktree     *string  `json:"worktree"`
	RecentCpu    num      `json:"recentCpu"`
	IdleSec      *float64 `json:"idleSec"`
	IsNew        bool     `json:"isNew"`
	Title        *string  `json:"title"`
	CanFocus     bool     `json:"canFocus"`
	TmuxTarget   string   `json:"tmuxTarget,omitempty"`
	TmuxAttached bool     `json:"tmuxAttached"`
	MemBytes     int64    `json:"memBytes"` // ~ sum of RSS over the process tree; shared pages count per process
	pane         string
}

type cacheEntry struct {
	cwd, etime   string
	git          gitInfo
	lastActiveAt int64
	active       bool
	title        sessionTitle
}

// sessionTitle caches claudeSessionTitle by the size and mtime of the files it read.
type sessionTitle struct {
	metaKey, logKey [2]int64 // zero until the file has been stable for a full mtime tick
	id, title       string
}

type metaEntry struct {
	mt                                int64
	finished                          bool
	parentAgentId, description, model any
}

// full session rescan interval; a new session file also triggers one via the dir mtime
const sessionRescanMs = 60e3

type sessionScan struct {
	at, dirMt int64
	names     []string // sessions active in the last 6h
}

type Subagent struct {
	Id            string `json:"id"`
	ParentPid     *int   `json:"parentPid"`
	ParentAgentId any    `json:"parentAgentId"`
	Cwd           string `json:"cwd"`
	Description   any    `json:"description"`
	Model         any    `json:"model"`
	Working       bool   `json:"working"`
}

type snap struct {
	agents    []Agent
	subagents []Subagent
	at        int64
	system    system
}

type payload struct {
	Agents    []Agent    `json:"agents"`
	Subagents []Subagent `json:"subagents"`
	At        int64      `json:"at"`
	System    system     `json:"system"`
}

func toPayload(s snap) payload {
	p := payload{s.agents, s.subagents, s.at, s.system}
	if p.Agents == nil {
		p.Agents = []Agent{}
	}
	if p.Subagents == nil {
		p.Subagents = []Subagent{}
	}
	return p
}

type server struct {
	mu        sync.Mutex
	run       Runner
	snap      *snap
	cache     map[int]*cacheEntry
	metaCache map[string]metaEntry
	sessions  map[string]sessionScan
	root      string // ~/.claude/projects
	port      int
	sysU32    func(name string) (uint32, bool) // nil disables system memory (tests, non-darwin)
	sys       system
	swapAt    int64
	swap      *int64
	prevProcs map[int]procRow
	procAt    int64
	ghostty   bool
	noCwd     map[int]int64 // pid -> next lsof retry
}

func newServer(run Runner) *server {
	home, _ := os.UserHomeDir()
	return &server{port: 4777, run: run, cache: map[int]*cacheEntry{}, metaCache: map[string]metaEntry{}, sessions: map[string]sessionScan{}, noCwd: map[int]int64{}, root: path.Join(home, ".claude/projects"), ghostty: ghosttyInstalled()}
}

func bn(p string) string {
	if b := path.Base(p); b != "" {
		return b
	}
	return p
}

// lsof retry interval for pids whose cwd it can't read (e.g. other users' processes)
const cwdRetryMs = 30e3

// listAgents must be called with s.mu held (or single-threaded).
func (s *server) listAgents(now int64) ([]Agent, error) {
	out, err := s.run("ps", "-axo", "pid=,ppid=,rss=,time=,etime=,args=")
	if err != nil {
		return nil, err
	}
	rows, procs := parsePs(out, os.Getpid())
	tree := buildTree(procs)
	panes := map[int]tmuxPane{}
	attached := false
	if len(rows) > 0 {
		if out, err := s.run("tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{pane_pid}\t#{window_name}\t#{session_name}\t#{pane_title}"); err == nil {
			panes = parseTmuxPanes(out)
		}
	}
	if len(panes) > 0 {
		out, err := s.run("tmux", "list-clients", "-F", "#{client_name}")
		attached = err == nil && strings.TrimSpace(out) != ""
	}
	// Per-process deltas avoid jumps when children start or disappear. Include
	// nested agents' activity in their parents, but keep memory accounting separate.
	rates := map[int]float64{}
	nextProcs := make(map[int]procRow, len(procs))
	for _, p := range procs {
		nextProcs[p.pid] = p
		prev, ok := s.prevProcs[p.pid]
		if !ok || now <= s.procAt || now-s.procAt > staleCpuMs || etimeSec(p.etime) < etimeSec(prev.etime) {
			continue
		}
		rate := (p.cpuSec - prev.cpuSec) / (float64(now-s.procAt) / 1000)
		if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			continue
		}
		seen := map[int]bool{}
		for pid := p.pid; pid > 0 && !seen[pid]; pid = tree.parents[pid] {
			seen[pid] = true
			rates[pid] += rate
		}
	}
	s.prevProcs, s.procAt = nextProcs, now
	listed := map[int]bool{}
	for _, a := range rows {
		listed[a.Pid] = true
	}
	for pid := range s.cache {
		if !listed[pid] {
			delete(s.cache, pid)
		}
	}
	for _, a := range rows { // etime going down means the pid was reused
		if c := s.cache[a.Pid]; c != nil && etimeSec(a.Etime) < etimeSec(c.etime) {
			delete(s.cache, a.Pid)
		}
	}
	for pid := range s.noCwd {
		if !listed[pid] {
			delete(s.noCwd, pid)
		}
	}
	var fresh []psRow
	var pids []string
	for _, a := range rows {
		if s.cache[a.Pid] == nil && s.noCwd[a.Pid] <= now {
			fresh = append(fresh, a)
			pids = append(pids, strconv.Itoa(a.Pid))
		}
	}
	if len(fresh) > 0 {
		// lsof exits 1 if any pid died but still prints the rest: use stdout regardless of err
		lo, _ := s.run("lsof", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(pids, ","))
		cwds := parseLsof(lo)
		for _, a := range fresh {
			cwd := cwds[a.Pid]
			if cwd == "" {
				s.noCwd[a.Pid] = now + cwdRetryMs
				continue
			}
			delete(s.noCwd, a.Pid)
			g := ""
			if o, err := s.run("git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel"); err == nil {
				g = o
			}
			s.cache[a.Pid] = &cacheEntry{cwd: cwd, etime: a.Etime, git: parseGit(g, cwd)}
		}
	}
	agents := make([]Agent, 0, len(rows))
	for _, r := range rows {
		c := s.cache[r.Pid]
		recent := rates[r.Pid]
		if c != nil && recent >= workingRate {
			c.lastActiveAt, c.active = now, true
		}
		a := Agent{Pid: r.Pid, Ppid: r.Ppid, Kind: r.Kind, CpuSec: num(r.CpuSec), Etime: r.Etime, Project: "unknown", RepoName: "unknown",
			RecentCpu: num(recent), IsNew: etimeSec(r.Etime) < 10, MemBytes: tree.treeRSS(r.Pid, listed)}
		if c != nil {
			c.etime = r.Etime
			cwd, repo, wt := c.cwd, c.git.repo, c.git.worktree
			a.Cwd, a.Repo, a.Worktree = &cwd, &repo, &wt
			a.Project, a.RepoName = bn(cwd), c.git.repoName
			if c.active {
				idle := float64(now-c.lastActiveAt) / 1000
				a.IdleSec = &idle
			}
		}
		if p, ok := tree.paneFor(r.Pid, panes); ok {
			a.pane, a.CanFocus, a.TmuxAttached = p.id, attached || s.ghostty, attached
			a.TmuxTarget = p.session + ":" + p.id
			if p.title != "" {
				a.Title = &p.title
			}
		}
		if r.Kind == "claude" && c != nil && (a.Title == nil || *a.Title == "Claude Code") {
			if title := claudeSessionTitle(s.root, r.Pid, c.cwd, now, &c.title); title != "" {
				a.Title = &title
			}
		}
		agents = append(agents, a)
	}
	return agents, nil
}

type tmuxPane struct{ id, title, session string }

var paneRe = regexp.MustCompile(`^%[0-9]+$`)

func tmuxTaskTitle(window, pane string) string {
	pane = strings.TrimSpace(pane)
	// OpenCode's label and Claude's status symbols precede the task title.
	for _, prefix := range []string{"OC | ", "✳ ", "✻ ", "✽ ", "✶ ", "✢ "} {
		if title, ok := strings.CutPrefix(pane, prefix); ok && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	// Codex publishes "task | project". Requiring both parts avoids shell titles.
	if task, project, ok := strings.Cut(pane, " | "); ok && strings.TrimSpace(task) != "" && strings.TrimSpace(project) != "" {
		return pane
	}
	window = strings.TrimSpace(window)
	base := strings.ToLower(path.Base(strings.TrimPrefix(window, "-")))
	if has([]string{"sh", "bash", "zsh", "fish", "dash", "ksh", "nu", "nushell", "pwsh", "shell"}, base) || has(kinds, base) || has(runtimes, base) {
		return ""
	}
	return window
}

func parseTmuxPanes(text string) map[int]tmuxPane {
	panes := map[int]tmuxPane{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.SplitN(line, "\t", 5)
		if len(f) != 5 || !paneRe.MatchString(f[0]) {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err == nil && pid > 0 {
			panes[pid] = tmuxPane{f[0], tmuxTaskTitle(f[2], f[4]), strings.TrimSpace(f[3])}
		}
	}
	return panes
}

// Match process ancestry, never a cwd heuristic: several agents can share a worktree.
func (t *procTree) paneFor(pid int, panes map[int]tmuxPane) (tmuxPane, bool) {
	seen := map[int]bool{}
	for pid > 0 && !seen[pid] {
		if pane, ok := panes[pid]; ok {
			return pane, true
		}
		seen[pid] = true
		pid = t.parents[pid]
	}
	return tmuxPane{}, false
}

func focusAgent(pid int, agents []Agent, run Runner, ghostty bool) (bool, string) {
	var agent *Agent
	for i := range agents {
		if agents[i].Pid == pid {
			agent = &agents[i]
			break
		}
	}
	if agent == nil {
		return false, "unknown pid"
	}
	if !paneRe.MatchString(agent.pane) {
		return false, "not in a tmux pane"
	}
	if ghostty {
		return focusGhostty(agent.pane, run)
	}
	if !agent.CanFocus {
		return false, "attach a tmux client first"
	}
	// tmux chooses the most recently used client when called outside a client.
	// A pane target selects its session, window, and pane in one command.
	if _, err := run("tmux", "switch-client", "-t", agent.pane); err != nil {
		return false, "tmux switch failed; attach a client first"
	}
	return true, ""
}

// claudeSlug: one '-' per non-alphanumeric UTF-16 unit
func claudeSlug(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func mtimeMs(p string) (int64, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.ModTime().UnixMilli(), nil
}

var sessionIDRe = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

// fileKey is a file's size and mtime, or zero while the mtime is within a tick of now
// (a same-size rewrite in that tick would otherwise go unnoticed).
func fileKey(fi os.FileInfo, now int64) [2]int64 {
	if mt := fi.ModTime().UnixMilli(); now-mt >= 1000 {
		return [2]int64{fi.Size(), mt}
	}
	return [2]int64{}
}

// claudeSessionTitle re-reads only files whose size or mtime changed since the cached read.
func claudeSessionTitle(root string, pid int, cwd string, now int64, c *sessionTitle) string {
	metaPath := path.Join(path.Dir(root), "sessions", strconv.Itoa(pid)+".json")
	fi, err := os.Stat(metaPath)
	if err != nil {
		*c = sessionTitle{}
		return ""
	}
	if k := fileKey(fi, now); k == [2]int64{} || k != c.metaKey {
		id, ok := readSessionID(metaPath, pid, cwd)
		if !ok {
			k = [2]int64{} // I/O error: retry next poll like an uncached read
		}
		*c = sessionTitle{metaKey: k, id: id}
	}
	if c.id == "" {
		return ""
	}
	logPath := path.Join(root, claudeSlug(cwd), c.id+".jsonl")
	if fi, err = os.Stat(logPath); err != nil {
		c.logKey, c.title = [2]int64{}, ""
		return ""
	}
	if k := fileKey(fi, now); k == [2]int64{} || k != c.logKey {
		title, ok := readSessionTitle(logPath, c.id)
		if !ok {
			k = [2]int64{}
		}
		c.logKey, c.title = k, title
	}
	return c.title
}

// readSessionID and readSessionTitle report ok=false on I/O errors, which must not be cached.
func readSessionID(metaPath string, pid int, cwd string) (string, bool) {
	f, err := os.Open(metaPath)
	if err != nil {
		return "", false
	}
	var session struct {
		Pid       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&session)
	f.Close()
	if err != nil {
		return "", false // read or decode error: retry rather than cache
	}
	if session.Pid != pid || session.Cwd != cwd || !sessionIDRe.MatchString(session.SessionID) {
		return "", true
	}
	return session.SessionID, true
}

func readSessionTitle(logPath, sessionID string) (string, bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false
	}
	// Inspect only the last 64 KiB; fall back to the pane label if title records are older.
	start := max(int64(0), info.Size()-(64<<10))
	b, err := io.ReadAll(io.NewSectionReader(f, start, 64<<10))
	if err != nil {
		return "", false
	}
	lines := bytes.Split(b, []byte{'\n'})
	if start > 0 {
		lines = lines[1:] // the first record may be partial
	}
	var title string
	for _, line := range lines {
		if !bytes.HasPrefix(line, []byte(`{"type":"ai-title",`)) {
			continue
		}
		var record struct {
			Title     string `json:"aiTitle"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(line, &record) == nil && record.SessionID == sessionID && strings.TrimSpace(record.Title) != "" {
			title = strings.TrimSpace(record.Title)
		}
	}
	return title, true
}

// Claude Code subagents live in <root>/<slug>/<session>/subagents/agent-<id>.{jsonl,meta.json}.
// meta.finished is rarely set, so liveness is the .jsonl mtime: alive < 30 min, working < 60 s.
func listClaudeSubagents(agents []Agent, root string, now int64, metaCache map[string]metaEntry, sessions map[string]sessionScan) []Subagent {
	type group struct {
		cwd  string
		pids []int
	}
	var groups []*group
	for _, a := range agents {
		if a.Kind != "claude" || a.Cwd == nil {
			continue
		}
		var g *group
		for _, x := range groups {
			if x.cwd == *a.Cwd {
				g = x
			}
		}
		if g == nil {
			g = &group{cwd: *a.Cwd}
			groups = append(groups, g)
		}
		g.pids = append(g.pids, a.Pid)
	}
	out := []Subagent{}
	seen := map[string]bool{}
	seenDir := map[string]bool{}
	for _, g := range groups {
		dir := path.Join(root, claudeSlug(g.cwd))
		dmt, err := mtimeMs(dir)
		if err != nil {
			continue
		}
		seenDir[dir] = true
		// Stat every session file only on a rescan: project dirs accumulate history.
		// Trust a cached list only if it was taken a full mtime tick after the dir last changed.
		sc, ok := sessions[dir]
		if !ok || sc.dirMt != dmt || sc.at-dmt < 1000 || now-sc.at >= sessionRescanMs {
			sc = sessionScan{at: now, dirMt: dmt}
			names, _ := os.ReadDir(dir)
			for _, n := range names {
				name := n.Name()
				if !strings.HasSuffix(name, ".jsonl") {
					continue
				}
				if mt, err := mtimeMs(path.Join(dir, name)); err == nil && now-mt <= 6*3600e3 {
					sc.names = append(sc.names, strings.TrimSuffix(name, ".jsonl"))
				}
			}
			sessions[dir] = sc
		}
		for _, session := range sc.names {
			sub := path.Join(dir, session, "subagents")
			files, err := os.ReadDir(sub)
			if err != nil {
				continue
			}
			for _, m := range files {
				mn := m.Name()
				if !strings.HasPrefix(mn, "agent-") || !strings.HasSuffix(mn, ".meta.json") || len(mn) <= len("agent-.meta.json") {
					continue
				}
				id := mn[len("agent-") : len(mn)-len(".meta.json")]
				mp := path.Join(sub, mn)
				mmt, err := mtimeMs(mp)
				if err != nil {
					continue
				}
				seen[mp] = true
				c, ok := metaCache[mp]
				if !ok || c.mt != mmt {
					b, err := os.ReadFile(mp)
					var v any
					var obj map[string]any
					if err == nil && json.Unmarshal(b, &v) == nil {
						obj, _ = v.(map[string]any)
					}
					if obj == nil {
						continue
					}
					fin, _ := obj["finished"].(bool)
					c = metaEntry{mmt, fin, obj["parentAgentId"], obj["description"], obj["model"]}
					metaCache[mp] = c
				}
				if c.finished {
					continue
				}
				jt, err := mtimeMs(path.Join(sub, "agent-"+id+".jsonl"))
				if err != nil || now-jt > 30*60e3 {
					continue
				}
				// limitation: the claude process exposes no session id, so attach only when exactly one claude runs in that cwd
				var pp *int
				if len(g.pids) == 1 {
					pp = &g.pids[0]
				}
				out = append(out, Subagent{id, pp, c.parentAgentId, g.cwd, c.description, c.model, now-jt < 60e3})
			}
		}
	}
	for k := range metaCache {
		if !seen[k] {
			delete(metaCache, k)
		}
	}
	for k := range sessions {
		if !seenDir[k] {
			delete(sessions, k)
		}
	}
	return out
}

type procTree struct {
	kids    map[int][]int
	rss     map[int]int64
	parents map[int]int
}

func buildTree(rows []procRow) *procTree {
	t := &procTree{map[int][]int{}, make(map[int]int64, len(rows)), make(map[int]int, len(rows))}
	for _, r := range rows {
		t.rss[r.pid] = r.rssKB
		t.parents[r.pid] = r.ppid
		if r.ppid != r.pid {
			t.kids[r.ppid] = append(t.kids[r.ppid], r.pid)
		}
	}
	return t
}

// treeRSS is pid's RSS plus its descendants', in bytes. Descendants in stop (other listed agents) and their subtrees are excluded; a visited set survives pid cycles.
func (t *procTree) treeRSS(pid int, stop map[int]bool) int64 {
	seen := map[int]bool{pid: true}
	stack := []int{pid}
	var kb int64
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		kb += t.rss[p]
		for _, c := range t.kids[p] {
			if !seen[c] && !stop[c] {
				seen[c] = true
				stack = append(stack, c)
			}
		}
	}
	return kb * 1024
}

type system struct {
	FreePct       *int   `json:"freePct"`
	Pressure      *int   `json:"pressure"` // 1 normal, 2 warn, 4 critical
	SwapUsedBytes *int64 `json:"swapUsedBytes"`
}

// parseSwap reads `used = 3749.75M` from `sysctl -n vm.swapusage` (units K/M/G).
func parseSwap(text string) (int64, bool) {
	_, rest, ok := strings.Cut(text, "used = ")
	f := strings.Fields(rest)
	if !ok || len(f) == 0 || len(f[0]) < 2 {
		return 0, false
	}
	mult := map[byte]float64{'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30}[f[0][len(f[0])-1]]
	v, err := strconv.ParseFloat(f[0][:len(f[0])-1], 64)
	if mult == 0 || err != nil {
		return 0, false
	}
	return int64(v * mult), true
}

// refreshSystem reads the two cheap sysctls every snapshot and the swap exec at most every 10 s. Caller holds s.mu.
func (s *server) refreshSystem(now int64) {
	if s.sysU32 == nil {
		return
	}
	s.sys = system{}
	if v, ok := s.sysU32("kern.memorystatus_level"); ok {
		n := int(v)
		s.sys.FreePct = &n
	}
	if v, ok := s.sysU32("kern.memorystatus_vm_pressure_level"); ok {
		n := int(v)
		s.sys.Pressure = &n
	}
	if s.swapAt == 0 || now-s.swapAt >= 10000 {
		s.swapAt, s.swap = now, nil
		if out, err := s.run("sysctl", "-n", "vm.swapusage"); err == nil {
			if b, ok := parseSwap(out); ok {
				s.swap = &b
			}
		}
	}
	s.sys.SwapUsedBytes = s.swap
}

// one snapshot per second no matter how many viewers or /focus calls
func (s *server) snapshot(now int64) (snap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap == nil || now-s.snap.at >= 1000 {
		agents, err := s.listAgents(now)
		if err != nil {
			return snap{}, err
		}
		s.refreshSystem(now)
		s.snap = &snap{agents, listClaudeSubagents(agents, s.root, now, s.metaCache, s.sessions), now, s.sys}
	}
	return *s.snap, nil
}

// focus runs the terminal switch outside the lock on a snapshot copy.
func (s *server) focus(pid int, now int64) (bool, string, error) {
	sn, err := s.snapshot(now)
	if err != nil {
		return false, "", err
	}
	ok, e := focusAgent(pid, sn.agents, s.run, s.ghostty)
	return ok, e, nil
}

//go:embed index.html
var indexHTML []byte

//go:embed assets/logo.svg
var logoSVG []byte

// page serves ./index.html next to the binary when present (UI edits need no rebuild), else the embedded copy.
func page() []byte {
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "index.html")); err == nil {
			return b
		}
	}
	return indexHTML
}

// marshal is json.Marshal without HTML escaping, like JS JSON.stringify.
func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func (s *server) writeJSON(w http.ResponseWriter, code int, v any, extra ...string) {
	w.Header().Set("content-type", "application/json")
	for i := 0; i+1 < len(extra); i += 2 {
		w.Header().Set(extra[i], extra[i+1])
	}
	w.WriteHeader(code)
	w.Write(marshal(v))
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	local := func(prefix string) []string {
		return []string{fmt.Sprintf("%s127.0.0.1:%d", prefix, s.port), fmt.Sprintf("%slocalhost:%d", prefix, s.port)}
	}
	if !has(local(""), r.Host) { // block DNS rebinding
		w.WriteHeader(403)
		return
	}
	h := w.Header() // block clickjacking and MIME sniffing
	h.Set("content-security-policy", "frame-ancestors 'none'")
	h.Set("x-frame-options", "DENY")
	h.Set("x-content-type-options", "nosniff")
	switch r.Method + " " + r.RequestURI {
	case "GET /":
		w.Header().Set("content-type", "text/html")
		w.Write(page())
	case "GET /logo.svg":
		w.Header().Set("content-type", "image/svg+xml")
		w.Write(logoSVG)
	case "GET /agents":
		sn, err := s.snapshot(time.Now().UnixMilli())
		if err != nil {
			s.writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.writeJSON(w, 200, toPayload(sn), "cache-control", "no-store")
	case "POST /focus":
		if o := r.Header.Get("Origin"); (o != "" && !has(local("http://"), o)) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			w.WriteHeader(403)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			w.WriteHeader(415)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1025))
		if len(body) > 1024 {
			s.writeJSON(w, 413, map[string]string{"error": "too large"})
			return
		}
		var v any
		if json.Unmarshal(body, &v) != nil || v == nil {
			s.writeJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
		pid := -1
		if m, ok := v.(map[string]any); ok {
			if f, ok := m["pid"].(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1<<31 {
				pid = int(f)
			}
		}
		ok, e, err := s.focus(pid, time.Now().UnixMilli())
		if err != nil {
			s.writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if ok {
			s.writeJSON(w, 200, map[string]bool{"ok": true})
		} else {
			s.writeJSON(w, 404, map[string]string{"error": e})
		}
	default:
		w.WriteHeader(404)
	}
}

// The desktop app alone holds the pipe's write end: EOF also covers a GUI crash.
func serve(s *server, ln net.Listener, parent io.Reader) error {
	if parent != nil {
		go func() {
			io.Copy(io.Discard, parent)
			ln.Close()
		}()
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

func main() {
	desktop := flag.Bool("desktop", false, "use the desktop app's port and exit when stdin closes")
	flag.Parse()
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(32 << 20) // safety net only
	s := newServer(execRun)
	s.sysU32 = sysctlU32
	if n, err := strconv.Atoi(os.Getenv("PORT")); err == nil && n != 0 {
		s.port = n
	}
	var parent io.Reader
	if *desktop {
		s.port, parent = 4778, os.Stdin
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("http://127.0.0.1:%d\n", s.port)
	if err := serve(s, ln, parent); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}
