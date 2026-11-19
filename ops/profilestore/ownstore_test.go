package profilestore_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// orgsEnv is a groundEnv that also names the configured org listing each
// attached store (profilestore.StoreOrger); a store not in orgs is one
// the Env cannot place.
type orgsEnv struct {
	groundEnv
	orgs map[store.Source]string
}

func (e orgsEnv) StoreOrg(name store.Source) (string, bool) {
	org, ok := e.orgs[name]
	return org, ok
}

// TestWriteGuardOwnStoreIsNotCompanyGround (devco D-694): a store a member
// made for their own notes (`lore store init --name notes` at any url, no
// configured org lists it) is judged like the personal store: their note
// and policy there pass. A store root an org lists, or one the Env cannot
// place, stays company ground and the member is refused. Before the fix
// every store attached at its root was company ground of the repo's org,
// so a member's note in their own store was refused by the company roster.
func TestWriteGuardOwnStoreIsNotCompanyGround(t *testing.T) {
	ctx := context.Background()
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	g := newGroundEnv(t, member, admin)
	for _, s := range []store.Source{"notes", "teamroot", "unplaced"} {
		g.levels[s] = policy.LevelStoreRoot
	}
	env := orgsEnv{groundEnv: g, orgs: map[store.Source]string{"notes": "", "teamroot": "agency"}}
	memID, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	polID, err := ids.New[ids.Policy]()
	if err != nil {
		t.Fatal(err)
	}
	note := engine.GuardedWrite{After: row.Row{Table: schema.TableMemories, ID: memID.Any(), Fields: map[string]value.Value{"body": txt(t, "run the setup script first")}}}
	pol := engine.GuardedWrite{After: row.Row{Table: schema.TablePolicies, ID: polID.Any(), Fields: map[string]value.Value{"key": txt(t, "branches.shared")}}}
	for _, c := range []struct {
		env    profilestore.PlaceEnv
		src    store.Source
		member bool
	}{
		{env, "notes", true},     // their own store: no org lists it
		{env, "teamroot", false}, // a store root the org lists
		{env, "unplaced", false}, // the Env cannot tell: fail closed
		{g, "notes", false},      // no StoreOrger: company ground as before
	} {
		for name, w := range map[string]engine.GuardedWrite{"note add": note, "policy set": pol} {
			err := profilestore.WriteGuard{Env: c.env}.GuardWrite(ctx, c.src, []engine.GuardedWrite{w})
			switch {
			case c.member && err != nil:
				t.Errorf("member %s in %s: %v, want ok", name, c.src, err)
			case !c.member && !errs.Has(err, errs.Refused):
				t.Errorf("member %s in %s: %v, want E_REFUSED", name, c.src, err)
			}
		}
	}
}
