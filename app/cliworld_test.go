package app

// cliworld_test.go: run lore out of process so CLI tests can run in
// parallel.
//
// run (main.go) reads the process's working directory, environment and
// stdio, and so does everything under it (wire, git subprocesses, identity
// lookups). An in-process test therefore has to Chdir, Setenv and swap
// os.Stdout (runCaptured), which makes it serial: in this package that cost
// ~200 s of serial time, almost all of it multi-clone tests running dozens
// of commands each.
//
// A cliWorld instead re-executes this test binary as the lore command
// (TestMain below sees envCLIChild and calls main), with cmd.Dir and
// cmd.Env set per command. Each world owns its own temp HOME and folders,
// so tests built on it call t.Parallel. The child is the same compiled
// package code as the in-process path (same build tags, same liveclock),
// not a separately built binary: nothing is compiled per test, and the
// child's main() is exactly the release entry point. The trade-off: `go
// test -cover` does not count lines the child runs. Tests that must swap an
// in-process global (version, geteuid, os.Stdin) keep using runCaptured.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testenv"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// envCLIChild marks a re-execution of this test binary as the lore command
// (cliWorld.lore). It is inherited by anything the child starts, so a git
// merge driver or hook resolved through os.Executable runs lore too, as
// it would with the release binary.
const envCLIChild = "LORE_CMDTEST_CHILD"

// TestMain runs the lore command instead of the tests when this binary was
// started by cliWorld.lore; otherwise the tests.
func TestMain(m *testing.M) {
	if os.Getenv(envCLIChild) == "1" {
		Main() // exits with lore's own status
	}
	os.Exit(m.Run())
}

// personAuthor is the git identity the multi-clone tests commit with.
var personAuthor = map[string]string{"GIT_AUTHOR_NAME": "p", "GIT_AUTHOR_EMAIL": "p@example.com", "GIT_COMMITTER_NAME": "p", "GIT_COMMITTER_EMAIL": "p@example.com"}

// cliWorld is a scratch root plus the environment every command run in it
// gets (on top of the test process's own). Invariant: nothing a world does
// touches process-global state, so worlds of different tests run in
// parallel; one world's commands may also run from parallel subtests, and
// set is safe against them (each command copies the env when it starts).
type cliWorld struct {
	gitBin string // resolved git binary
	root   string // EvalSymlinks'd temp folder the test's repos live in

	mu  sync.Mutex
	env map[string]string
}

// newCLIWorld is a world with its own HOME and GIT_CONFIG_NOSYSTEM=1 (the
// machine's git and lore config cannot leak in). It skips when git is not
// installed, as the in-process tests did.
func newCLIWorld(t *testing.T) *cliWorld {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := map[string]string{"GIT_CONFIG_NOSYSTEM": "1"}
	for _, k := range testenv.HomeVars { // HOME, and USERPROFILE for Windows
		env[k] = home
	}
	return &cliWorld{gitBin: git, root: root, env: env}
}

// set makes every later command of this world see k=v ("" is an empty
// value, as t.Setenv(k, "") was).
func (w *cliWorld) set(k, v string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.env[k] = v
}

// setAll sets every pair of kv.
func (w *cliWorld) setAll(kv map[string]string) {
	for k, v := range kv {
		w.set(k, v)
	}
}

// person is asPerson for this world: no claude marker, LORE_HUMAN=1.
func (w *cliWorld) person() *cliWorld {
	for _, k := range claudeEnv {
		w.set(k, "")
	}
	w.set("LORE_HUMAN", "1")
	return w
}

// claude is asClaude for this world: a claude_code session with the given
// tool session id.
func (w *cliWorld) claude(session string) *cliWorld {
	for _, k := range claudeEnv {
		w.set(k, "")
	}
	w.set("LORE_HUMAN", "")
	w.set("CLAUDECODE", "1")
	w.set("CLAUDE_CODE_CHILD_SESSION", "1")
	w.set("CLAUDE_CODE_SESSION_ID", session)
	return w
}

