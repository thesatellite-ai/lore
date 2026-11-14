//go:build machines && !windows

package machines

// life_setups_test.go — every setup kind (solo, join a team, start a
// team) driven through its whole life on every shape: create with every
// step → edit every step of the finished setup → rename (trash, restore
// under a new name) → change the default → purge → re-add with the same
// name → import → edit the import → remove. After every transition
// lifeModel.checkModel compares list, which-setup-applies, config.yaml,
// repo files, doctor and the page's reads against the model.
//
// lifecycle_test.go walks every short sequence of the list-level ops on
// one solo setup; this file walks one long life per kind with every step.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Setup kinds (setup plan --kind).
const (
	kindSolo   = "solo"
	kindMember = "member"
	kindAdmin  = "admin"
)

var lifeKinds = []string{kindSolo, kindMember, kindAdmin}

// Names the long life uses.
const (
	lsName    = "life"
	lsRenamed = "life-renamed"
	lsImport  = "life-imp"
	lsOrg     = "acmelife"
	lsFolder  = "~/code/work/app/**"
	lsFolder2 = "~/code/plain-git/**"
	lsImpDir  = "~/code/has-lore/**"
)

// lsFullShapes run every kind; the other shapes run one kind each,
// rotating (envFull runs all).
var lsFullShapes = map[string]bool{ShapeFresh: true, ShapeOwnerLike: true, ShapeGraveyard: true}

// lsMilestones are the transitions after which the machine-wide
// invariants run too (checkModel); every other one runs checkLight.
var lsMilestones = map[string]bool{"create": true, "rename": true, "purge": true, "re-add": true, "import": true}

// TestLifeSetups is the solo kind's life; TestLifeTeams the two team
// kinds' (a member's needs a second, admin machine).
func TestLifeSetups(t *testing.T) { lsMatrix(t, []string{kindSolo}) }

func TestLifeTeams(t *testing.T) { lsMatrix(t, []string{kindMember, kindAdmin}) }

func lsMatrix(t *testing.T, kinds []string) {
	full := os.Getenv(envFull) != ""
	ran := 0
	for si, shape := range EveryShape() {
		for _, kind := range kinds {
			ki := slices.Index(lifeKinds, kind)
			if !full && !lsFullShapes[shape] && (si+ki)%len(lifeKinds) != 0 {
				continue
			}
			ran++
			t.Run(shape+"/"+kind, func(t *testing.T) {
				t.Parallel()
				lsLife(t, shape, kind)
			})
		}
	}
	t.Logf("%d setup lives over %d shapes", ran, len(EveryShape()))
}

// lsRun runs a lore command that must succeed.
func lsRun(t *testing.T, m *Machine, cwd string, args ...string) string {
	t.Helper()
	r := m.Lore(t, cwd, append(args, "--json")...)
	if r.Code != 0 {
		t.Fatalf("%s", r)
	}
	return r.Stdout
}

func lsApply(t *testing.T, m *Machine, cwd, name, step string, answers any) string {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore(t, cwd, "setup", "apply", name, "--step", step, "--answers", string(b), "--json")
	if r.Code != 0 {
		// The check run names what failed and why (apply only names it).
		c := m.Lore(t, cwd, "setup", "check", name, "--step", step, "--answers", string(b), "--json")
		t.Fatalf("%s\ncheck says: %s", r, c.Stdout)
	}
	return r.Stdout
}

// lsTeamFor makes the team side: a bare store repo, and for a member an
// admin machine that starts the team there and invites email.
func lsTeamFor(t *testing.T, m *Machine, kind, email string) lsTeam {
	t.Helper()
	if kind == kindSolo {
		return lsTeam{}
	}
	// The team store is a GitHub url that git rewrites (url.insteadOf in
	// each machine's ~/.gitconfig) to a bare repo on this disk: lore sees
	// the url a real team has, git reaches a repo both machines share.
	bare := filepath.Join(m.Dir, "team.git")
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	tm := lsTeam{storeURL: lsTeamURL, bare: bare}
	tm.rewrite(t, m)
	if kind != kindMember {
		return tm
	}
	// The admin is another person: the owner-like machine, as its work
	// account (bravo over the github-bravo alias).
	admin := Build(t, ShapeOwnerLike, loreBin)
	lsGHHosts(t, admin)
	tm.rewrite(t, admin)
	lsCreate(t, admin, "boss", kindAdmin, "boss@example.test", []string{"~/code/**"}, true, tm, lsGitHub{LoginBravo, "github-bravo"})
	out := lsRun(t, admin, admin.Repos[RepoWorkApp], "team", "invite", email, "--yes")
	var o struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(out), &o)
	tm.invite = o.Text
	if !strings.HasPrefix(tm.invite, "lore-invite:") {
		t.Fatalf("team invite printed no invite line: %s", out)
	}
	// C5: the invite reaches the team store with the admin's sync
	lsRun(t, admin, admin.Repos[RepoWorkApp], "sync")
	return tm
}

