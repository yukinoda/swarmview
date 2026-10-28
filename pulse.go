package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"syscall"
	"time"
)

// The socket carries live metadata only. No journal, replay cursor, or event history.
// A viewer queue holds as many frames as the UI shows cues, so parallel Claude subagent starts fit.
const pulsePeers, pulseWorkers, pulseViewers, pulseQueue = 16, 64, 4, 4

type liveWorker struct {
	Key    string `json:"key"`
	Parent string `json:"parent"`
	Pid    int    `json:"pid"`
}

type pulseFrame struct {
	Workers   []liveWorker `json:"workers"`
	Key       string       `json:"key,omitempty"`
	Direction string       `json:"direction,omitempty"`
	Lost      bool         `json:"lost,omitempty"`
	Ambiguous bool         `json:"ambiguous,omitempty"`
	Pid       int          `json:"pid,omitempty"` // Claude cue: the host that must own the worker row
}

type pulseMessage struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	Service string `json:"service"`
	Session string `json:"session"`
	Child   string `json:"child,omitempty"`
	Parent  string `json:"parent,omitempty"`
}

type pulsePeer struct {
	pid              int
	service, session string
	seq              uint64
	workers          map[string]string
}

type pulseBridge struct {
	mu      sync.Mutex
	peers   map[*pulsePeer]net.Conn
	viewers map[chan pulseFrame]bool
	ln      net.Listener
	path    string
	lock    *os.File
	inode   os.FileInfo
	once    sync.Once
	closed  bool
}

// errBye ends a peer's stream after its goodbye record: a deliberate, lossless quit.
var errBye = errors.New("peer said goodbye")

var pulseID = regexp.MustCompile(`^ses[a-zA-Z0-9_-]{1,120}$`)
var pulseService = regexp.MustCompile(`^[a-f0-9]{64}:2\.1\.[34]$`)

func decodePulse(b []byte) (pulseMessage, error) {
	var m pulseMessage
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if len(b) > 1024 || d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, errors.New("invalid live metadata")
	}
	if m.Seq == 0 || !pulseService.MatchString(m.Service) || (m.Session != "" && !pulseID.MatchString(m.Session)) {
		return m, errors.New("invalid live metadata")
	}
	bare := m.Type == "presence" || m.Type == "gap" || m.Type == "bye"
	if bare && m.Child == "" && m.Parent == "" {
		return m, nil
	}
	switch m.Type {
	case "attempted", "forget", "sent", "returned":
		ids := pulseID.MatchString(m.Session) && pulseID.MatchString(m.Parent) && pulseID.MatchString(m.Child)
		if ids && m.Parent != m.Child {
			return m, nil
		}
	}
	return m, errors.New("invalid live event")
}

func newPulseBridge() *pulseBridge {
	return &pulseBridge{peers: map[*pulsePeer]net.Conn{}, viewers: map[chan pulseFrame]bool{}}
}

func (b *pulseBridge) start(s *server) error {
	if b.ln != nil || b.closed {
		return errors.New("communication bridge already started or closed")
	}
	path, err := pulseSocketPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0700 || fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New("communication directory must be owned by you with mode 0700")
	}
	lock, err := lockPulseDirectory(dir)
	if err != nil {
		return err
	}
	ln, inode, err := listenPulseSocket(path)
	if err != nil {
		lock.Close()
		return err
	}
	b.path, b.ln, b.inode, b.lock = path, ln, inode, lock
	publishHookBinary(dir)
	go func() {
		slots := make(chan bool, pulsePeers)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- true:
				go func() { defer func() { <-slots }(); b.accept(s, c) }()
			default:
				c.Close()
			}
		}
	}()
	return nil
}

func (b *pulseBridge) close() {
	b.once.Do(func() {
		if b.ln != nil {
			b.ln.Close()
			_ = removePulseSocket(b.path, b.inode)
			os.Remove(liveExePath(filepath.Dir(b.path)))
		}
		b.mu.Lock()
		b.closed = true
		for _, c := range b.peers {
			c.Close()
		}
		b.mu.Unlock()
		// Never unlink the lock inode: another starter may already have it open.
		if b.lock != nil {
			b.lock.Close()
		}
	})
}

func (b *pulseBridge) accept(s *server, c net.Conn) {
	defer c.Close()
	pid, err := pulsePeerPID(c)
	if err != nil {
		return
	}
	// Kernel peer PID, not a sender-supplied PID, authenticates the TUI process.
	sn, err := s.snapshot(time.Now().UnixMilli())
	if err != nil {
		return
	}
	opencode := false
	for _, a := range sn.agents {
		if a.Pid == pid && a.Kind == "opencode" {
			opencode = true
		}
	}
	if !opencode {
		b.acceptClaude(s, c, sn, pid)
		return
	}
	p := &pulsePeer{pid: pid, workers: map[string]string{}}
	b.mu.Lock()
	if b.closed || len(b.peers) >= pulsePeers {
		b.mu.Unlock()
		return
	}
	for x := range b.peers {
		if x.pid == pid {
			b.mu.Unlock()
			return
		}
	}
	b.peers[p] = c
	b.mu.Unlock()
	clean := false
	defer func() {
		b.mu.Lock()
		delete(b.peers, p)
		// Observations are session-scoped; a peer that never selected a session has nothing to lose.
		lost := !clean && p.session != ""
		// On bridge shutdown the viewers are being torn down; nothing to report.
		if !b.closed {
			b.broadcastLocked("", "", lost)
		}
		b.mu.Unlock()
	}()
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 1025), 1024)
	second, count := time.Now().Unix(), 0
	for {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if !scan.Scan() {
			return
		}
		now := time.Now().Unix()
		if now != second {
			second, count = now, 0
		}
		count++
		if count > 64 {
			return
		}
		m, err := decodePulse(scan.Bytes())
		if err != nil {
			return
		}
		b.mu.Lock()
		err = b.applyLocked(p, m)
		b.mu.Unlock()
		if err != nil {
			clean = errors.Is(err, errBye)
			return
		}
	}
}

