package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// sessionTitle caches claudeSessionTitle by the size and mtime of the files it read.
type sessionTitle struct {
	metaKey, logKey  [2]int64 // zero until the file has been stable for a full mtime tick
	id, title, model string
	cwd              string // the cwd sessions/<pid>.json recorded when Claude started, "" if unknown
}

// projectCwd is the cwd that names the session's project dir under root. Claude
// keeps writing there after its directory is renamed, while lsof reports the new
// name, so the recorded cwd wins over the live one.
func (c *sessionTitle) projectCwd(liveCwd string) string {
	if c.cwd != "" {
		return c.cwd
	}
	return liveCwd
}

type metaEntry struct {
	mt                                int64
	finished                          bool
	parentAgentId, description, model any
	logKey                            [2]int64 // transcript size and mtime at the last tail read
	logModel                          string   // last model in the transcript, "" if none seen yet
}

// workerHost is the Claude process whose SubagentStart hook reported a worker,
// with its estimated start (Unix seconds) and the order it was recorded in.
type workerHost struct {
	pid   int
	start float64
	seq   uint64
}

// hostKey names one Claude's record for one worker. A worker can have a
// record from each Claude that reported starting it.
type hostKey struct {
	agent string
	pid   int
}

// sessionHost is the earliest record for worker id made by one of its
// session's claimants (owners). Records from any other Claude are ignored.
func sessionHost(hosts map[hostKey]workerHost, id string, owners []int) (workerHost, bool) {
	var first workerHost
	found := false
	for _, pid := range owners {
		h, ok := hosts[hostKey{id, pid}]
		if !ok {
			continue
		}
		if !found || h.seq < first.seq {
			first = h
			found = true
		}
	}
	return first, found
}

// ps etime has one-second granularity and is read slightly after a snapshot's
// time, so two estimates of one process's start can differ by about a second.
// Attribution needs this close a match.
const startSlackSec = 2

// A record is deleted for a start mismatch only beyond this, when it is
// clearly another process; a smaller mismatch just doesn't attribute that poll.
const reusedStartSec = 30

// processStart estimates when a process started, in Unix seconds, from its
// elapsed time at now. It is NaN when ps gave no usable elapsed time.
func processStart(a Agent, now int64) float64 {
	return float64(now)/1000 - etimeSec(a.Etime)
}

// findClaude returns the detected Claude process with this PID.
func findClaude(agents []Agent, pid int) (Agent, bool) {
	isPid := func(a Agent) bool {
		return a.Pid == pid && a.Kind == "claude"
	}
	i := slices.IndexFunc(agents, isPid)
	if i < 0 {
		return Agent{}, false
	}
	return agents[i], true
}

// startDrift is how far apart, in seconds, the record's start time and the
// running Claude's are; known is false when the running one's is unknown.
// Records are only made with a known start time.
func (h workerHost) startDrift(a Agent, now int64) (drift float64, known bool) {
	start := processStart(a, now)
	if math.IsNaN(start) {
		return 0, false
	}
	return math.Abs(start - h.start), true
}

// proven reports whether the very Claude process the record was made for is
// running now: same PID and start time, so a reused PID never inherits the
// record. An unknown start time proves nothing.
func (h workerHost) proven(agents []Agent, now int64) bool {
	a, ok := findClaude(agents, h.pid)
	if !ok {
		return false
	}
	drift, known := h.startDrift(a, now)
	if !known {
		return false
	}
	return drift <= startSlackSec
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
// The session's latest model is left in c.model. start is the process's start
// in Unix seconds (see processStart), NaN if unknown.
func claudeSessionTitle(root string, pid int, start float64, cwd string, now int64, c *sessionTitle) string {
	metaPath := path.Join(path.Dir(root), "sessions", strconv.Itoa(pid)+".json")
	fi, err := os.Stat(metaPath)
	if err != nil {
		*c = sessionTitle{}
		return ""
	}
	if k := fileKey(fi, now); k == [2]int64{} || k != c.metaKey {
		id, recordedCwd, ok := readSessionID(metaPath, pid, start)
		if !ok {
			// I/O error: keep the cached session and retry next poll like an uncached read
			c.metaKey = [2]int64{}
		} else {
			// status rewrites keep the id; only a new session drops the old title and model
			if id != c.id {
				*c = sessionTitle{id: id}
			}
			c.cwd = recordedCwd
			c.metaKey = k
		}
	}
	if c.id == "" {
		return ""
	}
	logPath := path.Join(root, claudeSlug(c.projectCwd(cwd)), c.id+".jsonl")
	if fi, err = os.Stat(logPath); err != nil {
		c.logKey, c.title, c.model = [2]int64{}, "", ""
		return ""
	}
	if k := fileKey(fi, now); k == [2]int64{} || k != c.logKey {
		title, model, ok := readSessionTitle(logPath, c.id)
		if !ok {
			k = [2]int64{}
		}
		c.logKey, c.title = k, title
		// a long tool result can push every assistant record out of the tail; keep the last model seen
		if model != "" {
			c.model = model
		}
	}
	return c.title
}

// readSessionID and readSessionTitle report ok=false on I/O errors, which must not be cached.
// readSessionID also returns the cwd the session was started in. That cwd is
// untrusted, but claudeSlug turns it into [A-Za-z0-9-] only, so it cannot leave root.
// A file written before the process started (start, Unix seconds) is left by an
// earlier Claude with a reused pid that crashed before deleting it.
func readSessionID(metaPath string, pid int, start float64) (string, string, bool) {
	f, err := os.Open(metaPath)
	if err != nil {
		return "", "", false
	}
	var session struct {
		Pid       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		StartedAt int64  `json:"startedAt"` // Unix ms, written once when Claude starts
	}
	err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&session)
	f.Close()
	if err != nil {
		return "", "", false // read or decode error: retry rather than cache
	}
	if session.Pid != pid || !sessionIDRe.MatchString(session.SessionID) {
		return "", "", true
	}
	// an unknown start or a file without startedAt can't be checked: accept it
	startKnown := !math.IsNaN(start) && session.StartedAt > 0
	writtenBeforeStart := float64(session.StartedAt)/1000 < start-startSlackSec
	if startKnown && writtenBeforeStart {
		return "", "", true
	}
	// a relative or empty cwd can't name a project dir: fall back to the live cwd
	if !path.IsAbs(session.Cwd) {
		return session.SessionID, "", true
	}
	return session.SessionID, session.Cwd, true
}

