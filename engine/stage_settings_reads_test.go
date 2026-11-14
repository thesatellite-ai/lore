package engine

import (
	"context"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// settingsCountingBackend counts the reads that ask a backend for the
// policies table (a settings read of storeSettings).
type settingsCountingBackend struct {
	store.Backend
	reads *atomic.Int64
}

func (b settingsCountingBackend) Read(ctx context.Context, c store.Cursor) ([]store.Row, store.Cursor, error) {
	if t := store.TablesOf(ctx); t == nil || slices.Contains(t, row.Table(schema.TablePolicies)) {
		b.reads.Add(1)
	}
	return b.Backend.Read(ctx, c)
}

// TestStageReadsSettingsOnce (mergefix 2, perf): every staged edit
// re-read every store's policy rows and secret rules, so a 1,000-row merge
// or doctor --fix was O(n²) in store reads (11 s for 1,000 rows). Within
// one stage the settings rows are read once: staged rows are not visible
// to reads, so the stores answer the same until the stage lands. Adding
// many rows in one stage costs about as many policy reads as adding two.
func TestStageReadsSettingsOnce(t *testing.T) {
	t.Parallel()
	reads := func(n int) int64 {
		var count atomic.Int64
		data := memstore.NewData()
		f := setup(t, WithStore(Attached{Name: "kb", Backend: settingsCountingBackend{Backend: data.Open(store.OpenOptions{}), reads: &count}}))
		ctx := Staging(context.Background())
		count.Store(0)
		for i := range n {
			if _, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("staged note "+strconv.Itoa(i))); err != nil {
				t.Fatal(err)
			}
		}
		got := count.Load()
		if _, err := Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return got
	}
	two, many := reads(2), reads(40)
	if two == 0 {
		t.Fatal("no settings read counted: the counter misses the policy reads")
	}
	if many > two {
		t.Fatalf("40 staged rows read the policies %d times, 2 rows %d: settings are re-read per row", many, two)
	}
}
