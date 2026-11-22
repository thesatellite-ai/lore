package engine

import (
	"context"
	"slices"
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

// TestMetaRulesSkipActors pins devco D-885 (hunt H-144): a create rule
// (required_on, default_on) cannot name actors, and one an older lore
// stored is inert there: a person's actor row is created without the key,
// while the same rule still refuses a memory without it.
func TestMetaRulesSkipActors(t *testing.T) {
	t.Parallel()
	if AcceptsMetaRules(schema.TableActors) || !AcceptsMetaRules(schema.TableTeams) || !AcceptsMetaRules(schema.TableMemories) {
		t.Fatal("AcceptsMetaRules: actors refused, teams and memories accepted")
	}
	if got := MetaRuleTables(); slices.Contains(got, string(schema.TableActors)) || !slices.Contains(got, string(schema.TableTeams)) || !slices.IsSorted(got) {
		t.Fatalf("MetaRuleTables = %v: sorted, teams in, actors out", got)
	}
	p := &metaPlan{reg: []keyDef{{name: "desk", requiredOn: []string{string(schema.TableActors), string(schema.TableMemories)}}}}
	if err := p.createRules(context.Background(), string(schema.TableActors)); err != nil {
		t.Fatalf("an actor create under required_on actors: %v, want no refusal", err)
	}
	if err := p.createRules(context.Background(), string(schema.TableMemories)); !errs.Has(err, errs.MetaRequired) {
		t.Fatalf("a memory create under required_on memories: %v, want E_META_REQUIRED", err)
	}
}
