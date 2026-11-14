//go:build machines && !windows

package machines

// rest_life_test.go — every setup kind's lifecycle through the setup
// page's REST API (rest_client.go) on every machine shape:
//
//	create (every step, new mode) → edit every step (edit mode) → rename
//	(remove + restore --as, the product's only rename) → change default →
//	purge → re-add → import → edit again → remove
//
// After every transition the page must agree with itself and with the CLI
// run on the same HOME (restConsistent): setup.list = `lore setup list
// --json`, doctor.run = `lore doctor --all --json`, Home's Health cell =
// the doctor's count, no repair signal while every check passes, every
// non-passing doctor check names its issue, config.yaml parses and is in
// block style, plus the machine invariants (CheckMachine). Each step's
// check and save is judged as the page shows it (judgeStep): no "not
// checked" for a check the plan ran, an existing setup's name check says
// it is that setup and its save never answers E_SETUP_NAME_TAKEN, and
// every "Run it" fix clears its warning on re-check or says why not.
//
// The matrix is kinds × EveryShape(); LORE_REST_LIFE_STRIDE=n keeps every
// n-th (kind, shape) cell (default 1: all of them).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// envRestStride keeps every n-th matrix cell (unset or 1 = all).
const envRestStride = "LORE_REST_LIFE_STRIDE"

// The setups the lifecycle drives; names no shape uses.
const (
	restSetup   = "rest-life"
	restRenamed = "rest-renamed"
	restOrg     = "restorg"
)

// restKinds are the setup kinds the matrix drives. Join a team and Start
// a team need a team store that is a hosted git repo (orgconf refuses a
// file:// one, TestRestTeamStoreURL): the team store is the GitHub url of
// fake_gitserver.go, which git rewrites to a bare repo on this disk
// (run:life:rest-kinds-open: only solo ran until then). A member joins
// with the invite of an admin on another machine (lsTeamFor), as a real
// teammate does.
var restKinds = []onboard.Kind{onboard.KindSolo, onboard.KindAdmin, onboard.KindMember}

func TestRestLifecycle(t *testing.T) {
	stride := 1
	if v, err := strconv.Atoi(os.Getenv(envRestStride)); err == nil && v > 1 {
		stride = v
	}
	cell := 0
	for _, shape := range EveryShape() {
		for _, kind := range restKinds {
			cell++
			if (cell-1)%stride != 0 {
				continue
			}
			t.Run(string(kind)+"/"+shape, func(t *testing.T) {
				t.Parallel()
				runRestLife(t, shape, kind)
			})
		}
	}
}

// restLife is one lifecycle run: the machine, its page session and the
// setups that must be gone.
type restLife struct {
	t    *testing.T
	m    *Machine
	p    *Page
	kind onboard.Kind
	gone Gone
	// personal is a file:// folder made for this run.
	personal string
	// team is the team store (fake_gitserver.go) an admin starts and a
	// member joins; invite is the join line a member setup uses.
	team   lsTeam
	invite string
	// doctored is, per setup, the checks the last doctor run reported.
	doctored map[string]map[onboard.CheckID]bool
	// baseWarnings are config check's warnings on the shape as built (a
	// hand-edited config.yaml's own unknown keys, foreign_handcfg): every
	// step must report exactly these, no more and none lost.
	baseWarnings []string
}

