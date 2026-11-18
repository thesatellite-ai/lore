package renderhost

// render_budgetfix.go — the fixes the private context's over-budget
// warning offers, as commands that work where they are printed and only
// to someone who may run them (devco D-636, D-637).
//
//	pinned row of a company place (org folder, store root) ─┐
//	budget set at a company place                           ├─▶ the caller is an admin (or the org
//	                                                        │   names none yet) ─▶ the command
//	                                                        └─▶ a member ─▶ "ask an admin of <org>
//	                                                                         (<names>) to …"
//	the raise ─▶ `lore policy set … --to <place> --yes` where the value is set
//	             (a linked repo: its store folder, never the read-only repo)

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// budgetFix is where the raise goes and who may run each fix.
type budgetFix struct {
	// to is the --to of the raise; "" writes to the repo (the default).
	to string
	// raiseCompany is true when the raise goes to a company place.
	raiseCompany bool
	// unplaced is true when the place the budget is set cannot be named
	// here (a linked repo whose store folder is not attached): no raise
	// command is printed then, only `lore whoami` to find it (devco D-658).
	unplaced bool
	// company holds the ids of pinned rows kept in a company place.
	company map[string]bool
	// admins is the org's admins and whether the caller is one; its zero
	// value (no roster) refuses nobody.
	admins profilestore.Admins
}

// mayLower reports whether the caller may take pinned row id out of the
// pinned part (a member may not for a company row).
func (f budgetFix) mayLower(id string) bool { return !f.company[id] || f.admins.May() }

// mayRaise reports whether the caller may raise the budget where it is set.
func (f budgetFix) mayRaise() bool { return !f.raiseCompany || f.admins.May() }

// unplacedText is the raise when its place cannot be named: a command
// that works anywhere (`lore whoami`) and the key and value to write.
func (f budgetFix) unplacedText(coreBytes int) string {
	return fmt.Sprintf("raise %s to %d in the store folder `lore whoami` names for this repo", policy.KeyContextBudgetInternal, budgetToFit(coreBytes))
}

// raiseCmd is the policy set command that raises the budget to fit
// coreBytes, at the place it is set.
func (f budgetFix) raiseCmd(coreBytes int) string {
	c := fmt.Sprintf("lore policy set --key %s --value %d", policy.KeyContextBudgetInternal, budgetToFit(coreBytes))
	if f.to != "" {
		c += " --to " + f.to
	}
	return c + " --yes"
}

// raiseText is the left-out line's way to raise the budget: the command
// for whoever may run it, else the admin to ask.
func (f budgetFix) raiseText(coreBytes int) string {
	if f.unplaced {
		return "to raise it, " + f.unplacedText(coreBytes)
	}
	if f.mayRaise() {
		return "to raise it: `" + f.raiseCmd(coreBytes) + "`"
	}
	return "to raise it, ask " + f.admins.Who()
}

// planBudgetFix reads where the budget is set and, when a company place
// is involved, the org's admins. Only called when the pinned part is over
// the budget, so a render under budget never opens the roster.
func (h *Host) planBudgetFix(ctx context.Context, bodies []privateBody) budgetFix {
	var f budgetFix
	if h.app == nil {
		return f
	}
	var placed bool
	f.to, f.raiseCompany, placed = h.app.PolicyPlace(ctx, policy.KeyContextBudgetInternal)
	f.unplaced = !placed
	f.company = map[string]bool{}
	for _, b := range bodies {
		for _, pin := range b.pins {
			if lv, ok := h.app.StoreLevel(pin.src); ok && profilestore.CompanyLevel(lv) {
				f.company[pin.id] = true
			}
		}
	}
	if f.raiseCompany || len(f.company) > 0 {
		f.admins, _ = h.app.OrgAdmins(ctx)
	}
	return f
}

// overCoreFix names the fixes as commands: take a row out of the pinned
// part (a lower severity: the largest pinned rule and the largest pinned
// hotfix, the rows the warning lists first), or raise
// context.budget.internal to fit what is pinned now, where it is set.
// A fix the caller may not make (a company row or a company budget for a
// member) is not shown as a command: the line names the admins to ask.
func overCoreFix(coreBytes int, pins []privatePin, f budgetFix) string {
	rule, hotfix := "<rul_id>", ""
	for _, pin := range pinsBySize(pins) {
		switch {
		case pin.kind == privatePinRule && rule == "<rul_id>":
			rule = pin.id
		case pin.kind == privatePinHotfix && hotfix == "":
			hotfix = pin.id
		}
	}
	var lower, ask []string
	if f.mayLower(rule) {
		lower = append(lower, "`lore rule edit --id "+rule+" --severity should`")
	} else {
		ask = append(ask, "lower "+rule)
	}
	if hotfix != "" {
		if f.mayLower(hotfix) {
			lower = append(lower, "`lore hotfix edit --id "+hotfix+" --severity medium`")
		} else {
			ask = append(ask, "lower "+hotfix)
		}
	}
	var can []string
	if len(lower) > 0 {
		can = append(can, "take a row out of the pinned part with "+strings.Join(lower, " or "))
	}
	switch {
	case f.unplaced:
		can = append(can, f.unplacedText(coreBytes))
	case f.mayRaise():
		can = append(can, "raise the budget with `"+f.raiseCmd(coreBytes)+"`")
	default:
		ask = append(ask, fmt.Sprintf("raise %s to %d in %s", policy.KeyContextBudgetInternal, budgetToFit(coreBytes), f.to))
	}
	out := strings.Join(can, ", or ")
	if len(ask) == 0 {
		return out
	}
	// a company row or policy is an admin's change (devco D-637)
	adm := "ask " + f.admins.Who() + " to " + strings.Join(ask, " or ") + " (company rows and policies are changed by an admin)"
	if out == "" {
		return adm
	}
	return out + "; or " + adm
}
