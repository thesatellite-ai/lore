package wire

import (
	"context"
	"errors"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// caches is the engine's store.Cache: one SQLite cache file per attached
// store (spec 7.4: a cache is per store folder, so deleting one store's
// cache never touches another's), routed by source.
//
// Why a router and not one file: the engine's cache port names the source
// on every call, and per-store files let `lore` drop or rebuild one store's
// copy alone; search then asks each file in store precedence order.
type caches struct {
	mu    sync.Mutex
	order []store.Source
	by    map[store.Source]*sqlite.Cache
	// combined is the one file shared by every attached store past
	// MaxStoreCaches (CFG3 fallback), nil otherwise.
	combined *sqlite.Cache
}

// Combined reports whether this repo uses the CFG3 combined cache (for
// `lore doctor`: "more than 9 store folders: one combined cache").
func (a *App) Combined() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st != nil && a.st.caches.combined != nil
}

var _ store.Cache = (*caches)(nil)

// newCaches returns an empty router.
func newCaches() *caches { return &caches{by: map[store.Source]*sqlite.Cache{}} }

// open opens source's cache file at path.
func (c *caches) open(ctx context.Context, source store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	f, err := cacheOpen(ctx, path, reg, exp)
	if err != nil {
		return errs.Wrap(errs.CodeOf(err), "wire: open the cache of store "+string(source), err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[source] = f
	c.order = append(c.order, source)
	return nil
}

// openCombined opens ONE cache file at path for every source in names
// (the CFG3 fallback): each source's rows are applied under its own name,
// so the file's `_source` column tells them apart.
func (c *caches) openCombined(ctx context.Context, names []store.Source, path string, reg *schema.Registry, exp sqlite.Expander) error {
	f, err := cacheOpen(ctx, path, reg, exp)
	if err != nil {
		return errs.Wrap(errs.CodeOf(err), "wire: open the combined store cache", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range names {
		c.by[n] = f
		c.order = append(c.order, n)
	}
	c.combined = f
	return nil
}

// of returns source's cache file.
func (c *caches) of(source store.Source) (*sqlite.Cache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.by[source]
	if !ok {
		return nil, errs.New(errs.NotFound, "wire: no cache for store "+string(source))
	}
	return f, nil
}

// Apply implements store.Cache.
func (c *caches) Apply(ctx context.Context, source store.Source, rows []store.Row, next store.Cursor) error {
	f, err := c.of(source)
	if err != nil {
		return err
	}
	return f.Apply(ctx, source, rows, next)
}

// Cursor implements store.Cache.
func (c *caches) Cursor(ctx context.Context, source store.Source) (store.Cursor, error) {
	f, err := c.of(source)
	if err != nil {
		return "", err
	}
	return f.Cursor(ctx, source)
}

// Rebuild implements store.Cache.
func (c *caches) Rebuild(ctx context.Context, source store.Source) error {
	f, err := c.of(source)
	if err != nil {
		return err
	}
	return f.Rebuild(ctx, source)
}

// Close implements store.Cache: every file is closed, every error kept.
func (c *caches) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []error
	closed := map[*sqlite.Cache]bool{}
	for _, s := range c.order {
		if f := c.by[s]; !closed[f] {
			closed[f] = true
			all = append(all, f.Close())
		}
	}
	c.by = map[store.Source]*sqlite.Cache{}
	c.order = nil
	c.combined = nil
	return errors.Join(all...)
}

// search runs match over table in every cache, in store order, and keeps
// the first limit hits: each store's hits are best first, and the nearer
// store (repo, then stores.yaml order) wins a tie by coming first.
func (c *caches) search(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error) {
	c.mu.Lock()
	order := append([]store.Source(nil), c.order...)
	combined := c.combined
	c.mu.Unlock()
	var out []opsmemory.Hit
	searched := false
	for _, s := range order {
		if len(out) >= limit {
			break
		}
		f, err := c.of(s)
		if err != nil {
			return nil, err
		}
		if f == combined {
			if searched {
				continue
			}
			searched = true
			got, err := f.SearchHits(ctx, table, match, limit-len(out))
			if err != nil {
				return nil, err
			}
			for _, h := range got {
				hit, err := f.ResolveID(ctx, h.ID)
				if err != nil {
					return nil, err
				}
				out = append(out, opsmemory.Hit{Source: store.Source(hit.Source), ID: h.ID, Score: h.Score, Snippet: h.Snippet})
			}
			continue
		}
		got, err := f.SearchHits(ctx, table, match, limit-len(out))
		if err != nil {
			return nil, err
		}
		for _, h := range got {
			out = append(out, opsmemory.Hit{Source: s, ID: h.ID, Score: h.Score, Snippet: h.Snippet})
		}
	}
	return out, nil
}
