package trash_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// TestTrashListSinceOnTheAppClock (devco:D-460, the clock class): `trash
// list --since 1d` counts back from the app clock the deletions were
// stamped with (LORE_NOW in a test build, the harness's Wall here), never
// the machine's real clock, so a fixed-clock run lists the same rows on
// any real day.
func TestTrashListSinceOnTheAppClock(t *testing.T) {
	base := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC) // days before any real run of this test
	var off atomic.Int64
	h := opstest.New(t, func(o *wire.Options) {
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	old := trashed(t, h, "deleted on the 3rd", "")
	off.Store(int64(3 * 24 * time.Hour))
	fresh := trashed(t, h, "deleted on the 6th", "")
	off.Store(int64(3*24*time.Hour + time.Hour))
	got := listOf(t, h, `{"since":"1d"}`)
	if got.Count != 1 || got.Data[0].ID != fresh {
		t.Errorf("--since 1d an hour after %s was deleted (and 3 days after %s): %+v, want only the fresh one", fresh, old, got.Data)
	}
}
