package orgcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestMarkOrgStoreNamesTheOrg (devco:D-721): nothing wrote org:/org_store:
// into an org's other stores, so a join by such a store's url never found
// the org. MarkOrgStore writes both into lore-store.yaml (keeping its id
// and other keys), the store's org read then names them, a second run is
// Existed, and a repo with no marker yet is NotAStore with nothing written.
func TestMarkOrgStoreNamesTheOrg(t *testing.T) {
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	seedRepo := func(name, file, body string) string {
		origin := filepath.Join(hosts, "acme-co", name+".git")
		if err := os.MkdirAll(origin, 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, origin, "init", "-q", "--bare", "-b", "main")
		seed := filepath.Join(root, "seed-"+name)
		testGit(t, root, "clone", "-q", "https://github.com/acme-co/"+name, seed)
		writeFile(t, filepath.Join(seed, file), body)
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "main")
		return origin
	}
	ops := seedRepo("lore-ops", storeMarkerFile, "lore_store: 4\nid: sto_01a1a5aca7f57659be7320c5f4b70001\nvisibility: internal\npush: auto\n")
	seedRepo("plain", "README.md", "not a store\n")
	g := &GitLinks{Home: filepath.Join(root, "home"), Config: &orgconf.Config{}}
	ctx := context.Background()
	main := "https://github.com/acme-co/lore-team"
	mark := OrgStoreMark{URL: "https://github.com/acme-co/lore-ops", Org: "acme", MainStore: main, Push: true,
		Signature: gitref.Signature{Name: "t", Email: "t@example.com", When: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}}

	if named, cloned := g.StoreNamesOrg(ctx, mark.URL, "", "acme", main); named || cloned {
		t.Fatalf("before any clone: named %v cloned %v, want both false", named, cloned)
	}
	res, err := g.MarkOrgStore(ctx, mark)
	if err != nil || !res.Pushed || res.Existed || res.NotAStore {
		t.Fatalf("MarkOrgStore = %+v, %v; want a pushed commit", res, err)
	}
	got := testGit(t, ops, "show", "main:"+storeMarkerFile)
	for _, want := range []string{"org: acme", "org_store: " + main, "id: sto_01a1a5aca7f57659be7320c5f4b70001", "push: auto"} {
		if !strings.Contains(got, want) {
			t.Errorf("lore-store.yaml lacks %q:\n%s", want, got)
		}
	}
	_, rerr := g.ReadSuggestion(ctx, mark.URL, "")
	var note *OrgStoreNoteError
	if !errors.As(rerr, &note) || note.Note.Org != "acme" || note.Note.MainStore != main {
		t.Fatalf("the store's org read after the mark: %v (%+v)", rerr, note)
	}
	if named, cloned := g.StoreNamesOrg(ctx, mark.URL, "", "acme", main); !named || !cloned {
		t.Errorf("after the mark: named %v cloned %v, want both true", named, cloned)
	}
	if res, err = g.MarkOrgStore(ctx, mark); err != nil || !res.Existed {
		t.Errorf("second MarkOrgStore = %+v, %v; want Existed", res, err)
	}
	plain := mark
	plain.URL = "https://github.com/acme-co/plain"
	if res, err = g.MarkOrgStore(ctx, plain); err != nil || !res.NotAStore {
		t.Errorf("a repo with no marker: %+v, %v; want NotAStore and no error", res, err)
	}
}
