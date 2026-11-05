package main

// B11 port of saas/cmd/cli env_scope_test.go TestEnvScopeDefaults, run
// in process through run (the release binary's whole path): row commands
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
// Not parallel-safe: it swaps os.Stdout and os.Stderr.
func runCaptured(t *testing.T, args ...string) (code int, stdout, stderr string) {
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
	code = run(context.Background(), args)
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
	envScopeRepo(t)
	t.Setenv(hosthooks.EnvRepo, "nosuchrepo")
	if code, _, errOut := runCaptured(t, "memory", "add", "--body", "scoped note"); code == 0 || !strings.Contains(errOut, "nosuchrepo") {
		t.Fatalf("LORE_REPO is not honoured: exit %d %s", code, errOut)
	}
	t.Setenv(hosthooks.EnvRepo, "")
	t.Setenv(hosthooks.EnvProjectID, "prj_nope")
	if code, _, errOut := runCaptured(t, "memory", "list"); code == 0 || !strings.Contains(errOut, "prj_nope") {
		t.Fatalf("LORE_PROJECT_ID is not honoured: exit %d %s", code, errOut)
	}
	t.Setenv(hosthooks.EnvProjectID, "")
	code, out, errOut := runCaptured(t, "project", "list", "--json")
	if code != 0 {
		t.Fatalf("project list: exit %d %s", code, errOut)
	}
	pid := firstProjectID(out)
	if pid == "" {
		t.Fatalf("no project id in %s", out)
	}
	t.Setenv(hosthooks.EnvProjectID, "prj_nope")
	if code, _, errOut := runCaptured(t, "memory", "list", "--project", pid); code != 0 {
		t.Fatalf("--project does not beat LORE_PROJECT_ID: exit %d %s", code, errOut)
	}
	t.Setenv(hosthooks.EnvProjectID, "")
	if code, _, errOut := runCaptured(t, "memory", "add", "--body", "unscoped note"); code != 0 {
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
