package profilestore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestWriteGuardRoleRule (devco D-694, owner use pass 5 wrong 1 + 2): the
// one role check of every row write, as a table of role x folder kind x
// change kind. A member may not change a company must rule in any way
// (pass 5: a body edit passed) nor any policy of company ground (pass 5:
// branch share's branches.shared passed), and adds rules only in the
// folder of a team they are on; an admin may everything; the personal
// store and another store's folder are never company ground.
func TestWriteGuardRoleRule(t *testing.T) {
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
	anyID := func(prefix string) ids.AnyID {
		id, perr := ids.ParseAny(prefix + "_" + strings.TrimPrefix(admin.String(), "act_"))
		if perr != nil {
			t.Fatal(perr)
		}
		return id
	}
	mem.Put(row.Row{Table: schema.TableTeams, ID: anyID("tem"), Fields: map[string]value.Value{
		"handle":                txt(t, "web"),
		schema.FieldTeamMembers: txt(t, member.String()),
	}})
	adm := newGroundEnv(t, admin, admin)

	rule := func(sev, body string) row.Row {
		return row.Row{Table: schema.TableRules, ID: anyID("rul"), Fields: map[string]value.Value{
			"severity": txt(t, sev), "body": txt(t, body),
		}}
	}
	must, should := rule("must", "never push"), rule("should", "prefer x")
	edited := rule("must", "changed by member")
	gone := must
	gone.Deleted = true
	pol := row.Row{Table: schema.TablePolicies, ID: anyID("pol"), Fields: map[string]value.Value{"key": txt(t, "branches.shared")}}

	changes := map[string]engine.GuardedWrite{
		"must body edit":   {Before: &must, After: edited},
		"must delete":      {Before: &must, After: gone},
		"must lowered":     {Before: &must, After: should},
		"policy set":       {After: pol},
		"policy unset":     {Before: &pol, After: func() row.Row { p := pol; p.Deleted = true; return p }()},
		"add must":         {After: must},
		"add should":       {After: should},
		"raise to must":    {Before: &should, After: must},
		"should body edit": {Before: &should, After: rule("should", "prefer y")},
	}
	type cell struct {
		src    store.Source
		change string
		member bool // the member may
	}
	var cells []cell
	for _, c := range []string{"must body edit", "must delete", "must lowered", "policy set", "policy unset"} {
		for _, src := range []store.Source{"agency/org", "agency/web", "agency"} {
			cells = append(cells, cell{src, c, false})
		}
		cells = append(cells, cell{"me", c, true}, cell{"client/app", c, true})
	}
	for _, c := range []string{"add must", "add should", "raise to must"} {
		cells = append(cells, cell{"agency/org", c, false}, cell{"agency/app", c, false}, cell{"agency/web", c, true}, cell{"me", c, true})
	}
	cells = append(cells, cell{"agency/org", "should body edit", true}, cell{"agency/web", "should body edit", true})

	for _, c := range cells {
		w := changes[c.change]
		err := profilestore.WriteGuard{Env: mem}.GuardWrite(ctx, c.src, []engine.GuardedWrite{w})
		switch {
		case c.member && err != nil:
			t.Errorf("member %s in %s: %v, want ok", c.change, c.src, err)
		case !c.member && !errs.Has(err, errs.Refused):
			t.Errorf("member %s in %s: %v, want E_REFUSED", c.change, c.src, err)
		case !c.member && !strings.Contains(hintOf(err), "ask an admin of agency (Ada Admin) to run: "):
			t.Errorf("member %s in %s: hint %q does not send them to the admin", c.change, c.src, hintOf(err))
		}
		if err := (profilestore.WriteGuard{Env: adm}).GuardWrite(ctx, c.src, []engine.GuardedWrite{w}); err != nil {
			t.Errorf("admin %s in %s: %v, want ok", c.change, c.src, err)
		}
	}

	// the command's own wording (WithAsk) names the exact line, full --to
	line := "lore branch share --branch feat/a --to agency/web --yes"
	err = profilestore.WriteGuard{Env: mem}.GuardWrite(profilestore.WithAsk(ctx, "share a branch for everyone in a company folder", line), "agency/web", []engine.GuardedWrite{changes["policy set"]})
	if !strings.HasSuffix(hintOf(err), line) {
		t.Errorf("hint %q, want it to end with %q", hintOf(err), line)
	}
}

