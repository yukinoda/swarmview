package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

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
		// Several claude processes share this cwd: attribute sessions by each process's
		// sessions/<pid>.json, read only in this ambiguous case.
		owner := map[string]int{}
		if len(g.pids) > 1 {
			for _, pid := range g.pids {
				metaPath := path.Join(path.Dir(root), "sessions", strconv.Itoa(pid)+".json")
				if id, _ := readSessionID(metaPath, pid, g.cwd); id != "" {
					owner[id] = pid
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
				// attach to the sole claude in that cwd, else to the process whose session metadata claims this session
				var pp *int
				if len(g.pids) == 1 {
					pp = &g.pids[0]
				} else if pid, ok := owner[session]; ok {
					pp = &pid
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
