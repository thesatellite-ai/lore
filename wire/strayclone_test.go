package wire

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStrayCloneLines: A-16, a renamed store clone folder is named when
// lore sets up the clone at the store's path again; another store's clone
// and a person's own clone outside stores/ are not.
func TestStrayCloneLines(t *testing.T) {
	home := t.TempDir()
	stores := filepath.Join(home, ".lore", "stores")
	clone := func(rel, url string) string {
		dir := filepath.Join(stores, filepath.FromSlash(rel))
		for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", url}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		return dir
	}
	cur := clone("acme/team-store", "git@git.example.test:acme/team-store.git")
	renamed := clone("acme/team-store-renamed", "ssh://git@git.example.test/acme/team-store")
	clone("acme/client-store", "git@git.example.test:acme/client-store.git")
	got := strayCloneLines(cur)
	if len(got) != 1 || !strings.Contains(got[0], renamed+" is also a clone of this store") || !strings.Contains(got[0], "then delete it") {
		t.Fatalf("lines: %q", got)
	}
	if got := strayCloneLines(filepath.Join(stores, "acme", "client-store")); len(got) != 0 {
		t.Fatalf("another store's clone named: %q", got)
	}
	if got := strayCloneLines(filepath.Join(home, "own-clone")); got != nil {
		t.Fatalf("a clone outside stores/: %q", got)
	}
}

// A store used on its default branch and pinned to staging has two clones
// on purpose (<repo> and <repo>@staging): neither names the other as a
// renamed copy to delete; a renamed copy beside them still is named.
// Break-verified: without otherBranchClone the pinned clone names the
// default one ("then delete it").
func TestStrayCloneLinesKeepBranchClones(t *testing.T) {
	home := t.TempDir()
	stores := filepath.Join(home, ".lore", "stores")
	url := "git@git.example.test:acme/notes.git"
	clone := func(rel, branch string) string {
		dir := filepath.Join(stores, filepath.FromSlash(rel))
		for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", url}, {"-C", dir, "symbolic-ref", "HEAD", "refs/heads/" + branch}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		return dir
	}
	main := clone("acme/notes", "main")
	staging := clone("acme/notes@staging", "staging")
	if got := strayCloneLines(staging); len(got) != 0 {
		t.Fatalf("the pinned clone named the default clone: %q", got)
	}
	if got := strayCloneLines(main); len(got) != 0 {
		t.Fatalf("the default clone named the pinned clone: %q", got)
	}
	copied := clone("acme/notes-old", "main")
	if got := strayCloneLines(main); len(got) != 1 || !strings.Contains(got[0], copied+" is also a clone of this store") {
		t.Fatalf("a renamed copy beside branch clones: %q", got)
	}
}
