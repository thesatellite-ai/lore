package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestKeepMovedStores (G-16): lore-org.yaml still naming a store's old
// address (a member's set-url leaves the file to an admin) never undoes
// the move this machine recorded; without such a record the file wins.
func TestKeepMovedStores(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	const old = "file:///srv/stores/old-place"
	before := orgconf.Shared{Stores: []orgconf.Store{{URL: now, OrgFolder: "org"}}}
	file := orgconf.Shared{Stores: []orgconf.Store{{URL: old, OrgFolder: "org"}}, Own: []string{"example.test/acme/*"}}

	got, kept := keepMovedStores(home, before, file)
	if got.Stores[0].URL != old || len(kept) != 0 {
		t.Fatalf("no recorded move: want the file's url, got %q kept %v", got.Stores[0].URL, kept)
	}
	if err := recordStoreAddress(home, folderStore, now, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, kept = keepMovedStores(home, before, file)
	if got.Stores[0].URL != now || len(kept) != 1 || !strings.Contains(kept[0], old+" → "+now) {
		t.Fatalf("a recorded move: got %q kept %v", got.Stores[0].URL, kept)
	}
	if file.Stores[0].URL != old || len(got.Own) != 1 {
		t.Fatalf("the file's copy was changed in place, or other fields lost: %+v / %+v", file, got)
	}
}
