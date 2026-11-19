package orgcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestSecondStoreNamesItsOrg (devco:D-721): reading the org of a lore
// store that holds no lore-org.yaml (one of an org's other stores) said
// only ErrNoOrgFile, and a join of that url told the joiner the admin had
// not sent the org yet. The error now carries what the store's
// lore-store.yaml says about its org (org:, org_store:), so the join can
// follow the main store or name it; errors.Is(err, ErrNoOrgFile) holds.
func TestSecondStoreNamesItsOrg(t *testing.T) {
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	for name, marker := range map[string]string{
		"lore-ops":  "lore_store: 4\nid: sto_01a1a5aca7f57659be7320c5f4b70001\norg: acme\norg_store: https://github.com/acme-co/lore-team\n",
		"lore-bare": "lore_store: 4\nid: sto_01a1a5aca7f57659be7320c5f4b70002\n",
	} {
		origin := filepath.Join(hosts, "acme-co", name+".git")
		if err := os.MkdirAll(origin, 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, origin, "init", "-q", "--bare", "-b", "main")
		seed := filepath.Join(root, "seed-"+name)
		testGit(t, root, "clone", "-q", "https://github.com/acme-co/"+name, seed)
		writeFile(t, filepath.Join(seed, storeMarkerFile), marker)
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "main")
	}
	g := &GitLinks{Home: filepath.Join(root, "home"), Config: &orgconf.Config{}}
	ctx := context.Background()

	_, err := g.ReadSuggestion(ctx, "https://github.com/acme-co/lore-ops", "")
	var note *OrgStoreNoteError
	if !errors.As(err, &note) || !errors.Is(err, ErrNoOrgFile) {
		t.Fatalf("a store naming its org: got %v, want an OrgStoreNoteError that is ErrNoOrgFile", err)
	}
	if note.Note.Org != "acme" || note.Note.MainStore != "https://github.com/acme-co/lore-team" {
		t.Errorf("note = %+v, want org acme and main store https://github.com/acme-co/lore-team", note.Note)
	}

	_, err = g.ReadSuggestion(ctx, "https://github.com/acme-co/lore-bare", "")
	if !errors.As(err, &note) || note.Note.Org != "" || note.Note.MainStore != "" {
		t.Fatalf("a store naming no org: got %v (note %+v), want an OrgStoreNoteError with no org", err, note)
	}
}
