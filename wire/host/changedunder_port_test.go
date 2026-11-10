package host

// The v0.1.17 gitsetup Uncommitted tests (saas/pkg/aicoder/gitsetup
// gitsetup_test.go TestUncommittedAndIgnored, TestUncommitted_UnstagedAndSubdir),
// ported before phase E onto changedUnder, the pre-commit hook's list of
// changed row files (precommit.go). Review finding kept: an UNSTAGED
// modification (" M") as the first entry must not lose its first path
// character, a rename lists the new path only, and paths are relative to
// the project dir even when the project is a subdirectory of the repo.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

func TestChangedUnderUntrackedUnstagedRenameSubdir(t *testing.T) {
	ctx := context.Background()
	top := t.TempDir()
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	gitT(t, top, "init", "-q")
	gitT(t, top, "config", "user.email", "t@example.com")
	gitT(t, top, "config", "user.name", "t")
	project := filepath.Join(top, "svc")
	data := filepath.Join(project, ".lore", "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	repo, err := gitref.Open(ctx, project, gitref.Exec{})
	if err != nil {
		t.Fatal(err)
	}
	list := func() []string {
		t.Helper()
		got, _, lerr := changedUnder(ctx, repo, project, ".lore/data")
		if lerr != nil {
			t.Fatal(lerr)
		}
		slices.Sort(got)
		return got
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("empty data dir: %v", got)
	}
	for _, n := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(data, n), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file outside the pathspec is never listed.
	if err := os.WriteFile(filepath.Join(project, "other.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := list(), []string{".lore/data/a.json", ".lore/data/b.json"}; !slices.Equal(got, want) {
		t.Fatalf("untracked: %v, want %v", got, want)
	}
	gitT(t, top, "add", ".")
	gitT(t, top, "commit", "-q", "-m", "base")
	if got := list(); len(got) != 0 {
		t.Fatalf("clean after commit: %v", got)
	}
	// Unstaged modification (" M", the first entry) + staged rename.
	if err := os.WriteFile(filepath.Join(data, "a.json"), []byte(`{"x":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "mv", "svc/.lore/data/b.json", "svc/.lore/data/c.json")
	got := list()
	if want := []string{".lore/data/a.json", ".lore/data/c.json"}; !slices.Equal(got, want) {
		t.Fatalf("unstaged + rename: %v, want %v", got, want)
	}
	for _, p := range got {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(p))); err != nil {
			t.Fatalf("path %q does not resolve from the project dir: %v", p, err)
		}
	}
	// OS clutter (untracked, no global excludes file) is left out and
	// reported, so the hook stages files by name.
	for _, n := range []string{".DS_Store", "._a.json"} {
		if err := os.WriteFile(filepath.Join(data, n), []byte("\x00\x00\x00\x01Bud1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got2, junk, lerr := changedUnder(ctx, repo, project, ".lore/data"); lerr != nil || !junk || len(got2) != 2 {
		t.Fatalf("with clutter: %v junk %v err %v, want the two row files and junk", got2, junk, lerr)
	}
}
