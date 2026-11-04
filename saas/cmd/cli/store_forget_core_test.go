package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// forgetGit runs git in dir for a test fixture.
func forgetGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// coreCloneFixture lays out a core clone of a store the way the git
// backend does: <home>/clones/<host>/<path>.git (bare, tracking refs under
// refs/remotes/origin) and <home>/clones/<host>/<path>/work (its worktree
// on main), fetched from a pushed origin.
func coreCloneFixture(t *testing.T, home, norm string) coreClone {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	forgetGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	forgetGit(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "lore-store.yaml"), []byte("_v: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetGit(t, seed, "add", "-A")
	forgetGit(t, seed, "commit", "-q", "-m", "init")
	forgetGit(t, seed, "push", "-q", origin, "main")
	base := filepath.Join(home, "clones", filepath.FromSlash(norm))
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		t.Fatal(err)
	}
	forgetGit(t, root, "clone", "-q", "--bare", origin, base+".git")
	forgetGit(t, base+".git", "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
	forgetGit(t, base+".git", "worktree", "add", "-q", filepath.Join(base, "work"), "main")
	return coreClone{bare: base + ".git", base: base}
}

// TestCoreCloneUnpushed: what blocks `lore store forget` in the core's
// clone (side effect 20): a local commit no origin ref holds, a dirty
// worktree, an unreadable clone (fail closed); a clean clone does not.
func TestCoreCloneUnpushed(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		mess func(t *testing.T, c coreClone)
		want string // substring of the one pending entry; "" = nothing pending
	}{
		{"clean clone", func(*testing.T, coreClone) {}, ""},
		{"local commit not pushed", func(t *testing.T, c coreClone) {
			w := filepath.Join(c.base, "work")
			if err := os.WriteFile(filepath.Join(w, "row.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			forgetGit(t, w, "add", "-A")
			forgetGit(t, w, "commit", "-q", "-m", "written while offline")
		}, "1 unpushed commit(s)"},
		{"uncommitted change", func(t *testing.T, c coreClone) {
			if err := os.WriteFile(filepath.Join(c.base, "work", "ünï.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "uncommitted change(s)"},
		{"bare clone unreadable", func(t *testing.T, c coreClone) {
			if err := os.RemoveAll(filepath.Join(c.bare, "objects")); err != nil {
				t.Fatal(err)
			}
		}, "unreadable"},
		{"pushed after the commit", func(t *testing.T, c coreClone) {
			w := filepath.Join(c.base, "work")
			if err := os.WriteFile(filepath.Join(w, "row.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			forgetGit(t, w, "add", "-A")
			forgetGit(t, w, "commit", "-q", "-m", "w")
			forgetGit(t, c.bare, "push", "-q", "origin", "main")
			forgetGit(t, c.bare, "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := coreCloneFixture(t, t.TempDir(), "github.com/northwind/lore")
			tc.mess(t, c)
			got := coreCloneUnpushed(ctx, c)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want nothing pending, got %v", got)
				}
				return
			}
			if len(got) == 0 || !strings.Contains(strings.Join(got, "; "), tc.want) {
				t.Fatalf("want pending %q, got %v", tc.want, got)
			}
		})
	}
}

// TestCoreClonesOf: which folders a forget of a url covers: the clone the
// core made for every spelling of that url (https, scp-like, ssh, with
// .git or a branch query), its branch clones, and nothing for a url the
// core would not clone or a store never cloned.
func TestCoreClonesOf(t *testing.T) {
	home := t.TempDir()
	coreCloneFixture(t, home, "github.com/northwind/lore")
	branch := filepath.Join(home, "clones", "github.com", "northwind", "lore@staging")
	if err := os.MkdirAll(filepath.Join(branch, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		url  string
		want int
	}{
		{"https://github.com/northwind/lore", 2},
		{"https://github.com/northwind/lore.git", 2},
		{"git@github.com:northwind/lore.git", 2},
		{"ssh://git@github.com/northwind/lore?branch=staging", 2},
		{"https://user:tok@GitHub.com/northwind/lore", 2},
		{"https://github.com/northwind/other", 0},
		{"file:///tmp/lore", 0},
		{"", 0},
		{"not a url at all", 0},
	}
	for _, tc := range cases {
		got, err := coreClonesOf(home, tc.url)
		if err != nil {
			t.Fatalf("%q: %v", tc.url, err)
		}
		if len(got) != tc.want {
			t.Errorf("%q: %d clones %v, want %d", tc.url, len(got), got, tc.want)
		}
	}
	cs, err := coreClonesOf(home, "https://github.com/northwind/lore")
	if err != nil {
		t.Fatal(err)
	}
	if err := removeCoreClones(cs); err != nil {
		t.Fatal(err)
	}
	if left, _ := coreClonesOf(home, "https://github.com/northwind/lore"); len(left) != 0 {
		t.Fatalf("removed clones still found: %v", left)
	}
}
