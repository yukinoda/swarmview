package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func pulseHome(t *testing.T) (string, string) {
	t.Helper()
	// t.TempDir() makes the unix socket path too long on macOS.
	home, err := os.MkdirTemp("", "s-")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	cache, err := os.UserCacheDir()
	must(t, err)
	dir := filepath.Join(cache, "swarmview")
	must(t, os.MkdirAll(dir, 0700))
	return home, filepath.Join(dir, "live.sock")
}

func stalePulseSocket(t *testing.T, path string) os.FileInfo {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	must(t, err)
	ln.SetUnlinkOnClose(false)
	must(t, os.Chmod(path, 0600))
	must(t, ln.Close())
	fi, err := os.Lstat(path)
	must(t, err)
	return fi
}

func TestPulseStaleRecovery(t *testing.T) {
	_, path := pulseHome(t)
	stalePulseSocket(t, path)
	b := newPulseBridge()
	must(t, b.start(&server{}))
	b.close()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("owned socket survived close", err)
	}
	lock, err := os.Lstat(filepath.Join(filepath.Dir(path), "live.lock"))
	must(t, err)
	if !ownedPulseFile(lock, 0) || lock.Size() != 0 {
		t.Fatal("lock must remain empty/private, not an event log")
	}
	// A legacy/live listener may not hold the new lock. A failed probe must not replace it.
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	must(t, err)
	defer ln.Close()
	must(t, os.Chmod(path, 0600))
	before, err := os.Lstat(path)
	must(t, err)
	if err := newPulseBridge().start(&server{}); err == nil {
		t.Fatal("replaced legacy live listener")
	}
	after, err := os.Lstat(path)
	must(t, err)
	if !os.SameFile(before, after) {
		t.Fatal("live socket replaced")
	}
}

func TestPulseFailedStartReleasesLock(t *testing.T) {
	_, path := pulseHome(t)
	must(t, os.WriteFile(path, []byte("keep unexpected data"), 0600))
	b := newPulseBridge()
	if err := b.start(&server{}); err == nil {
		t.Fatal("accepted unexpected file")
	}
	must(t, os.Rename(path, path+".saved")) // move this owned fixture, never repair user files
	must(t, b.start(&server{}))
	b.close()
	data, err := os.ReadFile(path + ".saved")
	must(t, err)
	if string(data) != "keep unexpected data" {
		t.Fatal("failed startup damaged unrelated data")
	}
}

func TestPulseUnexpectedFiles(t *testing.T) {
	for _, kind := range []string{"regular", "symlink", "socket-mode", "lock-data", "lock-symlink", "lock-mode", "lock-hardlink"} {
		t.Run(kind, func(t *testing.T) {
			_, path := pulseHome(t)
			lock := filepath.Join(filepath.Dir(path), "live.lock")
			marker := filepath.Join(filepath.Dir(path), "keep")
			must(t, os.WriteFile(marker, []byte("keep user data"), 0600))
			switch kind {
			case "regular":
				must(t, os.WriteFile(path, []byte("not a socket"), 0600))
			case "symlink":
				must(t, os.Symlink(marker, path))
			case "socket-mode":
				stalePulseSocket(t, path)
				must(t, os.Chmod(path, 0644))
			case "lock-data":
				must(t, os.WriteFile(lock, []byte("not our lock"), 0600))
			case "lock-symlink":
				must(t, os.Symlink(marker, lock))
			case "lock-mode":
				must(t, os.WriteFile(lock, nil, 0644))
				must(t, os.Chmod(lock, 0644))
			case "lock-hardlink":
				empty := filepath.Join(filepath.Dir(path), "empty")
				must(t, os.WriteFile(empty, nil, 0600))
				must(t, os.Link(empty, lock))
			}
			target := path
			if strings.HasPrefix(kind, "lock-") {
				target = lock
			}
			before, err := os.Lstat(target)
			must(t, err)
			var content []byte
			if before.Mode().IsRegular() {
				content, err = os.ReadFile(target)
				must(t, err)
			}
			if err := newPulseBridge().start(&server{}); err == nil {
				t.Fatal("accepted unexpected endpoint", kind)
			}
			after, err := os.Lstat(target)
			must(t, err)
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("modified unknown file")
			}
			if before.Mode().IsRegular() {
				data, e := os.ReadFile(target)
				must(t, e)
				if !bytes.Equal(content, data) {
					t.Fatal("changed unknown file data")
				}
			}
			data, err := os.ReadFile(marker)
			must(t, err)
			if string(data) != "keep user data" {
				t.Fatal("followed/replaced symlink target")
			}
		})
	}
	t.Run("directory-symlink", func(t *testing.T) {
		_, path := pulseHome(t)
		dir := filepath.Dir(path)
		target := dir + "-owned"
		must(t, os.Rename(dir, target))
		must(t, os.Symlink(target, dir))
		if err := newPulseBridge().start(&server{}); err == nil {
			t.Fatal("followed a symlink directory")
		}
		if _, err := os.Lstat(filepath.Join(target, "live.lock")); !os.IsNotExist(err) {
			t.Fatal("wrote through directory symlink")
		}
	})
}

