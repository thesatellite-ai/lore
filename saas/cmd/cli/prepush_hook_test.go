package main

// Tests for the repository's own pre-push gate (.githooks/pre-push), which
// keeps code that has not passed `task check` from being pushed. It lives
// here, beside the docs drift test, because both guard the repository rather
// than the CLI's behaviour; the hook is a shell script, so the test runs it
// for real against scratch repositories.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// zeroSHA is how git names "no commit" in hook input (a deleted ref).
const zeroSHA = "0000000000000000000000000000000000000000"

// prePushHook is the hook under test, relative to this package.
var prePushHook = filepath.Join(docsRoot, ".githooks", "pre-push")

// loreTaskfile is the Taskfile whose check:stamp the stand-in task runs, so
// the stamp format the hook reads is the real one, not a copy.
var loreTaskfile = filepath.Join(docsRoot, "Taskfile.lore.yml")

// hookRepo is a scratch repository with one commit; the hook runs in it.
type hookRepo struct {
	t   *testing.T
	dir string
	bin string // holds the stand-in `task`
}

func newHookRepo(t *testing.T) *hookRepo {
	t.Helper()
	for _, tool := range []string{"git", "sh", "task"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	r := &hookRepo{t: t, dir: gitInitRepo(t), bin: t.TempDir()}
	r.git("config", "user.email", "t@example.com")
	r.git("config", "user.name", "t")
	r.write("a.txt", "one\n")
	r.git("add", ".")
	r.git("commit", "-qm", "one")
	return r
}

func (r *hookRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *hookRepo) write(rel, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, rel), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// standInTask installs a `task` that records each run and then either fails
// or runs the real check:stamp in the scratch repository.
func (r *hookRepo) standInTask(fail bool) {
	r.t.Helper()
	real, err := exec.LookPath("task")
	if err != nil {
		r.t.Fatal(err)
	}
	taskfile, err := filepath.Abs(loreTaskfile)
	if err != nil {
		r.t.Fatal(err)
	}
	body := "#!/bin/sh\necho ran >> \"" + filepath.Join(r.bin, "runs") + "\"\n"
	if fail {
		body += "exit 1\n"
	} else {
		body += "exec \"" + real + "\" --taskfile \"" + taskfile + "\" --dir \"" + r.dir + "\" check:stamp\n"
	}
	if err := os.WriteFile(filepath.Join(r.bin, "task"), []byte(body), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

// push runs the hook as git would for pushing HEAD (or deleting with sha =
// zeroSHA) and returns its exit code, its output, and how often task ran.
func (r *hookRepo) push(sha string, path string) (int, string, int) {
	r.t.Helper()
	hook, err := filepath.Abs(prePushHook)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command("sh", hook, "origin", "git@example.com:x.git")
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "PATH="+path)
	cmd.Stdin = strings.NewReader("refs/heads/main " + sha + " refs/heads/main " + zeroSHA + "\n")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	runs := 0
	if b, err := os.ReadFile(filepath.Join(r.bin, "runs")); err == nil {
		runs = strings.Count(string(b), "ran")
	}
	return code, string(out), runs
}

func (r *hookRepo) pathWithTask() string {
	return r.bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestPrePushRunsGateThenTrustsStamp(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(false)
	head := r.git("rev-parse", "HEAD")
	if code, out, runs := r.push(head, r.pathWithTask()); code != 0 || runs != 1 {
		t.Fatalf("first push must run the gate once and pass: %d runs=%d %s", code, runs, out)
	}
	if code, out, runs := r.push(head, r.pathWithTask()); code != 0 || runs != 1 || !strings.Contains(out, "already passed") {
		t.Fatalf("same code again must not re-run the gate: %d runs=%d %s", code, runs, out)
	}
	r.write("a.txt", "two\n")
	r.git("commit", "-qam", "two")
	if code, out, runs := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask()); code != 0 || runs != 2 {
		t.Fatalf("new code must re-run the gate: %d runs=%d %s", code, runs, out)
	}
}

func TestPrePushBlocksFailingGate(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(true)
	if code, out, _ := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask()); code == 0 || !strings.Contains(out, "push blocked") {
		t.Fatalf("a failing gate must block: %d %s", code, out)
	}
}

// The gate tests the working tree; with uncommitted changes that is not the
// code being pushed.
func TestPrePushBlocksUntestedCommit(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(false)
	head := r.git("rev-parse", "HEAD")
	r.write("a.txt", "edited, not committed\n")
	if code, out, _ := r.push(head, r.pathWithTask()); code == 0 || !strings.Contains(out, "uncommitted changes") {
		t.Fatalf("pushing code the gate did not test must be blocked: %d %s", code, out)
	}
}

func TestPrePushAllowsRefDeletion(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(true) // would fail if it ran
	if code, out, runs := r.push(zeroSHA, r.pathWithTask()); code != 0 || runs != 0 {
		t.Fatalf("deleting a remote ref needs no gate: %d runs=%d %s", code, runs, out)
	}
}

func TestPrePushWithoutTaskBlocks(t *testing.T) {
	r := newHookRepo(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(git), "task")); err == nil {
		t.Skip("task is installed next to git; cannot hide it")
	}
	path := filepath.Dir(git) + string(os.PathListSeparator) + filepath.Dir(sh)
	if code, out, _ := r.push(r.git("rev-parse", "HEAD"), path); code == 0 || !strings.Contains(out, "not installed") {
		t.Fatalf("without task the push must be blocked: %d %s", code, out)
	}
}
