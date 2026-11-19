package renderhost

// render_budgetfix.go — the fixes the private context's over-budget
// warning offers, as commands that work where they are printed and only
// to someone who may run them (devco D-636, D-637, D-681).
//
//	pinned row on company ground (org folder, store root,  ─┐
//	  any project folder of the company store)              │
//	budget set on company ground                            ├─▶ the caller is an admin (or the org
//	                                                        │   names none yet) ─▶ the command
//	                                                        └─▶ a member ─▶ "ask an admin of <org>
//	                                                             (<names>) to …; the admin would
//	                                                             run: `…`" (never as the member's fix)
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
	// raiseCompany is true when the raise goes to company ground.
	raiseCompany bool
	// unplaced is true when the place the budget is set cannot be named
	// here (a linked repo whose store folder is not attached and whose
	// link names no store of config.yaml; with one, the raise names the
	// link's --to, devco D-675): no raise command is printed then, only
	// `lore whoami` to find it (devco D-658).
	unplaced bool
	// company holds the ids of pinned rows kept on company ground.
	company map[string]bool
	// admins is the admins of the place the budget is set (and of every
	// company row rowAdmins does not name) and whether the caller is one;
	// its zero value (no roster) refuses nobody.
	admins profilestore.Admins
	// rowAdmins is the admins a company row is judged by when its store
	// is another org's (an ops folder's row read from a company repo,
	// devco D-681), so the member is sent to that org's admins.
	rowAdmins map[string]profilestore.Admins
}

// adminsOf is the admins who may change pinned row id.
func (f budgetFix) adminsOf(id string) profilestore.Admins {
	if ad, ok := f.rowAdmins[id]; ok {
		return ad
	}
	return f.admins
}

// mayLower reports whether the caller may take pinned row id out of the
// pinned part (a member may not for a company row).
func (f budgetFix) mayLower(id string) bool { return !f.company[id] || f.adminsOf(id).May() }

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
	return "to raise it, ask " + f.admins.Who() + "; the admin would run `" + f.raiseCmd(coreBytes) + "`"
}

// planBudgetFix reads where the budget is set and which pinned rows are on
// company ground, each judged by the roster of its own store's org
// (profilestore.Judge: the answer the role check of each printed write
// gives; devco D-681: in the ops store clone an ops folder's row and
// budget were judged by the company roster and printed to a member as
// their own commands). One Judge per render opens each org's roster once;
// only called when the pinned part is over the budget, so a render under
// budget never opens a roster.
func (h *Host) planBudgetFix(ctx context.Context, bodies []privateBody) budgetFix {
	var f budgetFix
	if h.app == nil {
		return f
	}
	j := profilestore.NewJudge(h.app)
	var placed bool
	f.to, f.raiseCompany, f.admins, placed = h.app.PolicyPlaceJudged(ctx, policy.KeyContextBudgetInternal, j)
	f.unplaced = !placed
	f.company = map[string]bool{}
	f.rowAdmins = map[string]profilestore.Admins{}
	for _, b := range bodies {
		for _, pin := range b.pins {
			if company, ad := j.At(ctx, pin.src); company {
				f.company[pin.id] = true
				f.rowAdmins[pin.id] = ad
			}
		}
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
	var lower, adminRuns []string
	var ask askList
	ruleCmd := "lore rule edit --id " + rule + " --severity should"
	if f.mayLower(rule) {
		lower = append(lower, "`"+ruleCmd+"`")
	} else {
		ask.add(f.adminsOf(rule), "lower "+rule)
		if rule != "<rul_id>" {
			adminRuns = append(adminRuns, "`"+ruleCmd+"`")
		}
	}
	if hotfix != "" {
		hotfixCmd := "lore hotfix edit --id " + hotfix + " --severity medium"
		if f.mayLower(hotfix) {
			lower = append(lower, "`"+hotfixCmd+"`")
		} else {
			ask.add(f.adminsOf(hotfix), "lower "+hotfix)
			adminRuns = append(adminRuns, "`"+hotfixCmd+"`")
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
		ask.add(f.admins, fmt.Sprintf("raise %s to %d in %s", policy.KeyContextBudgetInternal, budgetToFit(coreBytes), f.raisePlace()))
		adminRuns = append(adminRuns, "`"+f.raiseCmd(coreBytes)+"`")
	}
	out := strings.Join(can, ", or ")
	if ask.empty() {
		return out
	}
	// company rows and policies are an admin's change (devco D-637,
	// D-681): the member is told whom to ask, and the commands are labelled
	// as the admin's, so none reads as the member's own next step
	adm := ask.text() + " (company rows and policies are changed by an admin)"
	if len(adminRuns) > 0 {
		adm += "; the admin would run " + strings.Join(adminRuns, " or ")
	}
	if out == "" {
		return adm
	}
	return out + "; or " + adm
}

// raisePlace is where the raise writes, for a sentence: the --to, or "this
// folder" when the raise writes to the repo (a store clone's own folder).
func (f budgetFix) raisePlace() string {
	if f.to == "" {
		return "this folder"
	}
	return f.to
}

// askList is what a member asks of which admins, in the order the fixes
// are listed: rows of another org's store go to that org's admins (devco
// D-681), so one line may name two orgs.
type askList struct {
	who  []string
	what map[string][]string
}

// add records that the admins ad are asked to do what.
func (l *askList) add(ad profilestore.Admins, what string) {
	w := ad.Who()
	if l.what == nil {
		l.what = map[string][]string{}
	}
	if _, seen := l.what[w]; !seen {
		l.who = append(l.who, w)
	}
	l.what[w] = append(l.what[w], what)
}

// empty reports whether nothing is asked.
func (l askList) empty() bool { return len(l.who) == 0 }

// text is "ask <who> to a or b; or ask <other> to c".
func (l askList) text() string {
	parts := make([]string, 0, len(l.who))
	for _, w := range l.who {
		parts = append(parts, "ask "+w+" to "+strings.Join(l.what[w], " or "))
	}
	return strings.Join(parts, "; or ")
}
