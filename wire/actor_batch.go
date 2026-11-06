package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// withActorRows runs one create (write) together with the actor rows it
// writes first (ensureActor, ensureNamedActors) as ONE engine batch
// (engine.Staging/Commit): a create the engine refuses (F15 E_LEAK_BLOCKED,
// a secret answer, a bad ref, …) writes nothing at all, not even the
// caller's actor row, so "nothing written" holds (G-ENGINE-2). A ctx that
// already stages a batch (resource import) is joined, not nested. On a
// refusal the actor rows this write marked done are unmarked, so the next
// write ensures them again.
func (a *App) withActorRows(ctx context.Context, st *state, write func(context.Context) (engine.Written, error)) (engine.Written, error) {
	if engine.InStage(ctx) {
		return write(ctx)
	}
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
		undo() // the refused stage is dropped: nothing reaches the store
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
