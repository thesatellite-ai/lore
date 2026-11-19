package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/store"
)

// TestOrgRefreshKeepsToTheOpenedOrg (devco D-278): the refresh after an
// open read every org's store clone; a command that opened acme's store
// now refreshes acme only, and one that opened no org's store refreshes
// every entry, as before.
func TestOrgRefreshKeepsToTheOpenedOrg(t *testing.T) {
	cfg := &orgconf.Config{Orgs: []orgconf.Org{
		{Name: "acme", Stores: []orgconf.Store{{URL: "git@example.test:acme/notes.git"}}},
		{Name: "beta", Stores: []orgconf.Store{{URL: "git@example.test:beta/notes.git"}}},
	}}
	got := orgsOfStores(cfg, map[store.Source]string{"notes": "https://example.test/acme/notes"})
	if len(got) != 1 || !got["acme"] {
		t.Fatalf("opened acme's store: refresh %v, want acme only", got)
	}
	if got := orgsOfStores(cfg, map[store.Source]string{store.SourceRepo: ""}); got != nil {
		t.Fatalf("opened no org's store: %v, want nil (every entry)", got)
	}
}

// TestRepoSyncRefreshKeepsToTheSyncedOrgD699: after a repo sync fetched,
// the org entries are refreshed again so a teammate follows an admin's
// set-url in that sync; only the orgs whose stores the sync touched, and
// none (not every entry, as the open's fallback does) when the sync touched
// no org's store, so a sync in one org's repo never reads another org's
// clone (D-278).
func TestRepoSyncRefreshKeepsToTheSyncedOrgD699(t *testing.T) {
	cfg := &orgconf.Config{Orgs: []orgconf.Org{
		{Name: "acme", Stores: []orgconf.Store{{URL: "git@example.test:acme/notes.git"}}},
		{Name: "beta", Stores: []orgconf.Store{{URL: "git@example.test:beta/notes.git"}}},
	}}
	if got := orgsSynced(cfg, map[store.Source]string{"acme": "git@example.test:acme/notes.git"}); len(got) != 1 || !got["acme"] {
		t.Fatalf("synced acme's store: refresh %v, want acme only", got)
	}
	if got := orgsSynced(cfg, map[store.Source]string{"mine": "git@example.test:ann/personal.git"}); len(got) != 0 {
		t.Fatalf("synced a personal store only: refresh %v, want no org", got)
	}
	if got := orgsSynced(cfg, nil); len(got) != 0 {
		t.Fatalf("synced nothing: refresh %v, want no org", got)
	}
}
