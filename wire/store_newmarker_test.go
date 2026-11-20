package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// nmConfig is a machine holding an admin setup of acme (main store
// lore-team, a second store notes-two) and a member setup of globex.
const nmConfig = `setups:
  - name: acme-admin
    kind: admin
    org: acme
  - name: globex-member
    kind: member
    org: globex
orgs:
  - name: acme
    stores:
      - url: git@github.com:acme-co/lore-team.git
        org_folder: org
      - url: git@github.com:acme-co/notes-two.git
  - name: globex
    stores:
      - url: git@github.com:globex-co/lore-team.git
        org_folder: org
      - url: git@github.com:globex-co/notes.git
`

// TestNewStoreMarkerNamesTheOrg (devco:D-721 follow-up): the first marker
// of an admin's second org store names the org and its main store, so an
// empty repo becomes a store in one commit (the admin's sync has nothing
// left to name). The main store, a member's store and a store no org lists
// get a marker naming no org. Break-verified: with newStoreMarker leaving
// Org and OrgStore empty, the notes-two case fails (and
// TestJourneyFoldersManyStores counts 2 commits in each new store).
func TestNewStoreMarkerNamesTheOrg(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(nmConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	wall := func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		url, org, main string
	}{
		{"git@github.com:acme-co/notes-two.git", "acme", "git@github.com:acme-co/lore-team.git"},
		{"https://github.com/acme-co/notes-two", "acme", "git@github.com:acme-co/lore-team.git"},
		{"git@github.com:acme-co/lore-team.git", "", ""},
		{"git@github.com:globex-co/notes.git", "", ""},
		{"git@github.com:elsewhere/notes.git", "", ""},
	} {
		m, err := newStoreMarker(context.Background(), home, c.url, wall)
		if err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		if m.Org != c.org || m.OrgStore != c.main {
			t.Errorf("new marker of %s names org %q, main store %q; want %q, %q", c.url, m.Org, m.OrgStore, c.org, c.main)
		}
		if m.ID.IsZero() || m.Version == 0 {
			t.Errorf("new marker of %s has no id or version: %+v", c.url, m)
		}
	}
	if m, err := newStoreMarker(context.Background(), "", "git@github.com:acme-co/notes-two.git", wall); err != nil || m.Org != "" {
		t.Errorf("no home: marker %+v, %v; want one naming no org", m, err)
	}
}
