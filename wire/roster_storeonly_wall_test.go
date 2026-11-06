package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
)

// TestStoreOnlyRosterDefaultsWall: StoreOnlyRoster opens with the options
// New filled in, so a host that leaves Wall nil still gets a working
// store-only App (no "engine: missing wall clock").
func TestStoreOnlyRosterDefaultsWall(t *testing.T) {
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	a, ok, err := StoreOnlyRoster(background(), Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook}, rosterStore)
	if err != nil || !ok {
		t.Fatalf("no Wall: ok %v err %v", ok, err)
	}
	_ = a.Close()
}
