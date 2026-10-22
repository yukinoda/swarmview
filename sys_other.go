//go:build !darwin

package main

func sysctlU32(string) (uint32, bool) { return 0, false }

func ghosttyInstalled() bool { return false }

func focusGhostty(string, Runner) (bool, string) {
	return false, "Ghostty window control requires macOS"
}
