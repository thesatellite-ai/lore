package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// TestStoreOnlyRosterOutsideARepo pins BUG-T3 / FA-AGENT-9: outside any
// git work tree a roster command gets a store-only App that registers an
// actor and adds a team in the org store folder, writes nothing into the
// folder it ran in, and refuses a repo command (memory add) with
// E_NOT_A_STORE; inside a work tree StoreOnlyRoster declines, so the
// normal App (and its repo) is used.
func TestStoreOnlyRosterOutsideARepo(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	plain := t.TempDir()
	// Wall is set as every host sets it; StoreOnlyRoster without one is
	// PATCH g-orgmig -> g-core (it hands openStoreOnly the raw opts).
	opts := Options{RepoDir: plain, Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook, Wall: time.Now}

	if _, ok, err := StoreOnlyRoster(ctx, Options{RepoDir: gitRepo(t), Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook}, rosterStore); err != nil || ok {
		t.Fatalf("inside a work tree: ok %v err %v, want the normal App", ok, err)
	}

	a, ok, err := StoreOnlyRoster(ctx, opts, rosterStore)
	if err != nil || !ok {
		t.Fatalf("outside a repo: ok %v err %v", ok, err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id, created, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:ana@example.com", Name: "Ana", Handle: "ana"})
	if err != nil || !created {
		t.Fatalf("register outside a repo: %s %v %v", id, created, err)
	}
	if _, tcreated, terr := a.RosterTeamPut(ctx, rosterStore, "org", "frontend", "Frontend", []string{id}); terr != nil || !tcreated {
		t.Fatalf("team add outside a repo: %v %v", tcreated, terr)
	}
	if got, found := findActor(t, a, id); !found || got.Handle != "ana" {
		t.Fatalf("roster read back: %+v %v", got, found)
	}
	teams, err := a.RosterTeams(ctx, rosterStore)
	if err != nil || len(teams) != 1 || teams[0].Handle != "frontend" || len(teams[0].Members) != 1 || teams[0].Members[0] != id {
		t.Fatalf("teams: %+v %v", teams, err)
	}

	e, ok := a.Registry().Lookup("memory.add")
	if !ok {
		t.Fatal("no memory.add")
	}
	if _, err = a.Run(ctx, e, op.JSONDecoder{Body: []byte(`{"body":"a note with no repo"}`)}, op.Caller{Yes: true}); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("memory add outside a repo: %v, want E_NOT_A_STORE", err)
	}
	if _, err = a.Project(ctx); err == nil {
		t.Fatal("Project outside a repo answered") // no repo store is attached: fail closed
	}
	entries, err := os.ReadDir(plain)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the folder it ran in got files: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(plain, rowfs.LoreDir)); !os.IsNotExist(err) {
		t.Fatalf(".lore made outside a repo: %v", err)
	}
}
