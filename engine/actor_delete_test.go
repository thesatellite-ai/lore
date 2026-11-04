package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorNeverDeleted: Delete and Purge of an actor row answer E_REFUSED
// naming the actor and `lore actor deactivate`, and the row stays live
// (BATCH2 #8, DELETION 4.1: every *_by field points at an actor).
func TestActorNeverDeleted(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	act, err := f.eng.Create(ctx, store.SourceRepo, schema.TableActors, map[string]value.Value{
		"kind": txt(schema.ActorKindHuman), "display_name": txt("Aman Bansal"), "stable_key": txt("khanakia@gmail.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{ID: act.Row.ID}
	for name, call := range map[string]func(context.Context, Ref) (Written, error){"delete": f.eng.Delete, "purge": f.eng.Purge} {
		_, err := call(ctx, ref)
		if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), act.Row.ID.String()) {
			t.Fatalf("%s: %v, want E_REFUSED naming %s", name, err, act.Row.ID)
		}
		if e := asErr(err); e == nil || !strings.Contains(e.Hint, "lore actor deactivate") {
			t.Fatalf("%s: hint %v, want lore actor deactivate", name, err)
		}
	}
	if _, got, err := f.eng.Get(ctx, ref); err != nil || got.Deleted {
		t.Fatalf("the actor must stay live: %v deleted=%v", err, got.Deleted)
	}
}

// asErr returns err as *errs.Error, or nil.
func asErr(err error) *errs.Error {
	var e *errs.Error
	if !errors.As(err, &e) {
		return nil
	}
	return e
}
