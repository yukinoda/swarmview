package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func nowMs() int64 { return time.Now().UnixMilli() }

func TestClaudeGeneratedTitle(t *testing.T) {
	root, cwd := filepath.Join(t.TempDir(), "projects"), "/work/demo"
	id := "11111111-1111-1111-1111-111111111111"
	metadata := filepath.Join(filepath.Dir(root), "sessions", "100.json")
	transcript := filepath.Join(root, claudeSlug(cwd), id+".jsonl")
	write := func(file, data string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(file), 0700))
		must(t, os.WriteFile(file, []byte(data), 0600))
	}
	meta := func(pid int, session, dir string) string {
		return string(marshal(map[string]any{"pid": pid, "sessionId": session, "cwd": dir}))
	}
	record := func(title, session string) string {
		return `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + session + `"}` + "\n"
	}
	reply := func(model, session string) string {
		return `{"parentUuid":"p","message":{"model":"` + model + `","role":"assistant"},"type":"assistant","sessionId":"` + session + `"}` + "\n"
	}
	write(metadata, meta(100, id, cwd))
	write(transcript, strings.Repeat("x", 70<<10)+"\n"+record("Old title", id)+reply("claude-sonnet-5-5", id)+record("Generated task", id)+
		reply("claude-opus-5-5", id)+reply("<synthetic>", id)+reply("claude-haiku-5-5", "other")+record("Wrong session", "other")+`{"type":"ai-title",`)
	c := &sessionTitle{}
	if got := claudeSessionTitle(root, 100, math.NaN(), cwd, nowMs(), c); got != "Generated task" || c.model != "claude-opus-5-5" {
		t.Fatalf("generated title = %q, model = %q", got, c.model)
	}
	paneTitle := "✳ Claude Code"
	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "100 10 1000 0:01 01:00 claude\n", nil
		}
		if cmd == "tmux" && args[0] == "list-panes" {
			return "%1\t100\tzsh\tdev\t" + paneTitle + "\n", nil
		}
		return "", errors.New("unavailable")
	})
	s.root = root
	s.cache[100] = &cacheEntry{cwd: cwd, etime: "01:00"}
	if a := mustList(t, s, 1000)[0]; a.Title == nil || *a.Title != "Generated task" {
		t.Fatalf("generic pane must use generated title: %+v", a)
	}
	paneTitle = "✳ Renamed task"
	if a := mustList(t, s, 2000)[0]; a.Title == nil || *a.Title != "Renamed task" || a.Model != "claude-opus-5-5" {
		t.Fatalf("rename must take precedence and keep the model: %+v", a)
	}
	for _, data := range []string{meta(200, id, cwd), meta(100, "../invalid", cwd), "{"} {
		write(metadata, data)
		if got := claudeSessionTitle(root, 100, math.NaN(), cwd, nowMs(), &sessionTitle{}); got != "" {
			t.Fatalf("invalid session metadata returned %q", got)
		}
	}
}

