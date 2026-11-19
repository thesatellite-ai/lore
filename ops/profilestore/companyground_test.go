package profilestore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// groundEnv is an ops Env with the roster (profilestoretest.Fake) and the
// settings levels of its attached stores.
type groundEnv struct {
	*profilestoretest.Fake
	levels map[store.Source]policy.Level
}

func (e groundEnv) Get(context.Context, engine.Ref) (store.Source, row.Row, error) {
	return "", row.Row{}, errs.New(errs.NotFound, "no row")
}

func (e groundEnv) StoreLevel(src store.Source) (policy.Level, bool) {
	lv, ok := e.levels[src]
	return lv, ok
}

// newGroundEnv is a roster of org agency (agency/org) naming admin as its
// one admin, read by caller.
func newGroundEnv(t *testing.T, caller, admin ids.ID[ids.Actor]) groundEnv {
	t.Helper()
	f := profilestoretest.New(caller, time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC))
	f.Put(row.Row{Table: schema.TableActors, ID: admin.Any(), Fields: map[string]value.Value{
		schema.FieldActorDisplayName: txt(t, "Ada Admin"),
	}})
	pid, err := ids.New[ids.ActorPeriod]()
	if err != nil {
		t.Fatal(err)
	}
	f.Put(row.Row{Table: schema.TableActorPeriods, ID: pid.Any(), Fields: map[string]value.Value{
		schema.FieldPeriodActor:   txt(t, admin.String()),
		schema.FieldPeriodOrgRole: txt(t, profilestore.OrgRoleAdmin),
	}})
	return groundEnv{Fake: f, levels: map[store.Source]policy.Level{
		"agency/org": policy.LevelOrg,
		"agency":     policy.LevelFolder, // a project folder linked under the bare org name
		"agency/web": policy.LevelFolder,
		"client/app": policy.LevelFolder, // another store's folder
		"me":         policy.LevelPersonal,
	}}
}

// TestRequireAdminAtProjectFolder (devco D-681): a member's change to a
// project folder of the company store is refused like one to its org
// folder; the repo, the personal store and another store's folder stay
// the member's; an admin may everywhere. D-637 refused only the org
// folder, so a member demoted a project folder's must rule.
func TestRequireAdminAtProjectFolder(t *testing.T) {
	ctx := context.Background()
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	mem := newGroundEnv(t, member, admin)
	for _, src := range []store.Source{"agency/org", "agency/web", "agency"} {
		err := profilestore.RequireAdminAt(ctx, mem, src, "lower it", "lore rule edit --id rul_x --severity should")
		if !errs.Has(err, errs.Refused) {
			t.Errorf("member at %s: want E_REFUSED, got %v", src, err)
		}
	}
	for _, src := range []store.Source{store.SourceRepo, "me", "client/app"} {
		if err := profilestore.RequireAdminAt(ctx, mem, src, "lower it", "x"); err != nil {
			t.Errorf("member at %s: want pass, got %v", src, err)
		}
	}
	adm := newGroundEnv(t, admin, admin)
	for _, src := range []store.Source{"agency/org", "agency/web", "agency"} {
		if err := profilestore.RequireAdminAt(ctx, adm, src, "lower it", "x"); err != nil {
			t.Errorf("admin at %s: want pass, got %v", src, err)
		}
	}
	// the bulk/row form: a must rule of the project folder is named
	rid, err := ids.ParseAny("rul_" + strings.TrimPrefix(admin.String(), "act_")) // any UUIDv7 under the rule prefix
	if err != nil {
		t.Fatal(err)
	}
	must := row.Row{Table: schema.TableRules, ID: rid, Fields: map[string]value.Value{"severity": txt(t, schema.RuleSeverityMust)}}
	err = profilestore.RequireAdminForRows(ctx, mem, []profilestore.Placed{{Source: "agency/web", Row: must}}, "delete", "lore rule delete --id x --yes", "")
	if !errs.Has(err, errs.Refused) {
		t.Errorf("member, project folder must rule: want E_REFUSED, got %v", err)
	}
}

// TestCompanyGround: the one answer, by level and by name.
func TestCompanyGround(t *testing.T) {
	cases := []struct {
		lv   policy.Level
		src  store.Source
		org  string
		want bool
	}{
		{policy.LevelOrg, "agency/org", "agency", true},
		{policy.LevelStoreRoot, "kb", "", true},
		{policy.LevelFolder, "agency/web", "agency", true},
		{policy.LevelFolder, "agency", "agency", true},
		{policy.LevelFolder, "client/app", "agency", false},
		{policy.LevelFolder, "agency/web", "", false},
		{policy.LevelRepo, store.SourceRepo, "agency", false},
		{policy.LevelPersonal, "me", "agency", false},
		{"", "agency/web", "agency", true},
		{"", "agency/org", "", true},
		{"", "me", "agency", false},
		{"", store.SourceRepo, "agency", false},
	}
	for _, c := range cases {
		if got := profilestore.CompanyGround(c.lv, c.src, c.org); got != c.want {
			t.Errorf("CompanyGround(%q, %q, %q) = %v, want %v", c.lv, c.src, c.org, got, c.want)
		}
	}
}

// txt is a text value.
func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
