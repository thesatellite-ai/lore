package host

// live_stage.go — wire.LiveAtomic on the repo store's live backend
// (P3-SIGCOORD-6): a coordination write's rows ride in the signals push as
// one atomic push of refs/lore/signals and refs/lore/live, as v0.1.17's
// stagedLiveRows did, instead of a second push of the live ref at the
// run's finish (wire/coord_standalone_atomic.go has the caller's flow).
//
//	AtomicMark      local live OID kept as the rollback point (one open mark)
//	AtomicUpdate    local ahead of tracking ─► fast-forward refs/lore/live
//	AtomicConfirm   the atomic push succeeded: tracking + seen = pushed OID
//	AtomicRollback  lost race: local ref back to the mark, fetch, integrate
//	finish          an open mark (the write never succeeded) is rolled back
//	                before the run pushes, so no unfenced row goes out

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/rowroute"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

var _ wire.LiveAtomic = (*liveBackend)(nil)

// AtomicRemote implements wire.LiveAtomic.
func (b *liveBackend) AtomicRemote(ctx context.Context) (string, bool) {
	s := b.session(ctx)
	if s == nil || s.blocked != nil || s.ref.Remote == nil || !s.autopush {
		return "", false
	}
	return s.ref.RemoteName, true
}

// AtomicRoutesLive implements wire.LiveAtomic: Write's routing of one row
// (a live row stays live, a live kind or --live goes live, a branch row's
// edit stays with the code).
func (b *liveBackend) AtomicRoutesLive(ctx context.Context, table row.Table, id ids.AnyID) (bool, error) {
	s := b.session(ctx)
	if s == nil {
		return false, nil
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return false, err
	}
	k := keyOfID(table, id)
	_, inLive := files[k]
	chosen := liveChosen(ctx, table) && liveReadable(s, table)
	if chosen {
		return true, nil
	}
	if !isLiveKind(s, table) && (!inLive || !liveReadable(s, table)) {
		return false, nil
	}
	_, berr := b.inner.Get(ctx, table, id)
	if berr != nil && !errors.Is(berr, store.ErrNotFound) {
		return false, berr
	}
	place := rowroute.Decide(liveRequest(ctx, s), rowroute.Row{Table: k.Table, ID: k.ID, InLive: inLive, InBranch: berr == nil}).Place
	return place == rowroute.PlaceLive, nil
}

// AtomicMark implements wire.LiveAtomic.
func (b *liveBackend) AtomicMark(ctx context.Context) error {
	s := b.session(ctx)
	if s == nil || s.atomicOpen {
		return nil
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return err
	}
	s.atomicMark, s.atomicOpen = snap.Local, true
	return nil
}

// AtomicUpdate implements wire.LiveAtomic.
func (b *liveBackend) AtomicUpdate(ctx context.Context) (gitremote.RefUpdate, error) {
	s := b.session(ctx)
	if s == nil {
		return gitremote.RefUpdate{}, nil
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil || !snap.Ahead() {
		return gitremote.RefUpdate{}, err
	}
	return gitremote.RefUpdate{Src: snap.Local, Dst: s.ref.Name, Mode: gitremote.ModeFastForward}, nil
}

// AtomicConfirm implements wire.LiveAtomic.
func (b *liveBackend) AtomicConfirm(ctx context.Context, oid string) error {
	s := b.session(ctx)
	if s == nil {
		return nil
	}
	s.atomicOpen, s.atomicMark = false, ""
	return s.ref.Confirm(ctx, oid)
}

// AtomicRollback implements wire.LiveAtomic.
func (b *liveBackend) AtomicRollback(ctx context.Context) error {
	s := b.session(ctx)
	if s == nil || !s.atomicOpen {
		return nil
	}
	if err := s.atomicReset(ctx); err != nil {
		return err
	}
	// The race may have been lost on the live ref: read the winners' rows
	// before the decision runs again.
	s.fetch(ctx, s.clk.Now())
	res, err := s.ref.Integrate(ctx)
	if err != nil {
		return err
	}
	s.notes = append(s.notes, res.Notes...)
	return nil
}

// atomicReset moves the local live ref back to the open mark and closes
// it (a no-op without one).
func (s *liveSession) atomicReset(ctx context.Context) error {
	if !s.atomicOpen {
		return nil
	}
	s.atomicOpen = false
	snap, err := s.ref.Snapshot(ctx)
	if err != nil || snap.Local == s.atomicMark {
		return err
	}
	if s.atomicMark == "" {
		return s.repo.DeleteRef(ctx, s.ref.Name, snap.Local)
	}
	return s.repo.UpdateRef(ctx, s.ref.Name, s.atomicMark, snap.Local)
}

// keyOfID is the live key of table/id.
func keyOfID(table row.Table, id ids.AnyID) live.RowKey {
	return live.RowKey{Table: string(table), ID: id.String()}
}
