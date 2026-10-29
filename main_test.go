package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommunicationWanted(t *testing.T) {
	cases := []struct {
		name            string
		desktop         bool
		communication   bool
		noCommunication bool
		want            bool
	}{
		{"cli default stays off", false, false, false, false},
		{"cli opt-in", false, true, false, true},
		{"desktop default is on", true, false, false, true},
		{"desktop opt-out", true, false, true, false},
		{"explicit flag beats desktop opt-out", true, true, true, true},
	}
	for _, c := range cases {
		got := communicationWanted(c.desktop, c.communication, c.noCommunication)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckCommunicationFlags(t *testing.T) {
	cases := []struct {
		name            string
		desktop         bool
		communication   bool
		noCommunication bool
		wantErr         bool
	}{
		{"cli default", false, false, false, false},
		{"cli opt-in", false, true, false, false},
		{"desktop opt-out", true, false, true, false},
		{"contradiction", true, true, true, true},
		{"opt-out without desktop", false, false, true, true},
	}
	for _, c := range cases {
		err := checkCommunicationFlags(c.desktop, c.communication, c.noCommunication)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}

func newCommunicationServer() *server {
	return srvWith(func(string, ...string) (string, error) { return "", nil })
}

func TestStartCommunicationDesktopDefaultOpensSocket(t *testing.T) {
	_, path := pulseHome(t)
	s := newCommunicationServer()
	closeBridge, err := startCommunication(s, true, false, false)
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
	closeBridge, err := startCommunication(s, false, false, false)
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
	closeBridge, err := startCommunication(s, true, false, false)
	if err != nil {
		t.Fatalf("desktop default must fall back when the socket is owned: %v", err)
	}
	defer closeBridge()
	if s.pulse != nil {
		t.Fatal("fallback still reports communication on")
	}

	_, err = startCommunication(newCommunicationServer(), false, true, false)
	if !errors.Is(err, errPulseOwned) {
		t.Fatalf("explicit -communication must fail on a held socket, got %v", err)
	}
	_, err = startCommunication(newCommunicationServer(), true, true, false)
	if !errors.Is(err, errPulseOwned) {
		t.Fatalf("explicit -communication must fail in desktop mode too, got %v", err)
	}
}

func TestStartCommunicationRealErrorsStillFail(t *testing.T) {
	pulseHome(t)
	cache, err := os.UserCacheDir()
	must(t, err)
	must(t, os.Chmod(filepath.Join(cache, "swarmview"), 0755))

	s := newCommunicationServer()
	_, err = startCommunication(s, true, false, false)
	if err == nil {
		t.Fatal("a bad communication directory must abort desktop startup")
	}
	if errors.Is(err, errPulseOwned) {
		t.Fatalf("directory error misclassified as a conflict: %v", err)
	}
}
