package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// fakeBin writes an executable shell script name into dir.
func fakeBin(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// sandbox is a temp HOME and a fake-bin folder first on PATH; real git
// stays reachable. The process env is changed (t.Setenv), so tests using it
// are not parallel.
type sandbox struct {
	home, bin, tmp string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	s := sandbox{home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin"), tmp: filepath.Join(root, "tmp")}
	for _, d := range []string{s.home, s.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", s.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", s.home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.home, ".config"))
	t.Setenv("GIT_SSH_COMMAND", "")
	return s
}

func (s sandbox) prober(o Options) *Prober {
	o.Home, o.TmpDir = s.home, s.tmp
	return New(o)
}

// git runs real git in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=mira", "GIT_AUTHOR_EMAIL=mira@agency.example", "GIT_COMMITTER_NAME=mira", "GIT_COMMITTER_EMAIL=mira@agency.example")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// bareRepo makes a bare repo at path; with files, one commit on main holding them.
func bareRepo(t *testing.T, path string, files ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(path), "init", "--quiet", "--bare", "--initial-branch=main", path)
	if len(files) == 0 {
		return
	}
	work := t.TempDir()
	gitT(t, work, "init", "--quiet", "--initial-branch=main", ".")
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(work, f), []byte("x: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, work, "add", ".")
	gitT(t, work, "commit", "--quiet", "-m", "seed")
	gitT(t, work, "push", "--quiet", path, "main")
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
