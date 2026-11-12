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
	if _, err := e.attached(source); err != nil {
		return 0, err
	}
	cur, err := e.cache.Cursor(ctx, source)
	if err != nil {
		return 0, err
	}
	rows, next, err := e.readRows(ctx, source, cur) // purged ids reach the cache as tombstones
	if err != nil {
		return 0, err
	}
	if err := e.cache.Apply(ctx, source, rows, next); err != nil {
		return 0, err
	}
	return len(rows), nil
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