// readSessionTitle also returns the model of the session's last assistant record in the tail.
func readSessionTitle(logPath, sessionID string) (string, string, bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", "", false
	}
	// Inspect only the last 64 KiB; fall back to the pane label if title records are older.
	start := max(int64(0), info.Size()-(64<<10))
	b, err := io.ReadAll(io.NewSectionReader(f, start, 64<<10))
	if err != nil {
		return "", "", false
	}
	lines := bytes.Split(b, []byte{'\n'})
	if start > 0 {
		lines = lines[1:] // the first record may be partial
	}
	// newest first, so only the last assistant record is decoded
	var model string
	for i := len(lines) - 1; i >= 0 && model == ""; i-- {
		if bytes.Contains(lines[i], []byte(`"type":"assistant"`)) {
			model = assistantModel(lines[i], sessionID)
		}
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
	return title, model, true
}

func assistantModel(line []byte, sessionID string) string {
	var record struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Message   struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil {
		return ""
	}
	if record.Type != "assistant" || record.SessionID != sessionID {
		return ""
	}
	// Claude Code writes locally generated notices as "<synthetic>"
	if strings.HasPrefix(record.Message.Model, "<") {
		return ""
	}
	return record.Message.Model
}

// ancestorFinished reports whether a worker's parent worker, or one of its ancestors, has
// finished. It only consults cached metas from the same subagents dir: an ancestor whose meta
// is unknown stops the walk, so the worker stays listed. maxHops bounds a parentAgentId cycle.
func ancestorFinished(metaCache map[string]metaEntry, sub string, c metaEntry, maxHops int) bool {
	for range maxHops {
		parentId, _ := c.parentAgentId.(string)
		if parentId == "" {
			return false
		}
		parent, ok := metaCache[path.Join(sub, "agent-"+parentId+".meta.json")]
		if !ok {
			return false
		}
		if parent.finished {
			return true
		}
		c = parent
	}
	return false
}