func TestClaudeTitleCachedByFileKey(t *testing.T) {
	root, cwd, now := filepath.Join(t.TempDir(), "projects"), "/work/demo", nowMs()
	id, id2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	metadata := filepath.Join(filepath.Dir(root), "sessions", "100.json")
	log := func(session string) string { return filepath.Join(root, claudeSlug(cwd), session+".jsonl") }
	write := func(file, data string, ageMs int64) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(file), 0700))
		must(t, os.WriteFile(file, []byte(data), 0600))
		tm := time.UnixMilli(now - ageMs)
		must(t, os.Chtimes(file, tm, tm))
	}
	meta := func(session string) string {
		return string(marshal(map[string]any{"pid": 100, "sessionId": session, "cwd": cwd}))
	}
	record := func(title, session string) string {
		return `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + session + `"}` + "\n"
	}
	c := &sessionTitle{}
	title := func() string { return claudeSessionTitle(root, 100, math.NaN(), cwd, now, c) }
	write(metadata, meta(id), 10e3)
	write(log(id), record("Title A", id), 10e3)
	if got := title(); got != "Title A" {
		t.Fatalf("initial = %q", got)
	}
	write(log(id), record("Title Z", id), 10e3) // same size and mtime: must not be re-read
	if got := title(); got != "Title A" {
		t.Errorf("unchanged key re-read: %q", got)
	}
	write(log(id), record("Title A", id)+`{"type":"assistant","sessionId":"`+id+`","message":{"model":"claude-opus-5-5"}}`+"\n", 10e3)
	if title(); c.model != "claude-opus-5-5" {
		t.Errorf("model = %q", c.model)
	}
	write(log(id), record("Title A", id)+record("Title B", id), 10e3) // no assistant record left in the tail
	if got := title(); got != "Title B" || c.model != "claude-opus-5-5" {
		t.Errorf("after append = %q, model must be kept: %q", got, c.model)
	}
	write(metadata, meta(id)+" ", 8e3) // status rewrite, same session
	if got := title(); got != "Title B" || c.model != "claude-opus-5-5" {
		t.Errorf("after metadata rewrite = %q, model must be kept: %q", got, c.model)
	}
	write(metadata, `{"pid":100`, 9e3) // partially written status update: decode error
	if got := title(); got != "Title B" || c.model != "claude-opus-5-5" {
		t.Errorf("after failed metadata read = %q, model must be kept: %q", got, c.model)
	}
	write(metadata, meta(id)+"  ", 7e3) // complete again, same session
	if got := title(); got != "Title B" || c.model != "claude-opus-5-5" {
		t.Errorf("after metadata recovered = %q, model must be kept: %q", got, c.model)
	}
	write(log(id), record("Title C", id)+record("Title B", id), 0) // same size, mtime in this tick
	if got := title(); got != "Title B" {
		t.Errorf("after same-tick write = %q", got)
	}
	write(log(id), record("Title B", id)+record("Title C", id), 0) // same size and mtime again
	if got := title(); got != "Title C" {
		t.Errorf("unsettled file must be re-read: %q", got)
	}
	write(log(id2), record("Title D", id2), 10e3)
	write(metadata, meta(id2), 5e3) // same size, new session
	if got := title(); got != "Title D" || c.model != "" {
		t.Errorf("after session switch = %q, model %q must not carry over", got, c.model)
	}
	write(log(id2), record("Title D", id2), 20e3) // new stable mtime forces a read
	must(t, os.Chmod(log(id2), 0))                // unreadable (I/O error) must not be cached
	if got := title(); got != "" && os.Geteuid() != 0 {
		t.Errorf("unreadable transcript = %q", got)
	}
	must(t, os.Chmod(log(id2), 0600))
	if got := title(); got != "Title D" {
		t.Errorf("after transcript readable again = %q", got)
	}
	must(t, os.Remove(metadata))
	if got := title(); got != "" || *c != (sessionTitle{}) {
		t.Errorf("after metadata removal = %q, cache %+v", got, *c)
	}
}

// subagent fixtures
func mk(t *testing.T, root, cwd, session, id string, meta any, ageSec int64, now int64) {
	t.Helper()
	dir := filepath.Join(root, claudeSlug(cwd))
	sub := filepath.Join(dir, session, "subagents")
	must(t, os.MkdirAll(sub, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, session+".jsonl"), nil, 0o644))
	var mb []byte
	if s, ok := meta.(string); ok {
		mb = []byte(s)
	} else {
		mb, _ = json.Marshal(meta)
	}
	must(t, os.WriteFile(filepath.Join(sub, "agent-"+id+".meta.json"), mb, 0o644))
	j := filepath.Join(sub, "agent-"+id+".jsonl")
	must(t, os.WriteFile(j, nil, 0o644))
	tm := time.UnixMilli(now - ageSec*1000)
	must(t, os.Chtimes(j, tm, tm))
}

func claude(pid int, cwd string) Agent {
	return Agent{Pid: pid, Kind: "claude", Cwd: &cwd, projectCwd: cwd}
}

func subs(root string, now int64, as ...Agent) []Subagent {
	return subsWithHosts(root, now, map[hostKey]workerHost{}, as...)
}

// subsWithHosts lists workers with fresh caches and the given SubagentStart records.
func subsWithHosts(root string, now int64, hosts map[hostKey]workerHost, as ...Agent) []Subagent {
	return listClaudeSubagents(as, root, now, map[string]metaEntry{}, map[string]sessionScan{}, hosts)
}

func TestClaudeSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/y/.claude/x_y": "-Users-y--claude-x-y",
		"/w/日本語":               "-w----",
		"/w/😀":                 "-w---", // astral rune = 2 UTF-16 units
	} {
		if got := claudeSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRunningSubagentAttachedToSoleClaude(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "Review", "model": "sonnet"}, 3, now)
	got := subs(root, now, claude(100, "/w/a"))
	pid := 100
	want := []Subagent{{"a1", &pid, nil, "/w/a", "Review", "sonnet", true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestSubagentShowsResolvedModelFromTranscript(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	session := "11111111-1111-1111-1111-111111111111"
	meta := map[string]any{"description": "R", "model": "sonnet"}
	mk(t, root, "/w/a", session, "a1", meta, 3, now)
	record := `{"type":"assistant","sessionId":"` + session + `","message":{"model":"claude-sonnet-5-5"}}` + "\n"
	logPath := filepath.Join(root, claudeSlug("/w/a"), session, "subagents", "agent-a1.jsonl")
	must(t, os.WriteFile(logPath, []byte(record), 0o644))
	modTime := time.UnixMilli(now - 3000)
	must(t, os.Chtimes(logPath, modTime, modTime))

	got := subs(root, now, claude(100, "/w/a"))

	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Model != "claude-sonnet-5-5" {
		t.Errorf("model: got %v", got[0].Model)
	}
}

// A meta rewrite must not drop the model once the transcript tail no longer has an assistant record.
func TestSubagentKeepsTranscriptModelAcrossMetaRewrite(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	session := "11111111-1111-1111-1111-111111111111"
	mk(t, root, "/w/a", session, "a1", map[string]any{"description": "R", "model": "sonnet"}, 3, now)
	sub := filepath.Join(root, claudeSlug("/w/a"), session, "subagents")
	logPath := filepath.Join(sub, "agent-a1.jsonl")
	metaPath := filepath.Join(sub, "agent-a1.meta.json")
	record := `{"type":"assistant","sessionId":"` + session + `","message":{"model":"claude-sonnet-5-5"}}` + "\n"
	must(t, os.WriteFile(logPath, []byte(record), 0o644))
	modTime := time.UnixMilli(now - 3000)
	must(t, os.Chtimes(logPath, modTime, modTime))
	cache := map[string]metaEntry{}
	list := func(at int64) []Subagent {
		return listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, at, cache, map[string]sessionScan{}, nil)
	}
	list(now)

	// a long tool result pushes the assistant record out of the 64 KiB tail
	toolResult := `{"type":"user","content":"` + strings.Repeat("x", 70<<10) + `"}` + "\n"
	must(t, os.WriteFile(logPath, []byte(record+toolResult), 0o644))
	rewriteTime := modTime.Add(time.Second)
	must(t, os.Chtimes(logPath, rewriteTime, rewriteTime))
	must(t, os.WriteFile(metaPath, []byte(`{"description":"R","model":"sonnet"}`), 0o644))
	must(t, os.Chtimes(metaPath, rewriteTime, rewriteTime))

	got := list(now + 1)

	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Model != "claude-sonnet-5-5" {
		t.Errorf("model: got %v", got[0].Model)
	}
}

// A worker left over from an earlier session in the same cwd must not nest under
// the sole claude now running a different session; its own session's worker does.
func TestSoleClaudeAttachesOnlyItsCurrentSession(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessionA := "a0000000-0000-0000-0000-000000000000"
	sessionB := "b0000000-0000-0000-0000-000000000000"
	mk(t, root, "/w/a", sessionA, "old", map[string]any{"description": "R", "model": "inherit"}, 3, now)
	mk(t, root, "/w/a", sessionB, "new", map[string]any{"description": "R", "model": "inherit"}, 3, now)
	host := claude(100, "/w/a")
	host.session = sessionB
	parents := map[string]*int{}
	for _, s := range subs(root, now, host) {
		parents[s.Id] = s.ParentPid
	}
	if p, ok := parents["old"]; !ok || p != nil {
		t.Errorf("earlier session's worker: listed %v, parent %v; want listed without a parent", ok, p)
	}
	if p := parents["new"]; p == nil || *p != 100 {
		t.Errorf("current session's worker parent = %v, want 100", p)
	}
}

// A worker this process's SubagentStart hook reported (started before a /clear
// moved the process to a new session) keeps its parent.
func TestSoleClaudeKeepsHookReportedWorkerAfterClear(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessionA := "a0000000-0000-0000-0000-000000000000"
	sessionB := "b0000000-0000-0000-0000-000000000000"
	mk(t, root, "/w/a", sessionA, "old", map[string]any{"description": "R", "model": "inherit"}, 3, now)
	host := claudeUp(100, "/w/a", "10:00")
	host.session = sessionB
	hosts := hookRecords(map[string]workerHost{"old": hookRecord(host, now)})
	got := subsWithHosts(root, now, hosts, host)
	if !hasParent(parentPids(got), "old", 100) {
		t.Errorf("hook-reported worker should keep its parent: %+v", got)
	}
}

func TestFinishedSubagentExcluded(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m", "finished": true}, 3, now)
	if got := subs(root, now, claude(100, "/w/a")); len(got) != 0 {
		t.Error(got)
	}
}

func TestSubagentAgeWindow(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a9", map[string]any{"description": "R", "model": "m"}, 9*60, now)
	mk(t, root, "/w/a", "s1", "a31", map[string]any{"description": "R", "model": "m"}, 31*60, now)
	got := subs(root, now, claude(100, "/w/a"))
	if len(got) != 1 || got[0].Id != "a9" || got[0].Working {
		t.Errorf("got %+v", got)
	}
}

func TestTwoClaudeInCwdParentPidNull(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m"}, 3, now)
	if got := subs(root, now, claude(100, "/w/a"), claude(101, "/w/a")); got[0].ParentPid != nil {
		t.Error("parentPid should be nil")
	}
}

func TestTwoClaudeInCwdAttachBySessionMetadata(t *testing.T) {
	root, now := filepath.Join(t.TempDir(), "projects"), nowMs()
	s100, s101 := "10000000-0000-0000-0000-000000000000", "10100000-0000-0000-0000-000000000000"
	mk(t, root, "/w/a", s100, "a1", map[string]any{"description": "R", "model": "m"}, 3, now)
	mk(t, root, "/w/a", s101, "a2", map[string]any{"description": "R", "model": "m"}, 3, now)
	mk(t, root, "/w/a", "unclaimed", "a3", map[string]any{"description": "R", "model": "m"}, 3, now)
	sessions := filepath.Join(filepath.Dir(root), "sessions")
	must(t, os.MkdirAll(sessions, 0o755))
	for pid, id := range map[int]string{100: s100, 101: s101} {
		meta := marshal(map[string]any{"pid": pid, "sessionId": id, "cwd": "/w/a"})
		must(t, os.WriteFile(filepath.Join(sessions, strconv.Itoa(pid)+".json"), meta, 0o644))
	}
	parents := map[string]*int{}
	for _, s := range subs(root, now, claude(100, "/w/a"), claude(101, "/w/a")) {
		parents[s.Id] = s.ParentPid
	}
	if parents["a1"] == nil || *parents["a1"] != 100 || parents["a2"] == nil || *parents["a2"] != 101 {
		t.Errorf("workers should attach to the session owner: a1=%v a2=%v", parents["a1"], parents["a2"])
	}
	if parents["a3"] != nil {
		t.Error("a session no running process claims keeps a nil parentPid")
	}

	// a second live process resuming s101 makes its owner ambiguous
	resumed := marshal(map[string]any{"pid": 102, "sessionId": s101, "cwd": "/w/a"})
	must(t, os.WriteFile(filepath.Join(sessions, "102.json"), resumed, 0o644))
	for _, s := range subs(root, now, claude(100, "/w/a"), claude(101, "/w/a"), claude(102, "/w/a")) {
		parents[s.Id] = s.ParentPid
	}
	if parents["a1"] == nil || *parents["a1"] != 100 || parents["a2"] != nil {
		t.Errorf("a doubly claimed session should stay unattributed: a1=%v a2=%v", parents["a1"], parents["a2"])
	}
}

// claimSessions writes ~/.claude/sessions/<pid>.json beside root for each pid -> session.
func claimSessions(t *testing.T, root, cwd string, claims map[int]string) {
	t.Helper()
	sessions := filepath.Join(filepath.Dir(root), "sessions")
	must(t, os.MkdirAll(sessions, 0o755))
	for pid, id := range claims {
		b := marshal(map[string]any{"pid": pid, "sessionId": id, "cwd": cwd})
		must(t, os.WriteFile(filepath.Join(sessions, strconv.Itoa(pid)+".json"), b, 0o644))
	}
}

func parentPids(list []Subagent) map[string]*int {
	parents := map[string]*int{}
	for _, s := range list {
		parents[s.Id] = s.ParentPid
	}
	return parents
}

func hasParent(parents map[string]*int, id string, pid int) bool {
	p := parents[id]
	if p == nil {
		return false
	}
	return *p == pid
}

// claudeUp is a detected Claude that has been running for etime.
func claudeUp(pid int, cwd, etime string) Agent {
	a := claude(pid, cwd)
	a.Etime = etime
	return a
}

// hookRecord is what a SubagentStart hook from a records at now.
func hookRecord(a Agent, now int64) workerHost {
	return workerHost{pid: a.Pid, start: processStart(a, now)}
}

// hookRecords keys records as recordWorkerHost does, by worker and host PID.
func hookRecords(byWorker map[string]workerHost) map[hostKey]workerHost {
	hosts := map[hostKey]workerHost{}
	for id, h := range byWorker {
		hosts[hostKey{id, h.pid}] = h
	}
	return hosts
}

// hasRecord reports whether any Claude has a record for worker id.
func hasRecord(hosts map[hostKey]workerHost, id string) bool {
	for k := range hosts {
		if k.agent == id {
			return true
		}
	}
	return false
}

func TestDoublyClaimedSessionNestsByHook(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	now := nowMs()
	s100 := "10000000-0000-0000-0000-000000000000"
	s101 := "10100000-0000-0000-0000-000000000000"
	meta := map[string]any{"description": "R", "model": "m"}
	mk(t, root, "/w/a", s100, "a1", meta, 3, now)
	mk(t, root, "/w/a", s101, "a2", meta, 3, now)
	mk(t, root, "/w/a", s101, "a4", meta, 3, now)
	mk(t, root, "/w/a", s101, "a5", meta, 3, now)
	mk(t, root, "/w/a", "unclaimed", "a3", meta, 3, now)
	// 102 resumed s101 in a second terminal: s101 has two claimants.
	claimSessions(t, root, "/w/a", map[int]string{
		100: s100,
		101: s101,
		102: s101,
	})
	c100 := claudeUp(100, "/w/a", "10:00")
	c101 := claudeUp(101, "/w/a", "10:00")
	c102 := claudeUp(102, "/w/a", "10:00")
	exited := claudeUp(999, "/w/a", "10:00")
	hosts := hookRecords(map[string]workerHost{
		"a1": hookRecord(c101, now),   // must not override the single claimant
		"a2": hookRecord(c102, now),   // a claimant: proven parent
		"a3": hookRecord(c100, now),   // nobody claims the session
		"a4": hookRecord(c100, now),   // a live Claude, but not a claimant of s101
		"a9": hookRecord(exited, now), // its Claude has exited
	})
	agents := []Agent{
		c100,
		c101,
		c102,
	}
	list := subsWithHosts(root, now, hosts, agents...)
	parents := parentPids(list)
	if !hasParent(parents, "a1", 100) {
		t.Errorf("a single claimant keeps its worker: a1=%v", parents["a1"])
	}
	if !hasParent(parents, "a2", 102) {
		t.Errorf("a recorded claimant should own its worker: a2=%v", parents["a2"])
	}
	for _, id := range []string{"a3", "a4", "a5"} {
		if parents[id] != nil {
			t.Errorf("%s: an unproven parent must stay unattributed, got %d", id, *parents[id])
		}
	}
	if hasRecord(hosts, "a9") {
		t.Error("listing should prune a record whose Claude has exited")
	}

	// The sole Claude in a directory stays the parent whatever a record says.
	mk(t, root, "/w/b", "s", "a6", meta, 3, now)
	hosts[hostKey{"a6", 101}] = hookRecord(c101, now)
	agents = []Agent{
		claudeUp(100, "/w/b", "10:00"),
		claudeUp(101, "", "10:00"),
	}
	list = subsWithHosts(root, now, hosts, agents...)
	if len(list) != 1 {
		t.Fatalf("want one worker in /w/b, got %+v", list)
	}
	if !hasParent(parentPids(list), "a6", 100) {
		t.Errorf("sole Claude should own its worker: %+v", list[0])
	}
}

func TestUnrelatedRecordDoesNotBlockClaimant(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	now := nowMs()
	s100 := "10000000-0000-0000-0000-000000000000"
	s101 := "10100000-0000-0000-0000-000000000000"
	meta := map[string]any{"description": "R", "model": "m"}
	mk(t, root, "/w/a", s101, "a2", meta, 3, now)
	claimSessions(t, root, "/w/a", map[int]string{
		100: s100,
		101: s101,
		102: s101,
	})
	c100 := claudeUp(100, "/w/a", "10:00")
	c101 := claudeUp(101, "/w/a", "10:00")
	c102 := claudeUp(102, "/w/a", "10:00")
	agents := []Agent{
		c100,
		c101,
		c102,
	}
	s := &server{}
	// A program under 100, which doesn't claim s101, reports a2 first.
	s.recordWorkerHost("a2", hookRecord(c100, now))
	s.recordWorkerHost("a2", hookRecord(c102, now))
	// A later record from the other claimant doesn't replace the real one,
	// even within the same millisecond and though 101 is listed first.
	s.recordWorkerHost("a2", hookRecord(c101, now))
	list := subsWithHosts(root, now, s.hosts, agents...)
	if !hasParent(parentPids(list), "a2", 102) {
		t.Errorf("the claimant's record should win over an unrelated earlier one: %+v", list)
	}
}

func TestReusedPidDoesNotInheritRecord(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	now := nowMs()
	s101 := "10100000-0000-0000-0000-000000000000"
	meta := map[string]any{"description": "R", "model": "m"}
	mk(t, root, "/w/a", s101, "a2", meta, 3, now)
	claimSessions(t, root, "/w/a", map[int]string{
		101: s101,
		102: s101,
	})
	// The record was made for a 102 that started ten minutes ago; the 102
	// running now started five seconds ago, so it is another process.
	original := claudeUp(102, "/w/a", "10:00")
	hosts := hookRecords(map[string]workerHost{"a2": hookRecord(original, now)})
	agents := []Agent{
		claudeUp(101, "/w/a", "10:00"),
		claudeUp(102, "/w/a", "00:05"),
	}
	list := subsWithHosts(root, now, hosts, agents...)
	if parentPids(list)["a2"] != nil {
		t.Errorf("a reused PID must not inherit the record: %+v", list)
	}
	if hasRecord(hosts, "a2") {
		t.Error("a record for a reused PID should be pruned")
	}

	// 31 seconds apart is already clearly another process.
	hosts[hostKey{"a2", 102}] = hookRecord(original, now)
	agents[1] = claudeUp(102, "/w/a", "09:29")
	subsWithHosts(root, now, hosts, agents...)
	if hasRecord(hosts, "a2") {
		t.Error("a start 31 s off should prune the record")
	}

	// 5 seconds apart (a slow poll, say) doesn't prove the parent, but keeps
	// the record: the hook never resends it.
	hosts[hostKey{"a2", 102}] = hookRecord(original, now)
	agents[1] = claudeUp(102, "/w/a", "09:55")
	list = subsWithHosts(root, now, hosts, agents...)
	if parentPids(list)["a2"] != nil {
		t.Errorf("a start 5 s off must not prove the parent: %+v", list)
	}
	if !hasRecord(hosts, "a2") {
		t.Fatal("a start 5 s off must not drop the record")
	}

	// Whole-second elapsed time read later is back within the slack: proven.
	later := now + 1500
	agents[1] = claudeUp(102, "/w/a", "10:01")
	list = subsWithHosts(root, later, hosts, agents...)
	if !hasParent(parentPids(list), "a2", 102) {
		t.Errorf("the same process should keep its worker: %+v", list)
	}

	// Without a usable elapsed time the process can't be proven the same, but
	// the record is kept.
	agents[1] = claudeUp(102, "/w/a", "")
	list = subsWithHosts(root, later, hosts, agents...)
	if parentPids(list)["a2"] != nil {
		t.Errorf("an unknown start time must not prove the parent: %+v", list)
	}
	if !hasRecord(hosts, "a2") {
		t.Fatal("an unknown start time must not drop the record")
	}

	// Once the elapsed time is readable again, the same process is proven.
	agents[1] = claudeUp(102, "/w/a", "10:01")
	list = subsWithHosts(root, later, hosts, agents...)
	if !hasParent(parentPids(list), "a2", 102) {
		t.Errorf("a readable start time should prove the parent again: %+v", list)
	}
}

func TestIdleWorkerKeepsRecordedHost(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	now := nowMs()
	s101 := "10100000-0000-0000-0000-000000000000"
	meta := map[string]any{"description": "R", "model": "m"}
	claimSessions(t, root, "/w/a", map[int]string{
		101: s101,
		102: s101,
	})
	c102 := claudeUp(102, "/w/a", "10:00")
	agents := []Agent{
		claudeUp(101, "/w/a", "10:00"),
		c102,
	}
	hosts := hookRecords(map[string]workerHost{"a2": hookRecord(c102, now)})

	// Idle past the 30-minute window: hidden, but its record must survive.
	mk(t, root, "/w/a", s101, "a2", meta, 31*60, now)
	list := subsWithHosts(root, now, hosts, agents...)
	if len(list) != 0 {
		t.Fatalf("an idle worker should be hidden, got %+v", list)
	}
	if !hasRecord(hosts, "a2") {
		t.Fatal("hiding an idle worker must not drop its recorded host")
	}

	// It wakes up and is nested under the same Claude again.
	mk(t, root, "/w/a", s101, "a2", meta, 3, now)
	list = subsWithHosts(root, now, hosts, agents...)
	if !hasParent(parentPids(list), "a2", 102) {
		t.Errorf("a revived worker should keep its parent: %+v", list)
	}
}

func TestWorkerHostsPrunedAndBounded(t *testing.T) {
	now := nowMs()
	alive := claudeUp(100, "/w/a", "10:00")
	hosts := hookRecords(map[string]workerHost{
		"alive": hookRecord(alive, now),
		"dead":  hookRecord(claudeUp(999, "/w/a", "10:00"), now),
	})
	pruneWorkerHosts(hosts, []Agent{alive}, now)
	if len(hosts) != 1 {
		t.Errorf("want only the live Claude's record kept, got %v", hosts)
	}
	if !hasRecord(hosts, "alive") {
		t.Errorf("the live Claude's record was dropped: %v", hosts)
	}

	// A bare server has no records map yet; recording must not panic.
	s := &server{}
	for i := 0; i <= maxWorkerHosts; i++ {
		s.recordWorkerHost(fmt.Sprintf("a%07x", i), workerHost{pid: 100})
	}
	s.recordWorkerHost("a0000001", workerHost{pid: 100, start: 1})
	if len(s.hosts) != maxWorkerHosts {
		t.Fatalf("kept %d records, want %d", len(s.hosts), maxWorkerHosts)
	}
	if hasRecord(s.hosts, "a0000000") {
		t.Error("the oldest record should be dropped when full")
	}
	if s.hosts[hostKey{"a0000001", 100}].start != 0 {
		t.Error("a Claude's first record for a worker should win")
	}

	// One Claude flooding records evicts only its own, never another's.
	s = &server{}
	s.recordWorkerHost("a1000000", workerHost{pid: 200})
	for i := 0; i < 2*maxWorkerHosts; i++ {
		s.recordWorkerHost(fmt.Sprintf("a%07x", i), workerHost{pid: 100})
	}
	if !hasRecord(s.hosts, "a1000000") {
		t.Error("a flood from another Claude evicted this Claude's record")
	}
	if len(s.hosts) != maxWorkerHosts {
		t.Errorf("kept %d records, want %d", len(s.hosts), maxWorkerHosts)
	}
}

func TestSubagentEdgeCases(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	for _, meta := range []string{"{", "null", "[]", "true", "42", `"text"`, `{"description":"partial","model":}`} {
		mk(t, root, "/w/a", "bad", "bad", meta, 3, now)
		if got := subs(root, now, claude(100, "/w/a")); len(got) != 0 {
			t.Errorf("invalid metadata %q: %+v", meta, got)
		}
	}
	if got := subs(root, now, Agent{Pid: 5, Kind: "codex", Cwd: sp("/w/a")}, claude(6, "/nope")); got == nil || len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}

func TestSubagentMissingFieldsAreNull(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	b, _ := json.Marshal(subs(root, now, claude(100, "/w/a")))
	if !strings.Contains(string(b), `"description":null,"model":null`) {
		t.Errorf("got %s", b)
	}
}

func TestSubagentsExposeParentAgentId(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "p1", map[string]any{"description": "Parent", "model": "m", "spawnDepth": 1}, 3, now)
	mk(t, root, "/w/a", "s1", "c1", map[string]any{"description": "Child", "model": "m", "spawnDepth": 2, "parentAgentId": "p1"}, 3, now)
	got := subs(root, now, claude(100, "/w/a")) // ReadDir order is sorted: c1, p1
	if len(got) != 2 || got[0].Id != "c1" || got[0].ParentAgentId != "p1" || got[1].Id != "p1" || got[1].ParentAgentId != nil {
		t.Errorf("got %+v", got)
	}
}

