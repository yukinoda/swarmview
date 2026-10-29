package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func chromeBinary(t *testing.T) string {
	t.Helper()
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
			if p, err := exec.LookPath(name); err == nil {
				chrome = p
				break
			}
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium not found; set CHROME_BIN to run UI checks")
	}
	return chrome
}

func TestUIProduction(t *testing.T) {
	chrome := chromeBinary(t)
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	srv := httptest.NewUnstartedServer(s)
	s.port = srv.Listener.Addr().(*net.TCPAddr).Port
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--no-sandbox", "--disable-gpu",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=3000", "--timeout=10000", "--dump-dom", srv.URL)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("production page failed (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), `<span id="count">0 agents · 0 working</span>`) {
		t.Fatalf("production page did not load its script and render the real /agents response:\n%s", out)
	}
}

func TestUI(t *testing.T) {
	chrome := chromeBinary(t)
	checks, err := os.ReadFile("web/app_test.js")
	must(t, err)
	page, err := webFiles.ReadFile("web/index.html")
	must(t, err)
	entry, err := webFiles.ReadFile("web/app.js")
	must(t, err)
	script := string(entry)
	// Run the real UI in fresh profiles, replacing its initial poll with checks.
	i := strings.LastIndex(script, "poll();")
	if i < 0 {
		t.Fatal("initial poll not found")
	}
	script = script[:i] + `import('./app_test.js');` + script[i+len("poll();"):]
	if !strings.Contains(script, "function render(data, again = false) {") {
		t.Fatal("render instrumentation target missing")
	}
	script = "const testWork = { renders: 0 };\n" + strings.Replace(script,
		"function render(data, again = false) {", "function render(data, again = false) { testWork.renders++;", 1)
	script += "\nexport const testState = { render, poll, freshness, work: testWork, get timer() { return timer; }, get lastData() { return lastData; } };\n"
	modules := map[string]string{
		"app.js":      script,
		"app_test.js": string(checks),
	}
	for name, extra := range map[string]string{
		"dashboard.js":     "export const testState = { rows, groups, quietRows };",
		"search.js":        "export const testState = { searchRows, searchIndex, get searchReturnFocus() { return searchReturnFocus; } };",
		"notifications.js": "export const testState = { eps };",
		"communication.js": "export const testState = { activeFeedback, pendingFeedback, showFeedback, communicationFeedback, communicationStatus, get source() { return communicationSource; }, closeSource() { communicationSource?.close(); communicationSource = null; } };",
	} {
		code, err := webFiles.ReadFile("web/" + name)
		must(t, err)
		modules[name] = string(code) + "\n" + extra
	}
	for name, signature := range map[string]string{
		"rows.js":   "export function updateAgentRow(r, it) {",
		"agents.js": "function agentItem(a) {",
	} {
		code, err := webFiles.ReadFile("web/" + name)
		must(t, err)
		if !strings.Contains(string(code), signature) {
			t.Fatalf("missing test instrumentation target: %s", signature)
		}
		modules[name] = "const testWork = { calls: 0 };\n" + strings.Replace(string(code), signature, signature+" testWork.calls++;", 1) + "\nexport const testState = { work: testWork };\n"
	}
	if !strings.Contains(string(page), `<html lang="en">`) {
		t.Fatal("index.html lacks the <html lang=\"en\"> injection point")
	}
	// Only the harness frames the page; production is never framed.
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatal("csp no longer contains frame-ancestors 'none'; update the harness override")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/assets/"); ok {
			if code, ok := modules[name]; ok {
				w.Header().Set("Content-Type", "text/javascript")
				w.Write([]byte(code))
				return
			}
			code, err := webFiles.ReadFile("web/" + name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript")
			if name == "app.css" {
				w.Header().Set("Content-Type", "text/css")
			}
			w.Write(code)
			return
		}
		if r.URL.Path == "/logo.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write(logoSVG)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			// New headless Chrome clamps top-level windows to >=500px. A same-origin
			// frame exercises the real requested viewport; checks report to this body.
			width, height := 375, 812
			if r.URL.Query().Get("size") == "native" {
				width, height = 680, 820
			}
			fmt.Fprintf(w, `<!doctype html><body style="margin:0"><iframe title="Synthetic UI checks" style="display:block;border:0;width:%dpx;height:%dpx" src="/test?%s"></iframe>`, width, height, r.URL.Query().Encode())
			return
		}
		platform := "Linux"
		if r.URL.Query().Get("platform") == "MacIntel" {
			platform = "MacIntel"
		}
		// First in the document, so it also sees violations from the initial parse.
		// A single line, so line numbers in violation reports match index.html.
		platformScript := "Object.defineProperty(navigator, 'platform', { value: " +
			string(marshal(platform)) + " }); "
		violation := "e.violatedDirective + ' ' + e.blockedURI + " +
			"' at ' + e.sourceFile + ':' + e.lineNumber"
		recorder := "window.cspViolations = []; " +
			"document.addEventListener('securitypolicyviolation', " +
			"(e) => cspViolations.push(" + violation + "));"
		init := platformScript + recorder
		// Run under the production policy, allowing only the injected platform script.
		sum := sha256.Sum256([]byte(init))
		hash := base64.StdEncoding.EncodeToString(sum[:])
		framed := strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors 'self'", 1)
		policy := framed + "; script-src 'self' 'sha256-" + hash + "'"
		w.Header().Set("Content-Security-Policy", policy)
		injected := `<html lang="en"><script>` + init + `</script>`
		w.Write([]byte(strings.Replace(string(page), `<html lang="en">`, injected, 1)))
	}))
	defer srv.Close()
	// Four runs cover both shortcut platforms, compact/native widths, themes,
	// and reduced motion without launching a browser for every combination.
	for _, scenario := range []struct {
		name, platform, size, theme string
		reduced                     bool
	}{
		{"MacCompactDarkReduced", "MacIntel", "375,812", "dark", true},
		{"LinuxCompactLight", "Linux", "375,812", "light", false},
		{"MacNativeLight", "MacIntel", "680,820", "light", false},
		{"LinuxNativeDarkReduced", "Linux", "680,820", "dark", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var out []byte
			var err error
			// A cold Chrome on a fresh CI runner can hang at launch; one retry absorbs it.
			for attempt := 0; attempt < 2; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				args := []string{"--headless", "--no-sandbox", "--disable-gpu",
					"--user-data-dir=" + t.TempDir(), "--window-size=" + scenario.size,
					"--virtual-time-budget=10000", "--dump-dom", "--timeout=10000"}
				if scenario.theme == "dark" {
					args = append(args, "--force-dark-mode")
				} else {
					args = append(args, "--blink-settings=preferredColorScheme=1")
				}
				if scenario.reduced {
					args = append(args, "--force-prefers-reduced-motion")
				} else {
					args = append(args, "--force-prefers-no-reduced-motion")
				}
				size := "compact"
				if strings.HasPrefix(scenario.size, "680,") {
					size = "native"
				}
				args = append(args, srv.URL+"?platform="+scenario.platform+"&theme="+scenario.theme+"&reduced="+strconv.FormatBool(scenario.reduced)+"&size="+size)
				cmd := exec.CommandContext(ctx, chrome, args...)
				cmd.WaitDelay = 5 * time.Second // Chrome helpers can hold the output pipe open after the kill
				out, err = cmd.CombinedOutput()
				if errors.Is(err, exec.ErrWaitDelay) {
					err = nil // Chrome exited cleanly; only a lingering helper held the pipe
				}
				timedOut := err != nil && ctx.Err() != nil
				cancel()
				if !timedOut {
					break
				}
				t.Logf("Chrome timed out (attempt %d)", attempt+1)
			}
			if err != nil || !strings.Contains(string(out), `data-ui-tests="passed"`) {
				failure := string(out)
				if _, rest, ok := strings.Cut(failure, "<pre>"); ok {
					if message, _, ok := strings.Cut(rest, "</pre>"); ok {
						failure = message
					}
				}
				t.Fatalf("UI checks failed (%v):\n%s", err, failure)
			}
			if _, rest, ok := strings.Cut(string(out), `data-search-ms="`); ok {
				ms, _, _ := strings.Cut(rest, `"`)
				t.Logf("1,000-agent search: %s ms mean synchronous DOM update over 40 queries", ms)
			}
		})
	}
}

func TestUIAssetOverride(t *testing.T) {
	exe, err := os.Executable()
	must(t, err)
	file := filepath.Join(t.TempDir(), "app.js")
	must(t, os.WriteFile(file, []byte("local edit"), 0600))
	name, err := filepath.Rel(filepath.Dir(exe), file)
	must(t, err)
	embedded := []byte("embedded copy")
	if got := string(uiAsset(name, embedded)); got != "local edit" {
		t.Fatalf("local override = %q", got)
	}
	must(t, os.Remove(file))
	if got := string(uiAsset(name, embedded)); got != string(embedded) {
		t.Fatalf("missing file fallback = %q", got)
	}
}
