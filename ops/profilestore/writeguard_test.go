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
