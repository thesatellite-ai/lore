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
		"20285 bytes, over context.budget.internal 8000",
		"The largest: hfx_big (9000 bytes), rul_big (5000 bytes), rul_small (100 bytes).",
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
	if l := leftOutLine(3, 8000, 20285, budgetFix{}); !strings.Contains(l, "lore policy set --key context.budget.internal --value 22000 --yes") {
		t.Errorf("left-out line names no command: %s", l)
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
	if l := leftOutLine(3, 1000, 5000, f); !strings.Contains(l, cmd) {
		t.Errorf("the left-out line does not name the linked folder: %s", l)
	}
}

// TestOverCoreFixOnlyAdminsLowerCompanyRows guards devco D-637: every
// member was told to demote a company must rule. A company row (or a
// company budget) is shown as a command only to an admin; a member is
// told which admin to ask.
func TestOverCoreFixOnlyAdminsLowerCompanyRows(t *testing.T) {
	pins := []privatePin{
		{id: "rul_co", kind: privatePinRule, text: strings.Repeat("r", 5000)},
		{id: "hfx_web", kind: privatePinHotfix, text: strings.Repeat("h", 100)},
	}
	member := budgetFix{to: "acme/web", company: map[string]bool{"rul_co": true},
		admins: profilestore.Admins{Org: "acme", Names: []string{"Ada Admin"}}}
	got := overCoreFix(5100, pins, member)
	for _, want := range []string{
		"`lore hotfix edit --id hfx_web --severity medium`", // a project row: a member may lower it
		"--to acme/web --yes`",                              // a folder budget: a member may raise it
		"ask an admin of acme (Ada Admin) to lower rul_co",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("member's fix lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "rule edit --id rul_co") {
		t.Errorf("a member is told to demote a company must rule:\n%s", got)
	}
	admin := member
	admin.admins.Caller = true
	if got := overCoreFix(5100, pins, admin); !strings.Contains(got, "`lore rule edit --id rul_co --severity should`") || strings.Contains(got, "ask an admin") {
		t.Errorf("an admin is not given the company rule's command:\n%s", got)
	}
	company := budgetFix{to: "acme/org", raiseCompany: true, company: map[string]bool{"rul_co": true},
		admins: profilestore.Admins{Org: "acme", Names: []string{"Ada Admin"}}}
	got = overCoreFix(5000, pins[:1], company)
	if strings.Contains(got, "lore policy set") || !strings.Contains(got, "raise context.budget.internal to 7000 in acme/org") {
		t.Errorf("a member is given a company policy command:\n%s", got)
	}
	if l := leftOutLine(3, 1000, 5000, company); strings.Contains(l, "lore policy set") || !strings.Contains(l, "ask an admin of acme (Ada Admin)") {
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
	for _, got := range []string{overCoreFix(5000, pins, f), leftOutLine(3, 1000, 5000, f)} {
		if strings.Contains(got, "lore policy set") || strings.Contains(got, "<store") || !strings.Contains(got, "`lore whoami`") {
			t.Errorf("devco:D-658: an unplaced raise prints a command that cannot work:\n%s", got)
		}
	}
}
