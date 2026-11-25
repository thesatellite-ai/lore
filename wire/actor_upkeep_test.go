package wire

// actor_upkeep_test.go — issue #177 (devco:D-986): the upkeep of the
// caller's existing actor row (stable key back-fill, agent rename,
// SOURCE_OF_TRUTH refresh) is one update, so a staged batch (a write that
// commits all its rows together) neither refuses the second update of the
// row ("staged batch writes … twice") nor drops it.

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestEnsureActorUpkeepIsOneStagedWrite: the caller's repo copy has lost
// its stable_key (an older lore's shape) and is stale against the roster;
// a staged first write to the repo store back-fills the key AND takes the
// roster's name in one update, and the stage commits.
func TestEnsureActorUpkeepIsOneStagedWrite(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	if err := a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := st.actor.Any()
	ref := engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: me}
	if _, err = st.eng.Update(ctx, ref, map[string]value.Value{actorFieldKey: value.Null{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.CreateWithID(ctx, rosterStore, schema.TableActors, me, textValues(t, map[string]string{
		"kind": schema.ActorKindHuman, "display_name": "Alice Roster", "stable_key": testEmail})); err != nil {
		t.Fatal(err)
	}
	st.actorMu.Lock()
	delete(st.actorDone, store.SourceRepo)
	st.actorMu.Unlock()

	sctx := engine.Staging(ctx)
	if err = a.EnsureActor(sctx, store.SourceRepo); err != nil {
		t.Fatalf("staged upkeep of the actor row: %v", err)
	}
	if _, err = engine.Commit(sctx); err != nil {
		t.Fatalf("commit the stage: %v", err)
	}
	_, got, err := st.eng.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if fieldText(got, actorFieldKey) != st.who.StableKey {
		t.Errorf("stable_key %q, want the back-filled %q", fieldText(got, actorFieldKey), st.who.StableKey)
	}
	if fieldText(got, actorFieldDisplay) != "Alice Roster" || fieldText(got, schema.FieldActorOriginStore) != orgStoreID {
		t.Errorf("the stale copy was not refreshed in the same write: %v", got.Fields)
	}
}
