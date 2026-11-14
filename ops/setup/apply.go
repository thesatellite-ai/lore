package setup

// apply.go — setup.apply (ONBOARDING_DESIGN 2.3, 7.1, 7.2): the one write
// of a step, after its Needs are done and none of its error-level checks
// fails. Applier compares first, so a re-run reports changed=false
// ("unchanged") and writes nothing. The summary step writes nothing and
// deletes the setup's progress once every step is done or skipped (4.11).

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Apply is setup.apply.
//
//	order (Needs done) ── no ─▶ E_SETUP_STEP_ORDER
//	optional step with questions, no answers ─▶ skipped (nothing probed or written)
//	validate ── bad ─▶ E_INVALID_INPUT (the field errors)
//	probe ─▶ any error-level fail ─▶ progress failed ─▶ E_SETUP_STEP_FAILED
//	Applier.Apply ─▶ re-probe once ─▶ progress done (summary: delete all progress when complete)
func (e engine) Apply(ctx context.Context, in ApplyIn) (ApplyOut, error) {
	name := newSetupName(ctx, in.Setup, in.Step, in.Answers)
	if err := requireName(name); err != nil {
		return ApplyOut{}, err
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return ApplyOut{}, err
	}
	s, err := e.load(ctx, st, name, in.Answers)
	if err != nil {
		return ApplyOut{}, err
	}
	step, err := s.step(in.Step)
	if err != nil {
		return ApplyOut{}, err
	}
	if step.ID == onboard.StepKind {
		if err := e.nameTaken(ctx, st, s, in.Answers); err != nil {
			return ApplyOut{}, err
		}
	}
	if missing := s.missingNeeds(step); len(missing) > 0 {
		return ApplyOut{}, errs.New(errs.SetupStepOrder, "step "+string(step.ID)+" needs "+joinSteps(missing)+" first").
			WithNext("lore", "setup", "apply", "--setup", name, "--step", string(missing[0]))
	}
	answers := in.Answers
	if answers == nil {
		answers = s.progress[step.ID].Answers
	}
	answers = e.withoutUnasked(s, step, answers)
	step = e.askedStep(s, step, answers)
	answers, repeated := withoutDuplicateFolders(answers)
	out := ApplyOut{Step: step.ID, Wrote: []string{}, Checks: []onboard.CheckResult{}, Warnings: folderRepeatWarnings(repeated)}
	if step.Optional && len(answers) == 0 && len(step.Questions) > 0 {
		// An optional step applied with no answers is skipped: nothing
		// probed, nothing written; doctor still reports it (2.2 Optional).
		// A step with no questions (this_folder, 4.10) has no answers to
		// leave out: saving it runs its checks and marks it done, so the
		// summary counts it (skipping it is the page's Skip, which never
		// calls apply).
		out.Status = onboard.StepSkipped
		return out, e.saveDone(ctx, st, s, step, onboard.Progress{Setup: name, Step: step.ID, Answers: onboard.Answers{}, Checks: []onboard.CheckResult{}, Status: onboard.StepSkipped})
	}
	if joinedAlready(s, step, answers) {
		// A member setup that joined its team, its join step saved again
		// with no invite (the line is never kept): nothing to re-join, so
		// nothing is written. Before, this refused "Invite is required".
		out.Status, out.Checks = onboard.StepDone, e.joinedChecks(step)
		return out, e.saveDone(ctx, st, s, step, onboard.Progress{Setup: name, Step: step.ID, Answers: onboard.Answers{}, Checks: out.Checks, Status: onboard.StepDone})
	}
	if step.ID == onboard.StepKind {
		if answers, err = e.withKindDefault(ctx, st, s, step, answers); err != nil {
			return ApplyOut{}, err
		}
	}
	answers, routeErr := e.deriveRoute(ctx, step, answers)
	if fe := append(e.validateStep(ctx, step, askedKind(s, step, answers), answers), routeErr...); len(fe) > 0 {
		msgs := make([]string, 0, len(fe))
		for _, f := range fe {
			msgs = append(msgs, f.Question+": "+f.Message)
		}
		return ApplyOut{}, errs.New(errs.InvalidInput, "step "+string(step.ID)+" has "+itoa(len(fe))+" invalid answer(s): "+strings.Join(msgs, "; ")).
			WithNext("lore", "setup", "check", "--setup", name, "--step", string(step.ID))
	}
	// The save gate reuses what a check (or a fix's re-check) found for
	// these same answers in the last onboard.CheckFreshTTL; only missing,
	// failed or stale checks run (owner 2026-11-07: Save & next re-ran
	// all five store checks right after a fix had re-checked them).
	gate := onboard.FreshChecks(s.progress[step.ID], answers, e.now(), onboard.StatusOK, onboard.StatusSkip, onboard.StatusWarn)
	checks, err := e.probeReusing(ctx, s, step, answers, gate)
	if err != nil {
		return ApplyOut{}, err
	}
	if bad := blocking(checks); len(bad) > 0 {
		if !s.finished() {
			if err := st.PutProgress(ctx, onboard.Progress{Setup: name, Step: step.ID, Answers: answers, Checks: checks, Status: onboard.StepFailed}); err != nil {
				return ApplyOut{}, err
			}
		}
		// Each failing check with its detail, so the refusal itself says why
		// (the trashed namesake of a team, a store that cannot be read)
		// instead of only naming check ids.
		ids := make([]string, 0, len(bad))
		for _, id := range bad {
			ids = append(ids, failingWithDetail(checks, id))
		}
		return ApplyOut{}, errs.New(errs.SetupStepFailed, "step "+string(step.ID)+" has "+itoa(len(bad))+" failing check(s): "+strings.Join(ids, ", ")+"; fix them (lore setup check …) before saving").
			WithNext("lore", "setup", "check", "--setup", name, "--step", string(step.ID))
	}
	if step.ID == onboard.StepKind {
		if s, err = e.renameOnKind(ctx, st, s, answers); err != nil {
			return ApplyOut{}, err
		}
		name = s.name
	}
	if hs, ok := e.env.(HealthSource); ok && s.finished() {
		// Read before the write: an edit that normalizes the entry (fills
		// github route and key_fingerprint, drops a duplicate email) makes
		// SetupHealth answer nothing afterwards, and the other steps' kept
		// results would be lost with it.
		s.prior, _ = hs.SetupHealth(ctx, name)
	}
	// Judged before the write: the applier hands back the entry it would
	// save (res.Setup) even while the setup's own entry step is pending.
	out.Warnings = append(out.Warnings, pendingEntryWarnings(s, step)...)
	if step.Apply != onboard.ApplyNone {
		ap, err := e.env.SetupApplier(ctx)
		if err != nil {
			return ApplyOut{}, err
		}
		res, err := ap.Apply(ctx, ApplyRequest{Setup: s.entry, Step: step.ID, Apply: step.Apply, Answers: answers})
		if err != nil {
			return ApplyOut{}, err
		}
		out.Changed = res.Changed
		if res.Wrote != nil {
			out.Wrote = res.Wrote
		}
		if res.Setup.Name != "" {
			s.entry, s.exists = res.Setup, true
			if err := e.keepJoinedKind(ctx, st, s, step); err != nil {
				return ApplyOut{}, err
			}
		}
		// 2.3: "step status done; re-run its checks once" — what the
		// page shows after the save is the state after the write. The
		// checks that passed just now stand; the ones that warned (the
		// write is often their fix: store.marker before store init) run
		// again.
		after := onboard.FreshChecks(onboard.Progress{Answers: answers, Checks: checks}, answers, e.now(), onboard.StatusOK, onboard.StatusSkip)
		if checks, err = e.probeReusing(ctx, s, step, answers, after); err != nil {
			return ApplyOut{}, err
		}
	}
	reopen, err := e.reopens(ctx, st, s, step, answers)
	if err != nil {
		return ApplyOut{}, err
	}
	if reopen {
		s.done = false
	}
	out.Status, out.Checks = onboard.StepDone, checks
	return out, e.saveDone(ctx, st, s, step, onboard.Progress{Setup: name, Step: step.ID, Answers: answers, Checks: checks, Status: onboard.StepDone})
}

