//go:build stories && !windows

package stories

// WW_rolecells_test.go — story WW, devco D-694 (owner use pass 5 wrong 1
// and 2, the owner's role rule): one matrix cell per write op on company
// ground. In an org store a member may not change a company must rule in
// any way nor any folder policy, and adds rules only in the folder of a
// team they are on; an admin may do everything. Every cell runs the op as
// the member (refused with the ask-an-admin hint, and the member's store
// clone gets no commit), then as the admin (it works). The cells share one
// setup so the whole matrix stays a few seconds of wall time. Every name,
// email and url is made up.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wrcImportRerun is the --secret-unstored rerun a refused task import prints.
var wrcImportRerun = regexp.MustCompile(`lore task import [^|]*--secret-unstored`)

// Names of the role matrix (made up).
const (
	wrcOther = "api" // a company folder of a team the member is not on
)

// wrcHead is the HEAD of the company store clone on m: a refused write
// must leave it where it was.
func wrcHead(t *testing.T, m *journeys.Machine) string {
	t.Helper()
	dir := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		all, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "stores", "*", "*"))
		t.Fatalf("no company store clone at %s (%v); clones: %v", dir, err, all)
	}
	return strings.TrimSpace(string(out))
}

// wrcLog is what the commits after before hold in m's company store
// clone: a failing cell names what a refused write left behind.
func wrcLog(m *journeys.Machine, before string) string {
	dir := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
	out, err := exec.Command("git", "-C", dir, "log", "--stat", "--format=%h %s", before+"..HEAD").CombinedOutput()
	if err != nil {
		return err.Error()
	}
	return string(out)
}

