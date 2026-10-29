package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPulseDecode(t *testing.T) {
	valid := `{"type":"presence","seq":1,"service":"` + strings.Repeat("a", 64) + `:2.1.4","session":"ses_root"}`
	if _, err := decodePulse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		valid + "junk",
		valid + `{}`,
		strings.Replace(valid, `"seq":1`, `"seq":0`, 1),
		strings.Replace(valid, `ses_root`, `../secret`, 1),
		strings.Replace(valid, `2.1.4`, `1.2.0`, 1),
		strings.Replace(valid, `"type":`, `"prompt":"secret","type":`, 1),
		strings.Repeat("x", 1025),
		`null`,
		`[]`,
		`{}`,
	} {
		if _, err := decodePulse([]byte(bad)); err == nil {
			t.Errorf("accepted invalid metadata: %q", bad[:min(len(bad), 100)])
		}
	}
}

func TestPulseStateAndLoss(t *testing.T) {
	b := newPulseBridge()
	p := &pulsePeer{pid: 100, workers: map[string]string{}}
	b.peers[p] = nil
	ch := make(chan pulseFrame, pulseQueue)
	b.viewers[ch] = true
	seq := uint64(0)
	service := strings.Repeat("a", 64) + ":2.1.4"
	apply := func(kind, parent, child string) {
		t.Helper()
		seq++
		if err := b.applyLocked(p, pulseMessage{kind, seq, service, "ses_root", child, parent}); err != nil {
			t.Fatal(err)
		}
	}
	apply("presence", "", "")
	<-ch
	apply("attempted", "ses_root", "ses_child")
	f := <-ch
	if len(f.Workers) != 1 || f.Workers[0].Parent != "a:100" || f.Direction != "attempted" {
		t.Fatal(f)
	}
	if err := b.applyLocked(p, pulseMessage{"sent", seq, service, "ses_root", "ses_child", "ses_root"}); err != nil || len(ch) != 0 {
		t.Fatal("duplicate sequence replays")
	}
	apply("attempted", "ses_child", "ses_nested")
	f = <-ch
	if len(f.Workers) != 2 {
		t.Fatal(f)
	}
	seq++
	if err := b.applyLocked(p, pulseMessage{"sent", seq, service, "ses_root", "ses_child", "ses_nested"}); err == nil {
		t.Fatal("accepted cycle")
	}
	seq++
	if err := b.applyLocked(p, pulseMessage{"sent", seq, service, "ses_root", "ses_root", "ses_child"}); err == nil {
		t.Fatal("accepted the selected session as its own worker")
	}
	seq++
	if err := b.applyLocked(p, pulseMessage{"sent", seq, service, "ses_wrong", "ses_x", "ses_wrong"}); err == nil {
		t.Fatal("accepted mismatched session")
	}
	for i := 0; i < 500; i++ {
		apply("attempted", "ses_root", fmt.Sprintf("ses_%d", i))
	}
	if len(p.workers) != pulseWorkers || len(ch) > pulseQueue {
		t.Fatal("unbounded state/queue")
	}
	lost := false
	for len(ch) > 0 {
		if (<-ch).Lost {
			lost = true
		}
	}
	if !lost {
		t.Fatal("loss not acknowledged")
	}
	other := &pulsePeer{pid: 101, service: p.service, session: p.session, workers: map[string]string{}}
	b.peers[other] = nil
	if len(b.workersLocked()) != 0 {
		t.Fatal("duplicate viewers have guessed ownership")
	}
	b.broadcastLocked("", "", false)
	f = <-ch
	if !f.Ambiguous {
		t.Fatal("ambiguous ownership not acknowledged")
	}
	other.session = "ses_child"
	if len(b.workersLocked()) != 0 {
		t.Fatal("overlapping parent/child viewers have guessed ownership")
	}
	delete(b.peers, other)
	seq++
	if err := b.applyLocked(p, pulseMessage{"presence", seq, service, "ses_new", "", ""}); err != nil || len(p.workers) != 0 {
		t.Fatal("switch retains workers")
	}
	delete(b.peers, p)
	if len(b.workersLocked()) != 0 {
		t.Fatal("disconnect retains identity")
	}
}