// hermeticPath makes PATH, for every later command of this world, one
// folder that holds only git (a link to gitBin). A check that reads PATH,
// like doctor's "multiple lore binaries on PATH" warning, then sees this
// world and not the machine running the test: a dev box with two lore
// builds on its PATH must not change the result.
func (w *cliWorld) hermeticPath(t *testing.T) *cliWorld {
	t.Helper()
	if runtime.GOOS == "windows" {
		// git.exe finds its own install through its real location, and a
		// link named "git" is no program there: PATH is git's own folder
		// (Git\cmd holds git and its gui launchers, never a lore).
		w.set("PATH", filepath.Dir(w.gitBin))
		return w
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(w.gitBin, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	w.set("PATH", bin)
	return w
}

// author makes git commits (and lore's) carry the personAuthor identity.
func (w *cliWorld) author() *cliWorld {
	w.setAll(personAuthor)
	return w
}

// unscoped clears LORE_REPO and LORE_PROJECT_ID.
func (w *cliWorld) unscoped() *cliWorld {
	w.set(hosthooks.EnvRepo, "")
	w.set(hosthooks.EnvProjectID, "")
	return w
}

// environ is the test process's environment with this world's values (and
// extra, which wins) replacing any of the same name.
func (w *cliWorld) environ(extra map[string]string) []string {
	w.mu.Lock()
	over := make(map[string]string, len(w.env)+len(extra))
	for k, v := range w.env {
		over[k] = v
	}
	w.mu.Unlock()
	for k, v := range extra {
		over[k] = v
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k == envCLIChild {
			continue
		} else if _, ok := over[k]; ok {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range over {
		env = append(env, k+"="+v)
	}
	return env
}

// path is root/name.
func (w *cliWorld) path(name string) string { return filepath.Join(w.root, name) }

// git runs git in dir with the hooks switched off (the hooks lore init
// installs call the lore on PATH, which is not this build); it fails the
// test on error.
func (w *cliWorld) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// the resolved git binary, every argument its own argv entry, no shell
	cmd := &exec.Cmd{Path: w.gitBin, Args: append([]string{w.gitBin, "-C", dir, "-c", "core.hooksPath=/dev/null"}, args...), Env: w.environ(nil)}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

// lore runs lore args in dir and returns its exit status and both streams.
func (w *cliWorld) lore(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return w.loreEnv(t, dir, nil, args...)
}

// loreEnv is lore with extra variables for this one command.
func (w *cliWorld) loreEnv(t *testing.T, dir string, extra map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return w.loreIn(t, dir, extra, "", args...)
}

// loreIn is loreEnv with stdin reading input.
func (w *cliWorld) loreIn(t *testing.T, dir string, extra map[string]string, input string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return w.loreRun(t, dir, extra, input, 0, args...)
}

// loreWithin is lore that fails the test when the command has not returned
// within d (a deadlock guard); the child is killed.
func (w *cliWorld) loreWithin(t *testing.T, dir string, d time.Duration, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return w.loreRun(t, dir, nil, "", d, args...)
}

// cliRunDeadline bounds every lore run that names no deadline of its own:
// one command in a scratch repo answers in seconds even on a loaded gate,
// so a run past this is a hang (a deadlock, a prompt despite --yes) and
// fails at once with the command named and the child killed, instead of
// holding the package until go test's 10-minute alarm and leaving the
// child running after it (seen 2026-11-08: `move --yes` deadlocked).
const cliRunDeadline = 3 * time.Minute

// loreRun starts this test binary as lore in dir (envCLIChild set) and
// waits for it; deadline 0 is cliRunDeadline.
func (w *cliWorld) loreRun(t *testing.T, dir string, extra map[string]string, input string, deadline time.Duration, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if deadline <= 0 {
		deadline = cliRunDeadline
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	env := w.environ(extra)
	env = append(env, envCLIChild+"=1")
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr, cmd.Stdin = dir, env, &out, &errOut, strings.NewReader(input)
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("lore %s did not return within %s (a hang: deadlock or a prompt); stderr so far:\n%s", strings.Join(args, " "), deadline, errOut.String())
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("lore %v: %v", args, err)
		}
		return exit.ExitCode(), out.String(), errOut.String()
	}
	return 0, out.String(), errOut.String()
}

// loreOut is lore with both streams joined (fenceLore's shape).
func (w *cliWorld) loreOut(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	code, out, errOut := w.lore(t, dir, args...)
	return code, out + errOut
}

// mustLore is loreOut that fails the test on a non-zero exit.
func (w *cliWorld) mustLore(t *testing.T, dir string, args ...string) string {
	t.Helper()
	code, out := w.loreOut(t, dir, args...)
	if code != 0 {
		t.Fatalf("lore %s: exit %d %s", strings.Join(args, " "), code, out)
	}
	return out
}

// livePair is a pushed lore repo (a, named "tools") and a teammate's clone of
// it (b), both under w.root, with remote.git as their origin. The world
// should be person() and author() first.
func (w *cliWorld) livePair(t *testing.T) (string, string) {
	t.Helper()
	a := w.pushedLore(t, "a")
	b := w.path("b")
	w.git(t, w.root, "clone", "-q", w.path("remote.git"), b)
	return a, b
}

// pushedLore is a bare remote.git and a clone of it named name in which
// lore init ran (--name=tools), committed and pushed to main.
func (w *cliWorld) pushedLore(t *testing.T, name string) string {
	t.Helper()
	remote, a := w.path("remote.git"), w.path(name)
	w.git(t, w.root, "init", "-q", "--bare", "-b", "main", remote)
	w.git(t, w.root, "clone", "-q", remote, a)
	w.git(t, a, "commit", "-q", "--allow-empty", "-m", "init")
	w.mustLore(t, a, "init", "--non-interactive", "--name=tools")
	w.git(t, a, "add", "-A")
	w.git(t, a, "commit", "-q", "-m", "lore")
	w.git(t, a, "push", "-q", "origin", "HEAD:main")
	return a
}

// initRepo is envScopeRepo for this world: LORE_REPO and LORE_PROJECT_ID
// cleared, a git work tree at w.root with `lore init` run in it. It
// returns the work tree.
func (w *cliWorld) initRepo(t *testing.T) string {
	t.Helper()
	w.unscoped()
	w.git(t, w.root, "init", "-q", "-b", "main")
	w.mustLore(t, w.root, "init")
	return w.root
}