// keepJoinedKind records the kind a join step gave the setup in the saved
// kind step. Why (I-17): the org's admin joining from a second machine
// answers the kind step "member" (the join lines say so: the roster is not
// read before the join), and the join makes the setup an admin setup; the
// saved kind step wins over the entry on every later step (loadState), so
// the next step (folders) made it a member setup again.
func (e engine) keepJoinedKind(ctx context.Context, st Store, s stepState, step onboard.Step) error {
	if step.ID != onboard.StepJoin || s.entry.Kind != onboard.KindAdmin {
		return nil
	}
	row, ok := s.progress[onboard.StepKind]
	if !ok || answerKind(row.Answers) == onboard.KindAdmin {
		return nil
	}
	ans := maps.Clone(row.Answers)
	if ans == nil {
		ans = onboard.Answers{}
	}
	ans[kindAnswer] = onboard.Answer{Text: string(onboard.KindAdmin)}
	row.Answers = ans
	return st.PutProgress(ctx, row)
}

// withKindDefault fills a kind-step answer set that leaves out default
// with the value setup plan shows for it (questionValue: the saved
// entry's flag, else true while no setup is the default). The page always
// sends the plan's value; without this a CLI `setup apply --step kind`
// with only kind and name made the first solo setup a non-default one,
// unlike the page. answers is copied, never changed in place.
func (e engine) withKindDefault(ctx context.Context, st Store, s stepState, step onboard.Step, answers onboard.Answers) (onboard.Answers, error) {
	if _, given := answers[onboard.QIDDefault]; given {
		return answers, nil
	}
	i := slices.IndexFunc(step.Questions, func(q onboard.Question) bool { return q.ID == onboard.QIDDefault })
	kind := askedKind(s, step, answers)
	if i < 0 || !questionApplies(step.Questions[i], kind) {
		return answers, nil
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return nil, err
	}
	_, saved := s.progress[step.ID]
	v, ok := questionValue(step.ID, step.Questions[i], kind, s.entry, s.exists, saved, cfg, nil)
	if !ok {
		return answers, nil
	}
	out := make(onboard.Answers, len(answers)+1)
	maps.Copy(out, answers)
	out[onboard.QIDDefault] = v
	return out, nil
}

