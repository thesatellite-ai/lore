package setup

// check.go — setup.check (ONBOARDING_DESIGN 2.3, 7.1): validate one
// step's answers, run its checks, keep the answers and results in
// setup_progress. Never writes config, a store or a repo; checks of a
// later step may run before its Needs are done (2.3 "Order").

import (
	"context"
	"reflect"
	"slices"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Check is setup.check.
//
//	answers ─▶ Catalog.Validate ── field errors ─▶ status todo, nothing probed, nothing saved
//	          └▶ probe ─▶ failed | ready ─▶ Store.PutProgress (a draft row takes answers + results;
//	                                      a saved/skipped row only refreshes results for its own answers)
func (e engine) Check(ctx context.Context, in CheckIn) (CheckOut, error) {
	name := newSetupName(ctx, in.Setup, in.Step, in.Answers)
	if err := requireName(name); err != nil {
		return CheckOut{}, err
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return CheckOut{}, err
	}
	s, err := e.load(ctx, st, name, in.Answers)
	if err != nil {
		return CheckOut{}, err
	}
	step, err := s.step(in.Step)
	if err != nil {
		return CheckOut{}, err
	}
	answers := in.Answers
	if answers == nil {
		answers = s.progress[step.ID].Answers
	}
	answers = e.withoutUnasked(s, step, answers)
	step = e.askedStep(s, step, answers)
	answers, routeErr := e.deriveRoute(ctx, step, answers)
	out := CheckOut{Step: step.ID, FieldErrors: []onboard.FieldError{}, Checks: []onboard.CheckResult{}}
	if joinedAlready(s, step, answers) {
		// Joined already: no invite to check (see joinedAlready).
		out.Status, out.Checks = onboard.StepDone, e.joinedChecks(step)
		return out, nil
	}
	if fe := append(e.validateStep(ctx, step, askedKind(s, step, answers), answers), routeErr...); len(fe) > 0 {
		out.Status, out.FieldErrors = onboard.StepTodo, fe
		return out, nil
	}
	if out.Checks, err = e.probe(ctx, s, step, answers); err != nil {
		return CheckOut{}, err
	}
	out.Status = statusOf(out.Checks)
	prev, had := s.progress[step.ID]
	// A saved or skipped row records what Save persisted. A check never
	// changes it unless the checked answers ARE the saved ones, and then
	// only its results are refreshed (realmac #9b/#11: the step list must
	// always match what is saved). Checking a draft of a saved step only
	// reports, so apply with no answers still saves the saved answers. A
	// draft row (never saved) keeps the checked answers, so the CLI's
	// "check, then apply with no answers" flow works for unsaved steps.
	saved := had && (prev.Status == onboard.StepDone || prev.Status == onboard.StepSkipped)
	same := saved && reflect.DeepEqual(prev.Answers, answers)
	if same && out.Status == onboard.StepReady {
		// Re-checking a saved step does not undo the save (2.3: a
		// finished setup shows every step done with fresh checks).
		out.Status = prev.Status
	}
	if s.finished() || (saved && !same) {
		// A finished setup keeps no progress (4.11); a re-check only
		// reports, and its results become the setup's health (Home's
		// cell, setup list), so a setup that was never checked shows the
		// real health after any check run, not only after doctor.
		if s.finished() && e.savedAnswers(ctx, st, s, step, answers) {
			e.recordCheckHealth(ctx, name, out.Checks)
		}
		return out, nil
	}
	row := onboard.Progress{Setup: name, Step: step.ID, Answers: answers, Checks: out.Checks, Status: out.Status}
	if same {
		row.Status = prev.Status
	}
	return out, st.PutProgress(ctx, row)
}

// recordCheckHealth merges one step's fresh results into the setup's
// recorded health: a result of the same check (and tool) replaces the old
// one, every other recorded result stays. Best effort: health is derived
// state, and a failed write only leaves the older cell.
func (e engine) recordCheckHealth(ctx context.Context, name string, checks []onboard.CheckResult) {
	rec, ok := e.env.(HealthRecorder)
	if !ok || len(checks) == 0 {
		return
	}
	var merged []onboard.CheckResult
	if hs, ok := e.env.(HealthSource); ok {
		if old, ok := hs.SetupHealth(ctx, name); ok {
			for _, o := range old {
				if !slices.ContainsFunc(checks, func(c onboard.CheckResult) bool { return c.Check == o.Check && c.Target == o.Target }) {
					merged = append(merged, o)
				}
			}
		}
	}
	_ = rec.RecordHealth(ctx, name, append(merged, checks...))
}

// savedAnswers reports whether answers are the saved values of step of the
// finished setup s (what setup plan shows as its values; empty answers
// left out on both sides): only a check of what is saved is the setup's
// health, a check of a draft is not.
func (e engine) savedAnswers(ctx context.Context, st Store, s stepState, step onboard.Step, answers onboard.Answers) bool {
	cfg, err := st.Config(ctx)
	if err != nil {
		return false
	}
	saved := planStep(step, s.kind, s.entry, s.exists, true, cfg, nil).Values
	return reflect.DeepEqual(nonEmptyAnswers(saved), nonEmptyAnswers(answers))
}

// nonEmptyAnswers is a without the answers that say nothing (no text, no
// list items, not a given bool).
func nonEmptyAnswers(a onboard.Answers) onboard.Answers {
	out := onboard.Answers{}
	for k, v := range a {
		if v.Text != "" || len(v.List) > 0 || v.IsBool {
			out[k] = v
		}
	}
	return out
}