func runRestLife(t *testing.T, shape string, kind onboard.Kind) {
	m := Build(t, shape, loreBin)
	r := &restLife{t: t, m: m, kind: kind, gone: goneAtStart(shape)}
	if kind != onboard.KindSolo {
		lsGHHosts(t, m)
		r.team = lsTeamFor(t, m, string(kind), restSetup+"@example.test")
		r.invite = r.team.invite
	}
	r.p = m.StartPage(t)
	if lmBrokenHome(shape) {
		r.createRefused()
		return
	}
	r.baseWarnings = lmWarnings(t, m)
	r.consistent("start")
	r.walk(restSetup, kind, false)
	r.consistent("create")
	r.walk(restSetup, kind, true)
	r.consistent("edit")

	// rename: the trash's restore --as is the product's rename.
	r.must(opssetup.OpRemove, opssetup.RemoveIn{Name: restSetup}, nil)
	r.must(opssetup.OpTrashRestore, opssetup.RestoreIn{Name: restSetup, As: restRenamed}, nil)
	r.wantSetups("rename", []string{restRenamed}, []string{restSetup})
	r.consistent("rename")

	r.must(opssetup.OpEdit, opssetup.EditIn{Name: restRenamed, Default: true}, nil)
	r.wantDefault("default on", restRenamed, true)
	r.consistent("default on")
	r.must(opssetup.OpEdit, opssetup.EditIn{Name: restRenamed, NoDefault: true}, nil)
	r.wantDefault("default off", restRenamed, false)
	r.consistent("default off")

	r.must(opssetup.OpRemove, opssetup.RemoveIn{Name: restRenamed}, nil)
	r.must(opssetup.OpTrashPurge, opssetup.PurgeIn{Name: restRenamed, Confirm: true}, nil)
	r.gone.Purged = append(r.gone.Purged, restRenamed)
	r.wantSetups("purge", nil, []string{restRenamed})
	r.consistent("purge")

	r.walk(restSetup, kind, false)
	r.consistent("re-add")

	var scan opssetup.ImportScanOut
	r.must(opssetup.OpImportScan, opssetup.ImportScanIn{}, &scan)
	if len(scan.Proposals) > 0 {
		var setups opssetup.ImportSetups
		for _, pr := range scan.Proposals {
			setups = append(setups, pr.Setup)
		}
		var out opssetup.ImportOut
		r.must(opssetup.OpImport, opssetup.ImportIn{Setups: setups}, &out)
		r.consistent("import")
		// What was imported is no longer proposed.
		var again opssetup.ImportScanOut
		r.must(opssetup.OpImportScan, opssetup.ImportScanIn{}, &again)
		for _, pr := range again.Proposals {
			if slices.Contains(out.Imported, pr.Setup.Name) {
				t.Errorf("[import] %q imported, still proposed", pr.Setup.Name)
			}
		}
	}
	r.walk(restSetup, kind, true)
	r.consistent("edit again")

	r.must(opssetup.OpRemove, opssetup.RemoveIn{Name: restSetup}, nil)
	r.gone.Trashed = append(r.gone.Trashed, restSetup)
	r.gone.Purged = slices.DeleteFunc(r.gone.Purged, func(n string) bool { return n == restSetup })
	r.wantSetups("remove", nil, []string{restSetup})
	r.consistent("remove")
}

// createRefused is the lifecycle on a ~/.lore lore may not write (a file
// in its place, a 0500 folder, a 0444 config.yaml; lmBrokenHome): the page
// opens Home and walks a new setup's steps, and its first refused call
// must name ~/.lore and the fix (the REST twin of lsCreateRefused), never
// save every step.
func (r *restLife) createRefused() {
	t := r.t
	t.Helper()
	refused := func(op string, err error) {
		t.Helper()
		var pe *PageError
		switch {
		case !asPageError(err, &pe):
			t.Errorf("%s on a broken ~/.lore: %v, want a page error", op, err)
		case !strings.HasPrefix(string(pe.Body.Code), "E_"):
			t.Errorf("%s on a broken ~/.lore printed no E_ error: %v", op, err)
		case !strings.Contains(pe.Body.Message+pe.Body.Hint, ".lore"):
			t.Errorf("%s: the refusal does not name ~/.lore: %v", op, err)
		case pe.Body.Hint == "":
			t.Errorf("%s: the refusal names no fix: %v", op, err)
		}
	}
	var l opssetup.ListOut
	if err := r.p.Call(opssetup.OpList, opssetup.ListIn{}, &l); err != nil {
		refused(opssetup.OpList, err)
		return
	}
	planIn := opssetup.PlanIn{Setup: restSetup, Kind: string(r.kind)}
	var plan opssetup.PlanOut
	if err := r.p.Call(opssetup.OpPlan, planIn, &plan); err != nil {
		refused(opssetup.OpPlan, err)
		return
	}
	for _, step := range plan.Steps {
		if step.ID == onboard.StepSummary {
			continue
		}
		answers := restBaseline(step, progressIn(plan, step.ID))
		r.answersFor(restSetup, r.kind, step, answers)
		var out opssetup.ApplyOut
		if err := r.p.Call(opssetup.OpApply, opssetup.ApplyIn{Setup: restSetup, Step: string(step.ID), Answers: answers}, &out); err != nil {
			refused(opssetup.OpApply+" "+string(step.ID), err)
			return
		}
	}
	t.Errorf("every step of a new %s setup saved through the page on a ~/.lore lore may not write", r.kind)
}

