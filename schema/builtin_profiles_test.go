package schema

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
)

// TestProfileTables: exactly the five profile tables are NeverClient and
// Synced (TS-3), each table's FIRST unique index is the natural key of
// TEAM_PROFILES 2 (TS-5: the engine reads only the first), and every new
// actor field is Internal and not Sensitive (2.1, TP-6, TP-7).
func TestProfileTables(t *testing.T) {
	var never []string
	defs := map[string]TableDef{}
	for _, d := range Builtin() {
		defs[d.Name.String()] = d
		if d.NeverClient {
			never = append(never, d.Name.String())
		}
	}
	if !slices.Equal(never, NeverClientTables()) {
		t.Fatalf("NeverClient tables %v want %v", never, NeverClientTables())
	}
	keys := map[string][]string{
		TableActorContacts:  {FieldContactActor, FieldContactType, FieldContactValueNorm},
		TableContactTypes:   {FieldContactTypeName},
		TableRoles:          {FieldRoleName},
		TableActorPeriods:   {FieldPeriodActor, FieldPeriodStartedOn},
		TableProjectMembers: {FieldMemberProject, FieldMemberActor, FieldMemberStartedOn},
	}
	for name, want := range keys {
		d := defs[name]
		if !d.Synced {
			t.Errorf("%s is not synced", name)
		}
		i := slices.IndexFunc(d.Indexes, func(ix Index) bool { return ix.Unique })
		if i < 0 || !slices.Equal(d.Indexes[i].Fields, want) {
			t.Errorf("%s natural key: got %v want %v", name, d.Indexes, want)
		}
	}
	members := defs[TableProjectMembers]
	for _, r := range members.Relations {
		if r.Field == FieldMemberProject && !r.AllowDangling {
			t.Error("project_members.project_id must allow a dangling project (store-only roster App)")
		}
	}
	if f, ok := members.Field(FieldMemberRoles); !ok || f.Merge != MergeSetUnion || !f.Required {
		t.Errorf("project_members.roles: %+v", f)
	}
	actors := defs[TableActors]
	for _, name := range ProfileActorFields() {
		f, ok := actors.Field(name)
		if !ok || !f.Internal || f.Sensitive {
			t.Errorf("actors.%s: %+v (want Internal, not Sensitive)", name, f)
		}
	}
	if f, _ := actors.Field(FieldActorStatus); !slices.Equal(f.Values, ActorStatuses()) {
		t.Errorf("actors.status values %v", f.Values)
	}
}

// TestNeverClientPinnedInLeakcheck: the commit check (logic/leakcheck)
// repeats the NeverClient table names because it stays free of the
// registry; a new NeverClient table must be added there too (TEAM_PROFILES
// 7 layer 4).
func TestNeverClientPinnedInLeakcheck(t *testing.T) {
	var never []string
	for _, d := range Builtin() {
		if d.NeverClient {
			never = append(never, d.Name.String())
		}
	}
	got := leakcheck.ProfileTables()
	slices.Sort(never)
	slices.Sort(got)
	if !slices.Equal(never, got) {
		t.Fatalf("leakcheck.ProfileTables %v drifted from the registry's NeverClient tables %v", got, never)
	}
}
