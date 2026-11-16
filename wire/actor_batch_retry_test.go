package wire

// actor_batch_retry_test.go — devco D-470: six parallel first writes into
// a new store folder (three from agent sessions) each stage the folder's
// project row, the person's row and the agent's row; one writer lost a
// different race on its first and second attempt and failed with E_STALE,
// because withActorRows ran a stale batch only once more.

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

func TestActorBatchRetriesEachLostRace(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	// every race a first write can lose, lost one after the other
	lost := actorBatchAttempts - 1
	runs := 0
	_, err = a.withActorRows(background(), st, func(context.Context) (engine.Written, error) {
		runs++
		if runs <= lost {
			return engine.Written{}, store.ErrStale
		}
		return engine.Written{}, nil
	})
	if err != nil {
		t.Fatalf("a batch that lost %d races in a row, then could land, failed after %d runs: %v", lost, runs, err)
	}

	// a stale that never clears (a real conflict) still ends, as ErrStale
	runs = 0
	_, err = a.withActorRows(background(), st, func(context.Context) (engine.Written, error) {
		runs++
		return engine.Written{}, store.ErrStale
	})
	if !errors.Is(err, store.ErrStale) || runs != actorBatchAttempts {
		t.Fatalf("a batch that is always stale = %v after %d runs, want ErrStale after %d", err, runs, actorBatchAttempts)
	}
}
