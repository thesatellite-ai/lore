package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fenceTaskID is a task id in lore's output.
var fenceTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

// fenceGit runs git in dir with the hooks switched off (the hooks lore init
// installs call the lore on PATH, which is not this build).
func fenceGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// the resolved git binary, every argument its own argv entry, no shell
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin, "-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// fenceLore runs lore in dir and returns the exit code and both streams.
func fenceLore(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCaptured(t, args...)
	return code, out + errOut
}

// TestHeartbeatAfterCancelWithinFetchTTL (b-errs F2, TEAM F7 §7.4): a
// person cancels a held task in another clone and the holder's next
// heartbeat comes while its last fetch is still younger than
// live.fetch_ttl (the default, not the team suite's 0s). The fence must be
// explained from the remote's task row: E_CANCELLED, exit 13, never
// E_LOST_CLAIM from the stale local row. A revoke still answers
// E_LOST_CLAIM (the fresh row is not cancelled). A claim of a task added
// after the last fetch refetches too (B-ERRS2-1).
func TestHeartbeatAfterCancelWithinFetchTTL(t *testing.T) {
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
	t.Chdir(root) // restores the test's working directory at the end
	remote, person := filepath.Join(root, "remote.git"), filepath.Join(root, "person")
	fenceGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	fenceGit(t, root, "clone", "-q", remote, person)
	fenceGit(t, person, "commit", "-q", "--allow-empty", "-m", "init")
	if code, out := fenceLore(t, person, "init", "--non-interactive", "--name=tools"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	fenceGit(t, person, "add", "-A")
	fenceGit(t, person, "commit", "-q", "-m", "lore")
	fenceGit(t, person, "push", "-q", "origin", "HEAD:main")

	for _, tc := range []struct {
		human []string
		code  int
		want  string
	}{
		{[]string{"task", "cancel", "--yes"}, 13, "E_CANCELLED"},
		{[]string{"task", "revoke"}, 12, "E_LOST_CLAIM"},
	} {
		code, out := fenceLore(t, person, "task", "add", "held work "+tc.want)
		id := fenceTaskID.FindString(out)
		if code != 0 || id == "" {
			t.Fatalf("task add: %d %s", code, out)
		}
		// A fresh holder clone per case: its first command fetches, so the
		// task is there; the heartbeat below is within fetch_ttl of it.
		holder := filepath.Join(root, "holder-"+tc.want)
		fenceGit(t, root, "clone", "-q", remote, holder)
		if code, out = fenceLore(t, holder, "task", "claim", id); code != 0 {
			t.Fatalf("claim: %d %s", code, out)
		}
		if code, out = fenceLore(t, person, append(append([]string{}, tc.human[:2]...), append([]string{id}, tc.human[2:]...)...)...); code != 0 {
			t.Fatalf("%v: %d %s", tc.human, code, out)
		}
		code, out = fenceLore(t, holder, "task", "heartbeat", id)
		if code != tc.code || !strings.Contains(out, tc.want) {
			t.Fatalf("heartbeat after %v: exit %d %s, want %d %s", tc.human[:2], code, out, tc.code, tc.want)
		}
	}

	// A task a teammate adds after the holder's last fetch (within
	// fetch_ttl) is claimed, not E_NOT_FOUND (B-ERRS2-1). A well-formed id
	// nobody added stays E_NOT_FOUND after the one refetch.
	holder := filepath.Join(root, "holder-E_CANCELLED")
	code, out := fenceLore(t, person, "task", "add", "added after the holder fetched")
	id := fenceTaskID.FindString(out)
	if code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = fenceLore(t, holder, "task", "claim", id); code != 0 || !strings.Contains(out, "claimed") {
		t.Fatalf("claim of a task added after the last fetch: exit %d %s", code, out)
	}
	// The same holds for the other coordination ops on a task id (B-ERRS2-2):
	// a note on a task added after the claim's refetch, still within
	// fetch_ttl, lands instead of E_NOT_FOUND.
	code, out = fenceLore(t, person, "task", "add", "added after the claim")
	if id = fenceTaskID.FindString(out); code != 0 || id == "" {
		t.Fatalf("task add: %d %s", code, out)
	}
	if code, out = fenceLore(t, holder, "task", "note", id, "progress"); code != 0 || !strings.Contains(out, "note") {
		t.Fatalf("note on a task added after the last fetch: exit %d %s", code, out)
	}
	// task show, done and cancel too (B-ERRS2-3): each on a task added
	// after the holder's previous command, still within fetch_ttl.
	for _, c := range []struct{ args []string }{{[]string{"show"}}, {[]string{"done"}}, {[]string{"cancel", "--yes"}}} {
		code, out = fenceLore(t, person, "task", "add", "added before "+c.args[0])
		if id = fenceTaskID.FindString(out); code != 0 || id == "" {
			t.Fatalf("task add: %d %s", code, out)
		}
		if code, out = fenceLore(t, holder, append([]string{"task", c.args[0], id}, c.args[1:]...)...); code != 0 || !strings.Contains(out, id) {
			t.Fatalf("task %s of a task added after the last fetch: exit %d %s", c.args[0], code, out)
		}
	}
	if code, out = fenceLore(t, holder, "task", "claim", "tsk_01a1a681c07e7dfe8ec01fc44d487e86"); code != 27 || !strings.Contains(out, "E_NOT_FOUND") {
		t.Fatalf("claim of an unknown id: exit %d %s, want 27 E_NOT_FOUND", code, out)
	}
}
