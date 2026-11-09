package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func diagRun(t *testing.T, handler http.Handler, port *int, want string) {
	chrome := chromeBinary(t)
	start := time.Now()
	var mu sync.Mutex
	var events []string
	logf := func(s string) {
		mu.Lock()
		events = append(events, time.Since(start).Round(time.Millisecond).String()+" "+s)
		mu.Unlock()
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logf("begin " + r.Method + " " + r.URL.Path)
		handler.ServeHTTP(w, r)
		logf("end " + r.URL.Path)
	}))
	if port != nil {
		*port = srv.Listener.Addr().(*net.TCPAddr).Port
	}
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--no-sandbox", "--disable-gpu",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=3000", "--timeout=10000", "--dump-dom", "--enable-logging=stderr", "--v=0", srv.URL)
	logf("exec chrome")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	logf("chrome exited")
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) || !strings.Contains(string(out), want) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("DIAG HANG (%v) after %v\nrequests:\n%s\noutput:\n%s", err, time.Since(start), strings.Join(events, "\n"), out)
	}
	t.Logf("ok in %v\n%s\n%s", time.Since(start), strings.Join(events, "\n"), out)
}

func TestDiagProduction(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	diagRun(t, s, &s.port, `<span id="count">0 agents · 0 working</span>`)
}

func TestDiagStatic(t *testing.T) {
	diagRun(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		w.Write([]byte(`<!doctype html><p id="x">static</p>`))
	}), nil, `<p id="x">static</p>`)
}
