package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rosterStore is the org folder a roster test writes to.
const rosterStore store.Source = "agency/org"

// rosterApp opens an App over a fresh repo with the org store's org/
// folder attached as agency/org (what the old binary passes).
func rosterApp(t *testing.T, orgURL string, client bool) *App {
	t.Helper()
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	a := openWith(t, gitRepo(t), Options{OrgStores: hook, RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
	withProject(t, a)
	return a
}

// findActor returns the roster actor with id (ok false when absent).
func findActor(t *testing.T, a *App, id string) (RosterActor, bool) {
	t.Helper()
	list, err := a.RosterActors(background(), rosterStore)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id {
			return x, true
		}
	}
	return RosterActor{}, false
}

// TestRosterRegister (F18, CFG1): the actor id is the deterministic id of
// the stable key, so registering twice (or from a second machine over the
// same store) writes one row and reports created only the first time; a
// re-register refreshes the fields; deactivate keeps the row (active
// false, left_at set) and a later register brings it back active.
func TestRosterRegister(t *testing.T) {
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	a := rosterApp(t, orgURL, false)
	key := "mailto:ana@example.com"
	want, err := identity.ActorID(key)
	if err != nil {
		t.Fatal(err)
	}
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: key, Name: "Ana", Handle: "ana", Label: "ops"}
	id, created, err := a.RosterRegister(ctx, rosterStore, sp)
	if err != nil || !created || id != want.String() {
		t.Fatalf("first register: %s %v %v, want %s created", id, created, err, want)
	}
	sp.Name = "Ana B."
	id2, created, err := a.RosterRegister(ctx, rosterStore, sp)
	if err != nil || created || id2 != id {
		t.Fatalf("second register: %s %v %v, want the same id, not created", id2, created, err)
	}
	got, ok := findActor(t, a, id)
	if !ok || got.Name != "Ana B." || got.Handle != "ana" || got.Label != "ops" || got.StableKey != key || !got.Active {
		t.Fatalf("roster row = %+v (found %v)", got, ok)
	}

	// a second machine (another repo, its own App) over the same store
	b := rosterApp(t, orgURL, false)
	if id3, created3, rerr := b.RosterRegister(ctx, rosterStore, sp); rerr != nil || created3 || id3 != id {
		t.Fatalf("register from a second machine: %s %v %v", id3, created3, rerr)
	}
	if list, lerr := b.RosterActors(ctx, rosterStore); lerr != nil || countID(list, id) != 1 {
		t.Fatalf("one person must be one row: %+v %v", list, lerr)
	}

	if derr := a.RosterDeactivate(ctx, rosterStore, id); derr != nil {
		t.Fatal(derr)
	}
	if got, ok = findActor(t, a, id); !ok || got.Active {
		t.Fatalf("after deactivate: %+v (found %v), want the row kept, inactive", got, ok)
	}
	if _, created, err = a.RosterRegister(ctx, rosterStore, sp); err != nil || created {
		t.Fatalf("re-register after leaving: %v %v", created, err)
	}
	if got, _ = findActor(t, a, id); !got.Active {
		t.Fatalf("re-registered actor not active: %+v", got)
	}
}

func countID(list []RosterActor, id string) int {
	n := 0
	for _, x := range list {
		if x.ID == id {
			n++
		}
	}
	return n
}

