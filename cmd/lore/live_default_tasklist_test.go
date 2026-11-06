package main

import (
	"os"
	"os/exec"
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

	code, out := fenceLore(t, a, "task", "add", "ship the thing")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = fenceLore(t, b, "sync"); code != 0 {
		t.Fatalf("sync in b: %d %s", code, out)
	}
	if code, out = fenceLore(t, b, "task", "split", id, "first half", "second half"); code != 0 {
		t.Fatalf("task split in the second clone: exit %d %s", code, out)
	}
	if strings.Contains(out, "E_NOT_FOUND") {
		t.Fatalf("task split in the second clone: %s", out)
	}

	// a folder scope created on first use by a live rule rides with it
	if err = os.MkdirAll(filepath.Join(a, "svc"), 0o750); err != nil {
		t.Fatal(err)
	}
	code, out = fenceLore(t, a, "rule", "add", "--live", "--scope", "dir:svc", "--body", "keep handlers small")
	rid := ruleID.FindString(out)
	if code != 0 || rid == "" {
		t.Fatalf("rule add --live --scope: %d %s", code, out)
	}
	if code, out = fenceLore(t, b, "sync"); code != 0 {
		t.Fatalf("sync in b: %d %s", code, out)
	}
	if code, out = fenceLore(t, b, "rule", "edit", rid, "--body", "keep handlers short"); code != 0 {
		t.Fatalf("rule edit in the second clone: exit %d %s", code, out)
	}
	if code, out = fenceLore(t, b, "scope", "list"); code != 0 || !strings.Contains(out, "dir:svc") {
		t.Fatalf("scope list in the second clone does not show the svc scope with its rule: exit %d %s", code, out)
	}
}

var ruleID = regexp.MustCompile(`rul_[0-9a-f]{32}`)
