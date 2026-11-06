package main

// B-FLOW4-2 on the release binary: an agent's first writes in a repo
// whose store holds no actor row of it yet, with a session to record
// (LORE_ACTOR + LORE_SESSION), finish. Before the fix the actor-row write
// held the actor lock while its audit created this machine's session row,
// whose create took the same lock again: `lore task next` (and any first
// write of an agent with a session) hung forever (team docs, agents).

import (
	"strings"
	"testing"
	"time"
)

// agentWriteTimeout bounds each command: a deadlock never returns, a
// normal write takes well under a second.
const agentWriteTimeout = 60 * time.Second

// runWithin runs args in process and fails the test when run does not
// return in time (the goroutine of a deadlocked run is left behind).
func runWithin(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	type result struct {
		code     int
		out, err string
	}
	done := make(chan result, 1)
	go func() {
		c, o, e := runCaptured(t, args...)
		done <- result{c, o, e}
	}()
	select {
	case r := <-done:
		return r.code, r.out, r.err
	case <-time.After(agentWriteTimeout):
		t.Fatalf("lore %s did not return within %s (deadlock)", strings.Join(args, " "), agentWriteTimeout)
		return 0, "", ""
	}
}

// TestAgentFirstWritesWithSessionFinish: see the file comment. The person
// adds the tasks; the agent's first write is an edit (task start: an
// update, written at once, so its audit runs while the actor row is being
// written), then a claim (task next) and a create.
func TestAgentFirstWritesWithSessionFinish(t *testing.T) {
	asPerson(t)
	envScopeRepo(t)
	code, out, errOut := runWithin(t, "task", "add", "first task", "--json")
	if code != 0 {
		t.Fatalf("task add: exit %d\n%s\n%s", code, out, errOut)
	}
	id := firstID(out, "tsk_")
	if id == "" {
		t.Fatalf("no task id in %s", out)
	}
	if code, out, errOut = runWithin(t, "task", "add", "second task"); code != 0 {
		t.Fatalf("second task add: exit %d\n%s\n%s", code, out, errOut)
	}

	t.Setenv("LORE_HUMAN", "")
	t.Setenv("LORE_ACTOR", "agent:bflow4-bot")
	t.Setenv("LORE_SESSION", "s-bflow4")
	if code, out, errOut = runWithin(t, "task", "start", id); code != 0 {
		t.Fatalf("task start as an agent: exit %d\n%s\n%s", code, out, errOut)
	}
	// task next answers 0 with a task or an error code; either way it
	// must return.
	if code, out, errOut = runWithin(t, "task", "next"); code != 0 && !strings.Contains(errOut, "E_") {
		t.Fatalf("task next: exit %d without an error code\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut = runWithin(t, "task", "add", "agent task"); code != 0 {
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
