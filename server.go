package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

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

//go:embed web/index.html
var indexHTML []byte

//go:embed web/app.js
var appJS []byte

//go:embed web/app.css
var appCSS []byte

//go:embed assets/logo.svg
var logoSVG []byte

// uiAsset prefers files next to the binary so UI edits need no rebuild.
func uiAsset(name string, embedded []byte) []byte {
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), name)); err == nil {
			return b
		}
	}
	return embedded
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
	if !slices.Contains(local(""), r.Host) { // block DNS rebinding
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
		w.Write(uiAsset("web/index.html", indexHTML))
	case "GET /assets/app.js":
		w.Header().Set("content-type", "text/javascript")
		w.Write(uiAsset("web/app.js", appJS))
	case "GET /assets/app.css":
		w.Header().Set("content-type", "text/css")
		w.Write(uiAsset("web/app.css", appCSS))
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
		if o := r.Header.Get("Origin"); (o != "" && !slices.Contains(local("http://"), o)) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
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
