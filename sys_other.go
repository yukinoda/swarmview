//go:build !darwin

package main

// sysctlU32 is nil off macOS so refreshSystem skips the macOS-only sysctl calls.
var sysctlU32 func(name string) (uint32, bool)

func ghosttyInstalled() bool { return false }

func focusGhostty(string, Runner) (bool, string) {
	return false, "Ghostty window control requires macOS"
}