// lsGitHub is the github step's account and alias.
type lsGitHub struct{ login, alias string }

// lsAlpha is the personal account over github.com, on every shape.
var lsAlpha = lsGitHub{LoginAlpha, "github.com"}

// lsCreate saves every step of a new setup the way the page does, the
// summary last (which ends the new-setup flow).
func lsCreate(t *testing.T, m *Machine, name, kind, email string, folders []string, def bool, tm lsTeam, gh lsGitHub) {
	t.Helper()
	cwd := m.Repos[RepoWorkApp]
	ka := map[string]any{"kind": kind, "name": name}
	if kind == kindSolo {
		ka["default"] = def
	}
	lsApply(t, m, cwd, name, "kind", ka)
	lsApply(t, m, cwd, name, "identity", map[string]any{"name": PersonName, "email": email})
	lsApply(t, m, cwd, name, "github", map[string]any{"github_account": gh.login, "host_alias": gh.alias})
	lsApply(t, m, cwd, name, "personal_store", map[string]any{"personal_store": "local"})
	switch kind {
	case kindAdmin:
		lsApply(t, m, cwd, name, "team", map[string]any{"org": lsOrg, "store_url": tm.storeURL})
	case kindMember:
		lsApply(t, m, cwd, name, "join", map[string]any{"invite": tm.invite})
	}
	lsApply(t, m, cwd, name, "tools", map[string]any{"tools": []string{"claude_code"}})
	lsApply(t, m, cwd, name, "secrets", map[string]any{"backend": "none"})
	lsApply(t, m, cwd, name, "folders", map[string]any{"folders": folders, "default": def})
	lsApply(t, m, cwd, name, "this_folder", map[string]any{})
	lsApply(t, m, cwd, name, "summary", map[string]any{})
	if kind == kindAdmin || kind == kindMember {
		// C5: the new team store (admin) or the joiner's roster row (member)
		// reaches the remote with the person's sync (a shape that is offline
		// on purpose keeps it waiting; the life's own checks say what follows)
		m.Lore(t, cwd, "--setup", name, "sync")
	}
}

// lsCreateRefused saves the new setup's steps on a machine whose ~/.lore
// lore may not write: the first step that writes refuses clearly
// (lmRefusesLoreHome), and none is quietly lost.
func lsCreateRefused(t *testing.T, m *Machine, kind, email string, tm lsTeam) {
	t.Helper()
	cwd := m.Repos[RepoWorkApp]
	steps := []struct {
		id  string
		ans string
	}{
		{"kind", `{"kind":"` + kind + `","name":"` + lsName + `"}`},
		{"identity", `{"name":"` + PersonName + `","email":"` + email + `"}`},
		{"github", `{"github_account":"` + LoginAlpha + `","host_alias":"github.com"}`},
		{"personal_store", `{"personal_store":"local"}`},
	}
	switch kind {
	case kindAdmin:
		steps = append(steps, struct{ id, ans string }{"team", `{"org":"` + lsOrg + `","store_url":"` + tm.storeURL + `"}`})
	case kindMember:
		steps = append(steps, struct{ id, ans string }{"join", `{"invite":"` + tm.invite + `"}`})
	}
	for _, s := range steps {
		if r := m.Lore(t, cwd, "setup", "apply", lsName, "--step", s.id, "--answers", s.ans, "--json"); r.Code != 0 {
			lmRefusesLoreHome(t, r)
			return
		}
	}
	t.Errorf("every step of a new %s setup saved on a ~/.lore lore may not write", kind)
}

