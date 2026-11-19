package engine

// refrewrite.go — fold one row into another (`lore sync merge-rows`,
// DELETION_DESIGN 3.12 row "merge-rows"; `lore sync fix-projects`,
// PROVENANCE 10.6 / FA-AGENT-10: one project per repo data folder).
//
//	MergeRows(w, table, keep, drop)
//	  ├► keep and drop: two live rows of table, distinct ids
//	  ├► Referrers(drop): every live row of every attached store whose
//	  │    fields hold drop's id (a declared ref or ref list of any
//	  │    relation, a polymorphic pair, a ${…} reference in text)
//	  ├► every referrer's store writable? (checked before any write)
//	  ├► one bat_ batch (kind "merge") for the whole fold
//	  ├► each referrer: RefPatch(drop → keep) ─► w.Update
//	  │    (validate, scan, HLC, audit: the ordinary edit path)
//	  └► w.Delete(drop) with reason "merged into <keep>": a soft delete,
//	       visible to the team in `lore trash`, restorable by its batch
//
// Referrers go first: a restrict relation would refuse the delete while a
// live row still points at drop, and a cascade would take a referrer into
// the trash with it. A failure part way leaves the rows already
// re-pointed (each a complete, valid edit); a re-run finds only the rest.
//
// The referrer scan matches the id's text in each field's JSON form, not
// only the declared ref fields: ids are prefixed UUIDv7s, unique across
// tables, so a match is a reference, and a ${row:…} reference in a body or
// a ref field of a plugin kind the registry cannot see is re-pointed too
// (v0.1.17's rewriteIDEverywhere did the same in lore.db).