// storeDir makes an empty folder for a file:// store.
func (r *restLife) storeDir(name string) string {
	d := filepath.Join(r.m.Dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		r.t.Fatal(err)
	}
	return d
}

// must calls op and fails the test on an error.
func (r *restLife) must(op string, in, out any) {
	r.t.Helper()
	if err := r.p.Call(op, in, out); err != nil {
		r.t.Fatalf("%s %s: %v", op, mustJSON(in), err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v) // test inputs always encode
	return string(b)
}

// answersFor is what the person types or picks on a step on top of what
// the page fills itself (restBaseline).
func (r *restLife) answersFor(name string, kind onboard.Kind, step opssetup.PlanStep, a onboard.Answers) {
	text := func(id, v string) { a[id] = onboard.Answer{Text: v} }
	list := func(id string, v ...string) { a[id] = onboard.Answer{List: v, IsList: true} }
	has := func(id string) bool {
		return slices.ContainsFunc(step.Questions, func(q opssetup.PlanQuestion) bool { return q.ID == id })
	}
	switch step.ID {
	case onboard.StepKind:
		text("kind", string(kind))
		text("name", name)
	case onboard.StepIdentity:
		if !filledAnswer(a["name"]) {
			text("name", PersonName)
		}
		text("email", name+"@example.test")
		if has("emails") {
			delete(a, "emails")
		}
	case onboard.StepGitHub:
		text("github_account", LoginAlpha)
	case onboard.StepPersonalStore:
		// A local folder store: this machine reaches no real GitHub.
		text("personal_store", "local")
		delete(a, "store_url")
	case onboard.StepTeam:
		text("org", restOrg)
		text("store_url", r.team.storeURL)
	case onboard.StepJoin:
		text("invite", r.invite)
	case onboard.StepTools:
		list("tools", "claude_code")
	case onboard.StepSecrets:
		text("backend", "none")
	case onboard.StepFolders:
		list("folders", "~/code/"+name+"/**")
	}
}

// restBaseline is the page's starting answers for a step (logic.ts
// baselineAnswers): the questions' values, the step's values, then the
// saved progress answers.
func restBaseline(step opssetup.PlanStep, p *onboard.Progress) onboard.Answers {
	out := onboard.Answers{}
	for _, q := range step.Questions {
		if !opssetup.PlanValue.IsZero(q.Value) {
			out[q.ID] = onboard.Answer(q.Value)
		}
	}
	for k, v := range step.Values {
		out[k] = v
	}
	if p != nil {
		for k, v := range p.Answers {
			out[k] = v
		}
	}
	return out
}

// filledAnswer: "" / [] / false are "not answered" (logic.ts isEmptyAnswer).
func filledAnswer(a onboard.Answer) bool {
	switch {
	case a.IsBool:
		return a.Bool
	case a.IsList:
		return len(a.List) > 0
	}
	return a.Text != ""
}

// walk saves every step of setup name in plan order, the way the page
// does: new mode (edit false) fills each step from the person's answers;
// edit mode opens the saved setup and saves each step on its saved
// answers (what the page shows when Save is pressed without a change).
func (r *restLife) walk(name string, kind onboard.Kind, edit bool) {
	t := r.t
	t.Helper()
	mode := "new"
	if edit {
		mode = "edit"
	}
	// The page opens an existing setup with its own kind; a new one with
	// the kind picked in Add setup.
	planIn := opssetup.PlanIn{Setup: name, Kind: string(kind)}
	if edit {
		planIn.Kind = ""
	}
	var plan opssetup.PlanOut
	r.must(opssetup.OpPlan, planIn, &plan)
	if edit {
		// Opened after a doctor run: each step's row carries the results
		// doctor has for its checks, or Summary and the rail say "not
		// checked" for checks that ran.
		ran := r.doctored[name]
		for _, step := range plan.Steps {
			p := progressIn(plan, step.ID)
			for _, id := range step.Checks {
				if !ran[id] {
					continue
				}
				if p == nil || !slices.ContainsFunc(p.Checks, func(c onboard.CheckResult) bool { return c.Check == id }) {
					t.Errorf("[open %s %s/%s] doctor ran %s, the opened step shows no result for it (not checked)", kind, name, step.ID, id)
				}
			}
		}
	}
	for i := 0; i < len(plan.Steps); i++ {
		step := plan.Steps[i]
		label := fmt.Sprintf("%s %s %s/%s", mode, kind, name, step.ID)
		answers := restBaseline(step, progressIn(plan, step.ID))
		if !edit {
			r.answersFor(name, kind, step, answers)
		} else if step.ID == onboard.StepKind {
			// An existing setup opens on its own name (StepsScreen baseFor).
			answers[onboard.QIDName] = onboard.Answer{Text: name}
		}
		if step.ID == onboard.StepSummary {
			answers = onboard.Answers{}
		}
		var chk opssetup.CheckOut
		if step.ID != onboard.StepSummary {
			r.must(opssetup.OpCheck, opssetup.CheckIn{Setup: name, Step: string(step.ID), Answers: answers}, &chk)
			r.judgeCheck(label, edit, step, chk)
			r.runFixes(label, name, step, answers, chk)
		}
		var out opssetup.ApplyOut
		err := r.p.Call(opssetup.OpApply, opssetup.ApplyIn{Setup: name, Step: string(step.ID), Answers: answers}, &out)
		if err != nil {
			var pe *PageError
			if edit && step.ID == onboard.StepKind && asPageError(err, &pe) && pe.Body.Code == errs.SetupNameTaken {
				// The person moves on to the next step; the rest is still judged.
				t.Errorf("[%s] the page opened existing setup %s for edit, its name check said %s, and Save answered %v", label, name, checkText(chk.Checks), err)
				r.must(opssetup.OpPlan, planIn, &plan)
				continue
			}
			t.Fatalf("[%s] save: %v\nanswers %s\ncheck: %s", label, err, mustJSON(answers), checkText(chk.Checks))
		}
		r.judgeSaved(label, step, out)
		r.must(opssetup.OpPlan, planIn, &plan)
	}
	// After Summary the setup is finished: every step shows done.
	for _, p := range plan.Progress {
		if p.Status != onboard.StepDone {
			t.Errorf("[%s %s] finished setup's step %s is %s", mode, name, p.Step, p.Status)
		}
	}
}

func asPageError(err error, pe **PageError) bool {
	p, ok := err.(*PageError)
	*pe = p
	return ok
}

func progressIn(plan opssetup.PlanOut, id onboard.StepID) *onboard.Progress {
	for i := range plan.Progress {
		if plan.Progress[i].Step == id {
			return &plan.Progress[i]
		}
	}
	return nil
}

// nameFreeCheck is the kind step's name check.
const nameFreeCheck onboard.CheckID = "setup.name_free"

// judgeCheck: the step's check shows a result for every check the plan
// lists (a missing one is "not checked" on Summary), and the name check of
// an existing setup opened for edit passes and says it is that setup.
func (r *restLife) judgeCheck(label string, edit bool, step opssetup.PlanStep, chk opssetup.CheckOut) {
	t := r.t
	t.Helper()
	if len(chk.FieldErrors) > 0 {
		t.Errorf("[%s] check: field errors on the page's own answers: %+v", label, chk.FieldErrors)
	}
	r.coversPlan(label+" check", step, chk.Checks)
	if !edit || step.ID != onboard.StepKind {
		return
	}
	for _, c := range chk.Checks {
		if c.Check != nameFreeCheck {
			continue
		}
		if c.Status != onboard.StatusOK {
			t.Errorf("[%s] existing setup's name check: %s %s", label, c.Status, c.Detail)
		}
		if strings.Contains(strings.ToLower(c.Detail), "free") {
			t.Errorf("[%s] existing setup's name check says the name is free: %q", label, c.Detail)
		}
	}
}

// coversPlan: every check the plan lists for the step has a result.
func (r *restLife) coversPlan(label string, step opssetup.PlanStep, got []onboard.CheckResult) {
	r.t.Helper()
	for _, id := range step.Checks {
		if !slices.ContainsFunc(got, func(c onboard.CheckResult) bool { return c.Check == id }) {
			r.t.Errorf("[%s] plan lists check %s, no result for it (Summary: not checked); got %s", label, id, checkText(got))
		}
	}
}

// judgeSaved: a saved step shows its checks (unless skipped).
func (r *restLife) judgeSaved(label string, step opssetup.PlanStep, out opssetup.ApplyOut) {
	r.t.Helper()
	if out.Status == onboard.StepSkipped {
		return
	}
	if out.Status != onboard.StepDone {
		r.t.Errorf("[%s] save status %s", label, out.Status)
	}
	r.coversPlan(label+" save", step, out.Checks)
}

// fixRunIn is setup.fix's body as the page sends it after its confirm.
type fixRunIn struct {
	opssetup.FixIn
	Yes bool `json:"yes"`
}

// runFixes clicks "Run it" on every warning or failure with a runnable
// fix; the fix clears the warning on re-check, or says why not.
func (r *restLife) runFixes(label, name string, step opssetup.PlanStep, answers onboard.Answers, chk opssetup.CheckOut) {
	t := r.t
	t.Helper()
	for _, c := range chk.Checks {
		if (c.Status != onboard.StatusFail && c.Status != onboard.StatusWarn) || !c.Fix.Runnable {
			continue
		}
		var fx opssetup.FixOut
		err := r.p.Call(opssetup.OpFix, fixRunIn{FixIn: opssetup.FixIn{Setup: name, Check: string(c.Check), Target: c.Target}, Yes: true}, &fx)
		if err != nil {
			var pe *PageError
			if !asPageError(err, &pe) || strings.TrimSpace(pe.Body.Message) == "" {
				t.Errorf("[%s] Run it on %s: %v (no reason given)", label, c.Check, err)
			}
			continue
		}
		var again opssetup.CheckOut
		r.must(opssetup.OpCheck, opssetup.CheckIn{Setup: name, Step: string(step.ID), Answers: answers}, &again)
		still := slices.ContainsFunc(again.Checks, func(x onboard.CheckResult) bool {
			return x.Check == c.Check && x.Target == c.Target && x.Status == c.Status
		})
		if still && fx.StillFailing == "" && fx.Exit == 0 {
			t.Errorf("[%s] Run it on %s (%s) ran %v, exit 0, the re-check still says %s and nothing says why", label, c.Check, c.Detail, fx.Argv, c.Status)
		}
	}
}

// wantSetups: every want is listed, every not is not (REST setup.list).
func (r *restLife) wantSetups(label string, want, not []string) {
	r.t.Helper()
	var l opssetup.ListOut
	r.must(opssetup.OpList, opssetup.ListIn{}, &l)
	for _, n := range want {
		if !slices.ContainsFunc(l.Setups, func(s opssetup.SetupSummary) bool { return s.Name == n }) {
			r.t.Errorf("[%s] setup.list lacks %s", label, n)
		}
	}
	for _, n := range not {
		if slices.ContainsFunc(l.Setups, func(s opssetup.SetupSummary) bool { return s.Name == n }) {
			r.t.Errorf("[%s] setup.list still shows %s", label, n)
		}
	}
}

// wantDefault: name is (or is not) the default, and no other is when it is.
func (r *restLife) wantDefault(label, name string, on bool) {
	r.t.Helper()
	var l opssetup.ListOut
	r.must(opssetup.OpList, opssetup.ListIn{}, &l)
	for _, s := range l.Setups {
		switch {
		case s.Name == name && s.Default != on:
			r.t.Errorf("[%s] %s default=%v, want %v", label, name, s.Default, on)
		case s.Name != name && on && s.Default:
			r.t.Errorf("[%s] %s made default, %s still default too", label, name, s.Name)
		}
	}
}

// cliList is the part of `lore setup list --json` compared with the page.
type cliList struct {
	Setups []struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Default  bool   `json:"default"`
		Disabled bool   `json:"disabled"`
	} `json:"setups"`
	Active struct {
		Name string `json:"name"`
	} `json:"active"`
	Trashed int `json:"trashed"`
}