func TestPulseOwnerRaces(t *testing.T) {
	_, path := pulseHome(t)
	const attempts = 8
	type result struct {
		b   *pulseBridge
		err error
	}
	results := make(chan result, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b := newPulseBridge(); results <- result{b, b.start(&server{})} }()
	}
	wg.Wait()
	close(results)
	var owner *pulseBridge
	count := 0
	for r := range results {
		if r.err == nil {
			owner = r.b
			count++
		}
	}
	if count != 1 {
		t.Fatalf("simultaneous owners: %d", count)
	}
	defer owner.close()
	// Replace the pathname with a different live inode in this owned fixture.
	old := owner.inode
	must(t, os.Remove(path))
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	must(t, err)
	defer replacement.Close()
	must(t, os.Chmod(path, 0600))
	current, err := os.Lstat(path)
	must(t, err)
	if os.SameFile(old, current) {
		t.Fatal("fixture did not replace inode")
	}
	if removePulseSocket(path, old) == nil {
		t.Fatal("stale recovery accepted replacement inode")
	}
	owner.close()
	owner.close()
	after, err := os.Lstat(path)
	must(t, err)
	if !os.SameFile(current, after) {
		t.Fatal("old close unlinked replacement owner")
	}
	if err := newPulseBridge().start(&server{}); err == nil {
		t.Fatal("new starter replaced live replacement")
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	must(t, err)
	c.Close()
}

// Run production main in an owned subprocess, without launching an agent or browser.
func TestPulseCLIHelper(t *testing.T) {
	if os.Getenv("SWARMVIEW_PULSE_HELPER") != "1" {
		return
	}
	os.Args = []string{"swarmview"}
	if os.Getenv("SWARMVIEW_PULSE_ENABLED") == "1" {
		os.Args = append(os.Args, "--communication")
	}
	main()
	os.Exit(0)
}

type pulseChild struct {
	cmd    *exec.Cmd
	exit   chan struct{}
	ready  chan string
	err    error
	stderr bytes.Buffer
}

func pulseChildPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	return port
}

func startPulseChild(t *testing.T, home, port string, enabled bool) *pulseChild {
	t.Helper()
	p := &pulseChild{exit: make(chan struct{}), ready: make(chan string, 1)}
	p.cmd = exec.Command(os.Args[0], "-test.run=^TestPulseCLIHelper$")
	flag := "0"
	if enabled {
		flag = "1"
	}
	p.cmd.Env = append(os.Environ(), "SWARMVIEW_PULSE_HELPER=1", "SWARMVIEW_PULSE_ENABLED="+flag, "HOME="+home, "XDG_CACHE_HOME="+home, "PORT="+port)
	out, err := p.cmd.StdoutPipe()
	must(t, err)
	p.cmd.Stderr = &p.stderr
	must(t, p.cmd.Start())
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); p.ready <- line }()
	go func() { p.err = p.cmd.Wait(); close(p.exit) }()
	t.Cleanup(func() {
		select {
		case <-p.exit:
		default:
			p.cmd.Process.Kill()
			<-p.exit
		}
	})
	return p
}

