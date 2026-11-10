package repoguard

// Tests for the repository's own pre-push gate (.githooks/pre-push), which
// keeps code that has not passed `task check` from being pushed. The hook is
// a shell script, so the test runs it for real against scratch repositories.
// Ported from saas/cmd/cli/prepush_hook_test.go (B11).

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// repoRoot is the repository root, relative to this package.
const repoRoot = "../.."

// execMode is the mode of a stand-in script (the fake `task`): owner
// read/write/exec only, the strictest mode the shell can still exec.
const execMode os.FileMode = 0o700

// zeroSHA is how git names "no commit" in hook input (a deleted ref).
const zeroSHA = "0000000000000000000000000000000000000000"

// prePushHook is the hook under test, relative to this package.
var prePushHook = filepath.Join(repoRoot, ".githooks", "pre-push")

// loreTaskfile is the Taskfile whose check:stamp the stand-in task runs, so
// the stamp format the hook reads is the real one, not a copy.
var loreTaskfile = filepath.Join(repoRoot, "Taskfile.lore.yml")

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
	cmd := testCmd(r.t, "git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *hookRepo) write(rel, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, rel), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// Stand-in `task` behaviours.
type standInMode int

const (
	// standInPass runs the real check:begin and check:stamp: a passing gate.
	standInPass standInMode = iota
	// standInFail exits non-zero: a failing gate.
	standInFail
	// standInMutate runs check:begin, changes a tracked file (what `ds scan`
	// did to .ds/ledger.tsv), then check:stamp: a gate step that modifies
	// tracked files.
	standInMutate
)

// standInTask installs a `task` that records each run and then behaves as
// mode says, using the real Taskfile tasks in the scratch repository.
func (r *hookRepo) standInTask(mode standInMode) {
	r.t.Helper()
	real, err := exec.LookPath("task")
	if err != nil {
		r.t.Fatal(err)
	}
	taskfile, err := filepath.Abs(loreTaskfile)
	if err != nil {
		r.t.Fatal(err)
	}
	run := "\"" + real + "\" --taskfile \"" + taskfile + "\" --dir \"" + r.dir + "\" "
	body := "#!/bin/sh\nset -e\necho ran >> \"" + filepath.Join(r.bin, "runs") + "\"\n"
	switch mode {
	case standInFail:
		body += "exit 1\n"
	case standInMutate:
		body += run + "check:begin\necho changed >> \"" + filepath.Join(r.dir, "a.txt") + "\"\n" + run + "check:stamp\n"
	default:
		body += run + "check:begin\n" + run + "check:stamp\n"
	}
	writeExecutable(r.t, filepath.Join(r.bin, "task"), body)
}

// push runs the hook as git would for pushing HEAD (or deleting with sha =
// zeroSHA) and returns its exit code, its output, and how often task ran.
func (r *hookRepo) push(sha string, path string) (int, string, int) {
	r.t.Helper()
	hook, err := filepath.Abs(prePushHook)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := testCmd(r.t, "sh", hook, "origin", "git@example.com:x.git")
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "PATH="+path)
	cmd.Stdin = strings.NewReader("refs/heads/main " + sha + " refs/heads/main " + zeroSHA + "\n")
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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
	r.standInTask(standInPass)
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
	r.standInTask(standInFail)
	if code, out, _ := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask()); code == 0 || !strings.Contains(out, "push blocked") {
		t.Fatalf("a failing gate must block: %d %s", code, out)
	}
}

// The gate tests the working tree; with uncommitted changes that is not the
// code being pushed.
func TestPrePushBlocksUntestedCommit(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(standInPass)
	head := r.git("rev-parse", "HEAD")
	r.write("a.txt", "edited, not committed\n")
	if code, out, _ := r.push(head, r.pathWithTask()); code == 0 || !strings.Contains(out, "uncommitted changes") {
		t.Fatalf("pushing code the gate did not test must be blocked: %d %s", code, out)
	}
}

func TestPrePushAllowsRefDeletion(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(standInFail) // would fail if it ran
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

// A gate step that modifies tracked files (ds scan rewrote .ds/ledger.tsv)
// means the gate did not test one version of the code: the stamp is refused,
// so the push is blocked with the reason, instead of every later push being
// blocked as "untested" with no explanation.
func TestPrePushRefusesGateThatModifiesTrackedFiles(t *testing.T) {
	r := newHookRepo(t)
	r.standInTask(standInMutate)
	code, out, _ := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask())
	if code == 0 || !strings.Contains(out, "tracked files changed while the gate ran") || !strings.Contains(out, "a.txt") {
		t.Fatalf("a mutating gate step must be named and block the push: %d %s", code, out)
	}
}

// gitInitRepo makes an empty git repository and returns its resolved path.
func gitInitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := testCmd(t, "git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

// testCmd prepares name (resolved on PATH, one argv entry per argument, so
// no shell parses a variable argument); a missing program fails the test.
func testCmd(t testing.TB, name string, args ...string) *exec.Cmd {
	t.Helper()
	bin, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for this test: %v", name, err)
	}
	return &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}
}

// writeExecutable writes a stand-in script under syscall.ForkLock, so a
// concurrent fork cannot inherit the write descriptor and make the exec
// fail with ETXTBSY ("text file busy").
func writeExecutable(t testing.TB, path, script string) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte(script), execMode)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
}