import (
	"bytes"
	"context"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// BatchKindMerge is the batch kind of a fold (`lore batch list` shows it).
const BatchKindMerge = "merge"

// MergedIntoReason is the delete reason of a folded row: "merged into
// <keep id>" (DELETION 3.12).
const MergedIntoReason = "merged into "

// RowWriter is the write path MergeRows uses for each edit and the final
// delete. *Engine implements it; a composition root passes its own
// wrapper (wire.App) so its write guards (actor rows, target checks, the
// edit batch's before-images) run for every row of the fold too.
type RowWriter interface {
	Update(ctx context.Context, ref Ref, patch map[string]value.Value) (Written, error)
	Delete(ctx context.Context, ref Ref) (Written, error)
}

var _ RowWriter = (*Engine)(nil)

// Referrer is one live row whose fields name another row's id.
type Referrer struct {
	Store store.Source `json:"store"`
	Table row.Table    `json:"table"`
	ID    ids.AnyID    `json:"id"`
	// Fields are the fields holding the id, sorted.
	Fields []string `json:"fields"`
}

// MergeResult is what MergeRows did (or, on a dry run, would do).
type MergeResult struct {
	Table row.Table `json:"table"`
	Keep  ids.AnyID `json:"keep"`
	Drop  ids.AnyID `json:"drop"`
	// Batch is the fold's bat_ id (zero on a dry run): `lore trash restore
	// <bat_…>` brings drop back.
	Batch ids.ID[ids.Batch] `json:"batch,omitzero"`
	// Referrers are the rows re-pointed from drop to keep (keep itself is
	// listed when it named drop: the self reference is removed).
	Referrers []Referrer `json:"referrers"`
	DryRun    bool       `json:"dry_run,omitempty"`
}

// Referrers returns every live, untrashed row of every attached store
// (machine-local rows included, P3-LOCAL-1) whose fields hold id, the row
// itself aside, in store order then table and id. It reads each store from
// the beginning, like dependents: correct on every backend, O(rows).
func (e *Engine) Referrers(ctx context.Context, id ids.AnyID) ([]Referrer, error) {
	needle := []byte(id.String())
	var out []Referrer
	for _, name := range e.allStores() {
		rows, _, err := e.readRows(ctx, name, "")
		if err != nil {
			return nil, err
		}
		var here []Referrer
		for _, sr := range rows {
			if sr.Deleted || sr.ID == id {
				continue
			}
			r, err := sr.Decode()
			if err != nil {
				return nil, err
			}
			if trash.Is(r) {
				continue // a trashed row is not edited; its restore re-checks its parents
			}
			if fs := fieldsHolding(r, needle); len(fs) > 0 {
				here = append(here, Referrer{Store: name, Table: r.Table, ID: r.ID, Fields: fs})
			}
		}
		slices.SortFunc(here, func(a, b Referrer) int {
			if a.Table != b.Table {
				if a.Table < b.Table {
					return -1
				}
				return 1
			}
			return compareIDs(a.ID, b.ID)
		})
		out = append(out, here...)
	}
	return out, nil
}

// compareIDs orders ids by their text.
func compareIDs(a, b ids.AnyID) int {
	switch x, y := a.String(), b.String(); {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// fieldsHolding lists r's fields whose JSON form holds needle, sorted.
func fieldsHolding(r row.Row, needle []byte) []string {
	var out []string
	for _, f := range slices.Sorted(maps.Keys(r.Fields)) {
		if raw, ok := value.JSONForm(r.Fields[f]); ok && bytes.Contains(raw, needle) {
			out = append(out, f)
		}
	}
	return out
}

// RefPatch returns the patch that re-points r's fields from one id to
// another: a list item equal to from becomes to (an item already holding
// to is not repeated), a field equal to from becomes to, and from inside
// text (a ${…} reference) becomes to. With self (r is the kept row) a
// reference to from would point r at itself, so a list drops the item and
// a single ref is cleared (value.Null; Update refuses that on a required
// field, which leaves the fold to the person). Values are inferred from
// the rewritten JSON; Update re-types them to the declared kinds.
func RefPatch(r row.Row, from, to ids.AnyID, self bool) (map[string]value.Value, error) {
	oldB, newB := []byte(from.String()), []byte(to.String())
	quotedOld, quotedNew := canon.String(from.String()), canon.String(to.String())
	patch := map[string]value.Value{}
	for _, f := range fieldsHolding(r, oldB) {
		raw, _ := value.JSONForm(r.Fields[f])
		var next []byte
		switch {
		case canon.KindOf(raw) == canon.KindArray:
			elems, err := canon.Elements(raw)
			if err != nil {
				return nil, errs.Wrap(errs.InvalidInput, "re-point "+r.ID.String()+"."+f, err)
			}
			next = rewriteList(elems, quotedOld, quotedNew, oldB, newB, self)
		case bytes.Equal(raw, quotedOld) && self:
			patch[f] = value.Null{}
			continue
		default:
			next = bytes.ReplaceAll(raw, oldB, newB)
		}
		v, err := value.Infer(next)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "re-point "+r.ID.String()+"."+f, err)
		}
		patch[f] = v
	}
	return patch, nil
}

// rewriteList is RefPatch for a JSON array: items equal to the old id are
// replaced (dropped when self), other items have the id replaced inside,
// and an item already present is not repeated.
func rewriteList(elems [][]byte, quotedOld, quotedNew, oldB, newB []byte, self bool) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	seen := map[string]bool{}
	for _, e := range elems {
		switch {
		case bytes.Equal(e, quotedOld) && self:
			continue
		case bytes.Equal(e, quotedOld):
			e = quotedNew
		default:
			e = bytes.ReplaceAll(e, oldB, newB)
		}
		if seen[string(e)] {
			continue
		}
		seen[string(e)] = true
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(e)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// MergeRows folds drop into keep (see the file comment): every referrer
// of drop is re-pointed at keep through w, then drop is soft-deleted with
// the reason "merged into <keep>", all in one bat_ batch (ctx's batch when
// it carries one, so a caller folding several rows keeps one batch). w nil
// is the engine itself. dryRun returns the plan and writes nothing.
func (e *Engine) MergeRows(ctx context.Context, w RowWriter, table row.Table, keep, drop ids.AnyID, dryRun bool) (MergeResult, error) {
	if w == nil {
		w = e
	}
	res := MergeResult{Table: table, Keep: keep, Drop: drop, DryRun: dryRun}
	if keep == drop {
		return res, errs.New(errs.InvalidInput, "merge: keep and drop are the same row ("+keep.String()+")").
			WithHint("name two rows: --keep the row to keep, --drop its duplicate")
	}
	if _, err := e.tableDef(table); err != nil {
		return res, err
	}
	for _, id := range []ids.AnyID{keep, drop} {
		if err := e.liveRowOf(ctx, table, id); err != nil {
			return res, err
		}
	}
	refs, err := e.Referrers(ctx, drop)
	if err != nil {
		return res, err
	}
	res.Referrers = refs
	for _, r := range refs {
		if _, werr := e.writable(ctx, r.Store); werr != nil {
			return res, errs.Wrap(errs.ReadOnly, "merge: "+string(r.Table)+" "+r.ID.String()+" names "+drop.String()+" but its store "+string(r.Store)+" is not writable; nothing changed", werr)
		}
	}
	if dryRun {
		return res, nil
	}
	b, ok := BatchOf(ctx)
	if !ok {
		id, nerr := e.ids.NewID(ids.PrefixBatch)
		if nerr != nil {
			return res, nerr
		}
		bid, perr := ids.Parse[ids.Batch](id.String())
		if perr != nil {
			return res, perr
		}
		b = Batch{ID: bid, Kind: BatchKindMerge}
		ctx = WithBatch(ctx, b)
	}
	res.Batch = b.ID
	for _, r := range refs {
		_, cur, gerr := e.locate(ctx, Ref{Store: r.Store, Table: r.Table, ID: r.ID})
		if gerr != nil {
			return res, gerr
		}
		patch, perr := RefPatch(cur, drop, keep, r.ID == keep)
		if perr != nil {
			return res, perr
		}
		if len(patch) == 0 {
			continue
		}
		if _, uerr := w.Update(withRefRepoint(ctx), Ref{Store: r.Store, Table: r.Table, ID: r.ID}, patch); uerr != nil {
			return res, errs.Prefix("merge: re-point "+string(r.Table)+" "+r.ID.String()+" from "+drop.String()+" to "+keep.String(), uerr)
		}
	}
	if _, derr := w.Delete(WithDeleteReason(ctx, MergedIntoReason+keep.String()), Ref{Table: table, ID: drop}); derr != nil {
		return res, derr
	}
	return res, nil
}

// liveRowOf checks that id is a live row of table: a row in the trash is
// E_IN_TRASH (its message says how to restore it), a tombstone E_NOT_FOUND.
func (e *Engine) liveRowOf(ctx context.Context, table row.Table, id ids.AnyID) error {
	_, r, err := e.locate(ctx, Ref{Table: table, ID: id})
	if err != nil {
		return err
	}
	if !r.Deleted {
		return nil
	}
	if terr := e.InTrash(ctx, Ref{Table: table, ID: id}); terr != nil {
		return terr // reads show a trashed row as a tombstone (hideTrashed)
	}
	return errs.New(errs.NotFound, "merge: "+string(table)+" "+id.String()+" is deleted")
}

// refRepointKey marks the Update of a merge's re-point (withRefRepoint).
type refRepointKey struct{}

// withRefRepoint marks ctx as a merge re-point: the referrer only trades
// the dropped id for the kept one. Such an edit can land two rows on one
// natural key (two parallel bootstraps each made a repos row for mount
// "app"; fix-projects re-points one onto the kept project). That is the
// duplicate the merge exists to surface, not a rename by a person, so
// refuseTakenKeyOnEdit lets it through: refusing it stopped `lore sync
// fix-projects` half way, after earlier referrers were already re-pointed
// (docs example sync#fix-projects, batch 2026-11-08). The duplicate rows
// stay for `lore sync merge-rows`, as before the edit check existed.
func withRefRepoint(ctx context.Context) context.Context {
	return context.WithValue(ctx, refRepointKey{}, true)
}

// isRefRepoint reports whether ctx is a merge re-point (withRefRepoint).
func isRefRepoint(ctx context.Context) bool {
	v, _ := ctx.Value(refRepointKey{}).(bool)
	return v
}
