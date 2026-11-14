package wire

import (
	"testing"

	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestStoreSyncLineOneShape (devco N-15): lore sync's per-store line had two
// shapes, "store org: sent 1, received 0, merged 0, refused 0" in an own
// repo and "store client-store: pushed (sent 3, received 0, merged 0)" in
// a client repo or outside any repo. Both paths now print the same line for
// the same counts: "up to date", or the status and all four counts.
func TestStoreSyncLineOneShape(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		sent, received, merged, refused int
		pushed                          bool
		want                            string
	}{
		{0, 0, 0, 0, false, "store s: up to date"},
		{3, 0, 0, 0, false, "store s: pushed (sent 3, received 0, merged 0, refused 0)"},
		{0, 2, 1, 0, false, "store s: pulled (sent 0, received 2, merged 1, refused 0)"},
		{1, 1, 0, 0, false, "store s: pulled and pushed (sent 1, received 1, merged 0, refused 0)"},
		{0, 0, 0, 2, false, "store s: refused (sent 0, received 0, merged 0, refused 2)"},
	} {
		status := storeStatusOf(c.sent, c.received, c.merged, c.refused, c.pushed)
		own := storeCountsLine("s", status, c.sent, c.received, c.merged, c.refused)
		if own != c.want {
			t.Errorf("own repo line = %q, want %q", own, c.want)
		}
		outside := syncStoreLines(opssync.StoreReport{Store: "s", Status: status, Sent: c.sent, Received: c.received, Merged: c.merged, Refused: c.refused}, "")[0]
		if outside != own {
			t.Errorf("outside a repo %q, in a repo %q: two shapes for the same counts", outside, own)
		}
	}
}
