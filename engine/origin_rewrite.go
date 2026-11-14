package engine

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// OpRepoMergeIDs is the operation name RewriteOrigin's audit event carries
// when it runs outside Run (PROVENANCE_DESIGN 5.3, 11.9: the batch's cmd is
// recorded in the audit log, never in the rows).
const OpRepoMergeIDs = "repo.merge-ids"

// RewriteOrigin rewrites the `_o` block of one live row: the only write that
// changes `_o` after creation (PROVENANCE_DESIGN 5.3: `lore repo merge-ids`
// replaces a losing duplicate repo id). fn gets the current `_o` and returns
// the new one and whether it changed; unchanged means no write (Written is
// the current row). Every field, meta entry and the creation stamp are kept;
// the row gets a new HLC and updated_* like any edit, and is written with
// Expect set to the HLC read, so a concurrent edit is E_STALE (rerun).
// A tombstone is E_NOT_FOUND / E_PURGED and a trashed row E_IN_TRASH
// (deletedErr), exactly as Update refuses them.
//
// Flow: locate → writable → refuse deleted/trashed → fn → put → audit.
func (e *Engine) RewriteOrigin(ctx context.Context, ref Ref, fn func(value.JSON) (value.JSON, bool, error)) (Written, error) {
	cl := e.callOf(ctx)
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	st, err := e.writable(ctx, src)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, deletedErr(ctx, st, cur, "is deleted")
	}
	next, changed, err := fn(cur.Origin)
	if err != nil {
		return Written{}, err
	}
	if !changed {
		return Written{Row: cur}, nil
	}
	if next.IsZero() || !next.IsObject() {
		return Written{}, errs.New(errs.InvalidInput, "a rewritten _o must be a JSON object")
	}
	actor := e.actorOf(cl)
	ours := cur
	ours.Origin = next
	ours.HLC = e.clock.Now(cur.HLC)
	ours.UpdatedAt, ours.UpdatedBy = ours.HLC.Wall, actor
	got, err := e.put(ctx, st, ours, cur.HLC)
	if errors.Is(err, store.ErrStale) {
		return Written{}, errs.Wrap(errs.Stale, ""+cur.ID.String()+" changed while its origin was rewritten", err).
			WithHint("rerun the command")
	}
	if err != nil {
		return Written{}, err
	}
	opName := cl.op
	if opName == "" {
		opName = OpRepoMergeIDs
	}
	ev := Event{Op: opName, Action: ActionUpdate, Store: src, Table: cur.Table, ID: cur.ID, HLC: got.row.HLC, Actor: actor}
	aw, err := e.finishW(ctx, src, got.docs(), ev)
	return Written{Row: got.row, Warnings: aw}, err
}
