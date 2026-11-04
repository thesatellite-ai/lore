package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// coreFixture is a real lore repo for unit-level tests of code that reads
// or writes through the 0.1.18 core (coreApp): `lore init` and the row
// writes run through the built binary (e2eBin) with a scratch HOME, the
// way a user makes them, and mount swaps the package's coreApp for a core
// over that repo so the function under test can be called directly.
//
// Invariant: a test using it must NOT call t.Parallel. coreApp is one
// process-wide value (like rootCmd); swapping it is only safe while no
// parallel test runs, which Go guarantees for a non-parallel top-level
// test (parallel tests are paused until the sequential ones finish).
// t.Setenv and t.Chdir enforce this: both panic in a parallel test.
type coreFixture struct {
	t    *testing.T
	dir  string // the repo (git work tree, `lore init` done)
	home string // the scratch HOME; ~/.lore lives under it
	env  []string
}

// newCoreFixture makes a git repo with one commit and runs `lore init` in
// it. It skips under -short, where TestMain builds no binary.
func newCoreFixture(t *testing.T) *coreFixture {
	t.Helper()
	if e2eBin == "" {
		t.Skip("needs the lore binary: skipped with -short")
	}
	root := t.TempDir()
	f := &coreFixture{t: t, dir: filepath.Join(root, "repo"), home: filepath.Join(root, "home")}
	for _, d := range []string{f.dir, f.home} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	f.env = []string{"HOME=" + f.home, "PATH=" + e2ePath(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1",
		"GIT_AUTHOR_NAME=dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=dev", "GIT_COMMITTER_EMAIL=dev@example.com"}
	f.git("init", "-q", "-b", "main")
	f.git("commit", "-q", "--allow-empty", "-m", "init")
	f.lore("init")
	return f
}

// run executes name in the repo with the fixture's environment only (the
// developer's own HOME and git identity never leak in).
func (f *coreFixture) run(name string, args ...string) result {
	f.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = f.dir
	cmd.Env = f.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			f.t.Fatalf("%s %v: %v", name, args, err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func (f *coreFixture) git(args ...string) {
	f.t.Helper()
	if r := f.run("git", args...); r.code != 0 {
		f.t.Fatalf("git %v exited %d: %s", args, r.code, r.stderr)
	}
}

// lore runs the binary and fails the test on a non-zero exit.
func (f *coreFixture) lore(args ...string) result {
	f.t.Helper()
	r := f.run(e2eBin, args...)
	if r.code != 0 {
		f.t.Fatalf("lore %v exited %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// addID runs an `add` command with --json and returns the new row's id.
func (f *coreFixture) addID(args ...string) string {
	f.t.Helper()
	r := f.lore(append(args, "--json")...)
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.ID == "" {
		f.t.Fatalf("lore %v: no id in %q: %v", args, r.stdout, err)
	}
	return out.ID
}

// mount points coreApp at a core over the fixture repo (the same
// wire.Options as mountCore, repo and home aside: keep the two in step) for the rest of the
// test (HOME and the working directory follow, since the core's hooks read
// both). Call it after the binary's writes: the core opens its cache on
// first use. The previous coreApp is put back by t.Cleanup.
func (f *coreFixture) mount() {
	f.t.Helper()
	f.t.Setenv("HOME", f.home)
	f.t.Chdir(f.dir)
	app, err := wire.New(coreOptions(f.dir, filepath.Join(f.home, ".lore")))
	if err != nil {
		f.t.Fatal(err)
	}
	prev := coreApp
	coreApp = app
	f.t.Cleanup(func() {
		coreApp = prev
		if err := app.Close(); err != nil {
			f.t.Error(err)
		}
	})
}

// projectID is the core's project id of the fixture repo (mount first).
func (f *coreFixture) projectID() string {
	f.t.Helper()
	pid, err := coreApp.Project(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return pid.String()
}

// taskRow returns a task's row file: from the work tree
// (.lore/data/tasks/<id>.json) or, when the task lives on the repo's live
// ref (refs/lore/live/*), from there.
func (f *coreFixture) taskRow(id string) []byte {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, ".lore", "data", tableTasks, id+".json"))
	if err == nil {
		return b
	}
	if !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	refs := f.run("git", "for-each-ref", "--format=%(refname)", "refs/lore/live")
	for _, ref := range strings.Fields(refs.stdout) {
		for _, path := range strings.Split(f.run("git", "ls-tree", "-r", "--name-only", ref).stdout, "\n") {
			if strings.HasSuffix(path, "/"+id+".json") {
				return []byte(f.run("git", "show", ref+":"+path).stdout)
			}
		}
	}
	f.t.Fatalf("task %s: no row file in the work tree or on refs/lore/live", id)
	return nil
}

// copyTo makes a second clone of the fixture: the same checkout (git and
// .lore/data copied) in another directory, with its own fresh HOME, as a
// teammate's machine would hold it after a pull.
func (f *coreFixture) copyTo(t *testing.T) *coreFixture {
	t.Helper()
	root := t.TempDir()
	g := &coreFixture{t: t, dir: filepath.Join(root, "repo"), home: filepath.Join(root, "home")}
	if err := os.MkdirAll(g.home, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(g.dir, os.DirFS(f.dir)); err != nil {
		t.Fatalf("copy the checkout: %v", err)
	}
	g.env = append([]string(nil), f.env...)
	for i, kv := range g.env {
		if strings.HasPrefix(kv, "HOME=") {
			g.env[i] = "HOME=" + g.home
		}
	}
	return g
}
