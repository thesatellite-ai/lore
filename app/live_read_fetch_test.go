package app

import (
	"strings"
	"testing"
)

// TestReadFetchesWithAutopushOff (D18 amended, U-2): live.autopush is off
// by default and gates pushes only. Alice adds a task and runs `lore
// sync`; bob's plain `task list` (his clone's first lore command, so the
// fetch is due) fetches it with no sync of his own and pushes nothing: the
// remote's refs are the same before and after.
func TestReadFetchesWithAutopushOff(t *testing.T) {
	t.Parallel()
	w, alice, bob := twoLiveClones(t)
	remote := w.path("remote.git")

	code, out := w.loreOut(t, alice, "task", "add", "reach bob without his sync")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if strings.Contains(out, "(live, pushed)") {
		t.Fatalf("task add pushed with live.autopush off: %s", out)
	}
	if code, out = w.loreOut(t, alice, "sync"); code != 0 {
		t.Fatalf("sync in alice: %d %s", code, out)
	}
	before := w.git(t, remote, "for-each-ref")
	code, out = w.loreOut(t, bob, "task", "list")
	if code != 0 || !strings.Contains(out, "reach bob without his sync") {
		t.Fatalf("bob's task list after alice's sync does not show her task: exit %d %s", code, out)
	}
	if after := w.git(t, remote, "for-each-ref"); after != before {
		t.Fatalf("bob's read changed the remote:\nbefore %s\nafter  %s", before, after)
	}
	if strings.Contains(out, "pushed") {
		t.Fatalf("bob's read says it pushed: %s", out)
	}
}