// askedKind is the kind a step's questions are asked for: the setup's,
// except on the kind step, whose answer decides it (a setup reopened as
// admin and switched back to solo answers the solo questions, default
// among them; judged by the old kind they were refused as not asked).
func askedKind(s stepState, step onboard.Step, answers onboard.Answers) onboard.Kind {
	if k := answerKind(answers); step.ID == onboard.StepKind && k.Valid() {
		return k
	}
	return s.kind
}

// askedStep is step as it is asked for the kind its answers choose: on
// the kind step that kind's catalog entry (the plan holds the setup's
// kind; the default question is solo only), any other step as it is.
func (e engine) askedStep(s stepState, step onboard.Step, answers onboard.Answers) onboard.Step {
	k := askedKind(s, step, answers)
	if step.ID != onboard.StepKind || k == s.kind {
		return step
	}
	for _, st := range e.cat.Plan(k) {
		if st.ID == onboard.StepKind {
			return st
		}
	}
	return step
}

// withoutUnasked is the kind step's answers without those to a question
// the kind step asks some kind but not the answered one (default, for a
// team kind): switching a setup's kind drops them instead of refusing the
// save over a field the page no longer shows. An id no kind's kind step
// asks stays, and validation refuses it. answers is copied, never changed.
func (e engine) withoutUnasked(s stepState, step onboard.Step, answers onboard.Answers) onboard.Answers {
	if step.ID != onboard.StepKind || len(answers) == 0 {
		return answers
	}
	asked := map[string]bool{}
	for _, q := range e.askedStep(s, step, answers).Questions {
		asked[q.ID] = asked[q.ID] || questionApplies(q, askedKind(s, step, answers))
	}
	var out onboard.Answers
	for _, k := range onboard.Kinds() {
		for _, st := range e.cat.Plan(k) {
			if st.ID != onboard.StepKind {
				continue
			}
			for _, q := range st.Questions {
				if _, given := answers[q.ID]; given && !asked[q.ID] {
					if out == nil {
						out = maps.Clone(answers)
					}
					delete(out, q.ID)
				}
			}
		}
	}
	if out == nil {
		return answers
	}
	return out
}

// questionApplies reports whether q is asked for kind (no Kinds: every
// kind).
func questionApplies(q onboard.Question, kind onboard.Kind) bool {
	return len(q.Kinds) == 0 || slices.Contains(q.Kinds, kind)
}

// nameTaken is the E_SETUP_NAME_TAKEN gate of the kind step (ONB 4.1,
// 7.2), the same rule as the setup.name_free check and setup.rename: a
// setup's own name is never a clash, a name another setups[] entry holds
// is. Opening a setup that exists edits it (owner report 2026-11-08: the
// kind step of a finished setup refused its own name while name_free said
// "name is free"); a new name there is a rename (renameSetup).
//
//	target = answers.name, else the setup named by the call
//	target == the call's setup ─▶ fine (a new setup, or an edit of this one)
//	target != the call's setup ─▶ taken when a setups[] entry has that name
func (e engine) nameTaken(ctx context.Context, st Store, s stepState, answers onboard.Answers) error {
	target := strings.TrimSpace(answers[onboard.QIDName].Text)
	if target == "" || target == s.name {
		return nil
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return err
	}
	if _, ok := cfg.Setup(target); ok {
		return nameTakenErr(target)
	}
	return nil
}