func workerKey(p *pulsePeer, id string) string {
	return fmt.Sprintf("live:%d:%s:%s", p.pid, p.service, id)
}

func (b *pulseBridge) applyLocked(p *pulsePeer, m pulseMessage) error {
	if m.Seq <= p.seq {
		return nil
	}
	p.seq = m.Seq
	if m.Type == "presence" {
		if p.service != m.Service || p.session != m.Session {
			p.service, p.session, p.workers = m.Service, m.Session, map[string]string{}
			b.broadcastLocked("", "", false)
		}
		return nil
	}
	if p.service != m.Service || p.session == "" || p.session != m.Session {
		return errors.New("session mismatch")
	}
	if m.Type == "bye" {
		return errBye
	}
	if m.Type == "gap" {
		b.broadcastLocked("", "", true)
		return nil
	}
	if m.Child == p.session {
		return errors.New("worker cycle")
	}
	if m.Parent != p.session {
		if _, ok := p.workers[m.Parent]; !ok {
			return errors.New("unknown parent")
		}
	}
	// Verify the ancestry reaches this client's selected session; never accept cycles.
	for id, depth := m.Parent, 0; id != p.session; depth++ {
		if id == m.Child || depth >= pulseWorkers {
			return errors.New("worker cycle")
		}
		id = p.workers[id]
	}
	if parent, ok := p.workers[m.Child]; ok && parent != m.Parent {
		return errors.New("worker ownership mismatch")
	}
	if m.Type == "forget" {
		remove := map[string]bool{m.Child: true}
		for i := 0; i < pulseWorkers; i++ {
			changed := false
			for id, parent := range p.workers {
				if remove[parent] && !remove[id] {
					remove[id] = true
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		for id := range remove {
			delete(p.workers, id)
		}
		b.broadcastLocked("", "", false)
		return nil
	}
	if _, ok := p.workers[m.Child]; !ok && len(p.workers) >= pulseWorkers {
		return nil
	}
	p.workers[m.Child] = m.Parent
	b.broadcastLocked(workerKey(p, m.Child), m.Type, false)
	return nil
}

func (b *pulseBridge) ambiguousLocked(p *pulsePeer) bool {
	if p.session == "" {
		return false
	}
	for other := range b.peers {
		if other != p && p.service == other.service {
			_, viewsChild := p.workers[other.session]
			_, viewsParent := other.workers[p.session]
			if p.session == other.session || viewsChild || viewsParent {
				return true
			}
		}
	}
	return false
}

func (b *pulseBridge) workersLocked() []liveWorker {
	out := []liveWorker{}
	for p := range b.peers {
		if b.ambiguousLocked(p) || p.session == "" {
			continue
		}
		for id, parent := range p.workers {
			key := fmt.Sprintf("a:%d", p.pid)
			if parent != p.session {
				key = workerKey(p, parent)
			}
			out = append(out, liveWorker{workerKey(p, id), key, p.pid})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (b *pulseBridge) workers() []liveWorker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.workersLocked()
}

func (b *pulseBridge) broadcastLocked(key, direction string, lost bool) {
	if len(b.viewers) == 0 {
		return
	}
	f := b.frameLocked()
	f.Key, f.Direction, f.Lost = key, direction, lost
	// Ambiguous viewers must not emit a pulse with a guessed owner.
	if key != "" {
		found := false
		for _, w := range f.Workers {
			if w.Key == key {
				found = true
			}
		}
		if !found {
			f.Key, f.Direction = "", ""
		}
	}
	b.sendLocked(f)
}

// frameLocked is the current live worker state without a cue.
func (b *pulseBridge) frameLocked() pulseFrame {
	f := pulseFrame{Workers: b.workersLocked()}
	for p := range b.peers {
		if b.ambiguousLocked(p) {
			f.Ambiguous = true
		}
	}
	return f
}

func (b *pulseBridge) sendLocked(f pulseFrame) {
	for ch := range b.viewers {
		select {
		case ch <- f:
		default:
		draining:
			for {
				select {
				case <-ch:
				default:
					break draining
				}
			}
			ch <- pulseFrame{Workers: f.Workers, Lost: true, Ambiguous: f.Ambiguous}
		}
	}
}

func (b *pulseBridge) stream(w http.ResponseWriter, r *http.Request) {
	ch := make(chan pulseFrame, pulseQueue)
	b.mu.Lock()
	if len(b.viewers) >= pulseViewers {
		b.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	b.viewers[ch] = true
	ch <- b.frameLocked()
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.viewers, ch); b.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ctrl := http.NewResponseController(w)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		var data []byte
		select {
		case <-r.Context().Done():
			return
		case f := <-ch:
			data = append(append([]byte("data: "), marshal(f)...), '\n', '\n')
		case <-tick.C:
			data = []byte(": live only\n\n")
		}
		ctrl.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := w.Write(data); err != nil {
			return
		}
		if ctrl.Flush() != nil {
			return
		}
	}
}
