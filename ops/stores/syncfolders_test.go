package stores

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestAttributeFolders (bug hunt W-5): one sync of a clone carries every
// folder; a folder an attached store reads gets its own counts, a folder
// no attached store reads is reported under "<org>/<folder>", and nothing
// is added to the folder whose sync happened to carry it.
func TestAttributeFolders(t *testing.T) {
	t.Parallel()
	synced := []folderSync{
		{name: "acme/org", rep: store.SyncReport{Folder: "org", Sent: 0, Others: map[string]store.FolderCount{
			"core": {Received: 2},
			"sync": {Sent: 1, Received: 1},
		}}},
		{name: "acme/core", rep: store.SyncReport{Folder: "core"}},
	}
	reports := []Report{{Store: "acme/org"}, {Store: "acme/core"}}
	got := attributeFolders(reports, synced)
	if len(got) != 3 {
		t.Fatalf("reports = %+v, want org, core and the unattached sync folder", got)
	}
	if got[0].Sent != 0 || got[0].Received != 0 {
		t.Errorf("acme/org took another folder's rows: %+v", got[0])
	}
	if got[1].Received != 2 {
		t.Errorf("acme/core = %+v, want its 2 received rows", got[1])
	}
	if got[2].Store != "acme/sync" || !got[2].Unattached || got[2].Sent != 1 || got[2].Received != 1 {
		t.Errorf("unattached folder = %+v, want acme/sync sent 1 received 1", got[2])
	}
}
