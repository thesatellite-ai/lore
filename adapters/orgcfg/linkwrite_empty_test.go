package orgcfg

// F-1: `lore org store add` lists a brand-new empty repo as one of the
// org's stores and prints `lore org link … --store <url>` as the next step;
// the link is that store's first write, so it makes the branch with the
// marker in the same commit (the docs: "a brand-new empty repo becomes a
// lore store with the first write to it"). A repo that has branches, just
// not the store's, still refuses: a typo must not start a second line.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// newEmptyStoreWorld is a bare store at github.com/agency/notes with no
// commit at all; withBranch pushes one commit to that branch first.
func newEmptyStoreWorld(t *testing.T, withBranch string) *linkWorld {
	t.Helper()
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	origin := filepath.Join(hosts, "agency", "notes.git")
	if err := os.MkdirAll(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, origin, "init", "-q", "--bare", "-b", "main")
	if withBranch != "" {
		seed := filepath.Join(root, "seed")
		testGit(t, root, "clone", "-q", lwStoreURL, seed)
		writeFile(t, filepath.Join(seed, "README.md"), "code\n")
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "HEAD:"+withBranch)
	}
	cfg, err := orgconf.Parse([]byte("orgs:\n  - name: agency\n    stores:\n      - url: " + lwStoreURL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &linkWorld{origin: origin, links: &GitLinks{Home: filepath.Join(root, "home"), Config: cfg}}
}

func TestLinkMakesTheFirstCommitOfAnEmptyStore(t *testing.T) {
	w := newEmptyStoreWorld(t, "")
	res, err := w.linkWith(t, "client_z", true)
	if err != nil {
		t.Fatalf("org link into a brand-new empty store repo: %v (want its first commit)", err)
	}
	if !res.Pushed || !res.CreatedFolder {
		t.Fatalf("result = %+v, want the folder created and pushed", res)
	}
	files := testGit(t, w.origin, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{storeMarkerFile, "client_z/_meta.json"} {
		if !strings.Contains(files, want) {
			t.Fatalf("main holds %q, want %s in the first commit", files, want)
		}
	}
	if n := strings.TrimSpace(testGit(t, w.origin, "rev-list", "--count", "main")); n != "1" {
		t.Fatalf("main has %s commits, want the one root commit", n)
	}
	if body := testGit(t, w.origin, "show", "main:"+storeMarkerFile); body != strings.TrimSpace(nmMarker) {
		t.Fatalf("marker = %q, want the NewMarker bytes", body)
	}
}

func TestLinkStillRefusesAMissingBranchOfANonEmptyRepo(t *testing.T) {
	w := newEmptyStoreWorld(t, "other")
	_, err := w.linkWith(t, "client_z", true)
	if !errors.Is(err, ErrNoStoreBranch) {
		t.Fatalf("err = %v, want ErrNoStoreBranch (the repo has branches, not main)", err)
	}
}

func TestLinkWithoutNewMarkerRefusesAnEmptyStore(t *testing.T) {
	w := newEmptyStoreWorld(t, "")
	_, err := w.linkWith(t, "client_z", false)
	if !errors.Is(err, ErrNoStoreBranch) {
		t.Fatalf("err = %v, want ErrNoStoreBranch (no marker to make the store with)", err)
	}
}
