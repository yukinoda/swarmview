package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strconv"
)

func main() {
	desktop := flag.Bool("desktop", false, "use the desktop app's port and exit when stdin closes")
	flag.Parse()
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(32 << 20) // safety net only
	s := newServer(execRun)
	s.sysU32 = sysctlU32
	if n, err := strconv.Atoi(os.Getenv("PORT")); err == nil && n != 0 {
		s.port = n
	}
	var parent io.Reader
	if *desktop {
		s.port, parent = 4778, os.Stdin
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("http://127.0.0.1:%d\n", s.port)
	if err := serve(s, ln, parent); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}