func TestStoryWWRoleCellPerOp(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WW"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wubFolder).OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wrcOther).OK(t)
	// Mo is on @web (folder web), not on @api (folder api)
	m.Lore(pc.Dir, "team", "add", "--handle", "@"+wubFolder, "--members", "mo").OK(t)
	m.Lore(pc.Dir, "team", "add", "--handle", "@"+wrcOther, "--members", wAdminHandle(t, m, pc)).OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wubFolder, "--own", "--yes").OK(t)
	inWeb, inOther, inOrg := wOrg+"/"+wubFolder, wOrg+"/"+wrcOther, wOrg+"/org"
	// the company must rules the cells change, one per op that takes a row away
	must := map[string]string{}
	for _, k := range []string{"edit", "delete", "move", "archive", "pin", "org"} {
		to := inWeb
		if k == "org" {
			to = inOrg
		}
		must[k] = wubRuleID.FindString(m.Lore(wc.Dir, "rule", "add", "--to", to, "--severity", "must", "--body", "role cell "+k+" keeps its text", "--yes").OK(t).Out())
	}
	for i := 0; i < 2; i++ {
		m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "role bulk cell "+strconv.Itoa(i), "--yes").OK(t)
		m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "role batch cell "+strconv.Itoa(i), "--yes").OK(t)
	}
	m.Lore(wc.Dir, "sync").OK(t)
	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	mo.Lore(mc.Dir, "sync").OK(t)

	// refused runs args as Mo in the linked web repo: refused with the
	// ask-an-admin hint, and Mo's store clone keeps its HEAD.
	refused := func(cell string, args ...string) {
		t.Helper()
		before := wrcHead(t, mo)
		r := mo.Lore(mc.Dir, args...)
		r.Refused(t, "E_REFUSED", "only an admin of the org", "ask an admin of "+wOrg, wAdmin.Name)
		if !strings.Contains(r.Out(), "nothing was written") {
			t.Errorf("devco:D-694 cell %s: the refusal does not say nothing was written:\n%s", cell, r)
		}
		if after := wrcHead(t, mo); after != before {
			t.Fatalf("devco:D-694 cell %s: a refused member write committed to the store clone (%s -> %s):\n%s", cell, before, after, wrcLog(mo, before))
		}
	}
	// admin runs args as Ada in her linked web repo: it works.
	admin := func(args ...string) { t.Helper(); m.Lore(wc.Dir, args...).OK(t) }

	s.Step("D-694/rule-edit-of-a-company-must-rule", func() {
		id := must["edit"]
		for _, c := range []struct {
			cell string
			args []string
		}{
			{"rule edit --body", []string{"rule", "edit", "--id", id, "--body", "changed by a member"}},
			{"rule edit --summary", []string{"rule", "edit", "--id", id, "--summary", "a member's title"}},
			{"rule edit --severity", []string{"rule", "edit", "--id", id, "--severity", "should"}},
			{"rule edit --activation", []string{"rule", "edit", "--id", id, "--activation", "manual"}},
		} {
			refused(c.cell, c.args...)
		}
		mo.Lore(mc.Dir, "rule", "show", "--id", id).OK(t).Says(t, "role cell edit keeps its text", "must").Never(t, "changed by a member")
		// pass 5 wrong 1: the org/ folder's must rule body too
		refused("rule edit --body (org)", "rule", "edit", "--id", must["org"], "--body", "changed by a member")
		admin("rule", "edit", "--id", id, "--body", "role cell edit changed by the admin")
		admin("rule", "edit", "--id", id, "--summary", "the admin's title")
		admin("rule", "edit", "--id", id, "--activation", "manual")
		admin("rule", "edit", "--id", id, "--severity", "should")
		admin("rule", "edit", "--id", must["org"], "--body", "role cell org changed by the admin")
	})

	s.Step("D-694/rule-file-import-of-a-company-must-rule", func() {
		// row edit --markdown writes a rule back from its Markdown file
		md := mo.Lore(mc.Dir, "row", "markdown", "--id", must["pin"]).OK(t).Out()
		changed := strings.Replace(md, "role cell pin keeps its text", "rewritten from a file by a member", 1)
		if changed == md {
			t.Fatalf("the rule's text is not in its Markdown view:\n%s", md)
		}
		refused("row edit --markdown", "row", "edit", "--id", must["pin"], "--markdown", changed, "--yes")
		admin("row", "edit", "--id", must["pin"], "--markdown", strings.Replace(changed, "by a member", "by the admin", 1), "--yes")
	})

	s.Step("D-694/rule-add-must-in-another-teams-folder-or-org", func() {
		refused("rule add must (other team)", "rule", "add", "--to", inOther, "--severity", "must", "--body", "api answers in json", "--yes")
		refused("rule add must (org)", "rule", "add", "--to", inOrg, "--severity", "must", "--body", "the company works in UTC", "--yes")
		admin("rule", "add", "--to", inOther, "--severity", "must", "--body", "api answers in json", "--yes")
	})

	s.Step("D-694/rule-delete-move-archive", func() {
		refused("rule delete", "rule", "delete", "--id", must["delete"], "--yes")
		refused("row move", "row", "move", "--id", must["move"], "--to", inOther, "--yes")
		refused("rule archive", "rule", "archive", "--id", must["archive"], "--yes")
		admin("rule", "delete", "--id", must["delete"], "--yes")
		admin("row", "move", "--id", must["move"], "--to", inOther, "--yes")
		admin("rule", "archive", "--id", must["archive"], "--yes")
	})

	s.Step("D-694/pin-set-and-unset", func() {
		refused("pin set", "pin", "set", "--id", must["pin"], "--context", "line", "--yes")
		admin("pin", "set", "--id", must["pin"], "--context", "line", "--yes")
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		refused("pin unset", "pin", "unset", "--id", must["pin"], "--yes")
		admin("pin", "unset", "--id", must["pin"], "--yes")
	})

	s.Step("D-694/bulk-where-and-batch-undo", func() {
		refused("rule archive --where", "rule", "archive", "--where", "body~=role bulk cell", "--yes")
		// devco D-712: the bulk move runs the one-row move's role check for
		// every row it takes, before the question and before any write
		refused("move --from --where", "move", "--from", inWeb, "--to", inOther, "--where", "body~=role bulk cell", "--yes")
		admin("rule", "archive", "--where", "body~=role bulk cell", "--yes")
		admin("move", "--from", inWeb, "--to", inOther, "--where", "body~=role bulk cell", "--yes")
		// a batch log is the machine's own, so the member's undo is of
		// their own batch: Mo bulk-deletes two should rules of web (a
		// free change), Ada restores them and raises them to must; Mo's
		// undo of that batch must not touch the two company must rules
		var ids []string
		for i := 0; i < 2; i++ {
			ids = append(ids, wubRuleID.FindString(m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "should", "--body", "role undo cell "+strconv.Itoa(i), "--yes").OK(t).Out()))
		}
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		out := mo.Lore(mc.Dir, "rule", "delete", "--where", "body~=role undo cell", "--yes").OK(t).Out()
		bat := wogBatch.FindString(out)
		if bat == "" {
			t.Fatalf("no batch printed by the bulk delete:\n%s", out)
		}
		mo.Lore(mc.Dir, "sync").OK(t)
		admin("sync")
		admin("trash", "restore", "--id", bat)
		for _, id := range ids {
			admin("rule", "edit", "--id", id, "--severity", "must")
		}
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		// the undo of a delete batch only restores from the trash: Mo's
		// undo must take nothing out, refused or "nothing to undo", and
		// commit nothing
		before := wrcHead(t, mo)
		if r := mo.Lore(mc.Dir, "batch", "undo", "--id", bat, "--yes"); !strings.Contains(r.Out(), "nothing to undo") && !strings.Contains(r.Out(), "E_REFUSED") {
			t.Fatalf("devco:D-694 cell batch undo: a member's undo changed company must rules:\n%s", r)
		}
		if after := wrcHead(t, mo); after != before {
			t.Fatalf("devco:D-694 cell batch undo: the member's undo committed (%s -> %s)", before, after)
		}
		mo.Lore(mc.Dir, "rule", "list").OK(t).Says(t, "role undo cell 0", "role undo cell 1")
		// the admin's own batch undo works
		aout := m.Lore(wc.Dir, "rule", "archive", "--where", "body~=role batch cell", "--yes").OK(t).Out()
		abat := wogBatch.FindString(aout)
		if abat == "" {
			t.Fatalf("no batch printed by the bulk archive:\n%s", aout)
		}
		admin("batch", "undo", "--id", abat, "--yes")
		m.Lore(wc.Dir, "rule", "list").OK(t).Says(t, "role batch cell 0", "role batch cell 1")
	})

	s.Step("D-694/policy-set-and-unset", func() {
		refused("policy set", "policy", "set", "--key", wubKey, "--value", "30000", "--to", inWeb, "--yes")
		refused("policy set (org)", "policy", "set", "--key", wubKey, "--value", "30000", "--to", inOrg, "--yes")
		admin("policy", "set", "--key", wubKey, "--value", "30000", "--to", inWeb, "--yes")
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		refused("policy unset", "policy", "unset", "--key", wubKey, "--to", inWeb, "--yes")
		admin("policy", "unset", "--key", wubKey, "--to", inWeb, "--yes")
	})

	s.Step("D-725/budget-warning-is-the-admins", func() {
		// the over-budget warning is a cell too (devco D-725): a member can
		// change neither the folder's pinned rows nor its budget, so only
		// the admin is warned, once per change. Since H-179 the admin's write
		// refreshes the private file, so that write warns and the sync after
		// it, finding the file current, repeats nothing (devco:D-937, D-573)
		admin("policy", "set", "--key", wubKey, "--value", "300", "--to", inWeb, "--yes")
		wout := m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "role cell budget warns the admin", "--yes").OK(t).Out()
		if n := strings.Count(wout, wubWarn); n != 1 {
			t.Errorf("devco:D-937 cell budget warning: the admin's rule add over the budget warns %d times, want once:\n%s", n, wout)
		}
		aout := m.Lore(wc.Dir, "sync").OK(t).Out()
		if n := strings.Count(aout, wubWarn); n != 0 {
			t.Errorf("devco:D-725 cell budget warning: the admin's sync after the warned write warns again (%d times), want none:\n%s", n, aout)
		}
		mo.Lore(mc.Dir, "sync").OK(t).Never(t, wubWarn, "lore policy set --key "+wubKey)
		admin("policy", "unset", "--key", wubKey, "--to", inWeb, "--yes")
	})

	s.Step("D-694/branch-share-isolate-and-their-undo", func() {
		// pass 5 wrong 2: a member's branch share in a linked repo
		refused("branch share", "branch", "share", "--branch", "feat/cell-share", "--yes")
		refused("branch isolate", "branch", "isolate", "--branch", "feat/cell-own", "--yes")
		admin("branch", "share", "--branch", "feat/cell-share", "--yes")
		admin("branch", "isolate", "--branch", "feat/cell-own", "--yes")
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		refused("branch share --undo", "branch", "share", "--branch", "feat/cell-share", "--undo", "--yes")
		refused("branch isolate --undo", "branch", "isolate", "--branch", "feat/cell-own", "--undo", "--yes")
		admin("branch", "share", "--branch", "feat/cell-share", "--undo", "--yes")
		admin("branch", "isolate", "--branch", "feat/cell-own", "--undo", "--yes")
	})

	s.Step("D-694/member-adds-in-their-own-teams-folder", func() {
		// the other side of the add cell: Mo is on @web, so a rule added
		// to web (must included) is his to add
		mo.Lore(mc.Dir, "rule", "add", "--to", inWeb, "--severity", "must", "--body", "web forms validate on the server", "--yes").OK(t)
		mo.Lore(mc.Dir, "memory", "add", "--to", inWeb, "--body", "web deploys go out on tuesdays", "--yes").OK(t)
		// the bare --to the linked folder is attached under in this repo is
		// the same folder web: the guard judged that alias, found no folder
		// in it and refused Mo ("company folder acme")
		mo.Lore(mc.Dir, "rule", "add", "--to", wOrg, "--severity", "must", "--body", "web forms escape every field", "--yes").OK(t)
	})

	s.Step("D-694/team-changes-are-admin-only", func() {
		// the rule gap: anyone on the roster could change the teams, so Mo
		// put himself on @api and then added rules in api. In an org store
		// every team change is an admin's; Mo still reads the teams.
		line := "lore team member add --handle @" + wrcOther + " --members @mo"
		refused("team member add (self)", "team", "member", "add", "--handle", "@"+wrcOther, "--members", "@mo")
		mo.Lore(mc.Dir, "team", "member", "add", "--handle", "@"+wrcOther, "--members", "@mo").Refused(t, "E_REFUSED", "change the team @"+wrcOther, "to run: "+line)
		refused("team add", "team", "add", "--handle", "@side", "--members", "@mo")
		refused("team member remove", "team", "member", "remove", "--handle", "@"+wubFolder, "--members", "@mo")
		refused("team rename", "team", "rename", "--handle", "@"+wubFolder, "--to", "@frontend")
		refused("team remove", "team", "remove", "--handle", "@"+wrcOther)
		// devco D-867: a team's contacts (its chat channel) are a team
		// change too: Mo reads them, the admin changes them
		refused("team contact add", "team", "contact", "add", "--handle", "@"+wubFolder, "--type", "slack", "--value", "#ui-team")
		r := m.Lore(wc.Dir, "team", "contact", "add", "--handle", "@"+wubFolder, "--type", "slack", "--value", "#ui-team", "--yes").OK(t)
		tcn := regexp.MustCompile(`tcn_[0-9a-f]+`).FindString(r.Stdout)
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "team", "show", "--handle", "@"+wubFolder).OK(t).Says(t, "contacts (1):", "slack  #ui-team  [primary]")
		refused("team contact remove", "team", "contact", "remove", "--id", tcn)
		refused("team contact edit", "team", "contact", "edit", "--id", tcn, "--value", "#elsewhere")
		mo.Lore(mc.Dir, "team", "show", "--handle", "@"+wrcOther).OK(t).Never(t, "@mo")
		mo.Lore(mc.Dir, "team", "list").OK(t).Says(t, "@"+wubFolder, "@"+wrcOther)
		// so the add right stays the admin's to give
		refused("rule add (other team, after team add)", "rule", "add", "--to", inOther, "--body", "api pages are cached", "--yes")
		// the admin runs the printed line exactly; after a sync Mo adds there
		admin(strings.Fields(line)[1:]...)
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "rule", "add", "--to", inOther, "--body", "api pages are cached", "--yes").OK(t)
	})

	s.Step("D-694/refused-add-lines-work-as-printed", func() {
		// the step above ended with Mo on @api (wrcOther): the admin takes
		// him off again so his add there is refused once more
		admin("team", "member", "remove", "--handle", "@"+wrcOther, "--members", "@mo")
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		// a member's add in another team's folder names the admin's exact
		// add and the roster line that puts the member on that folder's
		// team; the admin runs both exactly as printed, in the repo the
		// member was in, and the member then adds there himself
		r := mo.Lore(mc.Dir, "rule", "add", "--to", inOther, "--severity", "should", "--body", "api errors carry a code", "--yes")
		r.Refused(t, "E_REFUSED", "company folder "+inOther, "ask an admin of "+wOrg)
		add, team, ok := strings.Cut(wogPrintedLine(t, r.Out()), ", or to put you on team @"+wrcOther+", whose folder it is: ")
		if !ok {
			t.Fatalf("devco:D-694 cell add line: the refusal names no roster line for team @%s:\n%s", wrcOther, r)
		}
		if want := "lore rule add --body 'api errors carry a code' --severity should --to " + inOther + " --yes"; add != want {
			t.Fatalf("devco:D-694 cell add line: the admin's add is %q, want %q", add, want)
		}
		if want := "lore team member add --handle @" + wrcOther + " --members @mo"; team != want {
			t.Fatalf("devco:D-694 cell add line: the roster line is %q, want %q", team, want)
		}
		id := wubRuleID.FindString(m.Lore(wc.Dir, wubFields(t, add)...).OK(t).Out())
		m.Lore(wc.Dir, wubFields(t, team)...).OK(t)
		m.Lore(wc.Dir, "rule", "show", "--id", id).OK(t).Says(t, "api errors carry a code", inOther)
		admin("sync")
		mo.Lore(mc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "memory", "add", "--to", inOther, "--body", "api pages are 50 rows", "--yes").OK(t)
	})

	s.Step("D-717/task-import-refuses-every-keyed-record", func() {
		// devco D-717 (SECURITY): a bulk op screens every row before any
		// write; the refusal names each keyed row and no store clone gets
		// a commit. The keys are made up, built at run time.
		keyA := "AKIA" + strings.Repeat("Q", 4) + "7XKD2M4N5P6R"
		keyB := "AKIA" + strings.Repeat("Z", 4) + "3WJH8B9C2V4T"
		// task import of a file with keys in two records, as the admin and
		// as the member in his own team's folder: refused naming both
		// records, no commit in either store clone
		csv := filepath.Join(t.TempDir(), "keys.csv")
		if err := os.WriteFile(csv, []byte("id,title,body\nK-1,one,key "+keyA+"\nK-2,plain,nothing\nK-3,three,key "+keyB+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var rerun string
		for _, who := range []struct {
			m   *journeys.Machine
			dir string
		}{{m, wc.Dir}, {mo, mc.Dir}} {
			before := wrcHead(t, who.m)
			r := who.m.Lore(who.dir, "task", "import", "--file", csv, "--to", inWeb, "--yes").
				Refused(t, "task import: 2 row(s) hold what looks like a secret", "line 2 (K-1)", "line 4 (K-3)", "nothing written").Never(t, keyA, keyB)
			rerun = wrcImportRerun.FindString(r.Out())
			if !strings.HasPrefix(rerun, "lore task import --file "+csv+" --from csv --to ") {
				t.Fatalf("devco:D-717 cell task import: no --secret-unstored rerun printed:\n%s", r)
			}
			if after := wrcHead(t, who.m); after != before {
				t.Fatalf("devco:D-717 cell task import: a refused import committed (%s -> %s)", before, after)
			}
		}
		// the member's printed rerun works exactly as printed, where printed
		mo.Lore(mc.Dir, strings.Fields(rerun)[1:]...).OK(t).Says(t, "3 tasks imported").Never(t, keyA, keyB)

		// bulk move after the allow entries are removed (use pass 5): two
		// notes kept with --not-a-secret, then every allow entry removed;
		// a bulk move of both is refused naming both ids, and no store
		// clone gets a commit
		var notes []string
		for _, k := range []string{keyA, keyB} {
			notes = append(notes, wrcMemID.FindString(m.Lore(wc.Dir, "memory", "add", "--to", inWeb, "--body", "moved key "+k,
				"--not-a-secret", "--reason", "a made-up fixture", "--yes").OK(t).Out()))
		}
		for _, sal := range wrcAllowID.FindAllString(m.Lore(wc.Dir, "secrets", "allow", "list").OK(t).Out(), -1) {
			m.Lore(wc.Dir, "secrets", "allow", "rm", "--id", sal, "--yes").OK(t)
		}
		if left := wrcAllowID.FindAllString(m.Lore(wc.Dir, "secrets", "allow", "list").OK(t).Out(), -1); len(left) > 0 {
			t.Fatalf("devco:D-717 cell bulk move: allow entries left after rm: %v", left)
		}
		before := wrcHead(t, m)
		m.Lore(wc.Dir, "move", "--from", inWeb, "--to", inOther, "--where", "body~=moved key", "--yes").
			Refused(t, "bulk move: 2 row(s) hold what looks like a secret", notes[0], notes[1], "nothing written").Never(t, keyA, keyB)
		if after := wrcHead(t, m); after != before {
			t.Fatalf("devco:D-717 cell bulk move: a refused move committed (%s -> %s)", before, after)
		}

		// an edit from the masked read (use pass 5 leftover): the admin
		// saves the note's Markdown view with a change elsewhere in the
		// body; the store keeps the key under the mask, no commit of the
		// store clone ever holds the mask, and the output never the key
		view := m.Lore(wc.Dir, "row", "markdown", "--id", notes[0]).OK(t).Says(t, "[redacted ").Never(t, keyA).Stdout
		m.Lore(wc.Dir, "row", "edit", "--id", notes[0], "--markdown", strings.Replace(view, "moved key", "kept key", 1)).OK(t).Never(t, keyA)
		clone := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team")
		log, err := exec.Command("git", "-C", clone, "log", "-p", "-3").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(log), "kept key "+keyA) || strings.Contains(string(log), "kept key [redacted") {
			t.Fatalf("devco:D-717 cell masked edit: the store commit does not keep the key under the mask:\n%s", wrcNoKeys(string(log), keyA, keyB))
		}
		m.Lore(wc.Dir, "trash", "list").OK(t).Never(t, keyA, keyB)
	})
}

// wrcNoKeys is s with the fixture keys cut out, for a failure message.
func wrcNoKeys(s string, keys ...string) string {
	for _, k := range keys {
		s = strings.ReplaceAll(s, k, "<key>")
	}
	return s
}

// wrcMemID / wrcAllowID find a memory id and an allow entry id in output.
var (
	wrcMemID   = regexp.MustCompile(`mem_[0-9a-z]+`)
	wrcAllowID = regexp.MustCompile(`sal_[0-9a-z]+`)
)
