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
	communication := flag.Bool("communication", false, "observe live metadata from the optional OpenCode TUI bridge")
	flag.Parse()
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(32 << 20) // safety net only
	if err := runServer(*desktop, *communication); err != nil {
		log.Print(err) // runServer's owned-resource defers have already finished.
		os.Exit(1)
	}
}

func runServer(desktop, communication bool) error {
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
