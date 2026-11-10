package main

// B-FLOW4-2 on the release binary: an agent's first writes in a repo
// whose store holds no actor row of it yet, with a session to record
// (LORE_ACTOR + LORE_SESSION), finish. Before the fix the actor-row write
// held the actor lock while its audit created this machine's session row,
// whose create took the same lock again: `lore task next` (and any first
// write of an agent with a session) hung forever (team docs, agents).

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agentWriteTimeout bounds each command: a deadlock never returns, a
// normal write takes well under a second.
const agentWriteTimeout = 60 * time.Second

// TestAgentFirstWritesWithSessionFinish: see the file comment. The person
// adds the tasks; the agent's first write is an edit (task start: an
// update, written at once, so its audit runs while the actor row is being
// written), then a claim (task next) and a create.
func TestAgentFirstWritesWithSessionFinish(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	dir := w.initRepo(t)
	code, out, errOut := w.loreWithin(t, dir, agentWriteTimeout, "task", "add", "first task", "--json")
	if code != 0 {
		t.Fatalf("task add: exit %d\n%s\n%s", code, out, errOut)
	}
	id := firstID(out, "tsk_")
	if id == "" {
		t.Fatalf("no task id in %s", out)
	}
	if code, out, errOut = w.loreWithin(t, dir, agentWriteTimeout, "task", "add", "second task"); code != 0 {
		t.Fatalf("second task add: exit %d\n%s\n%s", code, out, errOut)
	}

	w.set("LORE_HUMAN", "")
	w.set("LORE_ACTOR", "agent:bflow4-bot")
	w.set("LORE_SESSION", "s-bflow4")
	if code, out, errOut = w.loreWithin(t, dir, agentWriteTimeout, "task", "start", id); code != 0 {
		t.Fatalf("task start as an agent: exit %d\n%s\n%s", code, out, errOut)
	}
	// task next answers 0 with a task or an error code; either way it
	// must return.
	if code, out, errOut = w.loreWithin(t, dir, agentWriteTimeout, "task", "next"); code != 0 && !strings.Contains(errOut, "E_") {
		t.Fatalf("task next: exit %d without an error code\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut = w.loreWithin(t, dir, agentWriteTimeout, "task", "add", "agent task", "--commitment", "accepted"); code != 0 {
		t.Fatalf("task add as an agent: exit %d\n%s\n%s", code, out, errOut)
	}
}

// firstID is the first lore id with prefix in s ("" when none).
func firstID(s, prefix string) string {
	i := strings.Index(s, prefix)
	if i < 0 || len(s) < i+len(prefix)+32 {
		return ""
	}
	return s[i : i+len(prefix)+32]
}

// TestFirstMoveIntoAStoreFinishes: the same deadlock through the move
// path (2026-11-08). `lore move --yes` writes the caller's actor row into
// the target store outside a batch, holding the actor lock; the audit of
// that write updates this machine's session row, and Update took the actor
// lock again. The move hung forever under every agent (Claude Code sets
// the session), which held cmd/lore's whole test package for 10 minutes.
func TestFirstMoveIntoAStoreFinishes(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	dir := w.initRepo(t)
	if code, out, errOut := w.loreWithin(t, dir, agentWriteTimeout, "memory", "add", "--body", "moved by an agent"); code != 0 {
		t.Fatalf("memory add: exit %d\n%s\n%s", code, out, errOut)
	}
	mem := strings.TrimSuffix(filepath.Base(rowFiles(t, dir, "memories")[0]), ".json")
	if code, out, errOut := w.loreWithin(t, dir, agentWriteTimeout, "store", "init", "scratch", "file://"+filepath.Join(t.TempDir(), "scratch")); code != 0 {
		t.Fatalf("store init: exit %d\n%s\n%s", code, out, errOut)
	}
	// The person moves (as in TestShortcutsRunTheirCommand); the hang needed
	// a session to record, which this machine's session detection finds
	// when an ancestor process is a coding agent (every agent-run gate).
	if code, out, errOut := w.loreWithin(t, dir, agentWriteTimeout, "move", mem, "--to", "scratch", "--yes"); code != 0 {
		t.Fatalf("move --yes as an agent: exit %d\n%s\n%s", code, out, errOut)
	}
}
