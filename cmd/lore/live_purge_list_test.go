package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTrashPurgeReachesTeammatesAsPurged (BUG-LIVE-PURGE-LIST, DQ4): on a
// repo with a live ref, `lore trash purge` lists the id on the live ref's
// own purge list, so after `lore sync` the purging clone and a teammate
// both answer E_PURGED (never E_NOT_FOUND "was deleted (live, …)") to a
// restore and a show of the purged task.
func TestTrashPurgeReachesTeammatesAsPurged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	asPerson(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "p", "GIT_AUTHOR_EMAIL": "p@example.com", "GIT_COMMITTER_NAME": "p", "GIT_COMMITTER_EMAIL": "p@example.com"} {
		t.Setenv(k, v)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	remote, a, b := filepath.Join(root, "remote.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	fenceGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	fenceGit(t, root, "clone", "-q", remote, a)
	fenceGit(t, a, "commit", "-q", "--allow-empty", "-m", "init")
	if code, out := fenceLore(t, a, "init", "--non-interactive", "--name=tools"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	fenceGit(t, a, "add", "-A")
	fenceGit(t, a, "commit", "-q", "-m", "lore")
	fenceGit(t, a, "push", "-q", "origin", "HEAD:main")
	fenceGit(t, root, "clone", "-q", remote, b)

	code, out := fenceLore(t, a, "task", "add", "doomed")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = fenceLore(t, a, "task", "delete", id, "--yes"); code != 0 {
		t.Fatalf("task delete: %d %s", code, out)
	}
	if code, out = fenceLore(t, a, "trash", "purge", id, "--confirm"); code != 0 {
		t.Fatalf("trash purge: %d %s", code, out)
	}
	for _, dir := range []string{a, b} {
		if code, out = fenceLore(t, dir, "sync"); code != 0 {
			t.Fatalf("sync in %s: %d %s", filepath.Base(dir), code, out)
		}
		for _, args := range [][]string{{"trash", "restore", id}, {"task", "show", id}} {
			code, out = fenceLore(t, dir, args...)
			if code == 0 || !strings.Contains(out, "E_PURGED") || !strings.Contains(out, id) {
				t.Errorf("%s in %s after the purge and a sync: exit %d %s, want E_PURGED", strings.Join(args, " "), filepath.Base(dir), code, out)
			}
		}
	}
}