func TestMetaCacheKeyedByMtimeAndPruned(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	mk(t, root, "/w/a", "s1", "a1", map[string]any{"description": "R", "model": "m"}, 3, now)
	cache := map[string]metaEntry{}
	listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now, cache, map[string]sessionScan{}, nil)
	if len(cache) != 1 {
		t.Fatal("not cached")
	}
	listClaudeSubagents(nil, root, now, cache, map[string]sessionScan{}, nil)
	if len(cache) != 0 {
		t.Error("not pruned")
	}
}

func TestSessionScanCached(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessions := map[string]sessionScan{}
	list := func(at int64) int {
		return len(listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, at, map[string]metaEntry{}, sessions, nil))
	}
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	mk(t, root, "/w/a", "old", "a2", map[string]any{}, 3, now)
	dir := filepath.Join(root, claudeSlug("/w/a"))
	old := time.UnixMilli(now - 7*3600e3)
	must(t, os.Chtimes(filepath.Join(dir, "old.jsonl"), old, old))
	settle := func(ago int64) { tm := time.UnixMilli(now - ago); must(t, os.Chtimes(dir, tm, tm)) }
	settle(10e3)
	if got := list(now); got != 1 {
		t.Fatalf("initial = %d", got)
	}
	mk(t, root, "/w/a", "s2", "a3", map[string]any{}, 3, now)
	settle(5e3) // a new file changes the dir mtime
	if got := list(now + 1); got != 2 {
		t.Errorf("after new session = %d", got)
	}
	tm := time.UnixMilli(now)
	must(t, os.Chtimes(filepath.Join(dir, "old.jsonl"), tm, tm)) // resumed: no dir change
	if got := list(now + 2); got != 2 {
		t.Errorf("before rescan = %d", got)
	}
	if got := list(now + 1 + sessionRescanMs); got != 3 {
		t.Errorf("after rescan = %d", got)
	}
}