func TestPulsePeerPID(t *testing.T) {
	file, err := os.CreateTemp("", "sv-")
	must(t, err)
	name := file.Name()
	file.Close()
	os.Remove(name)
	ln, err := net.Listen("unix", name)
	must(t, err)
	defer ln.Close()
	result := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			result <- 0
			return
		}
		defer c.Close()
		pid, err := pulsePeerPID(c)
		if err != nil {
			pid = 0
		}
		result <- pid
	}()
	c, err := net.Dial("unix", name)
	must(t, err)
	defer c.Close()
	if pid := <-result; pid != os.Getpid() {
		t.Fatalf("kernel peer PID=%d want %d", pid, os.Getpid())
	}
}

func TestCommunicationObserver(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for optional bridge parser checks")
	}
	out, err := exec.Command(node, "--test", filepath.Join("integrations", "opencode", "observer_test.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	// Node's built-in TypeScript loader/hooks test the real TUI entry without installing host packages.
	if err := exec.Command(node, "-e", `process.exit(typeof require('node:module').registerHooks === 'function' ? 0 : 1)`).Run(); err == nil {
		out, err = exec.Command(node, "--experimental-strip-types", "--test", filepath.Join("integrations", "opencode", "tui_test.mjs")).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
}

func pulseWait(t *testing.T, b *pulseBridge, condition func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		b.mu.Lock()
		ok := condition()
		b.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("live state did not settle")
}

// pulseSnap returns a fresh snapshot; the cache expires after a second.
func pulseSnap(kind string) *snap {
	agents := []Agent{{Pid: os.Getpid(), Kind: kind}}
	return &snap{at: time.Now().UnixMilli(), agents: agents}
}

// pulseDial refreshes the snapshot so the bridge accepts this process as the peer, then connects.
func pulseDial(t *testing.T, s *server, b *pulseBridge, kind string) net.Conn {
	t.Helper()
	s.mu.Lock()
	s.snap = pulseSnap(kind)
	s.mu.Unlock()
	c, err := net.Dial("unix", b.path)
	must(t, err)
	return c
}

func pulseLine(kind string, seq int) string {
	return pulseSessionLine("ses_root", kind, seq)
}

func pulseSessionLine(session, kind string, seq int) string {
	service := strings.Repeat("a", 64) + ":2.1.4"
	line := fmt.Sprintf(`{"type":%q,"seq":%d,"service":%q,"session":%q}`, kind, seq, service, session)
	return line + "\n"
}

func TestPulseSocketBoundary(t *testing.T) {
	_, socket := pulseHome(t)
	dir := filepath.Dir(socket)
	must(t, os.Chmod(dir, 0755))
	s := &server{snap: pulseSnap("opencode")}
	b := newPulseBridge()
	if b.start(s) == nil {
		b.close()
		t.Fatal("accepted public socket directory")
	}
	must(t, os.Chmod(dir, 0700))
	must(t, b.start(s))
	defer b.close()
	fi, err := os.Stat(b.path)
	must(t, err)
	if fi.Mode().Perm() != 0600 {
		t.Fatal("socket not owner-only")
	}
	if other := newPulseBridge(); other.start(s) == nil {
		other.close()
		t.Fatal("replaced existing live listener")
	}
	kind := "opencode"
	c := pulseDial(t, s, b, kind)
	_, err = io.WriteString(c, pulseLine("presence", 1))
	must(t, err)
	pulseWait(t, b, func() bool {
		for p := range b.peers {
			if p.session == "ses_root" && p.pid == os.Getpid() {
				return true
			}
		}
		return false
	})
	// The live kernel-authenticated socket, not a reusable PID cache, owns identity.
	c.Close()
	pulseWait(t, b, func() bool { return len(b.peers) == 0 })
	for _, bad := range []string{`{"pid":999}` + "\n", strings.Repeat("x", 1025) + "\n", pulseLine("unknown", 1)} {
		c = pulseDial(t, s, b, kind)
		io.WriteString(c, bad)
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, err = c.Read(make([]byte, 1))
		c.Close()
		if err == nil {
			t.Fatal("invalid source record not closed")
		}
		pulseWait(t, b, func() bool { return len(b.peers) == 0 })
	}
	c = pulseDial(t, s, b, kind)
	for i := 1; i <= 200; i++ {
		if _, e := io.WriteString(c, pulseLine("presence", i)); e != nil {
			break
		}
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	_, err = c.Read(make([]byte, 1))
	c.Close()
	if err == nil {
		t.Fatal("unbounded protocol burst accepted")
	}
	pulseWait(t, b, func() bool { return len(b.peers) == 0 })
	c = pulseDial(t, s, b, kind)
	_, err = io.WriteString(c, pulseLine("presence", 1))
	must(t, err)
	pulseWait(t, b, func() bool {
		for p := range b.peers {
			if p.session == "ses_root" {
				return true
			}
		}
		return false
	})
	c.SetReadDeadline(time.Now().Add(11 * time.Second))
	_, err = c.Read(make([]byte, 1))
	c.Close()
	if err != io.EOF {
		t.Fatalf("missing-heartbeat identity was not expired: %v", err)
	}
	pulseWait(t, b, func() bool { return len(b.peers) == 0 })
	// With a viewer watching, a Claude peer reaches its record check and is still refused.
	ch, _ := pulseViewer(t, b)
	kind = "claude"
	c = pulseDial(t, s, b, kind)
	io.WriteString(c, pulseLine("presence", 1))
	c.SetReadDeadline(time.Now().Add(time.Second))
	_, err = c.Read(make([]byte, 1))
	c.Close()
	// The bridge closes mid-record (it exceeds a hook record's size), which Linux reports as a reset.
	closed := err == io.EOF || errors.Is(err, syscall.ECONNRESET)
	if !closed {
		t.Fatalf("OpenCode protocol accepted from a Claude peer: %v", err)
	}
	if len(ch) != 0 {
		t.Fatal("OpenCode protocol from a Claude peer produced a cue")
	}
}

// pulseViewer registers a viewer queue directly and returns a receive helper with a timeout.
func pulseViewer(t *testing.T, b *pulseBridge) (chan pulseFrame, func(string) pulseFrame) {
	t.Helper()
	ch := make(chan pulseFrame, pulseQueue)
	b.mu.Lock()
	b.viewers[ch] = true
	b.mu.Unlock()
	frame := func(what string) pulseFrame {
		t.Helper()
		select {
		case f := <-ch:
			return f
		case <-time.After(2 * time.Second):
			t.Fatalf("no frame: %s", what)
		}
		return pulseFrame{}
	}
	return ch, frame
}

func TestPulseDisconnectLoss(t *testing.T) {
	pulseHome(t)
	s := &server{snap: pulseSnap("opencode")}
	b := newPulseBridge()
	must(t, b.start(s))
	defer b.close()
	ch, frame := pulseViewer(t, b)
	rootPresence := pulseLine("presence", 1)
	homePresence := pulseSessionLine("", "presence", 1) // a TUI on a non-session screen
	for _, tc := range []struct {
		name       string
		presence   string
		lines      []string
		closeFirst bool // the peer hangs up; otherwise the bridge must drop it
		lost       bool
	}{
		{"goodbye", rootPresence, []string{pulseLine("bye", 2)}, true, false},
		{"records after goodbye are never read", rootPresence, []string{pulseLine("bye", 2), pulseLine("gap", 3)}, true, false},
		{"close without goodbye", rootPresence, nil, true, true},
		{"invalid line", rootPresence, []string{"{\n"}, false, true},
		{"goodbye for another session", rootPresence, []string{pulseSessionLine("ses_other", "bye", 2)}, false, true},
		{"goodbye without a session", homePresence, []string{pulseSessionLine("", "bye", 2)}, true, false},
		{"close without a session", homePresence, nil, true, false},
	} {
		c := pulseDial(t, s, b, "opencode")
		_, err := io.WriteString(c, tc.presence)
		must(t, err)
		frame(tc.name + " presence")
		for _, l := range tc.lines {
			_, err = io.WriteString(c, l)
			must(t, err)
		}
		if tc.closeFirst {
			c.Close()
		}
		last := frame(tc.name + " disconnect")
		pulseWait(t, b, func() bool { return len(b.peers) == 0 })
		if !tc.closeFirst {
			c.Close()
		}
		if last.Lost != tc.lost || len(ch) != 0 {
			t.Fatalf("%s: lost=%v queued=%d, want lost=%v and nothing else", tc.name, last.Lost, len(ch), tc.lost)
		}
	}
}

func TestPulseBridgeCloseIsNotLoss(t *testing.T) {
	pulseHome(t)
	s := &server{snap: pulseSnap("opencode")}
	b := newPulseBridge()
	must(t, b.start(s))
	defer b.close()
	ch, frame := pulseViewer(t, b)
	c := pulseDial(t, s, b, "opencode")
	defer c.Close()
	_, err := io.WriteString(c, pulseLine("presence", 1))
	must(t, err)
	frame("presence")
	b.close()
	pulseWait(t, b, func() bool { return len(b.peers) == 0 })
	if len(ch) != 0 {
		t.Fatal("bridge shutdown broadcast a frame")
	}
}

func TestPulseHTTPBoundaryAndNoReplay(t *testing.T) {
	s := &server{port: 4777}
	for _, test := range []struct {
		host, origin string
		code         int
	}{
		{"127.0.0.1:4777", "", 404}, {"attacker.test:4777", "", 403},
	} {
		r := httptest.NewRequest("GET", "/communication", nil)
		r.Host = test.host
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatal(w.Code)
		}
	}
	s.pulse = newPulseBridge()
	r := httptest.NewRequest("GET", "/communication", nil)
	r.Host = "127.0.0.1:4777"
	r.Header.Set("Origin", "https://attacker.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin stream accepted")
	}
	p := &pulsePeer{pid: 100, service: strings.Repeat("a", 64) + ":2.1.4", session: "ses_root", workers: map[string]string{"ses_child": "ses_root"}}
	s.pulse.peers[p] = nil
	s.pulse.broadcastLocked(workerKey(p, "ses_child"), "sent", false)
	web := httptest.NewServer(s)
	defer web.Close()
	req, err := http.NewRequest("GET", web.URL+"/communication", nil)
	must(t, err)
	req.Host = "127.0.0.1:4777"
	response, err := web.Client().Do(req)
	must(t, err)
	scan := bufio.NewScanner(response.Body)
	if !scan.Scan() {
		t.Fatal("missing initial live state")
	}
	var frame pulseFrame
	must(t, json.Unmarshal([]byte(strings.TrimPrefix(scan.Text(), "data: ")), &frame))
	if len(frame.Workers) != 1 || frame.Key != "" || frame.Direction != "" {
		t.Fatal("replayed event on initial attachment", frame)
	}
	response.Body.Close()
	pulseWait(t, s.pulse, func() bool { return len(s.pulse.viewers) == 0 })
	for i := 0; i < pulseViewers; i++ {
		s.pulse.viewers[make(chan pulseFrame, pulseQueue)] = true
	}
	r = httptest.NewRequest("GET", "/communication", nil)
	r.Host = "127.0.0.1:4777"
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("unbounded browser streams")
	}
}

