package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestEarlierSyncCarriesCommitsInD660 (devco:D-660): a sync --setup
// refreshes a store (a fetch-only sync that merges) before it syncs it;
// the commits that refresh brought in reach the sync's report with its
// rows, so the store line says "received 5" for 5 commits, not the 7 rows
// they changed.
func TestEarlierSyncCarriesCommitsInD660(t *testing.T) {
	t.Parallel()
	b := &storePush{}
	b.carry(store.SyncReport{Received: 7, CommitsIn: 5}, nil)
	got := b.withEarlier(store.SyncReport{Received: 1, CommitsIn: 1})
	if got.Received != 8 || got.CommitsIn != 6 {
		t.Fatalf("received %d rows / %d commits, want 8 / 6", got.Received, got.CommitsIn)
	}
	if again := b.withEarlier(store.SyncReport{}); again.CommitsIn != 0 || again.Received != 0 {
		t.Errorf("the earlier counts were reported twice: %+v", again)
	}
}
