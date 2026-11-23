package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Refresh brings source's cache up to date with its backend: every row
// changed since the cache's cursor (tombstones included) is applied with
// the backend's new cursor in one cache transaction. It is how rows written
// outside lore (a git pull, a hand edit, another process) reach search and
// render. Returns the number of rows applied.
func (e *Engine) Refresh(ctx context.Context, source store.Source) (int, error) {
	a, err := e.attached(source)
	if err != nil {
		return 0, err
	}
	cur, err := e.cache.Cursor(ctx, source)
	if err != nil {
		return 0, err
	}
	if cur, err = e.dropLostCursor(ctx, source, a.Backend, cur); err != nil {
		return 0, err
	}
	rows, next, err := e.readRows(ctx, source, cur) // purged ids reach the cache as tombstones
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 && next == cur && cur != "" {
		return 0, nil // nothing changed: the cache is current, not rewritten (B1-16)
	}
	// devco:R-14: search and sql read the cache, so they see the same
	// masked text show does (readmask.go). The rows are the backend's
	// current state: one older than the cache's copy replaces it when the
	// backend still holds it (stillCurrent, devco D-920).
	ctx = store.WithCurrentCheck(ctx, func(ctx context.Context, r store.Row) bool { return stillCurrent(ctx, a.Backend, r) })
	if err := e.cache.Apply(ctx, source, e.maskStoreRows(ctx, source, rows), next); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// CurrentRows is every row source's backend holds now, as a refresh from
// the start would hand them to the cache (purged and trashed rows as
// tombstones, readRows), through ctx's read snapshot when it has one. Why
// (devco D-920): `lore doctor` compares them with the versions the cache
// holds to find a stale search index.
func (e *Engine) CurrentRows(ctx context.Context, source store.Source) ([]store.Row, error) {
	if _, err := e.attached(source); err != nil {
		return nil, err
	}
	rows, _, err := e.readRows(ctx, source, "")
	return rows, err
}

// stillCurrent reports whether b holds r's version of its row now: the
// store.CurrentCheck a refresh hands the cache. A row b no longer has
// (gone, unreadable) is not current; a tombstone is current while b
// holds that tombstone or the row it hid (purged and trashed rows reach
// the cache as tombstones with the row's hlc, purge.go and trash.go).
func stillCurrent(ctx context.Context, b store.Backend, r store.Row) bool {
	got, err := b.Get(ctx, r.Table, r.ID)
	if err != nil {
		return false
	}
	return got.HLC.Compare(r.HLC) == 0
}

// dropLostCursor empties source's cache when its backend says cur no
// longer describes a past state of its rows (store.CursorChecker: a store
// clone reset by hand behind the cache), and returns the zero cursor so
// the refresh refills it from every row file; any other cursor is
// returned as is. Without it the cache kept the rows the reset dropped
// (owner P-5, devco D-763).
func (e *Engine) dropLostCursor(ctx context.Context, source store.Source, b store.Backend, cur store.Cursor) (store.Cursor, error) {
	if cur == "" {
		return cur, nil
	}
	for b != nil {
		if c, ok := b.(store.CursorChecker); ok {
			lost, err := c.CursorLost(ctx, cur)
			if err != nil || !lost {
				return cur, err
			}
			if err := e.cache.Rebuild(ctx, source); err != nil {
				return cur, err
			}
			return "", nil
		}
		u, ok := b.(unwrapper)
		if !ok {
			break
		}
		b = u.Unwrap()
	}
	return cur, nil
}

// Sync exchanges source's changes with its remote (Backend.Sync), then
// refreshes the cache with whatever arrived. A read-only store still
// syncs: it receives, it just never sends local writes (it has none).
func (e *Engine) Sync(ctx context.Context, source store.Source) (store.SyncReport, error) {
	a, err := e.attached(source)
	if err != nil {
		return store.SyncReport{}, err
	}
	dropSnapshot(ctx, a)
	rep, err := a.Backend.Sync(ctx)
	dropSnapshot(ctx, a) // readsnap.go: the next read sees what arrived
	if err != nil {
		return rep, err
	}
	if _, err := e.Refresh(ctx, source); err != nil {
		return rep, err
	}
	return rep, nil
}

// HasOtherWriter is the DOCS O6 test for the one-time `_v:1 → _v:2`
// question on the first 0.1.18 write in a project (FORMAT_EVOLUTION rule
// 2, F5): ask only when the rows show a writer other than the caller. A row
// whose writer is unknown (no created_by and no updated_by, as in rows
// written before actors existed) does not count: a solo repo never asks.
// Phase 2 calls it before that write; the engine does not ask by itself.
//
// A v0.1.17 row names its writer in a table field instead of the envelope
// (v1CreatorField on memories and other knowledge rows); it counts too.
func HasOtherWriter(rows []row.Row, caller ids.ID[ids.Actor]) bool {
	for _, r := range rows {
		writers := []ids.ID[ids.Actor]{r.CreatedBy, r.UpdatedBy, r.HLC.Actor}
		if v, ok := r.Fields[v1CreatorField]; ok && v != nil {
			if a, err := ids.Parse[ids.Actor](v.String()); err == nil {
				writers = append(writers, a)
			}
		}
		for _, a := range writers {
			if !a.IsZero() && a != caller {
				return true
			}
		}
	}
	return false
}

// v1CreatorField is the v0.1.17 column holding a row's creator
// (created_by_actor_id), which `_v: 1` rows keep as an ordinary field.
const v1CreatorField = "created_by_actor_id"
