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