func lsLife(t *testing.T, shape, kind string) {
	m := Build(t, shape, loreBin)
	lsGHHosts(t, m)
	email := "life-" + kind + "@example.test"
	tm := lsTeamFor(t, m, kind, email)
	if lmBrokenHome(shape) {
		lsCreateRefused(t, m, kind, email, tm)
		return
	}
	lm := newLifeModel(t, m)
	cwd := m.Repos[RepoWorkApp]
	step := func(what string) {
		t.Helper()
		at := shape + "/" + kind + "/" + what
		if lsMilestones[what] || strings.HasPrefix(what, "remove") {
			lm.checkModel(t, m, at)
		} else {
			lm.checkLight(t, m, at)
		}
		if t.Failed() {
			t.FailNow()
		}
	}

	// create
	lsCreate(t, m, lsName, kind, email, []string{lsFolder}, false, tm, lsAlpha)
	lm.add(lsName, lmSetup{Kind: kind, Folders: []string{lsFolder}, Email: email})
	step("create")

	// edit every step of the finished setup
	email2 := "life2-" + kind + "@example.test"
	// Every kind changes its email; a member keeps the invite's address
	// as another of its addresses (the join step needs it).
	id := map[string]any{"name": PersonName + " Two", "email": email2}
	if kind == kindMember {
		id["emails"] = []string{email}
	}
	if kind == kindSolo {
		// a solo setup's identity keeps the old key as an alias (an email
		// change keeps one actor); a team setup's identity is its org entry
		lm.ours[lsName].OldEmails = append(lm.ours[lsName].OldEmails, email)
	}
	lm.ours[lsName].Email = email2
	lm.emails[email2] = true
	lsApply(t, m, cwd, lsName, "identity", id)
	lsShowsEmail(t, m, lsName, email2)
	step("edit identity")
	lsApply(t, m, cwd, lsName, "github", map[string]any{"github_account": LoginAlpha, "route": "https"})
	step("edit github")
	lsApply(t, m, cwd, lsName, "personal_store", map[string]any{"personal_store": "none"})
	step("edit personal store")
	switch kind {
	case kindAdmin:
		lsApply(t, m, cwd, lsName, "team", map[string]any{"org": lsOrg, "store_url": tm.storeURL})
		step("edit team")
	case kindMember:
		lsApply(t, m, cwd, lsName, "join", map[string]any{"invite": tm.invite})
		step("edit join")
	}
	lsApply(t, m, cwd, lsName, "tools", map[string]any{"tools": []string{"codex", "cursor"}})
	step("edit tools")
	lsApply(t, m, cwd, lsName, "secrets", map[string]any{"backend": "none"})
	step("edit secrets")
	lsApply(t, m, cwd, lsName, "folders", map[string]any{"folders": []string{lsFolder, lsFolder2}, "default": false})
	lm.ours[lsName].Folders = []string{lsFolder, lsFolder2}
	step("edit folders")
	lsApply(t, m, cwd, lsName, "this_folder", map[string]any{})
	step("edit this folder")
	lsRun(t, m, "", "setup", "edit", lsName, "--note", "a lifecycle note", "--tool", "claude_code", "--folder", lsFolder)
	lm.ours[lsName].Folders = []string{lsFolder}
	step("setup edit")

	// rename: to the trash, back under a new name
	lsRun(t, m, "", "setup", "remove", lsName)
	s := *lm.ours[lsName]
	lm.trash(lsName)
	step("remove")
	lsRun(t, m, "", "setup", "trash", "restore", lsName, "--as", lsRenamed)
	lm.restore(lsName, lsRenamed, s)
	step("rename")

	// change the default, there and back
	lsRun(t, m, "", "setup", "edit", lsRenamed, "--default")
	lm.setDefault(lsRenamed, true)
	step("default on")
	lsRun(t, m, "", "setup", "edit", lsRenamed, "--no-default")
	lm.setDefault(lsRenamed, false)
	step("default off")

	// purge, then clear what only it used
	lsRun(t, m, "", "setup", "remove", lsRenamed)
	lm.trash(lsRenamed)
	step("remove renamed")
	lsRun(t, m, "", "setup", "trash", "purge", lsRenamed, "--confirm")
	lm.purge(lsRenamed)
	lsClearOwnLeftovers(t, m, lm)
	step("purge")

	// re-add under the same name
	lsCreate(t, m, lsRenamed, kind, email, []string{lsFolder}, false, tm, lsAlpha)
	lm.add(lsRenamed, lmSetup{Kind: kind, Folders: []string{lsFolder}, Email: email})
	step("re-add")

	// import: a taken name is skipped, a free one is written
	lsRun(t, m, "", "setup", "import-scan")
	out := lsRun(t, m, "", "setup", "import", "--setups", `[{"name":"`+lsRenamed+`","kind":"solo"},{"name":"`+lsImport+`","kind":"solo","folders":["`+lsImpDir+`"]}]`)
	if !strings.Contains(out, lsImport) {
		t.Errorf("import does not name %s: %s", lsImport, out)
	}
	lm.add(lsImport, lmSetup{Kind: kindSolo, Folders: []string{lsImpDir}})
	step("import")

	// edit the imported setup: a step and setup edit
	lsApply(t, m, cwd, lsImport, "identity", map[string]any{"name": PersonName, "email": "imp@example.test"})
	lm.ours[lsImport].Email = "imp@example.test"
	lm.emails["imp@example.test"] = true
	step("edit import identity")
	lsRun(t, m, "", "setup", "edit", lsImport, "--folder", lsFolder2)
	lm.ours[lsImport].Folders = []string{lsFolder2}
	step("edit import folders")

	// remove both
	for _, n := range []string{lsImport, lsRenamed} {
		lsRun(t, m, "", "setup", "remove", n)
		lm.trash(n)
		step("remove " + n)
	}
}

