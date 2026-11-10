package wire

// cmd/lore's store show|open|doctor host (storeshow_standalone.go) over a
// real store clone: entries on two spellings of one url share one view,
// show names the clone without the network, doctor passes a healthy store
// and lists a missing branch, open makes the worktree, and quiet output
// names no store url.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

func showGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	env := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	if _, err := (gitRun{dir: dir, env: env, args: args}).output(background()); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestStandaloneStoreShowOpenDoctor(t *testing.T) {
	t.Parallel()
	ctx := background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	showGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	showGit(t, root, "clone", "-q", origin, seed)
	if err := os.WriteFile(filepath.Join(seed, "lore-store.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	showGit(t, seed, "add", ".")
	showGit(t, seed, "commit", "-q", "-m", "init")
	showGit(t, seed, "push", "-q", "origin", "main")

	home := t.TempDir()
	cfg := "orgs:\n" +
		"  - name: agency\n    stores:\n      - url: https://example.com/agency/notes\n" +
		"  - name: northwind\n    stores:\n      - url: git@example.com:agency/notes.git\n        branch: staging\n"
	if err := os.WriteFile(orgcfg.ConfigPath(home), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &standaloneClones{home: home}

	show, err := c.showClones(ctx, opsstores.ShowIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(show.Stores) != 1 {
		t.Fatalf("two spellings of one url must share one view: %+v", show.Stores)
	}
	v := show.Stores[0]
	if !slices.Equal(v.Entries, []string{"agency", "northwind"}) || !slices.Equal(v.Branches, []string{"main", "staging"}) || v.Cloned || len(v.Problems) != 0 {
		t.Fatalf("never cloned view: %+v", v)
	}
	if v.Shown != redactedText || v.URL != "https://example.com/agency/notes" {
		t.Fatalf("quiet output must not name the url: shown %q url %q", v.Shown, v.URL)
	}

	// The clone the org layer made, pointed at the local origin.
	clone := orgcfg.Clones{Home: home}.Dir("example.com/agency/notes")
	showGit(t, root, "init", "-q", "--bare", clone)
	showGit(t, clone, "remote", "add", "origin", origin)
	// The clone follows the configured url (orgcfg ensureOrigin, f0c765e9),
	// so the test's example.com spellings must reach the local origin: git
	// rewrites each to it in this clone's own config (longest match wins).
	for _, u := range []string{"https://example.com/agency/notes", "https://example.com/agency/notes.git", "git@example.com:agency/notes.git"} {
		showGit(t, clone, "config", "--add", "url."+origin+".insteadOf", u)
	}

	doc, err := c.doctorClones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Stores[0].Problems
	if len(p) != 1 || !strings.HasPrefix(p[0], "branch staging does not exist") {
		t.Fatalf("doctor: want only the missing staging branch, got %v", p)
	}
	for _, s := range p {
		if strings.Contains(s, "example.com") {
			t.Fatalf("quiet problem names the store: %q", s)
		}
	}
	show, err = c.showClones(ctx, opsstores.ShowIn{Links: true})
	if err != nil || !show.Stores[0].Cloned || show.Stores[0].LastFetch == "" || show.Stores[0].DefaultBranch != "main" {
		t.Fatalf("show after doctor: %+v %v", show, err)
	}
	out, err := c.openClone(ctx, opsstores.OpenIn{URL: "https://example.com/agency/notes.git"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Branch != "main" || out.Commit == "" {
		t.Fatalf("open = %+v", out)
	}
	if _, err = os.Stat(filepath.Join(out.Dir, "lore-store.yaml")); err != nil {
		t.Fatalf("worktree: %v", err)
	}
}
