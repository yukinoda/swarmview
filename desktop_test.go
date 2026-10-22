package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDesktopLifetime(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer ln.Close()
	parent, owner := io.Pipe()
	defer parent.Close()
	defer owner.Close()
	s := srvWith(func(string, ...string) (string, error) { return "", nil })
	s.port = ln.Addr().(*net.TCPAddr).Port
	done := make(chan error, 1)
	go func() { done <- serve(s, ln, parent) }()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + ln.Addr().String() + "/agents")
	must(t, err)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owned server returned %d", response.StatusCode)
	}
	owner.Close() // Also what the kernel does when the GUI is killed.
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("unexpected shutdown error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("desktop server survived its owner's pipe")
	}
	if connection, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		connection.Close()
		t.Fatal("desktop listener survived shutdown")
	}
}