// renameOnKind is the kind step's rename: a setup that exists (or has
// saved progress) answered with a new name is renamed before the step's
// write, so the write edits it instead of making a second setup. s is
// returned under the new name.
func (e engine) renameOnKind(ctx context.Context, st Store, s stepState, answers onboard.Answers) (stepState, error) {
	target := strings.TrimSpace(answers[onboard.QIDName].Text)
	if target == "" || target == s.name || (!s.exists && len(s.progress) == 0) {
		return s, nil
	}
	if err := e.renameSetup(ctx, st, s.name, target); err != nil {
		return s, err
	}
	s.name, s.entry.Name = target, target
	for id, p := range s.progress {
		p.Setup = target
		s.progress[id] = p
	}
	return s, nil
}

// reopens reports a kind-step save of a finished setup whose new kind was
// not written: a solo setup turning into a team one (member, admin) is
// written whole by the join or team step, so until then the setup is
// unfinished again (its kind row kept; load reads the kind from it) and
// the steps the new kind asks for are saved one by one.
func (e engine) reopens(ctx context.Context, st Store, s stepState, step onboard.Step, answers onboard.Answers) (bool, error) {
	k := answerKind(answers)
	if step.ID != onboard.StepKind || !s.finished() || !k.Valid() {
		return false, nil
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return false, err
	}
	cur, ok := cfg.Setup(s.name)
	return ok && cur.Kind != k, nil
}

// saveDone records a done or skipped step. On the summary step of a
// complete setup it deletes every progress row instead (4.11: progress
// exists only for an unfinished setup); a finished setup gains no rows.
func (e engine) saveDone(ctx context.Context, st Store, s stepState, step onboard.Step, p onboard.Progress) error {
	s.progress[step.ID] = p
	if step.ID == onboard.StepSummary && s.complete() {
		if err := st.DeleteProgress(ctx, s.name); err != nil {
			return err
		}
		e.keepFinishedResults(ctx, s)
		return nil
	}
	if s.finished() {
		e.keepStepResults(ctx, s, step, p.Checks)
		return nil
	}
	return st.PutProgress(ctx, p)
}

// keepFinishedResults hands the newest check results of every step (the
// progress rows just deleted) to the HealthRecorder, in plan order, so the
// finished setup's Summary and Home show the checks that ran. Bookkeeping:
// a failed write only leaves the setup "not checked yet" until doctor runs.
func (e engine) keepFinishedResults(ctx context.Context, s stepState) {
	hr, ok := e.env.(HealthRecorder)
	if !ok {
		return
	}
	var results []onboard.CheckResult
	for _, st := range s.plan {
		results = append(results, s.progress[st.ID].Checks...)
	}
	if len(results) == 0 {
		return
	}
	_ = hr.RecordHealth(ctx, s.name, results)
}

// keepStepResults: a step of a finished setup was just saved (an edit), and
// rs are its checks of exactly the answers saved, so they replace that
// step's results in the HealthRecorder. The other steps' results are the
// ones kept before this write (s.prior): a step's save writes only its own
// fields, so their checks still stand, even when the save rewrote the
// entry (which makes SetupHealth answer nothing until the next record).
// Without it, an edited setup's Summary said "not checked" for the steps
// saved before the last entry-changing one, once the page was reloaded.
func (e engine) keepStepResults(ctx context.Context, s stepState, step onboard.Step, rs []onboard.CheckResult) {
	hr, ok := e.env.(HealthRecorder)
	if !ok || len(rs) == 0 {
		return
	}
	old := s.prior
	if hs, ok := e.env.(HealthSource); ok && old == nil {
		old, _ = hs.SetupHealth(ctx, s.name)
	}
	var keep []onboard.CheckResult
	for _, r := range old {
		if !slices.Contains(step.Checks, r.Check) {
			keep = append(keep, r)
		}
	}
	_ = hr.RecordHealth(ctx, s.name, append(keep, rs...))
}

// failingWithDetail is one failing check of a refused save: its id and,
// when it has one, its detail ("org.name_free (team hub belongs to …)").
func failingWithDetail(checks []onboard.CheckResult, id onboard.CheckID) string {
	for _, c := range checks {
		if c.Check == id && c.Status == onboard.StatusFail && strings.TrimSpace(c.Detail) != "" {
			return string(id) + " (" + strings.TrimSpace(c.Detail) + ")"
		}
	}
	return string(id)
}