// consistent asserts the page agrees with itself and with the CLI on the
// same HOME after a transition.
func (r *restLife) consistent(step string) {
	t, m := r.t, r.m
	t.Helper()
	label := string(r.kind) + "/" + m.Shape + "/" + step
	var l opssetup.ListOut
	r.must(opssetup.OpList, opssetup.ListIn{}, &l)
	if len(l.ConfigProblems) > 0 {
		t.Errorf("[%s] setup.list config problems: %+v", label, l.ConfigProblems)
	}
	cr := m.Lore(t, "", "setup", "list", "--json")
	var cl cliList
	if cr.Code != 0 || json.Unmarshal([]byte(cr.Stdout), &cl) != nil {
		t.Fatalf("[%s] setup list: %s", label, cr)
	}
	pageList := func() []string {
		var out []string
		for _, s := range l.Setups {
			out = append(out, fmt.Sprintf("%s/%s default=%v disabled=%v", s.Name, s.Kind, s.Default, s.Disabled))
		}
		return out
	}()
	var cliSetups []string
	for _, s := range cl.Setups {
		cliSetups = append(cliSetups, fmt.Sprintf("%s/%s default=%v disabled=%v", s.Name, s.Kind, s.Default, s.Disabled))
	}
	if !slices.Equal(pageList, cliSetups) {
		t.Errorf("[%s] page setup.list %v, CLI setup list %v", label, pageList, cliSetups)
	}
	if l.Trashed != cl.Trashed {
		t.Errorf("[%s] page says %d trashed, CLI %d", label, l.Trashed, cl.Trashed)
	}
	var trash opssetup.TrashListOut
	r.must(opssetup.OpTrashList, opssetup.TrashListIn{}, &trash)
	if len(trash.Setups) != l.Trashed {
		t.Errorf("[%s] Home says %d trashed, the trash lists %d", label, l.Trashed, len(trash.Setups))
	}

	// doctor: the page's run and the CLI's agree, and Home's Health cell
	// is the doctor's count once doctor ran.
	var doc opsdoctor.Report
	r.must(opsdoctor.OpRun, opsdoctor.RunIn{All: true}, &doc)
	dr := m.Lore(t, "", "doctor", "--all", "--json")
	var cd opsdoctor.Report
	if !slices.Contains(doctorExits, dr.Code) || json.Unmarshal([]byte(dr.Stdout), &cd) != nil {
		t.Fatalf("[%s] doctor --all: %s", label, dr)
	}
	if got, want := doctorView(doc), doctorView(cd); !slices.Equal(got, want) {
		t.Errorf("[%s] page doctor and CLI doctor --all disagree:\npage %v\ncli  %v", label, got, want)
	}
	if doc.Status != cd.Status {
		t.Errorf("[%s] page doctor status %s, CLI %s", label, doc.Status, cd.Status)
	}
	r.doctored = map[string]map[onboard.CheckID]bool{}
	for _, s := range doc.Setups {
		r.doctored[s.Name] = map[onboard.CheckID]bool{}
		for _, c := range s.Checks {
			r.doctored[s.Name][c.Check] = true
		}
	}
	var after opssetup.ListOut
	r.must(opssetup.OpList, opssetup.ListIn{}, &after)
	for _, s := range doc.Setups {
		i := slices.IndexFunc(after.Setups, func(x opssetup.SetupSummary) bool { return x.Name == s.Name })
		if i < 0 {
			t.Errorf("[%s] doctor checks %s, setup.list has no such setup", label, s.Name)
			continue
		}
		if h, want := after.Setups[i].Health, opssetup.HealthCell(s.Checks); h != want {
			t.Errorf("[%s] Home's Health for %s is %q, doctor's checks give %q (%s)", label, s.Name, h, want, checkText(s.Checks))
		}
		bad := 0
		for _, c := range s.Checks {
			if needsLook(c) {
				bad++
				if strings.TrimSpace(c.Detail) == "" {
					t.Errorf("[%s] doctor: %s %s %s without saying what is wrong", label, s.Name, c.Check, c.Status)
				}
			}
		}
		if bad == 0 && s.Status != opsdoctor.StatusHealthy {
			t.Errorf("[%s] %s: every check passes, doctor says %s (a repair signal)", label, s.Name, s.Status)
		}
		if bad == 0 && (strings.HasPrefix(after.Setups[i].Health, "⚠") || strings.HasPrefix(after.Setups[i].Health, "✗")) {
			t.Errorf("[%s] %s: every check passes, Home shows %q", label, s.Name, after.Setups[i].Health)
		}
	}
	if step != "start" { // a shape's own hand-written flow style stays until lore writes
		r.checkConfigFile(label)
	}
	if len(r.baseWarnings) == 0 {
		m.CheckMachine(t, label, r.gone)
		return
	}
	// CheckMachine wants no config warning at all; this shape has the
	// person's own (as lifeModel.checkModel does): the warnings compared
	// to the shape's, then doctor and file modes.
	if w := lmWarnings(t, m); !slices.Equal(w, r.baseWarnings) {
		t.Errorf("[%s] config check warns %v, the shape had %v", label, w, r.baseWarnings)
	}
	m.CheckDoctor(t, label)
	m.CheckModes(t, label)
}

