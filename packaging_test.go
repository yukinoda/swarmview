package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalBridgeReleaseArchive(t *testing.T) {
	// Exercise the actual workflow tar command with a synthetic binary; never run a server.
	for _, name := range []string{"bash", "tar"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(name + " required for release archive fixture")
		}
	}
	workflow, err := os.ReadFile(".github/workflows/release.yml")
	must(t, err)
	start := strings.Index(string(workflow), "          COPYFILE_DISABLE=1 tar -czf ")
	if start < 0 {
		t.Fatal("release archive command missing")
	}
	var command strings.Builder
	for _, line := range strings.Split(string(workflow)[start:], "\n") {
		line = strings.TrimSpace(line)
		command.WriteString(strings.TrimSuffix(line, "\\"))
		command.WriteByte(' ')
		if !strings.HasSuffix(line, "\\") {
			break
		}
	}
	root, err := os.Getwd()
	must(t, err)
	fixture := t.TempDir()
	must(t, os.Mkdir(filepath.Join(fixture, "dist"), 0700))
	must(t, os.WriteFile(filepath.Join(fixture, "dist", "swarmview"), []byte("synthetic binary\n"), 0755))
	cmd := exec.Command("bash", "-euc", command.String())
	cmd.Dir = fixture
	cmd.Env = append(os.Environ(), "BASH_ENV=", "GITHUB_REF_NAME=v0.0.0-fixture", "TARGET=native-fixture", "GITHUB_WORKSPACE="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release archive: %v\n%s", err, out)
	}
	f, err := os.Open(filepath.Join(fixture, "swarmview_v0.0.0-fixture_native-fixture.tar.gz"))
	must(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	must(t, err)
	defer gz.Close()
	reader := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		must(t, err)
		if header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(reader)
			must(t, err)
			files[strings.TrimPrefix(header.Name, "./")] = data
		}
	}
	bridgeFiles := []string{"package.json", "tui.ts", "observer.js", "README.md"}
	required := []string{"LICENSE", "README.md"}
	must(t, filepath.WalkDir("assets", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			required = append(required, filepath.ToSlash(path))
		}
		return nil
	}))
	for _, file := range bridgeFiles {
		required = append(required, "integrations/opencode/"+file)
	}
	for _, name := range required {
		source, err := os.ReadFile(filepath.FromSlash(name))
		must(t, err)
		if !bytes.Equal(files[name], source) {
			t.Fatal("archive missing or changed required file", name)
		}
	}
	var packaged []string
	for name := range files {
		if strings.HasPrefix(name, "integrations/opencode/") {
			packaged = append(packaged, name)
		}
		if strings.Contains(name, "node_modules/") || strings.HasSuffix(name, "_test.js") || strings.HasSuffix(name, "_test.mjs") {
			t.Fatal("release includes optional test/dependency files", name)
		}
	}
	if len(packaged) != len(bridgeFiles) {
		t.Fatalf("expected only four optional bridge files, got %v", packaged)
	}
	var pkg struct {
		Exports map[string]string `json:"exports"`
	}
	must(t, json.Unmarshal(files["integrations/opencode/package.json"], &pkg))
	if pkg.Exports["./tui"] != "./tui.ts" {
		t.Fatal("packaged TUI entrypoint mismatch")
	}
	if !bytes.Contains(files["integrations/opencode/tui.ts"], []byte("'./observer.js'")) {
		t.Fatal("local runtime import must be distributed with entrypoint")
	}
	if !bytes.Contains(files["README.md"], []byte("integrations/opencode/README.md")) {
		t.Fatal("CLI bridge README link missing")
	}
}