// hintOf is the hint of a lore error, "" for none.
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// TestWriteGuardTeamsAreAdminOnly (devco D-694 rule gap): a team is a
// member's add right (a member on @web adds rules in agency/web), so in an
// org store every team change is an admin's: before the fix a member put
// themselves on @api and then added rules there. The one member change
// let through is taking up their own invite: adding only themselves to a
// team the invite names (WithInviteJoin).
func TestWriteGuardTeamsAreAdminOnly(t *testing.T) {
	ctx := context.Background()
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	mem := newGroundEnv(t, member, admin)
	adm := newGroundEnv(t, admin, admin)
	tid, err := ids.ParseAny("tem_" + strings.TrimPrefix(admin.String(), "act_"))
	if err != nil {
		t.Fatal(err)
	}
	team := func(handle string, members ...ids.ID[ids.Actor]) row.Row {
		items := make([]value.Value, 0, len(members))
		for _, m := range members {
			items = append(items, txt(t, m.String()))
		}
		l, lerr := value.ListOf(value.KindText, items...)
		if lerr != nil {
			t.Fatal(lerr)
		}
		return row.Row{Table: schema.TableTeams, ID: tid, Fields: map[string]value.Value{"handle": txt(t, handle), schema.FieldTeamMembers: l}}
	}
	api := team("api", admin)
	removed := api
	removed.Deleted = true
	renamed := team("backend", admin)
	changes := map[string]engine.GuardedWrite{
		"team add":            {After: team("side", member)},
		"member adds self":    {Before: &api, After: team("api", admin, member)},
		"member adds another": {Before: &api, After: team("api", admin, other)},
		"member removes":      {Before: &api, After: team("api")},
		"rename":              {Before: &api, After: renamed},
		"remove":              {Before: &api, After: removed},
	}
	for name, w := range changes {
		for _, src := range []store.Source{"agency/org", "agency"} {
			err := profilestore.WriteGuard{Env: mem}.GuardWrite(ctx, src, []engine.GuardedWrite{w})
			if !errs.Has(err, errs.Refused) || !strings.Contains(hintOf(err), "ask an admin of agency (Ada Admin) to run: ") {
				t.Errorf("member %s in %s: %v (hint %q), want E_REFUSED sending them to the admin", name, src, err, hintOf(err))
			}
			if err := (profilestore.WriteGuard{Env: adm}).GuardWrite(ctx, src, []engine.GuardedWrite{w}); err != nil {
				t.Errorf("admin %s in %s: %v, want ok", name, src, err)
			}
		}
		// the personal store holds no teams of the org: never company ground
		if err := (profilestore.WriteGuard{Env: mem}).GuardWrite(ctx, "me", []engine.GuardedWrite{w}); err != nil {
			t.Errorf("member %s in the personal store: %v, want ok", name, err)
		}
	}

	// the command's line (WithAsk) is the exact one an admin runs
	line := "lore team member add --handle @api --members @mo"
	err = profilestore.WriteGuard{Env: mem}.GuardWrite(profilestore.WithAsk(ctx, profilestore.TeamDoes("api"), line), "agency/org", []engine.GuardedWrite{changes["member adds self"]})
	if !strings.HasSuffix(hintOf(err), line) || !strings.Contains(err.Error(), "change the team @api") {
		t.Errorf("refusal %v hint %q, want the team named and the hint ending with %q", err, hintOf(err), line)
	}

	// taking up an invite that names @api: only the joiner, only themselves
	join := profilestore.WithInviteJoin(ctx, member.String(), []string{"api"})
	if err := (profilestore.WriteGuard{Env: mem}).GuardWrite(join, "agency/org", []engine.GuardedWrite{changes["member adds self"]}); err != nil {
		t.Errorf("invite join of a named team: %v, want ok", err)
	}
	for _, name := range []string{"member adds another", "member removes", "rename", "remove", "team add"} {
		if err := (profilestore.WriteGuard{Env: mem}).GuardWrite(join, "agency/org", []engine.GuardedWrite{changes[name]}); !errs.Has(err, errs.Refused) {
			t.Errorf("invite join doing %s: %v, want E_REFUSED", name, err)
		}
	}
	if err := (profilestore.WriteGuard{Env: mem}).GuardWrite(profilestore.WithInviteJoin(ctx, member.String(), []string{"web"}), "agency/org", []engine.GuardedWrite{changes["member adds self"]}); !errs.Has(err, errs.Refused) {
		t.Errorf("invite join of a team the invite does not name: %v, want E_REFUSED", err)
	}
}
