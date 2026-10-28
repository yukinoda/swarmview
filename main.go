package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
)

func main() {
	desktop := flag.Bool("desktop", false, "use the desktop app's port and exit when stdin closes")
	communication := flag.Bool("communication", false, "observe live metadata from the optional OpenCode TUI bridge and Claude hooks")
	claudeHook := flag.Bool("claude-hook", false, "forward one Claude Code hook event from stdin to a running -communication instance")
	flag.Parse()
	if *claudeHook {
		runClaudeHook(os.Stdin)
		return
	}
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(32 << 20) // safety net only
	if err := runServer(*desktop, *communication); err != nil {
		log.Print(err) // runServer's owned-resource defers have already finished.
		os.Exit(1)
	}
}

func runServer(desktop, communication bool) error {
	// Ctrl-C, SIGTERM and a closed terminal (SIGHUP) all remove the owned socket.
	// A signal ignored at startup (nohup, a backgrounded script) stays ignored:
	// watching it would un-ignore it.
	var signals []os.Signal
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			signals = append(signals, sig)
		}
	}
	ctx := context.Background()
	stop := context.CancelFunc(func() {})
	// With no signals, NotifyContext would relay every signal; skip it then.
	if len(signals) > 0 {
		ctx, stop = signal.NotifyContext(ctx, signals...)
	}
	defer stop()
	s := newServer(execRun)
	s.sysU32 = sysctlU32
	if n, err := strconv.Atoi(os.Getenv("PORT")); err == nil && n != 0 {
		s.port = n
	}
	var parent io.Reader
	if desktop {
		s.port, parent = 4778, os.Stdin
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()
	if communication {
		s.pulse = newPulseBridge()
		if err := s.pulse.start(s); err != nil {
			return err
		}
		defer s.pulse.close()
	}
	fmt.Printf("http://127.0.0.1:%d\n", s.port)
	if err := serve(s, ln, parent); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
