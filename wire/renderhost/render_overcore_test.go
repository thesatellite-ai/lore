package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// TestOverCoreWarnNamesTheFixCommands guards devco D-573: the private
// context's over-budget warning said "unpin some or raise the key" with no
// command for either.
func TestOverCoreWarnNamesTheFixCommands(t *testing.T) {
	p := &privatePlan{budget: 8000, coreBytes: 20285, pins: []privatePin{
		{id: "rul_small", kind: privatePinRule, text: strings.Repeat("r", 100)},
		{id: "hfx_big", kind: privatePinHotfix, text: strings.Repeat("h", 9000)},
		{id: "rul_big", kind: privatePinRule, text: strings.Repeat("r", 5000)},
	}}
	w := privateOverCoreWarn(p)
	for _, want := range []string{
		"20285 bytes in full, over context.budget.internal 8000",
		"The longest:\n  - hotfix hfx_big (9000 bytes)\n  - rule rul_big (5000 bytes)\n  - rule rul_small (100 bytes)",
		"`lore rule edit --id rul_big --severity should`", // the largest listed rule, not the first found (devco D-602)
		"`lore hotfix edit --id hfx_big --severity medium`",
		"`lore policy set --key context.budget.internal --value 22000 --yes`",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("warning lacks %q:\n%s", want, w)
		}
	}
	if strings.Contains(w, "unpin some") {
		t.Errorf("warning still says unpin with no command:\n%s", w)
	}
	// D-909: the file stays within its budget, so its left-out line names
	// the budget and `lore search`, no fix of the pinned part
	if l := leftOutLine(3, 8000); !strings.Contains(l, "left out by context.budget.internal 8000 bytes: run `lore search`") {
		t.Errorf("left-out line: %s", l)
	}
}

// TestOverCoreWarnOncePerChange guards devco D-573: the warning repeated on
// every `lore sync`; it is printed only when a private file changes.
func TestOverCoreWarnOncePerChange(t *testing.T) {
	f := privateFile{path: filepath.Join(t.TempDir(), "acme.md"), body: "pinned\n"}
	if !privateFilesChanged([]privateFile{f}) {
		t.Fatal("a missing private file is not a change")
	}
	if err := os.WriteFile(f.path, []byte(f.body), 0o600); err != nil {
		t.Fatal(err)
	}
	if privateFilesChanged([]privateFile{f}) {
		t.Fatal("an unchanged private file counts as a change: the warning would repeat on every sync")
	}
	f.body = "pinned and more\n"
	if !privateFilesChanged([]privateFile{f}) {
		t.Fatal("a changed private file is not a change")
	}
}

// TestOverCoreFixWorksWherePrinted guards devco D-636: the raise named no
// place, so in a repo linked to a store folder it fell to --to repo and
// was refused (E_STORE_READ_ONLY). It names the place the budget is set.
func TestOverCoreFixWorksWherePrinted(t *testing.T) {
	pins := []privatePin{{id: "rul_big", kind: privatePinRule, text: strings.Repeat("r", 5000)}}
	f := budgetFix{to: "acme/web"}
	cmd := "`lore policy set --key context.budget.internal --value 7000 --to acme/web --yes`"
	if got := overCoreFix(5000, pins, f); !strings.Contains(got, cmd) {
		t.Errorf("the raise does not name the linked folder:\n%s", got)
	}

}

