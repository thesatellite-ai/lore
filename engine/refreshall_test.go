package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// overlapCache is fakeCache whose Apply of the first source waits until
// the next source's cursor was read: it returns only when the engine reads
// ahead while it applies (RefreshAll), and gives up after a while
// otherwise.
type overlapCache struct {
	*fakeCache
	first, next store.Source
	nextRead    chan struct{}
	once        sync.Once
	overlapped  bool
}

func (c *overlapCache) Cursor(ctx context.Context, src store.Source) (store.Cursor, error) {
	if src == c.next {
		c.once.Do(func() { close(c.nextRead) })
	}
	return c.fakeCache.Cursor(ctx, src)
}

func (c *overlapCache) Apply(ctx context.Context, src store.Source, rows []store.Row, next store.Cursor) error {
	if src == c.first {
		select {
		case <-c.nextRead:
			c.overlapped = true
		case <-time.After(3 * time.Second):
		}
	}
	return c.fakeCache.Apply(ctx, src, rows, next)
}

// TestRefreshAllReadsAheadInOrder (bug hunt H-282, devco:D-1020): the
// cache build of several stores reads the next store while the cache
// applies the current one, applies them in order with the same rows a
// Refresh per store applies, and stops at a store that fails, naming it,
// with the stores before it applied.
func TestRefreshAllReadsAheadInOrder(t *testing.T) {
	t.Parallel()
	oc := &overlapCache{fakeCache: newFakeCache(), next: agency, nextRead: make(chan struct{})}
	f := setup(t, WithCache(oc))
	oc.first = store.SourceRepo // armed after setup's own write
	ctx := context.Background()
	id, err := uuidIDs{}.NewID(ids.PrefixProject)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(row.Row{Table: schema.TableProjects, ID: id, HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: map[string]value.Value{"name": txt("pulled")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.agency.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if failed, err := f.eng.RefreshAll(ctx, []store.Source{store.SourceRepo, agency}); err != nil {
		t.Fatalf("RefreshAll: %s: %v", failed, err)
	}
	if !oc.overlapped {
		t.Fatal("the next store was not read while the first one was applied")
	}
	oc.mu.Lock()
	_, got := oc.rows[agency][store.Key{Table: schema.TableProjects, ID: id}]
	_, own := oc.rows[store.SourceRepo][store.Key{Table: schema.TableProjects, ID: f.project}]
	oc.mu.Unlock()
	if !got || !own {
		t.Fatalf("rows applied: agency row %v, repo row %v", got, own)
	}
	// every cursor moved: a second pass applies nothing
	for _, s := range []store.Source{store.SourceRepo, agency} {
		if n, err := f.eng.Refresh(ctx, s); err != nil || n != 0 {
			t.Fatalf("refresh of %s after RefreshAll applied %d rows (%v)", s, n, err)
		}
	}
	// an unknown store fails, named; the one before it is applied
	if err := oc.Rebuild(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	failed, err := f.eng.RefreshAll(ctx, []store.Source{store.SourceRepo, "nowhere", agency})
	if failed != "nowhere" || !errs.Has(err, errs.NotFound) {
		t.Fatalf("RefreshAll with an unknown store: %q %v, want nowhere and E_NOT_FOUND", failed, err)
	}
	if c, _ := oc.Cursor(ctx, store.SourceRepo); c == "" {
		t.Fatal("the store before the failing one was not applied")
	}
}
