package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// renameRepo is a git work tree with one commit holding files, then a
// second commit made by change (git mv / rm), in a HOME of its own.
func renameRepo(t *testing.T, files []string, change ...[]string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	dir := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		// built directly (as gitcommit does): the program is the resolved
		// git binary and every argument is its own argv entry, no shell
		cmd := &exec.Cmd{Path: gitPath, Args: append([]string{"git", "-C", dir}, args...)}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	for i, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		// distinct content per file, so rename detection pairs them exactly
		if err := os.WriteFile(p, []byte("file number "+string(rune('a'+i))+" with enough text to be detected as a rename\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	for _, c := range change {
		git(c...)
	}
	git("commit", "-q", "-m", "two")
	return dir
}

// TestRenamedTo (CONTEXT_BUDGET 6.2, OV25): lore sync's rename hint finds
// the folder a scoped folder's files moved to in git: a plain git mv, a
// nested folder, files deeper inside; a deleted folder, a rename into a
// folder that is gone again, a file renamed AND moved, and a folder whose
// name only shares a prefix give no hint.
func TestRenamedTo(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		files  []string
		change [][]string
		old    string
		want   string
	}{
		{name: "git mv folder", files: []string{"api/a.go", "api/b.go"}, change: [][]string{{"mv", "api", "service"}}, old: "api", want: "service"},
		{name: "nested old and new", files: []string{"apps/web/x.ts"}, change: [][]string{{"mv", "apps/web", "apps/site"}}, old: "apps/web", want: "apps/site"},
		{name: "deep files", files: []string{"old/x/y/z.go"}, change: [][]string{{"mv", "old", "new"}}, old: "old", want: "new"},
		{name: "deleted folder", files: []string{"gone/a.go"}, change: [][]string{{"rm", "-q", "-r", "gone"}}, old: "gone", want: ""},
		{name: "file renamed and moved", files: []string{"lib/a.go"}, change: [][]string{{"mv", "lib/a.go", "a2.go"}}, old: "lib", want: ""},
		{name: "prefix-only name", files: []string{"api2/a.go", "api/keep.go"}, change: [][]string{{"mv", "api2", "svc"}}, old: "api", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := renameRepo(t, c.files, c.change...)
			if got := renamedTo(ctx, dir, c.old); got != c.want {
				t.Fatalf("renamedTo(%s) = %q, want %q", c.old, got, c.want)
			}
		})
	}
	// renamed, then the new folder removed in a later commit: no hint
	dir := renameRepo(t, []string{"a/f.go"}, []string{"mv", "a", "b"})
	if err := os.RemoveAll(filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if got := renamedTo(ctx, dir, "a"); got != "" {
		t.Fatalf("new folder gone: %q", got)
	}
	// not a git checkout: no hint, no error
	if got := renamedTo(ctx, t.TempDir(), "a"); got != "" {
		t.Fatalf("no git: %q", got)
	}
}