// needsLook is the page's "to look at" rule (check-groups.ts): not ok,
// not skipped, not an info note.
func needsLook(c onboard.CheckResult) bool {
	switch c.Status {
	case onboard.StatusWarn, onboard.StatusFail, onboard.StatusError:
		return c.Level != onboard.LevelInfo
	}
	return false
}

// doctorView is a doctor report as comparable lines: setup, status and
// each check's id, target and status.
func doctorView(d opsdoctor.Report) []string {
	var out []string
	for _, s := range d.Setups {
		out = append(out, s.Name+" "+string(s.Status))
		for _, c := range s.Checks {
			out = append(out, fmt.Sprintf("  %s %s %s", c.Check, c.Target, c.Status))
		}
	}
	return out
}

// checkConfigFile: config.yaml parses, and what lore wrote for this run's
// setups is in block style: the setups list holding them and each of their
// entries (setups: [...] after purge + re-add was the real-Mac bug). A
// shape's own hand-written flow lists elsewhere stay as the person wrote
// them.
func (r *restLife) checkConfigFile(label string) {
	raw, err := os.ReadFile(filepath.Join(r.m.Home, ".lore", "config.yaml"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		r.t.Fatalf("[%s] config.yaml: %v", label, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		r.t.Errorf("[%s] config.yaml does not parse: %v\n%s", label, err, raw)
		return
	}
	if len(doc.Content) == 0 {
		return
	}
	flow := func(x *yaml.Node) bool {
		return (x.Kind == yaml.SequenceNode || x.Kind == yaml.MappingNode) && x.Style&yaml.FlowStyle != 0 && len(x.Content) > 0
	}
	var walk func(*yaml.Node) bool
	walk = func(x *yaml.Node) bool {
		if flow(x) {
			r.t.Errorf("[%s] config.yaml line %d, written by lore, is in flow style:\n%s", label, x.Line, raw)
			return false
		}
		for _, c := range x.Content {
			if !walk(c) {
				return false
			}
		}
		return true
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "setups" {
			continue
		}
		list := top.Content[i+1]
		for _, e := range list.Content {
			if !strings.HasPrefix(entryName(e), "rest-") {
				continue
			}
			if flow(list) {
				r.t.Errorf("[%s] config.yaml setups list (line %d) holding a setup lore wrote is in flow style:\n%s", label, list.Line, raw)
				return
			}
			walk(e)
		}
	}
}

// entryName is the name: value of a setups[] mapping node.
func entryName(e *yaml.Node) string {
	for i := 0; i+1 < len(e.Content); i += 2 {
		if e.Content[i].Value == "name" {
			return e.Content[i+1].Value
		}
	}
	return ""
}

func checkText(cs []onboard.CheckResult) string {
	var parts []string
	for _, c := range cs {
		s := string(c.Check) + "=" + string(c.Status)
		if c.Target != "" {
			s += "[" + c.Target + "]"
		}
		if c.Status != onboard.StatusOK && c.Detail != "" {
			s += "(" + c.Detail + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func progressText(ps []onboard.Progress) string {
	var parts []string
	for _, p := range ps {
		parts = append(parts, string(p.Step)+"="+string(p.Status))
	}
	return strings.Join(parts, " ")
}

// TestRestTeamStoreURL: the team step's store.url check and its Save agree
// on a folder url. Before the fix the check passed file://<folder> (✓
// "Store url is valid") and Save answered E_CONFIG_INVALID: the org entry
// in config.yaml must hold a hosted repo url.
func TestRestTeamStoreURL(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	r := &restLife{t: t, m: m, kind: onboard.KindAdmin}
	folder := r.storeDir("team-store")
	r.p = m.StartPage(t)
	var plan opssetup.PlanOut
	r.must(opssetup.OpPlan, opssetup.PlanIn{Setup: restSetup, Kind: string(onboard.KindAdmin)}, &plan)
	for _, step := range plan.Steps {
		if step.ID == onboard.StepTeam {
			break
		}
		if step.ID != onboard.StepKind && step.ID != onboard.StepIdentity && step.ID != onboard.StepGitHub {
			continue
		}
		a := restBaseline(step, nil)
		r.answersFor(restSetup, onboard.KindAdmin, step, a)
		r.must(opssetup.OpApply, opssetup.ApplyIn{Setup: restSetup, Step: string(step.ID), Answers: a}, nil)
	}
	for _, tc := range []struct {
		url    string
		wantOK bool
	}{
		{"file://" + folder, false},
		{"git@github.com:" + LoginAlpha + "/lore-team.git", true},
	} {
		a := onboard.Answers{"org": {Text: restOrg}, "store_url": {Text: tc.url}}
		var chk opssetup.CheckOut
		r.must(opssetup.OpCheck, opssetup.CheckIn{Setup: restSetup, Step: string(onboard.StepTeam), Answers: a}, &chk)
		var url onboard.CheckResult
		for _, c := range chk.Checks {
			if c.Check == onboard.CheckStoreURL {
				url = c
			}
		}
		if got := url.Status == onboard.StatusOK; got != tc.wantOK || (!tc.wantOK && !strings.Contains(url.Detail, "hosted git repo")) {
			t.Errorf("team store %s: store.url %s %q, want ok=%v (field errors %+v)", tc.url, url.Status, url.Detail, tc.wantOK, chk.FieldErrors)
		}
		if tc.wantOK {
			continue
		}
		err := r.p.Call(opssetup.OpApply, opssetup.ApplyIn{Setup: restSetup, Step: string(onboard.StepTeam), Answers: a}, nil)
		var pe *PageError
		if !asPageError(err, &pe) || pe.Body.Code != errs.SetupStepFailed || !strings.Contains(pe.Body.Message, string(onboard.CheckStoreURL)) {
			t.Errorf("team store %s: Save answered %v, want E_SETUP_STEP_FAILED naming store.url (what the check showed)", tc.url, err)
		}
	}
}
