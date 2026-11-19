package renderhost

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// TestOverCoreWarnsOnlyWhoCanFix guards devco D-725: every sync warned
// every member of a company folder that its pinned rules were over
// context.budget.internal, though only an admin may lower those rows or
// raise that budget. The warning (and --report's fix) goes to whoever can
// bring the pinned part under the budget.
func TestOverCoreWarnsOnlyWhoCanFix(t *testing.T) {
	pins := []privatePin{
		{id: "rul_co", kind: privatePinRule, text: strings.Repeat("r", 39000)},
		{id: "rul_mine", kind: privatePinRule, text: strings.Repeat("m", 500)},
	}
	adm := profilestore.Admins{Org: "acme", Names: []string{"Ada Admin"}}
	member := budgetFix{to: "acme/sync", raiseCompany: true, company: map[string]bool{"rul_co": true}, admins: adm}
	p := &privatePlan{budget: 32000, coreBytes: 39500, pins: pins, fix: member}
	if p.overCoreWarns() {
		t.Errorf("devco:D-725: a member who may change neither the company rows nor the budget is warned:\n%s", privateOverCoreWarn(p))
	}
	admin := member
	admin.admins.Caller = true
	p.fix = admin
	if !p.overCoreWarns() {
		t.Error("devco:D-725: the admin of the folder is not warned")
	}
	// a member whose own pinned rows are enough to fit is warned: they can act
	p.fix, p.coreBytes, p.budget = member, 32400, 32000
	if !p.overCoreWarns() {
		t.Error("devco:D-725: a member who can fit by lowering their own row is not warned")
	}
	// under the budget nobody is warned
	p.fix, p.coreBytes = admin, 1000
	if p.overCoreWarns() {
		t.Error("warned under the budget")
	}
}

// TestInternalBudgetDefaultFitsARealFolder guards devco D-725's default:
// 8000 bytes sat under the pinned part of most real 300-row folders
// (17-23 KB), so every sync warned. The default and the render's
// fallback are one number that holds such a folder.
func TestInternalBudgetDefaultFitsARealFolder(t *testing.T) {
	n, ok := policy.Defaults().Limit(policy.KeyContextBudgetInternal)
	if !ok || n < 32000 {
		t.Fatalf("devco:D-725: context.budget.internal defaults to %d, under a real folder's pinned part", n)
	}
	h := &Host{}
	if got := h.loadInternalBudget(t.Context()); int64(got) != n {
		t.Fatalf("the render's fallback budget %d is not the policy default %d", got, n)
	}
}
