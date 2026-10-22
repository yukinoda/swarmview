package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	checks, err := os.ReadFile("ui_test.js")
	must(t, err)
	page := string(indexHTML)
	// Run the real UI in fresh profiles, replacing its initial poll with checks.
	i := strings.LastIndex(page, "poll();")
	if i < 0 {
		t.Fatal("initial poll not found")
	}
	page = page[:i] + string(checks) + page[i+len("poll();"):]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/logo.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write(logoSVG)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		platform := "Linux"
		if r.URL.Query().Get("platform") == "MacIntel" {
			platform = "MacIntel"
		}
		init := "Object.defineProperty(navigator, 'platform', { value: " + string(marshal(platform)) + " });\n"
		w.Write([]byte(strings.Replace(page, "<script>", "<script>"+init, 1)))
	}))
	defer srv.Close()
	for _, platform := range []string{"MacIntel", "Linux"} {
		t.Run(platform, func(t *testing.T) {
			var out []byte
			var err error
			// A cold Chrome on a fresh CI runner can hang at launch; one retry absorbs it.
			for attempt := 0; attempt < 2; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				cmd := exec.CommandContext(ctx, chrome, "--headless", "--no-sandbox", "--disable-gpu",
					"--user-data-dir="+t.TempDir(), "--window-size=375,812", "--dump-dom", "--timeout=10000", srv.URL+"?platform="+platform)
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
