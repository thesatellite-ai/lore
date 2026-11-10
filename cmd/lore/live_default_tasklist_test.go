package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDefaultTasklistReachesTeammate (F-MOVE-2, b-errs2's finding): the
// "default" task list a live `task add` creates on demand rides live with
// the task, so a teammate who syncs can split that task (the sub-task
// joins its parent's tasklist) instead of answering E_NOT_FOUND on a
// tlt_ id only the first clone's working tree held. A folder scope a live
// `rule add --scope dir:<path>` creates on first use rides the same way.
func TestDefaultTasklistReachesTeammate(t *testing.T) {
	t.Parallel()
	w, a, b := twoLiveClones(t)

	code, out := w.loreOut(t, a, "task", "add", "ship the thing")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = w.loreOut(t, b, "sync"); code != 0 {
		t.Fatalf("sync in b: %d %s", code, out)
	}
	if code, out = w.loreOut(t, b, "task", "split", id, "first half", "second half"); code != 0 {
		t.Fatalf("task split in the second clone: exit %d %s", code, out)
	}
	if strings.Contains(out, "E_NOT_FOUND") {
		t.Fatalf("task split in the second clone: %s", out)
	}

	// a folder scope created on first use by a live rule rides with it
	if err := os.MkdirAll(filepath.Join(a, "svc"), 0o750); err != nil {
		t.Fatal(err)
	}
	code, out = w.loreOut(t, a, "rule", "add", "--live", "--scope", "dir:svc", "--body", "keep handlers small")
	rid := ruleID.FindString(out)
	if code != 0 || rid == "" {
		t.Fatalf("rule add --live --scope: %d %s", code, out)
	}
	if code, out = w.loreOut(t, b, "sync"); code != 0 {
		t.Fatalf("sync in b: %d %s", code, out)
	}
	if code, out = w.loreOut(t, b, "rule", "edit", rid, "--body", "keep handlers short"); code != 0 {
		t.Fatalf("rule edit in the second clone: exit %d %s", code, out)
	}
	if code, out = w.loreOut(t, b, "scope", "list"); code != 0 || !strings.Contains(out, "dir:svc") {
		t.Fatalf("scope list in the second clone does not show the svc scope with its rule: exit %d %s", code, out)
	}
}

var ruleID = regexp.MustCompile(`rul_[0-9a-f]{32}`)

// twoLiveClones is a pushed lore repo (a) and a teammate's clone of it (b),
// in a world of their own run as the person.
func twoLiveClones(t *testing.T) (*cliWorld, string, string) {
	t.Helper()
	w := newCLIWorld(t).person().author()
	a, b := w.livePair(t)
	return w, a, b
}

// TestUnlandedRefsRideLive (F-MOVE-2b): a row made in an EARLIER run and
// only in the work tree (not on origin/main) rides the live ref when a
// live row points at it, by any ref field: a tasklist added in one run and
// named by a live task in the next, and the same for a plan (no companion
// table: the rule is the table definition's refs, not a list of tables).
func TestUnlandedRefsRideLive(t *testing.T) {
	t.Parallel()
	w, a, b := twoLiveClones(t)
	add := func(args ...string) string {
		t.Helper()
		code, out := w.loreOut(t, a, args...)
		if code != 0 {
			t.Fatalf("%s: %d %s", strings.Join(args, " "), code, out)
		}
		return out
	}
	tl := tasklistID.FindString(add("tasklist", "add", "--title", "sprint 1", "--body", "two weeks"))
	pl := planID.FindString(add("plan", "add", "--title", "launch", "--body", "the plan"))
	if tl == "" || pl == "" {
		t.Fatalf("ids: tasklist %q plan %q", tl, pl)
	}
	id := fenceTaskID.FindString(add("task", "add", "in the sprint", "--tasklist", tl, "--plan", pl))
	if id == "" {
		t.Fatal("no task id")
	}
	if code, out := w.loreOut(t, b, "sync"); code != 0 {
		t.Fatalf("sync in b: %d %s", code, out)
	}
	if code, out := w.loreOut(t, b, "task", "split", id, "part"); code != 0 {
		t.Fatalf("task split of a task in an earlier run's list, second clone: exit %d %s", code, out)
	}
	if code, out := w.loreOut(t, b, "plan", "show", pl, "--json"); code != 0 || !strings.Contains(out, "\"launch\"") {
		t.Fatalf("plan show in the second clone: exit %d %s", code, out)
	}
	if code, out := w.loreOut(t, b, "task", "edit", id, "--plan", pl, "--title", "in the sprint, renamed"); code != 0 {
		t.Fatalf("task edit naming the plan in the second clone: exit %d %s", code, out)
	}
}

var (
	tasklistID = regexp.MustCompile(`tlt_[0-9a-f]{32}`)
	planID     = regexp.MustCompile(`pln_[0-9a-f]{32}`)
)