func (p *pulseChild) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.exit:
		return p.err
	case <-time.After(5 * time.Second):
		p.cmd.Process.Kill()
		<-p.exit
		t.Fatalf("owned child did not exit: %s", p.stderr.String())
		return p.err
	}
}

func (p *pulseChild) awaitReady(t *testing.T) {
	t.Helper()
	select {
	case line := <-p.ready:
		if !strings.HasPrefix(line, "http://127.0.0.1:") {
			_ = p.wait(t)
			t.Fatalf("owned child startup failed: %s", p.stderr.String())
		}
	case <-time.After(5 * time.Second):
		p.cmd.Process.Kill()
		_ = p.wait(t)
		t.Fatalf("owned child not ready: %s", p.stderr.String())
	}
}

func (p *pulseChild) stop(t *testing.T, sig os.Signal) {
	t.Helper()
	must(t, p.cmd.Process.Signal(sig))
	must(t, p.wait(t))
}

func TestPulseCLILifecycle(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			home, path := pulseHome(t)
			for i := 0; i < 2; i++ {
				p := startPulseChild(t, home, pulseChildPort(t), true)
				p.awaitReady(t)
				if _, err := os.Lstat(path); err != nil {
					t.Fatal(err)
				}
				p.stop(t, sig)
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("graceful shutdown left socket", err)
				}
				if _, err := os.Lstat(liveHookPath(filepath.Dir(path))); !os.IsNotExist(err) {
					t.Fatal("graceful shutdown left the hook copy", err)
				}
			}
		})
	}
	t.Run("killed-owner-recovery", func(t *testing.T) {
		home, path := pulseHome(t)
		p := startPulseChild(t, home, pulseChildPort(t), true)
		p.awaitReady(t)
		must(t, p.cmd.Process.Kill())
		if p.wait(t) == nil {
			t.Fatal("SIGKILL unexpectedly graceful")
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("fixture did not leave stale socket", err)
		}
		next := startPulseChild(t, home, pulseChildPort(t), true)
		next.awaitReady(t)
		next.stop(t, syscall.SIGTERM)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("recovered socket did not clean up")
		}
	})
	t.Run("tcp-startup-failure", func(t *testing.T) {
		home, path := pulseHome(t)
		p := startPulseChild(t, home, "-1", true)
		if p.wait(t) == nil {
			t.Fatal("invalid TCP port succeeded")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("TCP startup failure left socket", err)
		}
		next := startPulseChild(t, home, pulseChildPort(t), true)
		next.awaitReady(t)
		next.stop(t, syscall.SIGTERM)
	})
	t.Run("live-refusal-unwinds-http", func(t *testing.T) {
		home, path := pulseHome(t)
		owner := startPulseChild(t, home, pulseChildPort(t), true)
		owner.awaitReady(t)
		before, err := os.Lstat(path)
		must(t, err)
		port := pulseChildPort(t)
		other := startPulseChild(t, home, port, true)
		if other.wait(t) == nil {
			t.Fatal("replaced existing owner")
		}
		after, err := os.Lstat(path)
		must(t, err)
		if !os.SameFile(before, after) {
			t.Fatal("refused starter unlinked owner's socket")
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+port)
		must(t, err)
		ln.Close()
		owner.stop(t, syscall.SIGTERM)
	})
	t.Run("default-off", func(t *testing.T) {
		home, path := pulseHome(t)
		p := startPulseChild(t, home, pulseChildPort(t), false)
		p.awaitReady(t)
		p.stop(t, syscall.SIGTERM)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("default opened bridge")
		}
	})
}
