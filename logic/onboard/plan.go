package onboard

import (
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// The plan for a kind, the check → step map repair uses (6.5), and the
// merge of check results into a step status (2.3).

// PlanFor is kind's steps in catalog order, each with its Questions and
// Checks narrowed to the kind. this_folder (4.10) is included only when
// setup was started inside a git repo (inRepo). An invalid kind has no plan.
func PlanFor(kind Kind, inRepo bool) []Step {
	if !kind.Valid() {
		return nil
	}
	var out []Step
	for _, s := range Catalog() {
		if !slices.Contains(s.Kinds, kind) || (s.ID == StepThisFolder && !inRepo) {
			continue
		}
		s.Questions = slices.DeleteFunc(s.Questions, func(q Question) bool { return !questionApplies(q, kind) })
		s.Checks = slices.DeleteFunc(s.Checks, func(c CheckID) bool { return !slices.Contains(CheckKinds(c), kind) })
		s.Needs = slices.DeleteFunc(s.Needs, func(n StepID) bool { return !stepHasKind(n, kind) })
		out = append(out, s)
	}
	return out
}

// stepHasKind reports whether step id is part of kind's plan.
func stepHasKind(id StepID, kind Kind) bool {
	s, ok := StepByID(id)
	return ok && slices.Contains(s.Kinds, kind)
}

// StepOfCheck is the home step of check id for kind: the first step of the
// kind's full plan that runs it, else its doctor-only home (6.5 step 2).
// False when the check does not apply to the kind.
func StepOfCheck(id CheckID, kind Kind) (StepID, bool) {
	if !slices.Contains(CheckKinds(id), kind) {
		return "", false
	}
	for _, s := range PlanFor(kind, true) {
		if slices.Contains(s.Checks, id) {
			return s.ID, true
		}
	}
	if h, ok := doctorHome[id][kind]; ok {
		return h, true
	}
	return "", false
}

// MergeStatus folds one step's check results into its status (2.3): any
// error-level fail → failed; otherwise ready. warn, skip and a probe that
// could not run (StatusError) never fail a step (6.4: degraded, not broken).
func MergeStatus(results []CheckResult) StepStatus {
	if len(FailingErrors(results)) > 0 {
		return StepFailed
	}
	return StepReady
}

// FailingErrors lists the checks that fail at error level, in order and
// unique (the ids of E_SETUP_STEP_FAILED).
func FailingErrors(results []CheckResult) []CheckID {
	var out []CheckID
	for _, r := range results {
		if r.Level == LevelError && r.Status == StatusFail && !slices.Contains(out, r.Check) {
			out = append(out, r.Check)
		}
	}
	return out
}

// MissingNeeds lists step's Needs (those in kind's plan) whose progress is
// neither done nor skipped.
func MissingNeeds(step Step, kind Kind, progress []Progress) []StepID {
	var out []StepID
	for _, n := range step.Needs {
		if !stepHasKind(n, kind) {
			continue
		}
		i := slices.IndexFunc(progress, func(p Progress) bool { return p.Step == n })
		if i >= 0 && (progress[i].Status == StepDone || progress[i].Status == StepSkipped) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// NeedsDone is nil when every need of step is done, else E_SETUP_STEP_ORDER
// "step {step} needs {needs} first" (7.2).
func NeedsDone(step Step, kind Kind, progress []Progress) error {
	miss := MissingNeeds(step, kind, progress)
	if len(miss) == 0 {
		return nil
	}
	names := make([]string, len(miss))
	for i, m := range miss {
		names[i] = string(m)
	}
	return errs.New(errs.SetupStepOrder, "step "+string(step.ID)+" needs "+strings.Join(names, ", ")+" first").
		WithHint("finish " + strconv.Quote(names[0]) + " (lore setup apply <setup> --step " + names[0] + ") and try again")
}
