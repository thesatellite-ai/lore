package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// lostCursorBackend answers store.CursorChecker with lost for every cursor
// but the zero one: a store clone reset by hand behind the cache.
type lostCursorBackend struct {
	store.Backend
	lost bool
}

func (b lostCursorBackend) CursorLost(_ context.Context, c store.Cursor) (bool, error) {
	return b.lost && c != "", nil
}

// wrappedBackend is a wire-style layer over another backend (Unwrap).
type wrappedBackend struct{ store.Backend }

func (w wrappedBackend) Unwrap() store.Backend { return w.Backend }

// TestRefreshRebuildsALostCursor: the cache holds a row the backend no
// longer has (its clone was reset by hand) and the backend, under a
// wrapper, says the cache's cursor is lost: Refresh rebuilds the source, so
// the dropped row leaves the cache and the backend's rows fill it. A cursor
// that is not lost keeps the incremental refresh (owner P-5, devco D-763).
func TestRefreshRebuildsALostCursor(t *testing.T) {
	t.Parallel()
	for _, lost := range []bool{true, false} {
		f := setup(t)
		ctx := context.Background()
		dropped := projectRow(t, "dropped by the reset")
		if err := f.repo.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: dropped}}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.eng.Refresh(ctx, store.SourceRepo); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.cache.get(schema.TableProjects, dropped.ID); !ok {
			t.Fatal("the first refresh did not cache the row")
		}
		reset := memstore.NewData()
		kept := projectRow(t, "still in the clone")
		if err := reset.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: kept}}); err != nil {
			t.Fatal(err)
		}
		e := rewire(t, f, wrappedBackend{lostCursorBackend{Backend: reset.Open(store.OpenOptions{}), lost: lost}})
		if _, err := e.Refresh(ctx, store.SourceRepo); err != nil {
			t.Fatal(err)
		}
		_, stale := f.cache.get(schema.TableProjects, dropped.ID)
		if _, ok := f.cache.get(schema.TableProjects, kept.ID); !ok {
			t.Fatalf("lost=%v: the backend's row is not in the cache after refresh", lost)
		}
		if lost && stale {
			t.Fatal("a lost cursor kept the row the reset dropped in the cache")
		}
		if !lost && !stale {
			t.Fatal("a usable cursor rebuilt the cache")
		}
	}
}

// projectRow is a projects row named name, ready to write to a backend.
func projectRow(t *testing.T, name string) store.Row {
	t.Helper()
	id, err := uuidIDs{}.NewID(ids.PrefixProject)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(row.Row{Table: schema.TableProjects, ID: id, HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor),
		Fields: map[string]value.Value{"name": txt(name)}})
	if err != nil {
		t.Fatal(err)
	}
	return sr
}