// Claude Code subagents live in <root>/<slug>/<session>/subagents/agent-<id>.{jsonl,meta.json}.
// Claude Code sets meta.finished on background workers but never on foreground ones, so liveness
// is the .jsonl mtime (alive < 30 min, working < 60 s), and a quiet worker whose ancestor
// worker has finished is over too.
// hosts (from the SubagentStart hook) only chooses among processes that already claim a session.
func listClaudeSubagents(agents []Agent, root string, now int64, metaCache map[string]metaEntry, sessions map[string]sessionScan, hosts map[hostKey]workerHost) []Subagent {
	// Every record left names a Claude PID that is still running.
	pruneWorkerHosts(hosts, agents, now)
	type group struct {
		projectCwd string // names the project dir, see sessionTitle.projectCwd
		pids       []int
		cwds       []string // each process's live cwd, shown for its workers
		sessions   []string // each process's cached current session id, "" if unknown
	}
	var groups []*group
	for _, a := range agents {
		if a.Kind != "claude" {
			continue
		}
		// both are set when lsof gave the live cwd; an empty slug would name root itself
		if a.Cwd == nil || a.projectCwd == "" {
			continue
		}
		var g *group
		for _, x := range groups {
			if x.projectCwd == a.projectCwd {
				g = x
			}
		}
		if g == nil {
			g = &group{projectCwd: a.projectCwd}
			groups = append(groups, g)
		}
		g.pids = append(g.pids, a.Pid)
		g.cwds = append(g.cwds, *a.Cwd)
		g.sessions = append(g.sessions, a.session)
	}
	out := []Subagent{}
	seen := map[string]bool{}
	seenDir := map[string]bool{}
	for _, g := range groups {
		dir := path.Join(root, claudeSlug(g.projectCwd))
		dmt, err := mtimeMs(dir)
		if err != nil {
			continue
		}
		seenDir[dir] = true
		// Several claude processes share this project dir: attribute sessions by each process's
		// sessions/<pid>.json, read only in this ambiguous case. A session claimed by
		// two processes (e.g. resumed in a second terminal) has several claimants.
		claimants := map[string][]int{}
		if len(g.pids) > 1 {
			for _, pid := range g.pids {
				metaPath := path.Join(path.Dir(root), "sessions", strconv.Itoa(pid)+".json")
				a, _ := findClaude(agents, pid)
				id, _, _ := readSessionID(metaPath, pid, processStart(a, now))
				if id != "" {
					claimants[id] = append(claimants[id], pid)
				}
			}
		}
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
			// Load every meta first: an ancestor's file can sort after its descendants'.
			var ids []string
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
					var obj map[string]any
					if err != nil || json.Unmarshal(b, &obj) != nil || obj == nil {
						continue
					}
					fin, _ := obj["finished"].(bool)
					c = metaEntry{
						mt:            mmt,
						finished:      fin,
						parentAgentId: obj["parentAgentId"],
						description:   obj["description"],
						model:         obj["model"],
						logKey:        c.logKey,
						logModel:      c.logModel,
					}
					metaCache[mp] = c
				}
				ids = append(ids, id)
			}
			for _, id := range ids {
				mp := path.Join(sub, "agent-"+id+".meta.json")
				c := metaCache[mp]
				if c.finished {
					continue
				}
				logPath := path.Join(sub, "agent-"+id+".jsonl")
				logInfo, err := os.Stat(logPath)
				if err != nil {
					continue
				}
				jt := logInfo.ModTime().UnixMilli()
				if now-jt > 30*60e3 {
					continue
				}
				working := now-jt < 60e3
				// Foreground workers never get meta.finished, so one spawned by another worker would
				// linger as quiet for the full window. A worker can outlive its parent only when the
				// parent backgrounded it, and then it keeps writing: keep working ones.
				if !working && ancestorFinished(metaCache, sub, c, len(ids)) {
					continue
				}
				// The meta file only names the requested alias; the transcript has the resolved model.
				// Worker records carry the parent session's id.
				logKey := fileKey(logInfo, now)
				if logKey == [2]int64{} || logKey != c.logKey {
					_, logModel, ok := readSessionTitle(logPath, session)
					if ok {
						c.logKey = logKey
					}
					// a long tool result can push every assistant record out of the tail; keep the last model seen
					if logModel != "" {
						c.logModel = logModel
					}
					metaCache[mp] = c
				}
				model := c.model
				if c.logModel != "" {
					model = c.logModel
				}
				// Attach to the sole claude in that project dir unless its known current session is
				// another one (the worker outlived an earlier session) and its SubagentStart
				// hook didn't report this worker (one it started before a /clear). Else attach
				// to the one process whose session metadata claims this session. Of several
				// claimants, only the one whose hook reported this worker is its parent.
				var pp *int
				owners := claimants[session]
				host, claimed := sessionHost(hosts, id, owners)
				if len(g.pids) == 1 {
					current := g.sessions[0]
					fromOtherSession := current != "" && current != session
					soleHost, recorded := hosts[hostKey{id, g.pids[0]}]
					startedHere := recorded && soleHost.proven(agents, now)
					if !fromOtherSession || startedHere {
						pp = &g.pids[0]
					}
				} else if len(owners) == 1 {
					pp = &owners[0]
				} else if claimed && host.proven(agents, now) {
					pp = &host.pid
				}
				// Processes sharing a project dir can have different live cwds when its
				// directory was renamed and then recreated: show the parent's.
				// A worker without a known parent belongs to the project dir: prefer a
				// member still in it over one whose directory was renamed.
				cwd := g.cwds[0]
				if slices.Contains(g.cwds, g.projectCwd) {
					cwd = g.projectCwd
				}
				if pp != nil {
					cwd = g.cwds[slices.Index(g.pids, *pp)]
				}
				out = append(out, Subagent{id, pp, c.parentAgentId, cwd, c.description, model, working})
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

// pruneWorkerHosts drops records whose Claude process is gone: no detected
// Claude has the PID, or one does whose start time is clearly different (a
// reused PID). An unknown or marginally different start time keeps the record,
// since hooks never resend it; it just proves nothing that poll. A record for
// an idle, unlisted worker stays, so the worker keeps its parent if it wakes up.
func pruneWorkerHosts(hosts map[hostKey]workerHost, agents []Agent, now int64) {
	for key, h := range hosts {
		a, ok := findClaude(agents, h.pid)
		if !ok {
			delete(hosts, key)
			continue
		}
		drift, known := h.startDrift(a, now)
		if known && drift > reusedStartSec {
			delete(hosts, key)
		}
	}
}
