package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUI(t *testing.T) {
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
	checks, err := os.ReadFile("web/app_test.js")
	must(t, err)
	script := string(appJS)
	// Run the real UI in fresh profiles, replacing its initial poll with checks.
	i := strings.LastIndex(script, "poll();")
	if i < 0 {
		t.Fatal("initial poll not found")
	}
	script = script[:i] + string(checks) + script[i+len("poll();"):]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/assets/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Write([]byte(script))
			return
		case "/assets/app.css":
			w.Header().Set("Content-Type", "text/css")
			w.Write(appCSS)
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
		init := "Object.defineProperty(navigator, 'platform', { value: " + string(marshal(platform)) + " });\n"
		w.Write([]byte(strings.Replace(string(indexHTML), `<script src="/assets/app.js">`, "<script>"+init+`</script><script src="/assets/app.js">`, 1)))
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
					"--user-data-dir=" + t.TempDir(), "--window-size=" + scenario.size, "--dump-dom", "--timeout=10000"}
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
