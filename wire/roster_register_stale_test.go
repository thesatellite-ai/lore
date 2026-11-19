package wire

import (
	"context"
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestRegisterRefusesLeaverOnStaleCopy (devco D-277): two clones of one
// git org store. Dana registers from her clone; an admin's clone marks her
// left and pushes. Dana's copy of the store was fetched a moment before,
// so the fetch before a write is not due and her local roster still shows
// her active: `lore actor register` must still refuse with E_STATUS_CHANGE
// (it fetches the roster itself), and with the store's remote gone it
// refuses with E_OFFLINE rather than deciding on the old copy.
func TestRegisterRefusesLeaverOnStaleCopy(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, dir := bareStore(t, hosts, "agency/notes")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"notes","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init the org store: %v", err)
	}
	const danaKey = "human:dana@example.com"
	clone := func(key, handle string) *App {
		a := openWith(t, gitRepo(t), Options{
			FlagActor: key,
			OrgStores: func(context.Context) ([]attach.Store, error) {
				return []attach.Store{{Name: rosterStore, URL: url, Folder: "org"}}, nil
			},
			Org: func(context.Context) (*identity.Org, error) {
				return &identity.Org{Name: "agency", Identity: identity.Entry{Key: key, Name: "Dana", Handle: handle}}, nil
			},
			RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		})
		withProject(t, a)
		return a
	}
	ctx := background()
	dana := clone(danaKey, "dana")
	me, err := dana.ActorRegister(ctx, opsinfra.ActorRegisterIn{})
	if err != nil {
		t.Fatalf("Dana's first register: %v", err)
	}
	if _, err = dana.Sync(ctx, rosterStore); err != nil {
		t.Fatalf("Dana's push: %v", err)
	}

	// The admin marks her left (what `lore actor offboard` leaves on her
	// row) and pushes.
	admin := clone(testEmail, "lead")
	if _, err = admin.Sync(ctx, rosterStore); err != nil {
		t.Fatalf("the admin's fetch: %v", err)
	}
	aid, err := ids.ParseAny(me.ID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := admin.writeStateFor(ctx, rosterStore)
	if err != nil {
		t.Fatal(err)
	}
	left, err := value.TextOf(schema.ActorStatusLeft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.Update(ctx, engine.Ref{Store: rosterStore, Table: schema.TableActors, ID: aid}, map[string]value.Value{schema.FieldActorStatus: left}); err != nil {
		t.Fatalf("mark Dana left: %v", err)
	}
	if _, err = admin.Sync(ctx, rosterStore); err != nil {
		t.Fatalf("the admin's push: %v", err)
	}
	if got, found := findActor(t, dana, me.ID); !found || got.Status == schema.ActorStatusLeft {
		t.Fatalf("fixture: Dana's copy already shows the offboard (%+v %v); the test needs a stale copy", got, found)
	}

	if _, err = dana.ActorRegister(ctx, opsinfra.ActorRegisterIn{}); !errs.Has(err, errs.StatusChange) {
		t.Fatalf("register after being offboarded, on a stale copy: want E_STATUS_CHANGE, got %v", err)
	}

	// The remote is unreachable: no guess from the local copy.
	if err = os.Rename(dir, dir+".gone"); err != nil {
		t.Fatal(err)
	}
	if _, err = dana.ActorRegister(ctx, opsinfra.ActorRegisterIn{}); !errs.Has(err, errs.Offline) {
		t.Fatalf("register with the store's remote unreachable: want E_OFFLINE, got %v", err)
	}
}