func TestSessionScanRepeatedWhileDirUnsettled(t *testing.T) {
	root, now := t.TempDir(), nowMs()
	sessions := map[string]sessionScan{}
	mk(t, root, "/w/a", "s1", "a1", map[string]any{}, 3, now)
	dir := filepath.Join(root, claudeSlug("/w/a"))
	tm := time.UnixMilli(now)
	must(t, os.Chtimes(dir, tm, tm)) // dir changed in the same tick as the scan
	listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now, map[string]metaEntry{}, sessions, nil)
	mk(t, root, "/w/a", "s2", "a2", map[string]any{}, 3, now)
	must(t, os.Chtimes(dir, tm, tm)) // second create lands in that same tick
	if got := listClaudeSubagents([]Agent{claude(100, "/w/a")}, root, now+1, map[string]metaEntry{}, sessions, nil); len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
}

// A Claude whose directory was renamed while it ran keeps its session file,
// transcript and workers under the cwd it started in; lsof reports the new name.
func TestRenamedClaudeCwdUsesRecordedProjectDir(t *testing.T) {
	root, now := filepath.Join(t.TempDir(), "projects"), nowMs()
	renamed := "11111111-1111-1111-1111-111111111111"
	fresh := "22222222-2222-2222-2222-222222222222"
	recreated := "33333333-3333-3333-3333-333333333333"
	unclaimed := "44444444-4444-4444-4444-444444444444"
	// the processes below started 60 s ago
	writeMetaAt := func(pid int, session, cwd string, startedAt int64) {
		t.Helper()
		file := filepath.Join(filepath.Dir(root), "sessions", strconv.Itoa(pid)+".json")
		must(t, os.MkdirAll(filepath.Dir(file), 0700))
		data := marshal(map[string]any{"pid": pid, "sessionId": session, "cwd": cwd, "startedAt": startedAt})
		must(t, os.WriteFile(file, data, 0600))
	}
	writeMeta := func(pid int, session, cwd string) {
		t.Helper()
		writeMetaAt(pid, session, cwd, now-59e3)
	}
	// pid 100 started in /w/old, since renamed to /w/new; pid 101 started in /w/new
	// after the rename, and pid 102 in a recreated /w/old
	writeMeta(100, renamed, "/w/old")
	writeMeta(101, fresh, "/w/new")
	writeMeta(102, recreated, "/w/old")
	// pid 103 reused the pid of a Claude that crashed in /w/old without deleting its file
	writeMetaAt(103, unclaimed, "/w/old", now-600e3)
	mk(t, root, "/w/old", renamed, "a1", map[string]any{"description": "Old worker"}, 3, now)
	mk(t, root, "/w/new", fresh, "a2", map[string]any{"description": "New worker"}, 3, now)
	mk(t, root, "/w/old", recreated, "a3", map[string]any{"description": "Recreated worker"}, 3, now)
	mk(t, root, "/w/old", unclaimed, "a4", map[string]any{"description": "Unclaimed worker"}, 3, now)
	transcript := filepath.Join(root, claudeSlug("/w/old"), renamed+".jsonl")
	title := `{"type":"ai-title","aiTitle":"Renamed task","sessionId":"` + renamed + `"}` + "\n"
	must(t, os.WriteFile(transcript, []byte(title), 0600))

	s := srvWith(func(cmd string, args ...string) (string, error) {
		if cmd == "ps" {
			return "100 10 1000 0:01 01:00 claude\n101 10 1000 0:01 01:00 claude\n102 10 1000 0:01 01:00 claude\n103 10 1000 0:01 01:00 claude\n", nil
		}
		return "", errors.New("unavailable")
	})
	s.root = root
	s.cache[100] = &cacheEntry{cwd: "/w/new", etime: "01:00"}
	s.cache[101] = &cacheEntry{cwd: "/w/new", etime: "01:00"}
	s.cache[102] = &cacheEntry{cwd: "/w/old", etime: "01:00"}
	s.cache[103] = &cacheEntry{cwd: "/w/other", etime: "01:00"}
	agents := mustList(t, s, now)
	old := agentByPid(agents, 100)
	if old.Title == nil || *old.Title != "Renamed task" || old.session != renamed {
		t.Fatalf("renamed claude lost its session: %+v", old)
	}
	if *old.Cwd != "/w/new" || old.Project != "new" {
		t.Errorf("dashboard must keep the live cwd: %+v", old)
	}
	if reused := agentByPid(agents, 103); reused.session != "" || reused.projectCwd != "/w/other" {
		t.Errorf("a session file older than the process must be ignored: %+v", reused)
	}

	got := subs(root, now, agents...)
	p100, p101, p102 := 100, 101, 102
	want := []Subagent{
		{"a1", &p100, nil, "/w/new", "Old worker", nil, true},
		{"a3", &p102, nil, "/w/old", "Recreated worker", nil, true},
		{"a4", nil, nil, "/w/old", "Unclaimed worker", nil, true},
		{"a2", &p101, nil, "/w/new", "New worker", nil, true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}

	// a recorded cwd that can't name a project dir falls back to the live cwd
	for _, recorded := range []string{"", "w/old"} {
		writeMeta(100, renamed, recorded)
		c := &sessionTitle{}
		claudeSessionTitle(root, 100, math.NaN(), "/w/new", now, c)
		if c.id != renamed || c.projectCwd("/w/new") != "/w/new" {
			t.Errorf("recorded cwd %q: id %q, project cwd %q", recorded, c.id, c.projectCwd("/w/new"))
		}
	}
}
