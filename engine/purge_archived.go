package engine

// Purge of archived rows (DELETION_DESIGN 3.12, decision DEL-ARCH, owner
// 2026-11-05): `lore trash purge --archived-before <date|age>` replaces the
// v0.1.17 `lore sync purge --archived-before` (lore.db + lsync). It works on
// the new row files: every row of an attached store whose archived_at is
// before the cutoff is purged like `trash purge <id>` (a tombstone kept
// forever, its id in _purged.json, DQ4).
//
//	Archived(cutoff)       ─► each store once ─► raw rows ─► not a tombstone,
//	                          not purged, archived_at < cutoff
//	PurgeArchived(cutoff)  ─► Archived ─► per row: re-read (a cascade of an
//	                          earlier row may have tombstoned it) ─► remove(purge)

import (
	"bytes"
	"context"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// FieldArchivedAt is the archive stamp every archivable table carries
// (`lore <kind> archive`, schema built-ins).
const FieldArchivedAt = "archived_at"

// archivedAtKey is the cheap byte filter for a row file that may be archived.
var archivedAtKey = []byte(`"` + FieldArchivedAt + `"`)

// Archived lists the rows of every attached store (each place once) whose
// archived_at is before cutoff: alive rows and rows in the trash, never
// tombstones or purged ids. Info is empty for alive rows.
func (e *Engine) Archived(ctx context.Context, cutoff time.Time) (TrashResult, error) {
	var res TrashResult
	err := e.eachStoreOnce(func(name store.Source, st Attached) error {
		set, err := purgedSet(ctx, st)
		if err != nil {
			return err
		}
		rows, _, err := st.Backend.Read(ctx, "")
		if err != nil {
			return err
		}
		for _, sr := range rows {
			if sr.Deleted || set[sr.ID.String()] || !bytes.Contains(sr.Doc, archivedAtKey) {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				return derr
			}
			at, ok := archivedAt(r.Fields[FieldArchivedAt])
			if !ok || !at.Before(cutoff) {
				continue
			}
			if err := refuseActorDelete(r); err != nil {
				continue // BATCH2 #8: actors are never deleted, so never purged here either
			}
			res.Rows = append(res.Rows, Trashed{Store: name, Row: r})
		}
		return nil
	})
	return res, err
}

// PurgeArchived purges every row Archived lists (DEL-ARCH); with dryRun it
// only lists them. The caller has already asked for --confirm (DQ2). A row
// an earlier row's cascade already tombstoned is skipped, so a run twice
// purges nothing the second time.
func (e *Engine) PurgeArchived(ctx context.Context, cutoff time.Time, dryRun bool) (TrashResult, error) {
	res, err := e.Archived(ctx, cutoff)
	if err != nil || dryRun || len(res.Rows) == 0 {
		return res, err
	}
	ctx = withPurgeTrailer(ctx, e.clock.Now(hlc.HLC{}).Wall, "") // DELETION 2.6: the purge commit trailer
	var out TrashResult
	for _, t := range res.Rows {
		sr, err := e.stores[t.Store].Backend.Get(ctx, t.Row.Table, t.Row.ID)
		if err != nil {
			return out, err
		}
		if sr.Deleted {
			continue // tombstoned by the cascade of a row purged before it
		}
		cur, err := sr.Decode()
		if err != nil {
			return out, err
		}
		if _, err := e.remove(ctx, t.Store, cur, true); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, t)
	}
	return out, nil
}

// archivedAt reads an archived_at value: a DateTime or Date, or the RFC
// 3339 text an older row file holds; absent or null is not archived.
func archivedAt(v value.Value) (time.Time, bool) {
	if v == nil {
		return time.Time{}, false
	}
	switch x := v.(type) {
	case value.DateTime:
		return x.Time(), true
	case value.Date:
		return x.Time(), true
	case value.Null:
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, v.String()); err == nil {
		return t, true
	}
	return time.Time{}, false
}
