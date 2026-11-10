package app

// B11 port of saas/cmd/cli env_scope_test.go TestEnvScopeDefaults, run
// through main in a child process (the release binary's whole path,
// cliworld_test.go): row commands
// honour LORE_REPO and LORE_PROJECT_ID as v0.1.17 did (flag > env).

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// runCaptured runs args through run with stdout and stderr captured.
// Not parallel-safe: it swaps os.Stdout and os.Stderr. Only for tests that
// must swap an in-process global too; everything else uses a cliWorld.
func runCaptured(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return captureRun(t, func() int { return run(context.Background(), args) })
}

// captureRun calls do with os.Stdout and os.Stderr swapped for pipes and
// returns its status and what it printed (in-process: the test is serial).
func captureRun(t *testing.T, do func() int) (code int, stdout, stderr string) {
	t.Helper()
	capture := func(dst **os.File) (func() string, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		old := *dst
		*dst = w
		var buf bytes.Buffer
		done := make(chan struct{})
		go func() { _, _ = io.Copy(&buf, r); close(done) }()
		return func() string {
			*dst = old
			_ = w.Close()
			<-done
			_ = r.Close()
			return buf.String()
		}, nil
	}
	stopOut, err := capture(&os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	stopErr, err := capture(&os.Stderr)
	if err != nil {
		stopOut()
		t.Fatal(err)
	}
	code = do()
	return code, stopOut(), stopErr()
}

// envScopeRepo is an initialised lore repo in a git work tree, with HOME
// and the working directory pointed at scratch folders.
func envScopeRepo(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(hosthooks.EnvRepo, "")
	t.Setenv(hosthooks.EnvProjectID, "")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: git, Args: []string{git, "-C", dir, "init", "-q", "-b", "main"}}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(dir)
	if code, out, errOut := runCaptured(t, "init"); code != 0 {
		t.Fatalf("lore init: exit %d\n%s\n%s", code, out, errOut)
	}
	return dir
}

// TestEnvScopeDefaults: an unknown LORE_REPO or LORE_PROJECT_ID is refused
// naming the value; an explicit --project wins over the variable; with
// both unset the same command works.
func TestEnvScopeDefaults(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	dir := w.initRepo(t)
	w.set(hosthooks.EnvRepo, "nosuchrepo")
	if code, _, errOut := w.lore(t, dir, "memory", "add", "--body", "scoped note"); code == 0 || !strings.Contains(errOut, "nosuchrepo") {
		t.Fatalf("LORE_REPO is not honoured: exit %d %s", code, errOut)
	}
	w.set(hosthooks.EnvRepo, "")
	w.set(hosthooks.EnvProjectID, "prj_nope")
	if code, _, errOut := w.lore(t, dir, "memory", "list"); code == 0 || !strings.Contains(errOut, "prj_nope") {
		t.Fatalf("LORE_PROJECT_ID is not honoured: exit %d %s", code, errOut)
	}
	w.set(hosthooks.EnvProjectID, "")
	code, out, errOut := w.lore(t, dir, "project", "list", "--json")
	if code != 0 {
		t.Fatalf("project list: exit %d %s", code, errOut)
	}
	pid := firstProjectID(out)
	if pid == "" {
		t.Fatalf("no project id in %s", out)
	}
	w.set(hosthooks.EnvProjectID, "prj_nope")
	if code, _, errOut := w.lore(t, dir, "memory", "list", "--project", pid); code != 0 {
		t.Fatalf("--project does not beat LORE_PROJECT_ID: exit %d %s", code, errOut)
	}
	w.set(hosthooks.EnvProjectID, "")
	if code, _, errOut := w.lore(t, dir, "memory", "add", "--body", "unscoped note"); code != 0 {
		t.Fatalf("unset variables: exit %d %s", code, errOut)
	}
}

// firstProjectID is the first "prj_…" id in s.
func firstProjectID(s string) string {
	i := strings.Index(s, "prj_")
	if i < 0 {
		return ""
	}
	j := i
	for j < len(s) && (s[j] == '_' || s[j] >= '0' && s[j] <= '9' || s[j] >= 'a' && s[j] <= 'z') {
		j++
	}
	return s[i:j]
}
