package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestProfileResolveSyncsOnMiss: two clones of one git org store. Clone A
// registers @mira and pushes; clone B, whose copy of the store predates
// that push, still resolves @mira for a profile op, because a reference
// that misses on the org roster syncs the store once and looks again
// (profileRoster.ResolveActor, as rosterFind does for the roster verbs:
// BUG-T6). An unknown handle stays E_NOT_FOUND after the retry.
func TestProfileResolveSyncsOnMiss(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, dir := bareStore(t, hosts, "agency/notes")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"notes","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init the org store: %v", err)
	}
	clone := func() *App {
		a := openWith(t, gitRepo(t), Options{
			OrgStores: func(context.Context) ([]attach.Store, error) {
				return []attach.Store{{Name: rosterStore, URL: url, Folder: "org"}}, nil
			},
			Org: func(context.Context) (*identity.Org, error) {
				return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
			},
			RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		})
		withProject(t, a)
		return a
	}
	ctx := background()
	b := clone()
	resolve := func(ref string) error {
		t.Helper()
		r, done, err := b.Roster(ctx, false)
		defer done()
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.ResolveActor(ctx, ref)
		return err
	}
	if err := resolve("@mira"); !errs.Has(err, errs.NotFound) { // B's copy of the store is now open, before the push
		t.Fatalf("before the register: want E_NOT_FOUND, got %v", err)
	}

	a := clone()
	sp := RosterActor{Kind: schema.ActorKindHuman, StableKey: "human:mira@acme.com", Name: "Mira Patel", Handle: "mira"}
	id, _, err := a.RosterRegister(ctx, rosterStore, sp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Sync(ctx, rosterStore); err != nil {
		t.Fatalf("clone A push: %v", err)
	}
	if tree := gitIn(t, dir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, id) {
		t.Fatalf("fixture: clone A's register did not reach the remote:\n%s", tree)
	}
	if _, found := findActor(t, b, id); found {
		t.Fatal("fixture: clone B already sees @mira without a sync")
	}

	if err = resolve("@mira"); err != nil {
		t.Fatalf("clone B after A's push: %v", err)
	}
	if err = resolve("@nobody"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("an unknown handle after the retry: want E_NOT_FOUND, got %v", err)
	}
}
