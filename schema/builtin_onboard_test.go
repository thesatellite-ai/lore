package schema

import (
	"slices"
	"testing"
)

// TestOnboardTables: invites is Synced + NeverClient with no natural key
// (an email may hold a revoked and a pending invite, ONB 4.5);
// setup_progress and known_repos are MachineLocal with their natural keys
// first (3.4 (setup, step); Q11 root).
func TestOnboardTables(t *testing.T) {
	defs := map[string]TableDef{}
	for _, d := range Builtin() {
		defs[d.Name.String()] = d
	}
	inv, ok := defs[TableInvites]
	if !ok || !inv.Synced || !inv.NeverClient || inv.MachineLocal {
		t.Fatalf("invites: %+v", inv)
	}
	if slices.ContainsFunc(inv.Indexes, func(ix Index) bool { return ix.Unique }) {
		t.Error("invites must have no unique index")
	}
	if f, _ := inv.Field(FieldInviteStatus); !slices.Equal(f.Values, InviteStatuses()) || !f.Required {
		t.Errorf("invites.status: %+v", f)
	}
	if f, _ := inv.Field(FieldInvitePeriodType); !slices.Equal(f.Values, PeriodTypes()) {
		t.Errorf("invites.period_type: %+v", f)
	}
	if !slices.Contains(NeverClientTables(), TableInvites) {
		t.Error("NeverClientTables lacks invites")
	}
	keys := map[string][]string{
		TableSetupProgress: {FieldProgressSetup, FieldProgressStep},
		TableKnownRepos:    {FieldKnownRepoRoot},
	}
	for name, want := range keys {
		d := defs[name]
		if !d.MachineLocal || d.NeverClient { // MachineLocal tables are stored rows (Synced) of the machine store
			t.Errorf("%s: want MachineLocal only: %+v", name, d)
		}
		i := slices.IndexFunc(d.Indexes, func(ix Index) bool { return ix.Unique })
		if i < 0 || !slices.Equal(d.Indexes[i].Fields, want) {
			t.Errorf("%s natural key: %v want %v", name, d.Indexes, want)
		}
	}
}
