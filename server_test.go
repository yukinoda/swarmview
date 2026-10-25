package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func do(s *server, method, target, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestPayloadNulls(t *testing.T) {
	b, err := json.Marshal(toPayload(snap{
		agents:    []Agent{{Pid: 1, Kind: "claude", CpuSec: num(math.NaN()), pane: "secret:pane"}},
		subagents: []Subagent{{Id: "a", Cwd: "/x"}},
		at:        5,
	}))
	must(t, err)
	s := string(b)
	for _, want := range []string{`"cwd":null`, `"idleSec":null`, `"parentPid":null`, `"parentAgentId":null`, `"cpuSec":null`, `"repo":null`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "pane") {
		t.Error("pane leaked")
	}
	e, _ := json.Marshal(toPayload(snap{}))
	if string(e) != `{"agents":[],"subagents":[],"at":0,"system":{"freePct":null,"pressure":null,"swapUsedBytes":null}}` {
		t.Errorf("got %s", e)
	}
}

func TestSnapshotMemoized(t *testing.T) {
	var n int
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" && args[0] == "-axo" {
			n++
		}
		return "", nil
	})
	for _, at := range []int64{1000, 1500, 1999} {
		s.snapshot(at)
	}
	s.snapshot(2000)
	if n != 2 {
		t.Errorf("ps spawns = %d", n)
	}
}

func TestHandlers(t *testing.T) {
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "  100 1 1000 0:01.00 00:10 claude\n", nil
		}
		return "", nil
	})
	const h = "127.0.0.1:4777"
	js := map[string]string{"Content-Type": "application/json"}
	with := func(k, v string) map[string]string {
		return map[string]string{"Content-Type": "application/json", k: v}
	}
	for _, tc := range []struct {
		name, method, target, host string
		hdr                        map[string]string
		body                       string
		code                       int
		bodyHas                    string
	}{
		{"bad host", "GET", "/", "evil.com", nil, "", 403, ""},
		{"index", "GET", "/", h, nil, "", 200, "<!doctype html>"},
		{"logo", "GET", "/logo.svg", h, nil, "", 200, "<svg"},
		{"script", "GET", "/assets/app.js", h, nil, "", 200, "import"},
		{"communication module", "GET", "/assets/communication.js", h, nil, "", 200, "getCommunicationEdge"},
		{"styles", "GET", "/assets/app.css", h, nil, "", 200, ":root"},
		{"asset bad host", "GET", "/assets/app.js", "evil.com", nil, "", 403, ""},
		{"asset wrong method", "POST", "/assets/app.js", h, nil, "", 404, ""},
		{"unlisted asset", "GET", "/assets/README.md", h, nil, "", 404, ""},
		{"browser tests not served", "GET", "/assets/app_test.js", h, nil, "", 404, ""},
		{"source directory not served", "GET", "/web/app_test.js", h, nil, "", 404, ""},
		{"localhost host", "GET", "/", "localhost:4777", nil, "", 200, ""},
		{"agents", "GET", "/agents", h, nil, "", 200, `"agents":[`},
		{"agents with query", "GET", "/agents?x=1", h, nil, "", 404, ""},
		{"unknown route", "GET", "/nope", h, nil, "", 404, ""},
		{"focus bad origin", "POST", "/focus", h, with("Origin", "http://evil.com"), `{"pid":100}`, 403, ""},
		{"focus null origin", "POST", "/focus", h, with("Origin", "null"), `{"pid":100}`, 403, ""},
		{"focus cross-site", "POST", "/focus", h, with("Sec-Fetch-Site", "cross-site"), `{"pid":100}`, 403, ""},
		{"focus wrong content type", "POST", "/focus", h, map[string]string{"Content-Type": "text/plain"}, `{}`, 415, ""},
		{"focus no content type", "POST", "/focus", h, nil, `{}`, 415, ""},
		{"focus too large", "POST", "/focus", h, js, strings.Repeat("a", 1025), 413, "too large"},
		{"focus bad json", "POST", "/focus", h, js, `{`, 400, "bad json"},
		{"focus null", "POST", "/focus", h, js, `null`, 400, "bad json"},
		{"focus unknown pid", "POST", "/focus", h, js, `{"pid":7}`, 404, "unknown pid"},
		{"focus string pid", "POST", "/focus", h, js, `{"pid":"100"}`, 404, "unknown pid"},
		{"focus array", "POST", "/focus", h, js, `[]`, 404, "unknown pid"},
		{"focus number", "POST", "/focus", h, js, `5`, 404, "unknown pid"},
	} {
		w := do(s, tc.method, tc.target, tc.host, tc.hdr, tc.body)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.bodyHas) {
			t.Errorf("%s: %d %q", tc.name, w.Code, w.Body.String())
		}
		if (tc.code == 403 || tc.code == 415 || tc.code == 404 && tc.bodyHas == "") && w.Body.Len() != 0 {
			t.Errorf("%s: body should be empty", tc.name)
		}
	}
	w := do(s, "GET", "/agents", h, nil, "")
	if w.Header().Get("content-type") != "application/json" || w.Header().Get("cache-control") != "no-store" {
		t.Error("agents headers")
	}
	for route, mime := range map[string]string{"/assets/app.js": "text/javascript", "/assets/app.css": "text/css"} {
		w := do(s, "GET", route, h, nil, "")
		if w.Header().Get("content-type") != mime || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: incorrect asset headers: %v", route, w.Header())
		}
	}
}