// lsClearOwnLeftovers clears the leftovers that name an email only our
// gone setups used (a purge without --clear-identity leaves them, and
// Home offers Clear).
func lsClearOwnLeftovers(t *testing.T, m *Machine, lm *lifeModel) {
	t.Helper()
	for _, raw := range lmList(t, m, "").Leftovers {
		var l struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		}
		_ = json.Unmarshal(raw, &l)
		for e := range lm.emails {
			if !lm.emailUsed(e) && strings.Contains(l.Value, e) {
				lsRun(t, m, "", "setup", "leftovers", "clear", l.Kind)
			}
		}
	}
}

// TestLifeSetupEditAgreement: opening a finished setup in the page and
// saving its kind step unchanged is an edit, and the page's two reads of
// it agree: the name check (setup check) and the save (setup apply) give
// the same verdict on the setup's own name, and the save never turns the
// edit into the new-setup flow (one entry, same kind, no progress rows).
func TestLifeSetupEditAgreement(t *testing.T) {
	for _, shape := range []string{ShapeFresh, ShapeOwnerLike, ShapeGraveyard} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			lsGHHosts(t, m)
			lm := newLifeModel(t, m)
			lsCreate(t, m, lsName, kindSolo, "edit@example.test", []string{lsFolder}, false, lsTeam{}, lsAlpha)
			lm.add(lsName, lmSetup{Kind: kindSolo, Folders: []string{lsFolder}, Email: "edit@example.test"})
			ans := `{"kind":"solo","name":"` + lsName + `","default":false}`
			c := m.Lore(t, m.Repos[RepoWorkApp], "setup", "check", lsName, "--step", "kind", "--answers", ans, "--json")
			var o lmCheckOut
			if json.Unmarshal([]byte(c.Stdout), &o) != nil {
				t.Fatalf("setup check: %s", c)
			}
			free := false
			for _, ck := range o.Checks {
				free = free || (ck.Check == "setup.name_free" && ck.Status == "ok")
			}
			a := m.Lore(t, m.Repos[RepoWorkApp], "setup", "apply", lsName, "--step", "kind", "--answers", ans, "--json")
			if e := csErrOf(a); free && e != nil && e.Code == "E_SETUP_NAME_TAKEN" {
				t.Errorf("the name check says %q is free for itself, the save says E_SETUP_NAME_TAKEN: %s", lsName, a)
			}
			lm.checkModel(t, m, shape+"/edit kind")
		})
	}
}

// lsShowsEmail: after an email change, the page's reads name the new
// email: setup plan's identity step value (what the page shows when the
// setup is opened), and setup list's here block in the setup's folder
// (who a write there is stamped as). For a team setup the email lives in
// orgs[] (onboard.Config.IdentityOf), never in its setups[] entry.
func lsShowsEmail(t *testing.T, m *Machine, name, email string) {
	t.Helper()
	r := m.Lore(t, "", "setup", "plan", "--setup", name, "--json")
	var p struct {
		Steps []struct {
			ID     string                     `json:"id"`
			Values map[string]json.RawMessage `json:"values"`
		} `json:"steps"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &p) != nil {
		t.Fatalf("setup plan: %s", r)
	}
	for _, st := range p.Steps {
		if st.ID == "identity" {
			if got := strings.Trim(string(st.Values["email"]), `"`); got != email {
				t.Errorf("setup plan shows email %q for %s, it uses %s", got, name, email)
			}
		}
	}
	r = m.Lore(t, m.Repos[RepoWorkApp], "setup", "list", "--json")
	var l struct {
		Here struct {
			IdentityKey string `json:"identity_key"`
		} `json:"here"`
	}
	_ = json.Unmarshal([]byte(r.Stdout), &l)
	if l.Here.IdentityKey != "human:"+email {
		t.Errorf("setup list here: identity %q in %s's folder, want human:%s", l.Here.IdentityKey, name, email)
	}
}