// TestRosterRefusals: a store not attached is E_NOT_FOUND, a client repo
// is never a roster target (E_LEAK_BLOCKED, F18 keeps people out of
// customer repos), bad ids are E_INVALID_INPUT, and deactivating an actor
// that is not on the roster is E_NOT_FOUND. Nothing is written by any.
func TestRosterRefusals(t *testing.T) {
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	a := rosterApp(t, orgURL, true)
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:bo@example.com", Name: "Bo", Handle: "bo"}
	cases := []struct {
		name string
		do   func() error
		code errs.Code
	}{
		{"unattached store", func() error { _, _, err := a.RosterRegister(ctx, "nowhere", sp); return err }, errs.NotFound},
		{"client repo", func() error { _, _, err := a.RosterRegister(ctx, store.SourceRepo, sp); return err }, errs.LeakBlocked},
		{"client repo team", func() error {
			_, _, err := a.RosterTeamPut(ctx, store.SourceRepo, "org", "ops", "", nil)
			return err
		}, errs.LeakBlocked},
		{"bad owner id", func() error {
			o := sp
			o.OwnerID = "not-an-id"
			_, _, err := a.RosterRegister(ctx, rosterStore, o)
			return err
		}, errs.InvalidInput},
		{"bad actor id to deactivate", func() error { return a.RosterDeactivate(ctx, rosterStore, "act_nope") }, errs.InvalidInput},
		{"unknown actor to deactivate", func() error {
			return a.RosterDeactivate(ctx, rosterStore, "act_0e45506e64357c94a87e70ef1c6bbb00")
		}, errs.NotFound},
		{"bad member id", func() error {
			_, _, err := a.RosterTeamPut(ctx, rosterStore, "org", "ops", "", []string{"x"})
			return err
		}, errs.InvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.do(); !errs.Has(err, c.code) {
				t.Fatalf("got %v, want %s", err, c.code)
			}
		})
	}
	if list, err := a.RosterActors(ctx, rosterStore); err != nil || len(list) > 1 {
		// only the caller's own actor row (written on open) may be there
		t.Fatalf("a refusal wrote a roster row: %+v %v", list, err)
	}
	if teams, err := a.RosterTeams(ctx, rosterStore); err != nil || len(teams) != 0 {
		t.Fatalf("a refusal wrote a team: %+v %v", teams, err)
	}
}

// TestRosterTeamPut: the first put of a handle creates the team (and the
// folder's project row when it has none), a second put of the same handle
// updates members and keeps the id, an empty name keeps the old name, and
// teams list sorted by handle.
func TestRosterTeamPut(t *testing.T) {
	ctx := background()
	a := rosterApp(t, folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal), false)
	ana, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:ana@example.com", Name: "Ana", Handle: "ana"})
	if err != nil {
		t.Fatal(err)
	}
	bot, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindAgent, StableKey: "agent:ci", Name: "CI", Handle: "ci", OwnerID: ana})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := findActor(t, a, bot); got.OwnerID != ana || got.Kind != schema.ActorKindAgent {
		t.Fatalf("agent row = %+v, want owner %s", got, ana)
	}
	id, created, err := a.RosterTeamPut(ctx, rosterStore, "org", "ops", "Operations", []string{ana})
	if err != nil || !created {
		t.Fatalf("first put: %s %v %v", id, created, err)
	}
	id2, created, err := a.RosterTeamPut(ctx, rosterStore, "org", "ops", "", []string{ana, bot})
	if err != nil || created || id2 != id {
		t.Fatalf("second put: %s %v %v, want the same id", id2, created, err)
	}
	if _, _, err = a.RosterTeamPut(ctx, rosterStore, "org", "dev", "Dev", nil); err != nil {
		t.Fatalf("a team with no members: %v", err)
	}
	teams, err := a.RosterTeams(ctx, rosterStore)
	if err != nil || len(teams) != 2 || teams[0].Handle != "dev" || teams[1].Handle != "ops" {
		t.Fatalf("teams = %+v %v, want dev then ops", teams, err)
	}
	ops := teams[1]
	if ops.Name != "Operations" || len(ops.Members) != 2 || !ops.Active {
		t.Fatalf("ops = %+v, want the old name kept and two members", ops)
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := eng.List(ctx, rosterStore, schema.TableProjects)
	if err != nil || len(projects) != 1 {
		t.Fatalf("org folder projects = %d %v, want exactly one (created once, reused)", len(projects), err)
	}
}
