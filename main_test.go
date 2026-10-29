package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommunicationMode(t *testing.T) {
	cases := []struct {
		name            string
		desktop         bool
		communication   bool
		noCommunication bool
		wantWant        bool
		wantRequired    bool
	}{
		{"cli default stays off", false, false, false, false, false},
		{"cli opt-in is required", false, true, false, true, true},
		{"cli -no-communication is a no-op", false, false, true, false, false},
		{"desktop default is on but optional", true, false, false, true, false},
		{"desktop opt-out", true, false, true, false, false},
		{"desktop explicit is required", true, true, false, true, true},
	}
	for _, c := range cases {
		want, required := communicationMode(c.desktop, c.communication, c.noCommunication)
		if want != c.wantWant || required != c.wantRequired {
			t.Errorf("%s: got want=%v required=%v, expected %v/%v", c.name, want, required, c.wantWant, c.wantRequired)
		}
	}
}

func newCommunicationServer() *server {
	return srvWith(func(string, ...string) (string, error) { return "", nil })
}

func TestStartCommunicationDesktopDefaultOpensSocket(t *testing.T) {
	_, path := pulseHome(t)
	s := newCommunicationServer()
	closeBridge, err := startCommunication(s, true, false)
	must(t, err)
	defer closeBridge()
	if s.pulse == nil {
		t.Fatal("desktop default did not start the bridge")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("desktop default did not create the socket: %v", err)
	}
}

func TestStartCommunicationCLIStaysOff(t *testing.T) {
	_, path := pulseHome(t)
	s := newCommunicationServer()
	closeBridge, err := startCommunication(s, false, false)
	must(t, err)
	defer closeBridge()
	if s.pulse != nil {
		t.Fatal("CLI default started the bridge")
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("CLI default created the socket")
	}
}

func TestStartCommunicationHeldSocket(t *testing.T) {
	pulseHome(t)
	holder := newPulseBridge()
	must(t, holder.start(newCommunicationServer()))
	defer holder.close()

	s := newCommunicationServer()
	closeBridge, err := startCommunication(s, true, false)
	if err != nil {
		t.Fatalf("desktop default must fall back when the socket is owned: %v", err)
	}
	defer closeBridge()
	if s.pulse != nil {
		t.Fatal("fallback still reports communication on")
	}

	_, err = startCommunication(newCommunicationServer(), true, true)
	if !errors.Is(err, errPulseOwned) {
		t.Fatalf("explicit -communication must fail on a held socket, got %v", err)
	}
}

func TestStartCommunicationRealErrors(t *testing.T) {
	pulseHome(t)
	cache, err := os.UserCacheDir()
	must(t, err)
	must(t, os.Chmod(filepath.Join(cache, "swarmview"), 0755))

	s := newCommunicationServer()
	closeBridge, err := startCommunication(s, true, false)
	if err != nil {
		t.Fatalf("a default-on bridge must never stop the app launching: %v", err)
	}
	defer closeBridge()
	if s.pulse != nil {
		t.Fatal("failed default bridge still reports communication on")
	}

	_, err = startCommunication(newCommunicationServer(), true, true)
	if err == nil {
		t.Fatal("explicit -communication must fail on a bad communication directory")
	}
	if errors.Is(err, errPulseOwned) {
		t.Fatalf("directory error misclassified as a conflict: %v", err)
	}
}
