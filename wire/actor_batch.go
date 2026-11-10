package wire

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// withActorRows runs one write (a create, an edit or a delete) together with the actor rows it
// writes first (ensureActor, ensureNamedActors) as ONE engine batch
// (engine.Staging/Commit): a write the engine refuses (F15 E_LEAK_BLOCKED,
// a secret answer, a bad ref, …) writes nothing at all, not even the
// caller's actor row, so "nothing written" holds (G-ENGINE-2). A ctx that
// already stages a batch (resource import) is joined, not nested. A delete
// writes straight to the store (it may span stores), so it carries the
// staged actor rows of its store in its own one backend write
// (engine absorbStaged): both land or neither does. On a
// refusal the actor rows this write marked done are unmarked, so the next
// write ensures them again.
//
// The backend checks a staged create's "must not exist yet" only at
// Commit, so two processes making the same person's first write into one
// store (two clones sharing one bare store clone, side effect 32) both
// stage the same actor row and the later Commit is store.ErrStale for the
// whole batch. Nothing of that batch was written (all or none), so it is
// run once more: ensureActor then reads the row the other process wrote
// and skips it. A second ErrStale is returned as is (a real conflict, or a
// keyed create whose key another run stored first: its caller handles it).
func (a *App) withActorRows(ctx context.Context, st *state, write func(context.Context) (engine.Written, error)) (engine.Written, error) {
	if engine.InStage(ctx) {
		return write(ctx)
	}
	w, err := a.actorBatchOnce(ctx, st, write)
	if errors.Is(err, store.ErrStale) {
		return a.actorBatchOnce(ctx, st, write)
	}
	return w, err
}

// actorBatchOnce is one staged attempt of withActorRows.
func (a *App) actorBatchOnce(ctx context.Context, st *state, write func(context.Context) (engine.Written, error)) (engine.Written, error) {
	st.actorMu.Lock()
	before := make(map[store.Source]bool, len(st.actorDone))
	for k, v := range st.actorDone {
		before[k] = v
	}
	st.actorMu.Unlock()
	undo := func() {
		st.actorMu.Lock()
		defer st.actorMu.Unlock()
		for k := range st.actorDone {
			if !before[k] {
				delete(st.actorDone, k)
			}
		}
	}
	sctx := engine.Staging(ctx)
	w, err := write(sctx)
	if err != nil {
		undo()
		engine.Discard(sctx) // the refused stage is dropped: nothing reaches the store
		return w, err
	}
	warns, err := engine.Commit(sctx)
	if err != nil {
		undo()
		return engine.Written{}, err
	}
	w.Warnings = append(w.Warnings, warns...)
	return w, nil
}
