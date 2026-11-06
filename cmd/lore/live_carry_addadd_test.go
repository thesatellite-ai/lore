package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCarriedRowLeavesWorkTree (F-ADDADD-1): a tasklist only this work tree
// holds, carried live by a live task that names it (F-MOVE-2b), moves to the
// live ref: its uncommitted work-tree file goes, so the user's next commit
// cannot add a stale copy of a row the landing also adds on main. Before the
// fix the tasks docs section (link-commits) hit CONFLICT (add/add) on the
// task list file at `git pull --rebase`. Both clones then commit, pull with
// rebase and push cleanly, and both still read the list's latest edit.
func TestCarriedRowLeavesWorkTree(t *testing.T) {
	a, b := twoLiveClones(t)
	run := func(dir string, args ...string) string {
		t.Helper()
		code, out := fenceLore(t, dir, args...)
		if code != 0 {
			t.Fatalf("%s: %d %s", strings.Join(args, " "), code, out)
		}
		return out
	}
	tl := tasklistID.FindString(run(a, "tasklist", "add", "--title", "launch week", "--body", "before the shop opens"))
	if tl == "" {
		t.Fatal("no tasklist id")
	}
	file := filepath.Join(a, ".lore", "data", "task_lists", tl+".json")
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("tasklist add must write the work-tree row first: %v", err)
	}
	id := fenceTaskID.FindString(run(a, "task", "add", "check the links", "--tasklist", tl))
	if id == "" {
		t.Fatal("no task id")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("a row carried live must leave the work tree (it lives on the live ref now): stat err %v", err)
	}
	run(a, "tasklist", "edit", tl, "--status", "closed")
	run(a, "task", "done", id, "--no-lesson")
	for _, dir := range []string{a, b} {
		run(dir, "sync")
		if out := run(dir, "tasklist", "show", tl, "--json"); !strings.Contains(out, "closed") {
			t.Fatalf("%s: the list's latest edit must be readable: %s", dir, out)
		}
		if err := os.WriteFile(filepath.Join(dir, "pricing-"+filepath.Base(dir)+".md"), []byte("Pro plan: 19 a month\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fenceGit(t, dir, "add", "-A")
		fenceGit(t, dir, "commit", "-q", "-m", "fix the price")
		fenceGit(t, dir, "pull", "-q", "--rebase", "origin", "main") // add/add conflict before the fix
		fenceGit(t, dir, "push", "-q", "origin", "HEAD:main")
		if out := run(dir, "tasklist", "show", tl, "--json"); !strings.Contains(out, "closed") {
			t.Fatalf("%s after the pull: %s", dir, out)
		}
	}
}

// TestCarriedCommittedRowNoAddAdd (F-ADDADD-2): the list's file is already
// COMMITTED in this clone but not pushed (HEAD holds it, origin/main does
// not) when a live task names it. Carried live, it lived in two places: the
// edit after the carry went live, the landing added the edited list on
// main, and the user's `git pull --rebase` replayed the commit that adds
// the old copy: CONFLICT (add/add) on the list file. Now such a row is not
// carried (a warning says why): it stays in git with its commit, its edit
// stays on the branch, every pull rebases cleanly, and both clones read the
// latest edit once the commit reaches main.
func TestCarriedCommittedRowNoAddAdd(t *testing.T) {
	a, b := twoLiveClones(t)
	run := func(dir string, args ...string) string {
		t.Helper()
		code, out := fenceLore(t, dir, args...)
		if code != 0 {
			t.Fatalf("%s: %d %s", strings.Join(args, " "), code, out)
		}
		return out
	}
	tl := tasklistID.FindString(run(a, "tasklist", "add", "--title", "launch week", "--body", "before the shop opens"))
	if tl == "" {
		t.Fatal("no tasklist id")
	}
	fenceGit(t, a, "add", "-A")
	fenceGit(t, a, "commit", "-q", "-m", "plan the launch") // committed, not pushed
	out := run(a, "task", "add", "check the links", "--tasklist", tl)
	id := fenceTaskID.FindString(out)
	if id == "" {
		t.Fatal("no task id")
	}
	if !strings.Contains(out, tl+" is committed here but not on origin/main") {
		t.Errorf("the task add must say the committed list stays on the branch: %s", out)
	}
	run(a, "tasklist", "edit", tl, "--status", "closed")
	run(a, "task", "done", id, "--no-lesson")
	for _, dir := range []string{a, b} {
		run(dir, "sync")
		if err := os.WriteFile(filepath.Join(dir, "pricing-"+filepath.Base(dir)+".md"), []byte("Pro plan: 19 a month\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fenceGit(t, dir, "add", "-A")
		fenceGit(t, dir, "commit", "-q", "-m", "fix the price")
		// the test binary is no merge driver (run as one it would run the
		// suite): any driver call here is the add/add this test forbids
		fenceGit(t, dir, "-c", "merge.lore.driver=false", "pull", "-q", "--rebase", "origin", "main") // add/add conflict before the fix
		fenceGit(t, dir, "push", "-q", "origin", "HEAD:main")
		if out := run(dir, "tasklist", "show", tl, "--json"); !strings.Contains(out, "closed") {
			t.Fatalf("%s after the pull: %s", dir, out)
		}
		if out := run(dir, "task", "show", id, "--json"); !strings.Contains(out, tl) {
			t.Fatalf("%s: the task must still name its list: %s", dir, out)
		}
	}
}
