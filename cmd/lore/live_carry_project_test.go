package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUncommittedInitStaysInDataAfterLiveTask (BUGFIX-04): `lore init` is
// not committed yet when the first live task is added. The task names the
// project (project_id) and its creator (created_by); before the fix both
// rows rode the live ref as carried companions (F-MOVE-2b) and left the
// work tree (F-ADDADD-1), so .lore/data/projects was empty and every
// LORE_LIVE=0 or LORE_SYNC=0 command answered "this repository has no lore
// project". The project and actor rows are the repository's identity: they
// are never carried, they stay in .lore/data.
func TestUncommittedInitStaysInDataAfterLiveTask(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author()
	root := w.root
	repo := filepath.Join(root, "repo")
	w.git(t, root, "init", "-q", "-b", "main", repo)
	w.git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	run := func(args ...string) string {
		t.Helper()
		code, out := w.loreOut(t, repo, args...)
		if code != 0 {
			t.Fatalf("%s: %d %s", strings.Join(args, " "), code, out)
		}
		return out
	}
	run("init", "--non-interactive", "--name=tools") // not committed
	if id := fenceTaskID.FindString(run("task", "add", "t1")); id == "" {
		t.Fatal("no task id")
	}
	for _, table := range []string{"projects", "actors"} {
		files, err := os.ReadDir(filepath.Join(repo, ".lore", "data", table))
		if err != nil || len(files) == 0 {
			t.Fatalf(".lore/data/%s must keep its row after the first live task (err %v, %d files)", table, err, len(files))
		}
	}
	for _, env := range []string{"LORE_LIVE", "LORE_SYNC"} {
		w.set(env, "0")
		if id := ruleID.FindString(run("rule", "add", "Use tabs under "+env+"=0")); id == "" {
			t.Fatalf("%s=0 rule add: no rule id", env)
		}
		w.set(env, "")
	}
	if out := run("task", "list"); !strings.Contains(out, "t1") {
		t.Fatalf("the live task must still be listed: %s", out)
	}
}
