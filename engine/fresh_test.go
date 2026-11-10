package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// freshBackend is a backend with a fetch stamp (a git clone's Freshness).
type freshBackend struct {
	store.Backend
	fresh store.Fresh
}

func (b freshBackend) Fresh(context.Context) (store.Fresh, error) { return b.fresh, nil }

// TestStoreFreshUnwraps (P2-RES-7, bee05ab, later 548): the first layer
// that implements store.Freshness answers, however many wrappers sit above
// it; a backend without one (a folder store) is Known false; an unknown
// store is an error.
func TestStoreFreshUnwraps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	base := memstore.NewData().Open(store.OpenOptions{})
	clone := freshBackend{Backend: base, fresh: store.Fresh{Known: true, FetchedAt: at, Commit: "abc"}}
	cases := []struct {
		name string
		b    store.Backend
		want store.Fresh
	}{
		{"plain backend, no stamp", base, store.Fresh{}},
		{"the clone itself", clone, clone.fresh},
		{"one wrapper", wrapped{clone, base}, clone.fresh},
		{"two wrappers", wrapped{wrapped{clone, base}, base}, clone.fresh},
		{"wrapper over a plain backend", wrapped{base, base}, store.Fresh{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Engine{stores: map[store.Source]Attached{"kb": {Name: "kb", Backend: c.b}}}
			got, err := e.StoreFresh(ctx, "kb")
			if err != nil || got != c.want {
				t.Fatalf("StoreFresh = %+v %v, want %+v", got, err, c.want)
			}
			if _, err = e.StoreFresh(ctx, "nope"); err == nil {
				t.Fatal("an unknown store must be an error")
			}
		})
	}
}

// wrapped is a wrapper without Freshness of its own (the CLI's
// push-after-write layer): a full store.Backend by delegation (the
// embedded backend; StoreFresh never calls its methods) whose Unwrap
// returns inner.
type wrapped struct {
	inner store.Backend
	store.Backend
}

func (w wrapped) Unwrap() store.Backend { return w.inner }
