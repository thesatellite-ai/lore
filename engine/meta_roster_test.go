package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// rosterMetaIn is a roster op's input: a handle and the row meta flags.
type rosterMetaIn struct {
	Handle string `json:"handle" flag:"handle" desc:"h"`
	RowMetaFlags
}

// rosterMetaOut is its empty output.
type rosterMetaOut struct{}

// TestRosterRowsTakeMeta pins owner org-setup item 3 (devco D-862):
// people and teams accept metadata, a direct roster op's --meta binds to
// its own table, and BindInputMeta refuses flags no write took
// (E_META_TABLE) as Run does, so a value is never dropped silently.
func TestRosterRowsTakeMeta(t *testing.T) {
	t.Parallel()
	for _, tb := range []row.Table{schema.TableActors, schema.TableTeams} {
		if !AcceptsMeta(tb) {
			t.Errorf("%s does not accept metadata", tb)
		}
	}
	for name, want := range map[string]string{"team.add": schema.TableTeams, "actor.edit": schema.TableActors} {
		if got := opTable(name); string(got) != want {
			t.Errorf("opTable(%s) = %q, want %q", name, got, want)
		}
	}
	e := op.Define(op.Spec{Name: "team.add", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite},
		func(context.Context, rosterMetaIn) (rosterMetaOut, error) { return rosterMetaOut{}, nil })
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"handle":"@web","meta":["emoji=x"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, check, err := BindInputMeta(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !MetaBound(ctx) {
		t.Fatal("--meta of a direct op is not bound to its write")
	}
	if err = check(); !errs.Has(err, errs.MetaTable) {
		t.Fatalf("unused --meta: %v, want E_META_TABLE", err)
	}
	none, err := e.Decode(op.JSONDecoder{Body: []byte(`{"handle":"@web"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, check, err = BindInputMeta(context.Background(), none); err != nil || check() != nil {
		t.Fatalf("no --meta: %v / %v, want nothing bound and no refusal", err, check())
	}
}