func TestPulseForgetAndNamespace(t *testing.T) {
	b := newPulseBridge()
	service := strings.Repeat("a", 64) + ":2.1.4"
	p := &pulsePeer{pid: 100, service: service, session: "ses_root", workers: map[string]string{"ses_child": "ses_root", "ses_nested": "ses_child"}}
	b.peers[p] = nil
	q := &pulsePeer{pid: 101, service: strings.Repeat("b", 64) + ":2.1.4", session: "ses_root", workers: map[string]string{"ses_child": "ses_root"}}
	b.peers[q] = nil
	if len(b.workersLocked()) != 3 {
		t.Fatal("different service namespaces collided")
	}
	if err := b.applyLocked(p, pulseMessage{"forget", 1, service, "ses_root", "ses_child", "ses_root"}); err != nil {
		t.Fatal(err)
	}
	if len(p.workers) != 0 || len(b.workersLocked()) != 1 {
		t.Fatal("failed descendant metadata retained")
	}
}

func TestPulseAtomicConfirmedWorker(t *testing.T) {
	for _, kind := range []string{"sent", "returned"} {
		b := newPulseBridge()
		service := strings.Repeat("a", 64) + ":2.1.4"
		p := &pulsePeer{pid: 100, service: service, session: "ses_root", workers: map[string]string{}}
		b.peers[p] = nil
		ch := make(chan pulseFrame, pulseQueue)
		b.viewers[ch] = true
		must(t, b.applyLocked(p, pulseMessage{kind, 1, service, "ses_root", "ses_new", "ses_root"}))
		frame := <-ch
		if len(frame.Workers) != 1 || frame.Direction != kind || frame.Lost {
			t.Fatal("confirmed identity must publish atomically without an attempt burst", frame)
		}
	}
}
