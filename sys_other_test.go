//go:build !darwin

package main

import "testing"

func TestNonDarwinSkipsSysctl(t *testing.T) {
	var sysctlRuns int
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "sysctl" {
			sysctlRuns++
		}
		return "", nil
	})
	s.sysU32 = sysctlU32

	_, err := s.snapshot(1000)
	must(t, err)

	if sysctlRuns != 0 {
		t.Errorf("sysctl ran %d times on a non-darwin build", sysctlRuns)
	}
}
