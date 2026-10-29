package main

import "testing"

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
