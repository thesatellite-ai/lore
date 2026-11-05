package wire

// Ports of saas/cmd/cli store_forget_core_test.go (TestCoreCloneUnpushed,
// TestCoreClonesOf) and failclosed_team_test.go TestRemoveStoreWorktrees*
// (B11) to cmd/lore's store forget (storeforget_standalone.go): what blocks
// a forget (side effect 20: a local commit no origin ref holds, a dirty
// worktree, a clone git cannot read: fail closed), which folders every
// spelling of a url covers, and a url lore cannot place is an error, never
// "forgot".

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

const forgetURL = "https://github.com/northwind/lore"

// coreCloneBase is the git backend's clone base of raw under home.
func coreCloneBase(t *testing.T, home, raw string) string {
	t.Helper()
	canon, err := gitbackend.CanonicalURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(canon)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gitbackend.RemoteOf(u)
	if err != nil {
		t.Fatal(err)
	}
	base, err := gitbackend.CloneBase(home, r)
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// coreCloneFixture lays out the core's clone of forgetURL the way the git
// backend does (<base>.git bare with tracking refs, <base>/work on main),
// fetched from a pushed origin; it returns the base.
func coreCloneFixture(t *testing.T, home string) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	showGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	showGit(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "lore-store.yaml"), []byte("_v: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	showGit(t, seed, "add", "-A")
	showGit(t, seed, "commit", "-q", "-m", "init")
	showGit(t, seed, "push", "-q", origin, "main")
	base := coreCloneBase(t, home, forgetURL)
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		t.Fatal(err)
	}
	showGit(t, root, "clone", "-q", "--bare", origin, base+".git")
	showGit(t, base+".git", "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
	showGit(t, base+".git", "worktree", "add", "-q", filepath.Join(base, "work"), "main")
	return base
}

func TestStoreForgetRefusesUnpushedWork(t *testing.T) {
	cases := []struct {
		name string
		mess func(t *testing.T, base string)
		want string // substring of the refusal; "" = forgotten
	}{
		{"clean clone", func(*testing.T, string) {}, ""},
		{"local commit not pushed", func(t *testing.T, base string) {
			w := filepath.Join(base, "work")
			if err := os.WriteFile(filepath.Join(w, "row.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			showGit(t, w, "add", "-A")
			showGit(t, w, "commit", "-q", "-m", "written while offline")
		}, "1 unpushed commit(s)"},
		{"uncommitted change", func(t *testing.T, base string) {
			if err := os.WriteFile(filepath.Join(base, "work", "ünï.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "uncommitted change(s)"},
		{"bare clone unreadable", func(t *testing.T, base string) {
			if err := os.RemoveAll(filepath.Join(base+".git", "objects")); err != nil {
				t.Fatal(err)
			}
		}, "unreadable"},
		{"pushed after the commit", func(t *testing.T, base string) {
			w := filepath.Join(base, "work")
			if err := os.WriteFile(filepath.Join(w, "row.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			showGit(t, w, "add", "-A")
			showGit(t, w, "commit", "-q", "-m", "w")
			showGit(t, base+".git", "push", "-q", "origin", "main")
			showGit(t, base+".git", "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			base := coreCloneFixture(t, home)
			tc.mess(t, base)
			c := &standaloneClones{home: home}
			out, err := c.ForgetClone(background(), opsstores.ForgetIn{URL: forgetURL})
			if tc.want == "" {
				if err != nil || out.Removed == "" || exists(base) || exists(base+".git") {
					t.Fatalf("forget = %+v %v; base left: %v", out, err, exists(base))
				}
				return
			}
			if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal naming %q, got %v", tc.want, err)
			}
			if !exists(base + ".git") {
				t.Fatal("a refused forget deleted the clone")
			}
		})
	}
}

// TestStoreForgetCoversEverySpelling: every spelling of the url finds the
// core clone and its branch clone; another url, a url the core would not
// clone, or a store never cloned finds none; a url lore cannot place is an
// error, never "forgot".
func TestStoreForgetCoversEverySpelling(t *testing.T) {
	home := t.TempDir()
	base := coreCloneFixture(t, home)
	if err := os.MkdirAll(filepath.Join(base+"@staging", "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &standaloneClones{home: home}
	cases := []struct {
		url  string
		want int
	}{
		{"https://github.com/northwind/lore", 2},
		{"https://github.com/northwind/lore.git", 2},
		{"git@github.com:northwind/lore.git", 2},
		{"ssh://git@github.com/northwind/lore?branch=staging", 2},
		{"https://github.com/northwind/other", 0},
	}
	for _, tc := range cases {
		norm, err := repourl.Normalize(tc.url)
		if err != nil {
			t.Fatalf("%q: %v", tc.url, err)
		}
		got, err := c.clonesOf(tc.url, norm)
		if err != nil {
			t.Fatalf("%q: %v", tc.url, err)
		}
		if len(got) != tc.want {
			t.Errorf("%q: %d clones %v, want %d", tc.url, len(got), got, tc.want)
		}
	}
	for _, bad := range []string{"", "not a url at all", "https://example.com/../x"} {
		if out, err := c.ForgetClone(background(), opsstores.ForgetIn{URL: bad}); err == nil {
			t.Errorf("%q: a url lore cannot place must fail, got %+v", bad, out)
		}
	}
	// The bare-folder branch clone git cannot read blocks the forget (fail
	// closed); once it is gone the forget removes the rest.
	if _, err := c.ForgetClone(background(), opsstores.ForgetIn{URL: forgetURL}); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("an unreadable branch clone must block the forget: %v", err)
	}
	if err := os.RemoveAll(base + "@staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ForgetClone(background(), opsstores.ForgetIn{URL: forgetURL}); err != nil {
		t.Fatal(err)
	}
	if exists(base) || exists(base+".git") || exists(base+"@staging") {
		t.Fatal("forget left a clone folder")
	}
}