// joinedAlready reports the join step of a member setup that already
// joined (its entry names an org) saved or checked with no invite: the
// invite line is a one-time token lore never stores, so an edit of a
// joined setup has none to send, and there is nothing to re-join.
func joinedAlready(s stepState, step onboard.Step, answers onboard.Answers) bool {
	return step.ID == onboard.StepJoin && s.exists && s.entry.Org != "" && strings.TrimSpace(answers[onboard.QIDInvite].Text) == ""
}

// joinedChecks are the results a joined setup's join step shows on an
// edit (joinedAlready): every check the plan lists, skipped with the
// reason. Without them the step and Summary said "not checked" for six
// checks the page had just run (run:life:rest-kinds-open, member edit).
func (e engine) joinedChecks(step onboard.Step) []onboard.CheckResult {
	out := make([]onboard.CheckResult, 0, len(step.Checks))
	for _, id := range step.Checks {
		out = append(out, onboard.CheckResult{Check: id, Status: onboard.StatusSkip, Level: onboard.LevelInfo, Detail: onboard.SkipJoinedAlready, CheckedAt: e.now()})
	}
	return out
}

// complete reports every plan step done or skipped.
func (s stepState) complete() bool {
	for _, st := range s.plan {
		p, ok := s.progress[st.ID]
		if !ok || (p.Status != onboard.StepDone && p.Status != onboard.StepSkipped) {
			return false
		}
	}
	return true
}

// missingNeeds lists the step's Needs not done or skipped yet, in order.
func (s stepState) missingNeeds(step onboard.Step) []onboard.StepID {
	var out []onboard.StepID
	for _, n := range step.Needs {
		if !s.stepDone(n) {
			out = append(out, n)
		}
	}
	return out
}

// joinSteps is "a, b".
func joinSteps(ids []onboard.StepID) string {
	s := make([]string, 0, len(ids))
	for _, id := range ids {
		s = append(s, string(id))
	}
	return strings.Join(s, ", ")
}

// withoutDuplicateFolders is a with the folders answer's repeats dropped
// (the first of each kept, order unchanged): `--folder ~/code/** --folder
// ~/code/**` saves one glob, not a setup that lists it twice. repeated
// names each dropped folder once (uniqueFolders), so the caller can say
// so. a itself is never changed (it may be the caller's map); nil stays
// nil.
func withoutDuplicateFolders(a onboard.Answers) (onboard.Answers, []string) {
	f, ok := a[onboard.QIDFolders]
	if !ok || !f.IsList {
		return a, nil
	}
	out := maps.Clone(a)
	var repeated []string
	f.List, repeated = uniqueFolders(f.List)
	out[onboard.QIDFolders] = f
	return out, repeated
}

// uniqueFolders is l without repeats (compared with surrounding spaces
// trimmed), the first of each kept in order; nil stays nil. repeated is
// every folder given more than once, trimmed, each named once in the
// order its first repeat appears (nil when there is none).
func uniqueFolders(l []string) (unique, repeated []string) {
	if l == nil {
		return nil, nil
	}
	unique = make([]string, 0, len(l))
	seen := map[string]int{}
	for _, v := range l {
		k := strings.TrimSpace(v)
		seen[k]++
		switch seen[k] {
		case 1:
			unique = append(unique, v)
		case 2:
			repeated = append(repeated, k)
		}
	}
	return unique, repeated
}

// folderRepeatWarnings are the notices of folders dropped as repeats, one
// line each: the setup still saves (a repeat changes nothing), but the
// person learns their list had one, instead of it vanishing silently
// (owner, 2026-11-08). nil when nothing repeated, so JSON leaves
// "warnings" out.
func folderRepeatWarnings(repeated []string) []string {
	if len(repeated) == 0 {
		return nil
	}
	out := make([]string, 0, len(repeated))
	for _, f := range repeated {
		out = append(out, "folder "+quote(f)+" was given more than once; it is saved once")
	}
	return out
}

// pendingEntryWarnings says that a step saved on an org setup whose
// config entry is not written yet (the member's join step, the admin's
// team step writes it) only takes effect with that step: before
// (devco:K-1) the folders step after a refused join printed status done
// and nothing else, and lore whoami then found no org at all.
func pendingEntryWarnings(s stepState, step onboard.Step) []string {
	if s.exists {
		return nil
	}
	var entryStep onboard.StepID
	switch s.kind {
	case onboard.KindMember:
		entryStep = onboard.StepJoin
	case onboard.KindAdmin:
		entryStep = onboard.StepTeam
	default:
		return nil
	}
	if step.ID == entryStep || step.ID == onboard.StepKind {
		return nil
	}
	return []string{"saved for later: setup " + quote(s.name) + " takes effect once its " + string(entryStep) +
		" step is done (lore setup apply --setup " + s.name + " --step " + string(entryStep) + ")"}
}
