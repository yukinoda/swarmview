package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	kinds            = []string{"claude", "codex", "opencode", "gemini", "aider", "cursor-agent"}
	runtimes         = []string{"node", "bun", "deno", "python", "python3"}
	daemonSubcommand = []string{"app-server", "mcp", "mcp-server", "daemon"}
)

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
		if slices.Contains(runtimes, path.Base(tokens[0])) {
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
		rest := tokens[i+1:]
		// Cursor's bundled runtime can be named "agent"; require its script path, not the generic name alone.
		if kind == "agent" || kind == "cursor-agent" {
			for j, t := range rest {
				if strings.HasPrefix(t, "-") {
					continue
				}
				if strings.Contains(t, "/cursor-agent/versions/") && strings.HasSuffix(t, "/index.js") {
					kind, rest = "cursor-agent", rest[j+1:]
				}
				break
			}
		}
		if !slices.Contains(kinds, kind) {
			continue
		}
		// limitation: ps drops quoting, so a one-word prompt equal to a daemon subcommand (`claude -p mcp`) is dropped
		sub := ""
		for _, t := range rest {
			if !strings.HasPrefix(t, "-") {
				sub = t
				break
			}
		}
		if slices.Contains(daemonSubcommand, sub) || pid == selfPid {
			continue
		}
		if kind == "opencode" && sub == "serve" || kind == "cursor-agent" && (sub == "worker" || sub == "worker-server") {
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
			a.Project, a.RepoName = path.Base(cwd), c.git.repoName
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
