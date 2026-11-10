package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// errInjected is the backend write failure the test injects.
var errInjected = errors.New("injected write failure")

// failingWrites is a backend whose writes can be made to fail, recording
// the changes each write call carried.
type failingWrites struct {
	store.Backend
	fail  bool
	calls [][]store.Change
}

func (b *failingWrites) Write(ctx context.Context, changes []store.Change) error {
	b.calls = append(b.calls, changes)
	if b.fail {
		return errInjected
	}
	return b.Backend.Write(ctx, changes)
}

// TestDeleteCarriesStagedActorRow: a delete made with a staging ctx (wire
// writes the caller's actor row first in the same stage) sends the staged
// actor row and the delete in ONE backend write. A failed write leaves
// neither (the deleted row is still live, no actor row), so no later
// Commit can write the actor row of a delete that never happened or fail
// after the delete landed; a good write stores both, audits both, and
// leaves nothing for Commit to write.
func TestDeleteCarriesStagedActorRow(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{true, false} {
		name := map[bool]string{true: "write fails", false: "write succeeds"}[fail]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bg := context.Background()
			spy := &failingWrites{Backend: memstore.NewData().Open(store.OpenOptions{})}
			f := setup(t, WithStore(Attached{Name: kbStore, Backend: spy}))
			key, err := f.eng.Create(bg, kbStore, schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
			if err != nil {
				t.Fatal(err)
			}
			bob, err := ids.New[ids.Actor]()
			if err != nil {
				t.Fatal(err)
			}
			sctx := Staging(bg)
			if _, err = f.eng.CreateWithID(sctx, kbStore, schema.TableActors, bob.Any(), map[string]value.Value{
				"kind": txt(schema.ActorKindHuman), "display_name": txt("Bob"), "stable_key": txt("human:bob@example.com")}); err != nil {
				t.Fatal(err)
			}
			spy.calls, spy.fail = nil, fail
			_, derr := f.eng.Delete(sctx, Ref{Store: kbStore, ID: key.Row.ID})
			if len(spy.calls) != 1 || len(spy.calls[0]) != 2 || spy.calls[0][0].Row.Table != schema.TableActors || spy.calls[0][1].Row.ID != key.Row.ID {
				t.Fatalf("want one write carrying the actor row then the delete, got %d writes: %+v", len(spy.calls), spy.calls)
			}
			spy.fail = false
			_, aerr := spy.Get(bg, schema.TableActors, bob.Any())
			got, gerr := spy.Get(bg, schema.TableMetaKeys, key.Row.ID)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if fail {
				if !errors.Is(derr, errInjected) {
					t.Fatalf("delete err = %v, want the injected failure", derr)
				}
				Discard(sctx)
				if !errors.Is(aerr, store.ErrNotFound) {
					t.Fatalf("a failed delete left the actor row (err %v)", aerr)
				}
				if got.HLC.Compare(key.Row.HLC) != 0 {
					t.Fatal("a failed delete changed the row")
				}
				return
			}
			if derr != nil {
				t.Fatal(derr)
			}
			spy.calls = nil
			if _, err = Commit(sctx); err != nil {
				t.Fatal(err)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("Commit wrote again after the delete carried the stage: %+v", spy.calls)
			}
			if aerr != nil {
				t.Fatalf("the actor row was not stored with the delete: %v", aerr)
			}
			created := false
			for _, ev := range f.audit.events() {
				if ev.Table == schema.TableActors && ev.ID == bob.Any() && ev.Action == ActionCreate {
					created = true
				}
			}
			if !created {
				t.Fatal("the actor row stored with the delete was not audited")
			}
		})
	}
}
