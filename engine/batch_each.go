package engine

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/store"
)

// CommitEach writes everything staged on ctx as one backend write PER
// STORE, in the order the stores were first written, and returns the
// stores it wrote. Why (devco D-925, a 440-row import took hours with one
// store commit per row): `lore batch apply` runs a file of mixed writes
// whose rows land in whichever store each op routes to, so one stage may
// hold rows of several stores; Commit refuses that because no backend
// write covers two stores. CommitEach keeps each store all or none (its
// part is one Backend.Write) and stops at the first store that fails:
// the stores before it in written are written, the rest are not. The
// stage is closed either way, as with Commit.
func CommitEach(ctx context.Context) (written []store.Source, warns []Warning, err error) {
	s, ok := ctx.Value(stageKey{}).(*stage)
	if !ok || s == nil {
		return nil, nil, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, nil
	}
	s.closed = true
	e, order := s.e, slices.Clone(s.order)
	s.mu.Unlock()
	if e == nil {
		return nil, nil, nil
	}
	defer e.trackStage(s, false) // committed rows are read from the backend from now on
	for _, name := range order {
		p := s.bySrc[name]
		if len(p.changes) == 0 {
			continue
		}
		if err = e.writeChanges(ctx, p.st, p.changes, p.journal); err != nil {
			return written, warns, err
		}
		w, ferr := e.finishW(ctx, p.st.Name, p.docs, p.events...)
		warns = append(warns, w...)
		written = append(written, name)
		if ferr != nil {
			return written, warns, ferr
		}
	}
	return written, warns, nil
}

// StagedStores is the stores ctx's open stage holds changes for, in the
// order they were first written (none outside a stage): a caller that
// lands the stage with CommitEach names them before it writes.
func StagedStores(ctx context.Context) []store.Source {
	s := stageOf(ctx)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Source
	for _, name := range s.order {
		if len(s.bySrc[name].changes) > 0 {
			out = append(out, name)
		}
	}
	return out
}
