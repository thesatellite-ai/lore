package schema

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestManyToManyRejectReasons (O11): each bad many-to-many relation is
// refused for ITS reason (TestRegistryRejects only checks that something
// failed, which a wrong reason would also satisfy).
func TestManyToManyRejectReasons(t *testing.T) {
	t.Parallel()
	base := Table("things", ids.PrefixTag, Text("name"))
	other := func(items ...Item) TableDef { return Table("others", ids.PrefixTeam, items...) }
	cases := []struct {
		name string
		defs []TableDef
		want string
	}{
		{"field missing", []TableDef{base, other(ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))}, "field t_ids does not exist"},
		{"single ref", []TableDef{base, other(Ref("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))}, "is not a list of refs"},
		{"text list", []TableDef{base, other(List("t_ids", value.KindText), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))}, "is not a list of refs"},
		{"ref table differs", []TableDef{base, other(RefList("t_ids", "others"), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))}, "refers to others, not things"},
		{"untyped ref list", []TableDef{base, other(RefList("t_ids", ""), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))}, "not things"},
		{"join is a table", []TableDef{base, other(RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "things", Cascade))}, "join table things is also a table"},
		{"join shared in one table", []TableDef{base, other(RefList("a_ids", "things"), RefList("b_ids", "things"),
			ManyToManyRel("a", "a_ids", "things", "o_t", Cascade), ManyToManyRel("b", "b_ids", "things", "o_t", Cascade))}, "is also used by others."},
		{"join shared across tables", []TableDef{base,
			other(RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "shared_join", Cascade)),
			Table("thirds", ids.PrefixMemory, RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "shared_join", Cascade))}, "is also used by"},
		{"join name invalid", []TableDef{base, other(RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "Bad-Join", Cascade))}, "invalid join table name"},
		{"join name empty", []TableDef{base, other(RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "", Cascade))}, "invalid join table name"},
	}
	for _, c := range cases {
		_, err := NewRegistry(c.defs...)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a problem containing %q", c.name, err, c.want)
		}
	}
	// Every on-delete rule is accepted on a well-formed relation.
	for _, od := range OnDeletes() {
		if _, err := NewRegistry(base, other(RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "o_t", od))); err != nil {
			t.Errorf("on delete %s: %v", od, err)
		}
	}
}

// TestBuiltinTagIDsAlwaysHaveTheirRelation (O11): every built-in table
// carrying tag_ids has exactly one tags many-to-many over it, its join
// table is used by no other relation and is not a table; no table has the
// relation without the field.
func TestBuiltinTagIDsAlwaysHaveTheirRelation(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	joins := map[row.Table]row.Table{}
	carriers := 0
	for _, def := range r.Tables() {
		f, has := def.Field(FieldTagIDs)
		var rels []Relation
		for _, rel := range def.Relations {
			if rel.Kind == ManyToMany && rel.Field == FieldTagIDs {
				rels = append(rels, rel)
			}
		}
		if !has {
			if len(rels) != 0 {
				t.Errorf("%s: tags relation without a %s field", def.Name, FieldTagIDs)
			}
			continue
		}
		carriers++
		if f.Kind != value.KindList || f.Elem != value.KindRef || f.RefTable != TableTags {
			t.Errorf("%s.%s is not a list of refs to tags: %+v", def.Name, FieldTagIDs, f)
		}
		if len(rels) != 1 || rels[0].Target != TableTags || rels[0].Name != RelTags {
			t.Errorf("%s: %d tags relations over %s, want one to tags", def.Name, len(rels), FieldTagIDs)
			continue
		}
		if prev, dup := joins[rels[0].JoinTable]; dup {
			t.Errorf("join %s used by %s and %s", rels[0].JoinTable, prev, def.Name)
		}
		joins[rels[0].JoinTable] = def.Name
		if _, clash := r.Table(rels[0].JoinTable); clash {
			t.Errorf("join %s is a registered table", rels[0].JoinTable)
		}
	}
	if carriers < 4 {
		t.Fatalf("only %d tables carry tag_ids; actors, projects, repos and memories must", carriers)
	}
}
