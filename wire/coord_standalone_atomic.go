package wire

// coord_standalone_atomic.go — one push per coordination attempt
// (P3-SIGCOORD-6, v0.1.17 stagedLiveRows): when the claims and the rows of
// a fenced write live in the same repo, and the rows go to the repo's live
// ref, the row commit joins the signals push as one `git push --atomic`
// instead of a second push of refs/lore/live after it.
//
//	engineRows.Stage ─► every row routes live (LiveAtomic.AtomicRoutesLive)
//	                    and the live ref pushes to the signals remote?
//	   yes ─► atomicCompanion (a local signals.Companion):
//	            Prepare  : AtomicMark, the rows through the engine onto the
//	                       local live ref, AtomicUpdate = its fast-forward
//	            push     : signals lease + refs/lore/live, atomic
//	            Finalize : AtomicConfirm (tracking = pushed commit, so the
//	                       run's finish has nothing left to push)
//	            lost race: the next Get/List (the re-run decision) calls
//	                       AtomicRollback first: the local live ref goes back
//	                       to the mark, the remote live ref is fetched and
//	                       integrated, so the decision reads what it read
//	                       before plus the winners' rows
//	   no  ─► engineCompanion (rows after the signals push, as before)
//
// The local live ref moves in Prepare (the engine has no staged write), so
// an attempt that never succeeds is undone by the host when the run ends
// (the session rolls back an open mark before its push). Without a host
// that implements LiveAtomic (the old binary, tests) nothing changes.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// LiveAtomic is implemented by a repo store backend that keeps rows on a
// live ref (wire/host's liveBackend), so a coordination write can carry its
// rows in the signals push. Every method acts on the run in ctx; with no
// live session there, AtomicRemote answers ok=false and the rest are
// no-ops.
type LiveAtomic interface {
	// AtomicRemote is the remote the live ref pushes to; ok=false when this
	// run has no live session that pushes (live off, autopush off, no
	// remote, writes blocked).
	AtomicRemote(ctx context.Context) (remote string, ok bool)
	// AtomicRoutesLive reports whether a write of table/id goes to the live
	// ref (the backend's routing rule); a row that would go to the branch
	// cannot be undone, so the caller then keeps the old order.
	AtomicRoutesLive(ctx context.Context, table row.Table, id ids.AnyID) (bool, error)
	// AtomicMark records the local live ref as the point a lost attempt
	// rolls back to (once; a later call while marked keeps the first).
	AtomicMark(ctx context.Context) error
	// AtomicUpdate is the fast-forward of the remote live ref to the local
	// one; Dst "" when the local ref holds nothing the remote lacks.
	AtomicUpdate(ctx context.Context) (gitremote.RefUpdate, error)
	// AtomicConfirm records that the remote holds oid (the atomic push
	// succeeded) and closes the mark.
	AtomicConfirm(ctx context.Context, oid string) error
	// AtomicRollback undoes an open mark: the local live ref back to it,
	// then a fetch and integrate of the remote live ref. No open mark: a
	// no-op.
	AtomicRollback(ctx context.Context) error
}

// liveAtomicOf finds the LiveAtomic layer of the repo store's backend
// (nil when none).
func (a *App) liveAtomicOf(ctx context.Context) LiveAtomic {
	if _, err := a.state(ctx); err != nil {
		return nil
	}
	a.mu.Lock()
	var b store.Backend
	if a.st != nil {
		b = a.st.byName[store.SourceRepo]
	}
	a.mu.Unlock()
	for b != nil {
		if la, ok := b.(LiveAtomic); ok {
			return la
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return nil
		}
		b = u.Unwrap()
	}
	return nil
}

// atomicLive is the LiveAtomic of this world's write when its claims sit
// on this repo's own signals ref and the live ref pushes to the same
// remote; nil otherwise (a store's ref, a client task, no live session).
func (w *coordWorld) atomicLive(ctx context.Context) LiveAtomic {
	if w.client || w.spot.store || w.spot.remote == nil || !w.autopush {
		return nil
	}
	la := w.a.liveAtomicOf(ctx)
	if la == nil {
		return nil
	}
	if remote, ok := la.AtomicRemote(ctx); !ok || remote != w.spot.remoteName {
		return nil
	}
	return la
}

// atomicCompanion carries a coordination write's rows in the signals push
// (see the file comment).
type atomicCompanion struct {
	a    *App
	la   LiveAtomic
	rows []coord.RowWrite
	// update is what Prepare returned (Finalize confirms its Src).
	update gitremote.RefUpdate
}

var _ signals.Companion = (*atomicCompanion)(nil)

// Prepare writes every row onto the local live ref and returns its push.
func (c *atomicCompanion) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	if err := c.la.AtomicMark(ctx); err != nil {
		return gitremote.RefUpdate{}, err
	}
	for _, w := range c.rows {
		if err := c.a.writeDoc(ctx, rowTable(w), w.ID, w.Doc, ""); err != nil {
			return gitremote.RefUpdate{}, err
		}
	}
	u, err := c.la.AtomicUpdate(ctx)
	if err != nil {
		return gitremote.RefUpdate{}, err
	}
	c.update = u
	return u, nil
}

// Finalize records the pushed live commit.
func (c *atomicCompanion) Finalize(ctx context.Context) error {
	return c.la.AtomicConfirm(ctx, c.update.Src)
}

// stageAtomic is the atomic companion of rows, or ok=false when one of
// them would not go to the live ref.
func (r engineRows) stageAtomic(ctx context.Context, rows []coord.RowWrite) (signals.Companion, bool, error) {
	for _, w := range rows {
		id, err := ids.ParseAny(w.ID)
		if err != nil {
			return nil, false, nil // writeDoc reports the bad id
		}
		if src, _, found, lerr := r.a.locateRow(ctx, rowTable(w), w.ID); lerr != nil || (found && src != store.SourceRepo) {
			return nil, false, lerr // a row of another store is not on this live ref
		}
		live, err := r.at.AtomicRoutesLive(ctx, rowTable(w), id)
		if err != nil || !live {
			return nil, false, err
		}
	}
	return &atomicCompanion{a: r.a, la: r.at, rows: rows}, true, nil
}
