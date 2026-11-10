package profile

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
)

// testNow is the fake's wall clock.
var testNow = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

// person seeds a human actor with stable key human:<email> and @handle.
func person(t *testing.T, f *profilestoretest.Fake, handle, name, email string) ids.ID[ids.Actor] {
	t.Helper()
	id, err := identity.ActorID(humanKeyPrefix + email)
	if err != nil {
		t.Fatal(err)
	}
	f.Put(row.Row{Table: schema.TableActors, ID: id.Any(), Fields: map[string]value.Value{
		"kind": txt(t, schema.ActorKindHuman), schema.FieldActorDisplayName: txt(t, name), schema.FieldActorHandle: txt(t, handle),
		"stable_key": txt(t, humanKeyPrefix+email), schema.FieldActorActive: value.BoolOf(true)}})
	return id
}

// world is a fake with the caller Sam and the people Mira and Ben.
func world(t *testing.T) (*profilestoretest.Fake, ids.ID[ids.Actor], ids.ID[ids.Actor]) {
	t.Helper()
	sam, err := identity.ActorID(humanKeyPrefix + "sam@acme-agency.com")
	if err != nil {
		t.Fatal(err)
	}
	f := profilestoretest.New(sam, testNow)
	person(t, f, "sam", "Sam Rivera", "sam@acme-agency.com")
	mira := person(t, f, "mira", "Mira Patel", "mira@acme-agency.com")
	ben := person(t, f, "ben", "Ben Okafor", "ben@acme-agency.com")
	return f, mira, ben
}

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// yes is a context carrying the caller's --yes.
func yes() context.Context { return op.WithConfirmed(context.Background(), true) }

// wantCode fails unless err carries code.
func wantCode(t *testing.T, what string, err error, code errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want %s", what, code)
	}
	if got := errs.CodeOf(err); got != code {
		t.Fatalf("%s: %s (%v), want %s", what, got, err, code)
	}
}

// live returns the live rows of table in f.
func live(f *profilestoretest.Fake, table row.Table) []row.Row {
	var out []row.Row
	for _, x := range f.All(table) {
		if _, trashed := x.Fields[trash.FieldDeletedReason]; !x.Deleted && !trashed {
			out = append(out, x)
		}
	}
	return out
}
