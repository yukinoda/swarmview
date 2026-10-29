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
	noCommunication := flag.Bool("no-communication", false, "turn off the live communication bridge that the desktop app enables by default (no effect on the CLI, which is already off)")
	claudeHook := flag.Bool("claude-hook", false, "forward one Claude Code hook event from stdin to a running -communication instance")
	flag.Parse()
	if *communication && *noCommunication {
		fmt.Fprintln(os.Stderr, communicationConflictMessage)
		os.Exit(2)
	}
	if *claudeHook {
		runClaudeHook(os.Stdin)
		return
	}
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(32 << 20) // safety net only
	if err := runServer(*desktop, *communication, *noCommunication); err != nil {
		log.Print(err) // runServer's owned-resource defers have already finished.
		os.Exit(1)
	}
}

const communicationConflictMessage = "-communication and -no-communication contradict each other"

// communicationMode returns whether to start the bridge and whether failing to
// start it is fatal. -communication is explicit, so required. The desktop app
// wants it by default (unless -no-communication), but only as an optional
// feature. -no-communication is a no-op outside -desktop: the CLI is already off.
func communicationMode(desktop, communication, noCommunication bool) (want, required bool) {
	required = communication
	want = communication || (desktop && !noCommunication)
	return want, required
}

// startCommunication starts the bridge when wanted and returns its cleanup.
// A failure is fatal only when required; otherwise the server runs without it.
func startCommunication(s *server, want, required bool) (func(), error) {
	if !want {
		return func() {}, nil
	}
	bridge := newPulseBridge()
	err := bridge.start(s)
	if err == nil {
		s.pulse = bridge
		return bridge.close, nil
	}
	if required {
		if errors.Is(err, errPulseOwned) {
			err = fmt.Errorf("%w (the desktop app enables it by default; quit it or launch it with --no-communication)", err)
		}
		return nil, err
	}
	log.Printf("live communication unavailable: %v", err)
	return func() {}, nil
}

func runServer(desktop, communication, noCommunication bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	want, required := communicationMode(desktop, communication, noCommunication)
	closeBridge, err := startCommunication(s, want, required)
	if err != nil {
		return err
	}
	defer closeBridge()
	fmt.Printf("http://127.0.0.1:%d\n", s.port)
	if err := serve(s, ln, parent); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