func TestFeatureModulesDoNotImportCoordinator(t *testing.T) {
	entries, err := webFiles.ReadDir("web")
	must(t, err)
	for _, entry := range entries {
		if entry.Name() == "app.js" || !strings.HasSuffix(entry.Name(), ".js") {
			continue
		}
		code, err := webFiles.ReadFile("web/" + entry.Name())
		must(t, err)
		for _, dependency := range []string{"'./app.js'", `"./app.js"`} {
			if bytes.Contains(code, []byte(dependency)) {
				t.Fatalf("%s must not import the app coordinator", entry.Name())
			}
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	w := do(s, "GET", "/", "127.0.0.1:4777", nil, "")
	for k, v := range map[string]string{
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestWebAssets(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	entries, err := webFiles.ReadDir("web")
	must(t, err)
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			if entry.IsDir() || strings.Contains(entry.Name(), "_test") {
				t.Fatal("only production files may be embedded")
			}
			route, mime := "/assets/"+entry.Name(), "text/javascript"
			switch entry.Name() {
			case "index.html":
				route, mime = "/", "text/html"
			case "app.css":
				mime = "text/css"
			}
			want, err := webFiles.ReadFile("web/" + entry.Name())
			must(t, err)
			response := do(s, "GET", route, "127.0.0.1:4777", nil, "")
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatalf("%s did not serve its embedded file", route)
			}
			if response.Header().Get("content-type") != mime || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s has incorrect headers: %v", route, response.Header())
			}
			if bytes.Contains(response.Body.Bytes(), []byte("testState")) {
				t.Fatal("test instrumentation leaked into production")
			}
		})
	}
}

func TestAgentsPsFailure500(t *testing.T) {
	s := srvWith(func(string, ...string) (string, error) { return "", errors.New("boom") })
	w := do(s, "GET", "/agents", "127.0.0.1:4777", nil, "")
	if w.Code != 500 || w.Header().Get("content-type") != "application/json" || !strings.Contains(w.Body.String(), `{"error":`) || s.snap != nil {
		t.Errorf("%d %q", w.Code, w.Body.String())
	}
}

func TestConcurrentSnapshotsOnePs(t *testing.T) {
	var n atomic.Int32
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" && args[0] == "-axo" {
			n.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
		return "", nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.snapshot(5000) }()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Errorf("ps spawns = %d", n.Load())
	}
}
