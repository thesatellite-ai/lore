package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorCopyInRepoHasNoOrigin (user report C-7): an actor row created in
// the repo store (here a client repo) is written with no `_o`, so the file
// lore writes passes its own `lore check leaks --ci` and client-repo guard
// ({id, display name, kind} only); an actor row in a store keeps its `_o`,
// and any other row in the repo still gets one. Break-verified: without
// noOriginCopy the repo actor carries `_o`.
func TestActorCopyInRepoHasNoOrigin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	fields := func() map[string]value.Value {
		return map[string]value.Value{schema.FieldActorKind: txt(schema.ActorKindHuman), "display_name": txt("Rosa")}
	}
	repoActor, err := ids.Parse[ids.Actor]("act_01a1cb5743e371b7bab946c6cf48ab02")
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.eng.CreateWithID(ctx, store.SourceRepo, schema.TableActors, repoActor.Any(), fields())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Row.Origin.IsZero() {
		t.Fatalf("the repo actor copy carries _o = %s", got.Row.Origin)
	}
	inStore, err := w.eng.CreateWithID(ctx, agency, schema.TableActors, repoActor.Any(), fields())
	if err != nil {
		t.Fatal(err)
	}
	if inStore.Row.Origin.IsZero() {
		t.Fatal("an actor row in a store lost its _o")
	}
	prj := w.project(t, store.SourceRepo, "widgets")
	if m := w.memIn(t, store.SourceRepo, prj, "repo memory"); m.Origin.IsZero() {
		t.Fatal("a repo memory lost its _o")
	}
}