// TestOverCoreFixOnlyAdminsLowerCompanyRows guards devco D-637 and D-681:
// every member was told to demote a company must rule, and (D-681) a
// project folder's must rule and budget were offered to members as their
// own commands. A company row or budget, in the org folder or a project
// folder of the company store, is a member's command nowhere: the member
// is told which admin to ask, and the command shows only labelled as the
// admin's ("the admin would run").
func TestOverCoreFixOnlyAdminsLowerCompanyRows(t *testing.T) {
	pins := []privatePin{
		{id: "rul_co", kind: privatePinRule, text: strings.Repeat("r", 5000)},
		{id: "hfx_web", kind: privatePinHotfix, text: strings.Repeat("h", 100)},
	}
	adm := profilestore.Admins{Org: "acme", Names: []string{"Ada Admin"}}
	// D-681: the project folder acme/web is company ground: its pinned rows and budget too
	member := budgetFix{to: "acme/web", raiseCompany: true, company: map[string]bool{"rul_co": true, "hfx_web": true}, admins: adm}
	got := overCoreFix(5100, pins, member)
	own, _, _ := strings.Cut(got, "the admin would run")
	for _, bad := range []string{"rule edit --id rul_co", "hotfix edit --id hfx_web", "lore policy set"} {
		if strings.Contains(own, bad) {
			t.Errorf("devco:D-681: a member is given %q as their own command:\n%s", bad, got)
		}
	}
	for _, want := range []string{
		"ask an admin of acme (Ada Admin) to lower rul_co or lower hfx_web or raise context.budget.internal to 7000 in acme/web",
		"the admin would run `lore rule edit --id rul_co --severity should` or `lore hotfix edit --id hfx_web --severity medium` or `lore policy set --key context.budget.internal --value 7000 --to acme/web --yes`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("member's fix lacks %q:\n%s", want, got)
		}
	}
	admin := member
	admin.admins.Caller = true
	if got := overCoreFix(5100, pins, admin); !strings.Contains(got, "`lore rule edit --id rul_co --severity should`") || strings.Contains(got, "ask an admin") {
		t.Errorf("an admin is not given the company rule's command:\n%s", got)
	}
	company := budgetFix{to: "acme/org", raiseCompany: true, company: map[string]bool{"rul_co": true}, admins: adm}
	got = overCoreFix(5000, pins[:1], company)
	if own, _, _ := strings.Cut(got, "the admin would run"); strings.Contains(own, "lore policy set") || !strings.Contains(got, "raise context.budget.internal to 7000 in acme/org") {
		t.Errorf("a member is given a company policy command:\n%s", got)
	}
	if l := leftOutLine(3, 1000); strings.Contains(l, "lore policy set") {
		t.Errorf("the left-out line gives a member a company policy command: %s", l)
	}
}

// TestOverCoreFixUnplacedPrintsNoPlaceholder guards devco D-658: when the
// place the budget is set cannot be named here, the raise printed a
// placeholder --to no shell runs; it prints no policy command then, only
// `lore whoami`, which works anywhere.
func TestOverCoreFixUnplacedPrintsNoPlaceholder(t *testing.T) {
	pins := []privatePin{{id: "rul_big", kind: privatePinRule, text: strings.Repeat("r", 5000)}}
	f := budgetFix{unplaced: true}
	for _, got := range []string{overCoreFix(5000, pins, f)} {
		if strings.Contains(got, "lore policy set") || strings.Contains(got, "<store") || !strings.Contains(got, "`lore whoami`") {
			t.Errorf("devco:D-658: an unplaced raise prints a command that cannot work:\n%s", got)
		}
	}
}

// TestOverCoreFixAsksEachRowsOwnOrg guards devco D-681: in the ops store
// clone an ops folder's pinned rule was judged by the company roster and
// printed to an ops member as their own command. A row of another org's
// store goes to that org's admins, the budget to its place's admins, and
// an admin of the row's org keeps the command.
func TestOverCoreFixAsksEachRowsOwnOrg(t *testing.T) {
	pins := []privatePin{{id: "rul_ops", kind: privatePinRule, text: strings.Repeat("r", 5000)}}
	ops := profilestore.Admins{Org: "acmeops", Names: []string{"Oli Ops"}}
	co := profilestore.Admins{Org: "acme", Names: []string{"Ada Admin"}}
	member := budgetFix{to: "acme/web", raiseCompany: true, company: map[string]bool{"rul_ops": true}, rowAdmins: map[string]profilestore.Admins{"rul_ops": ops}, admins: co}
	got := overCoreFix(5000, pins, member)
	if own, _, _ := strings.Cut(got, "the admin would run"); strings.Contains(own, "rule edit --id rul_ops") || strings.Contains(own, "lore policy set") {
		t.Errorf("devco:D-681: an ops member is given an ops row's command as their own:\n%s", got)
	}
	want := "ask an admin of acmeops (Oli Ops) to lower rul_ops; or ask an admin of acme (Ada Admin) to raise context.budget.internal to 7000 in acme/web"
	if !strings.Contains(got, want) {
		t.Errorf("member's fix lacks %q:\n%s", want, got)
	}
	opsAdmin := member
	opsAdmin.rowAdmins = map[string]profilestore.Admins{"rul_ops": {Org: "acmeops", Names: []string{"Oli Ops"}, Caller: true}}
	if got := overCoreFix(5000, pins, opsAdmin); !strings.Contains(got, "take a row out of the pinned part with `lore rule edit --id rul_ops --severity should`") {
		t.Errorf("an ops admin lost the ops row's command:\n%s", got)
	}
	// a company budget raised in a store clone's own folder (no --to)
	clone := budgetFix{raiseCompany: true, admins: ops}
	if got := overCoreFix(5000, nil, clone); strings.Contains(got, " in ;") || !strings.Contains(got, "raise context.budget.internal to 7000 in this folder") {
		t.Errorf("a clone-folder raise names no place:\n%s", got)
	}
}
