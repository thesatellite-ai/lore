package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// A read's fetch that merged rows of the store's other folders carries
// them into `lore sync`'s report (W-5 split them off as Others; carry kept
// only the folder's own counts, so a whole-store sync outside a repo said
// "up to date" after pulling a row).
func TestCarryKeepsOtherFolders(t *testing.T) {
	b := &storePush{}
	b.carry(store.SyncReport{Received: 1, Others: map[string]store.FolderCount{"client_a": {Received: 2}}}, nil)
	b.carry(store.SyncReport{Others: map[string]store.FolderCount{"client_a": {Sent: 1}, "client_b": {Received: 1}}}, nil)
	got := b.earlier.Others
	if got["client_a"] != (store.FolderCount{Sent: 1, Received: 2}) || got["client_b"] != (store.FolderCount{Received: 1}) || b.earlier.Received != 1 {
		t.Fatalf("carried %+v", b.earlier)
	}
}
