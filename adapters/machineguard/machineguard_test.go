package machineguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// sandbox gives the test its own HOME (global git config) and lore home.
func sandbox(t *testing.T) (home, lore string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home, filepath.Join(home, ".lore")
}

func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestInstallRemoveRestoresExactly: remove puts the global core.hooksPath
// back to its exact text (a ~/ folder), and unset stays unset.
func TestInstallRemoveRestoresExactly(t *testing.T) {
	ctx := context.Background()
	for _, prev := range []string{"", "~/.hook-manager"} {
		home, lore := sandbox(t)
		if prev != "" {
			if out, err := git(t, home, "config", "--global", HooksPathKey, prev); err != nil {
				t.Fatal(out)
			}
		}
		if _, err := Install(ctx, gitref.Exec{}, lore, true); err != nil {
			t.Fatal(err)
		}
		if got, _ := git(t, home, "config", "--global", "--get", HooksPathKey); got != prev {
			t.Fatalf("a dry run changed core.hooksPath to %q", got)
		}
		res, err := Install(ctx, gitref.Exec{}, lore, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.PrevOn != (prev != "") || res.Prev != prev {
			t.Fatalf("install recorded previous %v %q, want %q", res.PrevOn, res.Prev, prev)
		}
		if got, _ := git(t, home, "config", "--global", "--get", HooksPathKey); !SameDir(got, Dir(lore)) {
			t.Fatalf("install set core.hooksPath to %q, want %s", got, Dir(lore))
		}
		again, err := Install(ctx, gitref.Exec{}, lore, false)
		if err != nil || !again.Already || again.Prev != prev {
			t.Fatalf("a second install lost the previous value: %+v %v", again, err)
		}
		st, err := Read(ctx, gitref.Exec{}, lore)
		if err != nil || !st.On || len(st.Broken) != 0 {
			t.Fatalf("status after install: %+v %v", st, err)
		}
		if _, err := Remove(ctx, gitref.Exec{}, lore, false); err != nil {
			t.Fatal(err)
		}
		got, gerr := git(t, home, "config", "--global", "--get", HooksPathKey)
		if prev == "" && gerr == nil {
			t.Fatalf("remove left core.hooksPath set to %q; none was set before", got)
		}
		if prev != "" && got != prev {
			t.Fatalf("remove restored core.hooksPath to %q, want exactly %q", got, prev)
		}
		if _, err := os.Stat(Dir(lore)); !os.IsNotExist(err) {
			t.Fatalf("remove left the folder %s: %v", Dir(lore), err)
		}
	}
}

// TestRemoveLeavesAChangedSetting: a global core.hooksPath someone
// changed after the install is not overwritten.
func TestRemoveLeavesAChangedSetting(t *testing.T) {
	home, lore := sandbox(t)
	if _, err := Install(context.Background(), gitref.Exec{}, lore, false); err != nil {
		t.Fatal(err)
	}
	if out, err := git(t, home, "config", "--global", HooksPathKey, "/opt/other-hooks"); err != nil {
		t.Fatal(out)
	}
	res, err := Remove(context.Background(), gitref.Exec{}, lore, false)
	if err != nil || res.Left != "/opt/other-hooks" {
		t.Fatalf("remove: %+v %v", res, err)
	}
	if got, _ := git(t, home, "config", "--global", "--get", HooksPathKey); got != "/opt/other-hooks" {
		t.Fatalf("remove overwrote a changed core.hooksPath: %q", got)
	}
}

// TestDispatchersChainTheRepoHooks: with the guard on, a repo's own
// .git/hooks still run (a refusing pre-commit stops the commit), and
// Own maps the guard's folder to that .git/hooks.
func TestDispatchersChainTheRepoHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh hooks")
	}
	home, lore := sandbox(t)
	t.Setenv("PATH", "/usr/bin:/bin") // no lore on PATH: only the chaining runs
	if _, err := Install(context.Background(), gitref.Exec{}, lore, false); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	for _, a := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "config", "user.email", "pat@example.test"}, {"-C", repo, "config", "user.name", "Pat"}} {
		if out, err := git(t, home, a...); err != nil {
			t.Fatal(out)
		}
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho own-hook-refuses >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := git(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	if err == nil || !strings.Contains(out, "own-hook-refuses") {
		t.Fatalf("the repo's own pre-commit did not run under the guard (err %v): %s", err, out)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := git(t, repo, "commit", "-q", "--allow-empty", "-m", "x"); err != nil {
		t.Fatalf("commit failed with a passing own hook: %s", out)
	}
	own, shared, ok := Own(context.Background(), gitref.Exec{}, lore, Dir(lore), repo)
	if !ok || shared || !SameDir(own, filepath.Join(repo, ".git", "hooks")) {
		t.Fatalf("Own = %q shared=%v ok=%v, want the repo's .git/hooks", own, shared, ok)
	}
}
