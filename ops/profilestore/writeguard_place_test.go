package profilestore_test

import (
	"context"
	"strings"
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

// placedEnv is a groundEnv as wire.App sees one machine: the folder web
// of the store agency linked into a repo is attached under the bare name
// agency, a client store's folder app under the bare name client
// (StoreLabel), a store clone's repo source stands for the folder it is
// in (RepoStandsFor), and the client's own roster opens for its folder
// (RosterOfOrg).
type placedEnv struct {
	groundEnv
	here store.Source
}

func (placedEnv) StoreLabel(name store.Source) string {
	switch name {
	case "agency":
		return "agency/web"
	case "client":
		return "client/app"
	}
	return string(name)
}

func (e placedEnv) RepoStandsFor(context.Context) (store.Source, error) { return e.here, nil }

func (e placedEnv) RosterOfOrg(ctx context.Context, org string) (profilestore.Roster, func(), bool, error) {
	if org != "client" {
		return nil, func() {}, false, nil
	}
	r, done, err := e.Roster(ctx, false)
	if err != nil {
		return nil, done, false, err
	}
	return clientRoster{r}, done, true, nil
}

// clientRoster is the same roster rows read as the client org's roster.
type clientRoster struct{ profilestore.Roster }

func (clientRoster) Source() store.Source { return "client/org" }

// TestWriteGuardJudgesTheLinkedFolder (devco D-694, owner use pass 5): a
// member on the team @web adds a must rule to the folder web in every way
// they reach it: from inside the store clone's folder (repo stands for
// agency/web), at the store top or from home with --to agency/web, from a
// repo linked to agency/web (attached as "agency") and from a client repo
// linked to client/app (attached as "client"). The guard cut the folder
// from the attached name, so in a linked repo it found no folder, matched
// no team and refused the member on the folder's team ("company folder
// agency"). A member not on the team is refused naming the full folder;
// an admin passes everywhere.
func TestWriteGuardJudgesTheLinkedFolder(t *testing.T) {
	ctx := context.Background()
	onTeam, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	offTeam, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	ruleID, err := ids.ParseAny("rul_" + strings.TrimPrefix(admin.String(), "act_"))
	if err != nil {
		t.Fatal(err)
	}
	teamFor := func(handle string) row.Row {
		id, perr := ids.ParseAny("tem_" + strings.TrimPrefix(onTeam.String(), "act_"))
		if handle == "app" {
			id, perr = ids.ParseAny("tem_" + strings.TrimPrefix(offTeam.String(), "act_"))
		}
		if perr != nil {
			t.Fatal(perr)
		}
		return row.Row{Table: schema.TableTeams, ID: id, Fields: map[string]value.Value{
			"handle": txt(t, "@"+handle), schema.FieldTeamMembers: txt(t, onTeam.String()),
		}}
	}
	env := func(caller ids.ID[ids.Actor], here store.Source) placedEnv {
		g := newGroundEnv(t, caller, admin)
		g.levels["client"] = policy.LevelFolder
		g.Put(teamFor("web"))
		g.Put(teamFor("app"))
		return placedEnv{groundEnv: g, here: here}
	}
	add := []engine.GuardedWrite{{After: row.Row{Table: schema.TableRules, ID: ruleID, Fields: map[string]value.Value{
		"severity": txt(t, "must"), "body": txt(t, "never push"),
	}}}}

	for _, p := range []struct {
		name   string
		target store.Source
		here   store.Source // the folder a store clone's repo source stands for
		folder string       // the full folder the refusal names
	}{
		{"store folder", store.SourceRepo, "agency/web", "agency/web"},
		{"store top", "agency/web", "", "agency/web"},
		{"home --to", "agency/web", "", "agency/web"},
		{"linked own", "agency", "", "agency/web"},
		{"linked client", "client", "", "client/app"},
	} {
		if err := (profilestore.WriteGuard{Env: env(onTeam, p.here)}).GuardWrite(ctx, p.target, add); err != nil {
			t.Errorf("%s: member on the folder's team: %v, want ok", p.name, err)
		}
		err := profilestore.WriteGuard{Env: env(offTeam, p.here)}.GuardWrite(ctx, p.target, add)
		if !errs.Has(err, errs.Refused) {
			t.Errorf("%s: member not on the team: %v, want E_REFUSED", p.name, err)
		} else if !strings.Contains(err.Error(), "company folder "+p.folder+" ") {
			t.Errorf("%s: refusal %q does not name the company folder %s", p.name, err, p.folder)
		}
		if err := (profilestore.WriteGuard{Env: env(admin, p.here)}).GuardWrite(ctx, p.target, add); err != nil {
			t.Errorf("%s: admin: %v, want ok", p.name, err)
		}
	}
}
