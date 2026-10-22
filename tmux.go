package main

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type tmuxPane struct{ id, title, session string }

var paneRe = regexp.MustCompile(`^%[0-9]+$`)

func tmuxTaskTitle(window, pane string) string {
	pane = strings.TrimSpace(pane)
	// OpenCode's label and Claude's status symbols precede the task title.
	for _, prefix := range []string{"OC | ", "✳ ", "✻ ", "✽ ", "✶ ", "✢ "} {
		if title, ok := strings.CutPrefix(pane, prefix); ok && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	// Codex publishes "task | project". Requiring both parts avoids shell titles.
	if task, project, ok := strings.Cut(pane, " | "); ok && strings.TrimSpace(task) != "" && strings.TrimSpace(project) != "" {
		return pane
	}
	window = strings.TrimSpace(window)
	base := strings.ToLower(path.Base(strings.TrimPrefix(window, "-")))
	if slices.Contains([]string{"sh", "bash", "zsh", "fish", "dash", "ksh", "nu", "nushell", "pwsh", "shell"}, base) || slices.Contains(kinds, base) || slices.Contains(runtimes, base) {
		return ""
	}
	return window
}

func parseTmuxPanes(text string) map[int]tmuxPane {
	panes := map[int]tmuxPane{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.SplitN(line, "\t", 5)
		if len(f) != 5 || !paneRe.MatchString(f[0]) {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err == nil && pid > 0 {
			panes[pid] = tmuxPane{f[0], tmuxTaskTitle(f[2], f[4]), strings.TrimSpace(f[3])}
		}
	}
	return panes
}

// Match process ancestry, never a cwd heuristic: several agents can share a worktree.
func (t *procTree) paneFor(pid int, panes map[int]tmuxPane) (tmuxPane, bool) {
	seen := map[int]bool{}
	for pid > 0 && !seen[pid] {
		if pane, ok := panes[pid]; ok {
			return pane, true
		}
		seen[pid] = true
		pid = t.parents[pid]
	}
	return tmuxPane{}, false
}

func focusAgent(pid int, agents []Agent, run Runner, ghostty bool) (bool, string) {
	var agent *Agent
	for i := range agents {
		if agents[i].Pid == pid {
			agent = &agents[i]
			break
		}
	}
	if agent == nil {
		return false, "unknown pid"
	}
	if !paneRe.MatchString(agent.pane) {
		return false, "not in a tmux pane"
	}
	if ghostty {
		return focusGhostty(agent.pane, run)
	}
	if !agent.CanFocus {
		return false, "attach a tmux client first"
	}
	// tmux chooses the most recently used client when called outside a client.
	// A pane target selects its session, window, and pane in one command.
	if _, err := run("tmux", "switch-client", "-t", agent.pane); err != nil {
		return false, "tmux switch failed; attach a client first"
	}
	return true, ""
}
