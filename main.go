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
	noCommunication := flag.Bool("no-communication", false, "only with -desktop: turn off the live communication bridge that the desktop app enables by default")
	claudeHook := flag.Bool("claude-hook", false, "forward one Claude Code hook event from stdin to a running -communication instance")
	flag.Parse()
	if err := checkCommunicationFlags(*desktop, *communication, *noCommunication); err != nil {
		fmt.Fprintln(os.Stderr, err)
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

// communicationWanted is on for an explicit -communication, and by default for
// the desktop app unless it was launched with -no-communication. The CLI stays opt-in.
func communicationWanted(desktop, communication, noCommunication bool) bool {
	if communication {
		return true
	}
	return desktop && !noCommunication
}

func checkCommunicationFlags(desktop, communication, noCommunication bool) error {
	if communication && noCommunication {
		return errors.New("-communication and -no-communication contradict each other")
	}
	if noCommunication && !desktop {
		return errors.New("-no-communication only applies with -desktop; the CLI is already off by default")
	}
	return nil
}

// startCommunication starts the bridge when wanted and returns its cleanup.
func startCommunication(s *server, desktop, communication, noCommunication bool) (func(), error) {
	if !communicationWanted(desktop, communication, noCommunication) {
		return func() {}, nil
	}
	bridge := newPulseBridge()
	err := bridge.start(s)
	if err == nil {
		s.pulse = bridge
		return bridge.close, nil
	}
	if communication || !errors.Is(err, errPulseOwned) {
		return nil, err
	}
	// Only the expected conflict is tolerated: the app's default must not
	// stop it launching while another Swarmview owns the socket.
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
	closeBridge, err := startCommunication(s, desktop, communication, noCommunication)
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
