package git

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestValidBranch: the branch names lore accepts match what git accepts
// (git check-ref-format --branch), never anything that walks out of the
// clone folder.
func TestValidBranch(t *testing.T) {
	t.Parallel()
	good := []string{"main", "staging", "release/1.2", "feature/x_y-z", "a.b", "v1", "A/B/C"}
	bad := []string{
		"", "-x", ".hidden", "a..b", "../up", "a/../b", "a//b", "a/", "a.", "a.lock", "x/y.lock",
		"a/.b", "a b", "a~1", "a^", "a:b", "a?", "a*", "a[", `a\b`, "@", "a@{1}", "ブランチ", "a\nb",
	}
	for _, n := range good {
		if !validBranch(n) {
			t.Errorf("validBranch(%q) refused", n)
		}
	}
	for _, n := range bad {
		if validBranch(n) {
			t.Errorf("validBranch(%q) accepted", n)
		}
	}
}

// TestBranchOf: ?branch= is taken out of the url (other params kept for
// rowfs.FolderOf); a bad, empty or repeated branch is E_INVALID_INPUT.
func TestBranchOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw, branch, rest string
		code              errs.Code
	}{
		{"https://h/a/b", "", "", ""},
		{"https://h/a/b?branch=staging", "staging", "", ""},
		{"https://h/a/b?branch=staging&folder=org", "staging", "folder=org", ""},
		{"https://h/a/b?branch=", "", "", errs.InvalidInput},
		{"https://h/a/b?branch=a..b", "", "", errs.InvalidInput},
		{"https://h/a/b?branch=x&branch=y", "", "", errs.InvalidInput},
		{"https://h/a/b?branch=%2e%2e%2fup", "", "", errs.InvalidInput},
		{"https://h/a/b?%zz", "", "", errs.StoreScheme},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatal(err)
		}
		br, rest, err := branchOf(u)
		if c.code != "" {
			if !errs.Has(err, c.code) {
				t.Errorf("%s: err %v, want %s", c.raw, err, c.code)
			}
			continue
		}
		if err != nil || br != c.branch || rest.RawQuery != c.rest {
			t.Errorf("%s: %q %q %v", c.raw, br, rest.RawQuery, err)
		}
	}
	if _, err := OpenClone(t.Context(), "unused", t.TempDir(), rowfs.FolderRoot, store.OpenOptions{}, WithBranch("a//b")); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("WithBranch bad name: %v", err)
	}
}

// TestFolderURLRefusesBranch: a file:// store has no branches; ?branch= is
// an unknown parameter there (E_STORE_SCHEME), never silently ignored.
func TestFolderURLRefusesBranch(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("file://" + filepath.ToSlash(t.TempDir()) + "?branch=staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := folder.NewOpener()(t.Context(), u, store.OpenOptions{Home: t.TempDir()}); !errs.Has(err, errs.StoreScheme) {
		t.Fatalf("file:// with a branch: %v", err)
	}
}

// TestOpenerPinnedBranch drives NewOpener over a real https-shaped url
// (rewritten to a local bare repo with git's url.<x>.insteadOf): a pinned
// branch gets its own clone at <home>/clones/<host>/<path>@<branch>, writes
// and syncs land on that branch only, and the default clone is separate.
// Not parallel: it sets the process's git config environment.
func TestOpenerPinnedBranch(t *testing.T) {
	remote := newRemote(t, true)
	seed := filepath.Join(t.TempDir(), "seed")
	gitT(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	gitT(t, seed, "push", "--quiet", "origin", testBranch+":staging")
	const fake = "https://example.invalid/acme/notes"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+remote+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", fake)
	home := t.TempDir()
	open := NewOpener()
	openURL := func(raw string) *Backend {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		sb, err := open(t.Context(), u, store.OpenOptions{Home: home})
		if err != nil {
			t.Fatalf("open %s: %v", raw, err)
		}
		b, ok := sb.(*Backend)
		if !ok {
			t.Fatalf("open %s: %T", raw, sb)
		}
		t.Cleanup(func() {
			if err := b.Close(); err != nil {
				t.Error(err)
			}
		})
		return b
	}
	staging := openURL(fake + "?branch=staging&folder=org")
	if staging.Branch() != "staging" {
		t.Fatalf("branch %q", staging.Branch())
	}
	wantBase := filepath.Join(home, ClonesDir, "example.invalid", "acme", "notes@staging")
	if staging.Dir() != filepath.Join(wantBase, workDir) {
		t.Fatalf("clone dir %s, want under %s", staging.Dir(), wantBase)
	}
	if _, err := os.Stat(wantBase + bareSuffix); err != nil {
		t.Fatalf("bare clone: %v", err)
	}
	def := openURL(fake)
	if def.Branch() != testBranch || def.Dir() == staging.Dir() {
		t.Fatalf("default clone %s on %s", def.Dir(), def.Branch())
	}
	mainTip := gitT(t, remote, "rev-parse", testBranch)
	g := newGen(t)
	write(t, staging, store.Change{Row: g.row(newID(t), "staging only", hlc.HLC{})})
	syncOK(t, staging)
	if gitT(t, remote, "rev-parse", testBranch) != mainTip {
		t.Fatal("a staging write moved main")
	}
	if gitT(t, remote, "rev-parse", "staging") != gitT(t, staging.Dir(), "rev-parse", "HEAD") {
		t.Fatal("staging was not pushed")
	}
}
