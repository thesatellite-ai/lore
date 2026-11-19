package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveReadAfterAWriteReturnsOnlyWhatChanged (devco:T-12): every write
// moves the live ref, and the next read with the cache's cursor re-read
// and re-applied every live row (5,000 open tasks: 2-12 s of each command
// after any write, a teammate's fetched one included). The read now
// returns the rows whose live file changed since the cursor's commit.
func TestLiveReadAfterAWriteReturnsOnlyWhatChanged(t *testing.T) {
	f := newLiveFixture(t)
	const many = 40
	writeManyLive(t, f, many)
	all, cur, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < many {
		t.Fatalf("full read: %d rows, want at least %d", len(all), many)
	}
	if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "one more", false)}}); err != nil {
		t.Fatal(err)
	}
	moved, next, err := f.b.Read(f.ctx, cur)
	if err != nil {
		t.Fatal(err)
	}
	tasks, found := 0, false
	for _, r := range moved {
		if r.Table == schema.TableTasks {
			tasks++
		}
		found = found || r.ID.String() == ltTaskA
	}
	if !found {
		t.Fatalf("the read after a live write misses the new row (%d rows)", len(moved))
	}
	if tasks > 1 {
		t.Errorf("the read after one live write returned %d live tasks, want 1 (the %d unchanged ones are in the cache)", tasks, many)
	}
	if next == cur {
		t.Error("the cursor did not move with the live ref")
	}
}
