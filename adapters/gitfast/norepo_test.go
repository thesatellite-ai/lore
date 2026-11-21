package gitfast

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitStderr runs git in dir and returns its exit status and trimmed
// standard error.
func gitStderr(t *testing.T, dir string, args []string) (int, string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), strings.TrimSpace(stderr.String())
	}
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return 0, strings.TrimSpace(stderr.String())
}

// noRepoDir is a folder git finds no repository in or above (the test is
// skipped when the temp folder sits inside one).
func noRepoDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if code, _ := gitStderr(t, dir, []string{"rev-parse", "--show-toplevel"}); code == 0 {
		t.Skip("the temp folder is inside a git repository")
	}
	return dir
}

// TestNoRepoAnsweredWithoutGit: a repository question asked outside every
// repository is answered here with git's own status and message, starting
// no git (devco:D-826: a read outside every repo started up to ten git
// processes to hear "not a git repository"). A folder that holds a
// repository is never answered that way.
func TestNoRepoAnsweredWithoutGit(t *testing.T) {
	dir := noRepoDir(t)
	sub := filepath.Join(dir, "a", "b")
	must(t, os.MkdirAll(sub, 0o755))
	asks := [][]string{
		{"rev-parse", "--show-toplevel"},
		{"rev-parse", "--is-inside-work-tree"},
		{"rev-parse", "--is-bare-repository", "--show-object-format"},
		{"rev-parse", "--is-bare-repository", "--is-inside-git-dir"},
		{"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir"},
		{"rev-parse", "--git-path", "hooks"},
		{"-C", sub, "rev-parse", "--show-toplevel"},
	}
	ctx := context.Background()
	s := New()
	defer s.Close()
	for _, args := range asks {
		rep, ok := s.Answer(ctx, dir, args, nil, nil)
		if !ok {
			t.Fatalf("%v: not answered (git would run)", args)
		}
		code, stderr := gitStderr(t, dir, args)
		if rep.Code != code || rep.Stderr != stderr || len(rep.Out) != 0 {
			t.Errorf("%v: answered %d %q %q, git says %d %q", args, rep.Code, rep.Stderr, rep.Out, code, stderr)
		}
	}
	// not a repository question: left to git
	for _, args := range [][]string{{"rev-parse", "--sq-quote", "x"}, {"rev-parse", "HEAD"}, {"rev-parse"}} {
		if _, ok := s.Answer(ctx, dir, args, nil, nil); ok {
			t.Errorf("%v: answered, want git", args)
		}
	}
	// a repository made there later: a new process never answers "none"
	gitT(t, dir, "init", "-q", "-b", "main")
	s2 := New()
	defer s2.Close()
	if rep, ok := s2.Answer(ctx, sub, []string{"rev-parse", "--is-inside-work-tree"}, nil, nil); ok && rep.Code != 0 {
		t.Fatalf("inside a new repository answered %d %q", rep.Code, rep.Stderr)
	}
	// a variable that moves discovery: left to git
	t.Setenv("GIT_DIR", filepath.Join(dir, ".git"))
	if noRepoAbove(t.TempDir()) {
		t.Error("GIT_DIR set: answered none")
	}
}

// TestConfigPersistedOutsideRepo: the config snapshot of a folder outside
// every repository is kept across processes like a repository's (each
// read outside a repo ran `git config --list` once, devco:D-826), and is
// not used once a repository exists there.
func TestConfigPersistedOutsideRepo(t *testing.T) {
	dir := noRepoDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	must(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\temail = one@example.test\n"), 0o644))
	cache := t.TempDir()
	ctx := context.Background()
	execs := 0
	ask := func() string {
		s := New()
		s.SetCacheDir(cache)
		inner := s.execGit
		s.git = func(ctx context.Context, dir string, args []string) ([]byte, error) {
			execs++
			s.git = nil // inner is execGit itself: run the real git
			return inner(ctx, dir, args)
		}
		defer s.Close()
		rep, ok := s.Answer(ctx, dir, []string{"config", "--get", "user.email"}, nil, nil)
		if !ok {
			t.Fatal("not answered")
		}
		return string(rep.Out)
	}
	if got := ask(); got != "one@example.test\n" || execs != 1 {
		t.Fatalf("first: %q, %d execs", got, execs)
	}
	if got := ask(); got != "one@example.test\n" || execs != 1 {
		t.Fatalf("second process ran git again or read wrong: %q, %d execs", got, execs)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "config", "user.email", "repo@example.test")
	if got := ask(); got != "repo@example.test\n" {
		t.Fatalf("inside the new repository: %q (the kept snapshot of the plain folder was used)", got)
	}
}
