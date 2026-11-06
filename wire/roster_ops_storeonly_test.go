package wire

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// TestActorRegisterOutsideARepoWithoutHost (BUG-T3 on cmd/lore): an App
// with no roster host (Options.Roster nil, as cmd/lore builds it) run
// outside any git work tree registers the caller and an agent it owns,
// and deactivates the caller, in the org folder store through a store-only
// App; nothing is written into the folder it ran in, and a second run is
// a refresh (Created false). Before rosterApp it failed E_NOT_A_STORE.
func TestActorRegisterOutsideARepoWithoutHost(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1") // the run may sit in a coding agent: register as the person
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	plain := t.TempDir()
	opts := Options{RepoDir: plain, Home: t.TempDir(), FlagActor: testEmail, OrgStores: hook, Wall: time.Now,
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
		}}
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	me, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{})
	if err != nil || !me.Created || me.Handle != "alice" {
		t.Fatalf("register outside a repo: %+v %v", me, err)
	}
	again, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{})
	if err != nil || again.Created || again.ID != me.ID {
		t.Fatalf("second register: %+v %v, want a refresh of %s", again, err, me.ID)
	}
	bot, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: "agent-frontend", Owner: "@alice"})
	if err != nil || !bot.Created {
		t.Fatalf("agent register outside a repo: %+v %v", bot, err)
	}
	if _, err = a.ActorDeactivate(ctx, "@alice"); err != nil {
		t.Fatalf("deactivate outside a repo: %v", err)
	}

	check, ok, err := StoreOnlyRoster(ctx, opts, rosterStore)
	if err != nil || !ok {
		t.Fatalf("reopen: %v %v", ok, err)
	}
	t.Cleanup(func() { _ = check.Close() })
	if got, found := findActor(t, check, me.ID); !found || got.Handle != "alice" || got.Active {
		t.Fatalf("roster read back: %+v %v, want @alice deactivated", got, found)
	}
	if got, found := findActor(t, check, bot.ID); !found || !got.Active || got.OwnerID != me.ID {
		t.Fatalf("agent read back: %+v %v, want active, owned by %s", got, found, me.ID)
	}
	if left, rerr := os.ReadDir(plain); rerr != nil || len(left) != 0 {
		t.Fatalf("the folder it ran in got %d entries (%v)", len(left), rerr)
	}
}
