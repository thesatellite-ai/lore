package doctor

// fixproject.go — the one implementation behind `lore fix` and
// `lore doctor --fix` (decisions D-ak, D-al): every repair lore can make for this
// project, in order, each step printed (what, where, result), then the
// report again so the status printed last is the state now.
//
//	FixProject(dry) ─▶ FixStepsEnv.FixSteps(dry): migrate run (v0.1.17
//	            │   rows), render (stale LORE.md, tool files), agent hooks
//	            │   (what lore setup --agent-hooks writes), old store clones
//	            │   the doctor proved safe to remove
//	            ─▶ step "doctor fixes": the checks' own repairs (git info/
//	            │   exclude in client repos, store clone identity, merge
//	            │   driver, config style, actor roster, …), then the re-check
//	            ─▶ (re-check finds a stale rendered copy: a repair changed
//	            │   rows after render) FixRenderAgainEnv renders once more,
//	            │   step "render" after "doctor fixes", and checks again
//	            ─▶ Report (finalized) with Steps; Status is after the fixes
//
// doctor.run --fix calls the same function, so the two can never drift:
// its text says it ran lore fix's steps. A dry run changes nothing:
// each step says what it would do, and the doctor step lists the findings
// a fix would repair.

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// FixStep is one step of a fix run: what ran, where, and its result.
type FixStep struct {
	// Step names the step (migrate, render, agent hooks, old clones,
	// doctor fixes).
	Step string `json:"step"`
	// Where is the folder or file the step works on.
	Where string `json:"where,omitempty"`
	// Result is the one-line outcome ("current" when nothing was needed).
	Result string `json:"result"`
	// Detail are further lines of the outcome (rows rewritten, files).
	Detail []string `json:"detail,omitempty"`
	// Changed is true when the step changed something (or would, under
	// a dry run).
	Changed bool `json:"changed"`
	// Failed is true when the step could not run; Result names why and the
	// later steps still run.
	Failed bool `json:"failed,omitempty"`
}

// Step names.
const (
	StepMigrate    = "migrate"
	StepRender     = "render"
	StepAgentHooks = "agent hooks"
	StepOldClones  = "old clones"
	StepDoctorFix  = "doctor fixes"
)

// ResultCurrent is a step's result when nothing was needed.
const ResultCurrent = "current"

// FixStepsEnv is the optional upgrade of Env that runs the fix steps
// outside the doctor checks (*wire.App implements it).
type FixStepsEnv interface {
	FixSteps(ctx context.Context, dry bool) []FixStep
}

// Line is the step's printed line.
func (s FixStep) Line() string {
	where := ""
	if s.Where != "" {
		where = " (" + s.Where + ")"
	}
	mark := "ok"
	switch {
	case s.Failed:
		mark = "FAILED"
	case !s.Changed:
		mark = "--"
	}
	return fmt.Sprintf("%-6s %s%s: %s", mark, s.Step, where, s.Result)
}

// StepsText is the steps, one line each, their details indented.
func StepsText(steps []FixStep) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString("  " + s.Line() + "\n")
		for _, d := range s.Detail {
			b.WriteString("         " + d + "\n")
		}
	}
	return b.String()
}

// FixProject runs every repair for this project (or, dry, lists them) and
// returns the report after them, finalized, with Steps.
func FixProject(ctx context.Context, env Env, dry bool) (Report, error) {
	r, err := fixProject(ctx, env, dry)
	if err != nil {
		return Report{}, err
	}
	r.finalize()
	r.OK = r.Status == StatusHealthy
	r.Exit = exitOf(r.Status)
	return r, nil
}

// fixProject is FixProject before finalize (doctor.run adds its setups).
func fixProject(ctx context.Context, env Env, dry bool) (Report, error) {
	var steps []FixStep
	if ue, ok := env.(FixStepsEnv); ok {
		steps = ue.FixSteps(ctx, dry)
	}
	if dry {
		r, err := runRepo(ctx, env, false)
		if err != nil {
			return Report{}, err
		}
		fixable := fixableLines(r)
		st := FixStep{Step: StepDoctorFix, Result: ResultCurrent}
		if len(fixable) > 0 {
			st.Result, st.Detail, st.Changed = fmt.Sprintf("would try to fix %d finding(s)", len(fixable)), fixable, true
		}
		r.Steps = append(steps, st)
		return r, nil
	}
	// owner item 43: render ran before the doctor fixes, and a repair that
	// changes rows (an actor merge, a --supersedes repair, a hand edit
	// picked up, a live hook) leaves LORE.md stale; when the re-check finds
	// a stale rendered copy, render runs again and the re-check after it
	// is the status printed
	var again *FixStep
	afterFix := func(after Report) bool {
		re, ok := env.(FixRenderAgainEnv)
		if !ok || !renderStale(after) || renderStepFailed(steps) {
			return false
		}
		st := re.FixRenderAgain(ctx)
		again = &st
		return st.Changed && !st.Failed
	}
	r, err := repairRepo(ctx, env, afterFix)
	if err != nil {
		return Report{}, err
	}
	st := FixStep{Step: StepDoctorFix, Result: ResultCurrent}
	if len(r.Fixed) > 0 {
		st.Result, st.Detail, st.Changed = fmt.Sprintf("fixed %d", len(r.Fixed)), slices.Clone(r.Fixed), true
	}
	// owner item 38: a repair that tried and could not (a merge a store
	// refused) says why in the step, not only in the notes a fix's text
	// leaves out; the warning it left stays below
	for _, n := range r.fixNotes {
		st.Detail = append(st.Detail, NotFixedPrefix+n)
	}
	r.Steps = append(steps, st)
	if again != nil {
		r.Steps = append(r.Steps, *again)
	}
	return r, nil
}

// FixRenderAgainEnv is the optional upgrade of Env that renders again
// after the doctor fixes changed rows (*wire.App implements it): the
// returned step is the render step's, Changed when it rendered.
type FixRenderAgainEnv interface {
	FixRenderAgain(ctx context.Context) FixStep
}

// checkRenderedCopy is the rendered-copy check's name (wire
// DoctorCheckRenderedCopy).
const checkRenderedCopy = "rendered_copy"

// renderStale reports whether r warns of a rendered copy lore render
// refreshes (not a rendered_copy check that could not run).
func renderStale(r Report) bool {
	for _, l := range r.Warnings {
		if m, ok := r.meta[l]; ok && string(m.check) == checkRenderedCopy && m.fix != "" {
			return true
		}
	}
	return false
}

// renderStepFailed reports whether the first render step failed or was
// skipped (rendering again would fail or skip the same way).
func renderStepFailed(steps []FixStep) bool {
	for _, s := range steps {
		if s.Step == StepRender {
			return s.Failed || strings.HasPrefix(s.Result, "skipped")
		}
	}
	return false
}

// NotFixedPrefix starts a doctor fixes step detail naming a repair that
// tried and could not fix (lore fix --all runs such a repo once more,
// alone).
const NotFixedPrefix = "not fixed: "

// RepairFailed reports whether steps hold a doctor fixes step with a
// repair that tried and could not fix.
func RepairFailed(steps []FixStep) bool {
	for _, st := range steps {
		if st.Step == StepDoctorFix && slices.ContainsFunc(st.Detail, func(d string) bool { return strings.HasPrefix(d, NotFixedPrefix) }) {
			return true
		}
	}
	return false
}

// fixOnlyNotes are the notes of the fixing run that say why a repair
// could not fix: a note the re-check does not give, of a check that still
// warns (or errs) after the fix, which is not a fixed line itself.
func fixOnlyNotes(fixing []string, after Report) []string {
	still := map[string]bool{}
	for _, l := range append(slices.Clone(after.Warnings), after.Errors...) {
		if c, _, ok := strings.Cut(l, ":"); ok {
			still[c] = true
		}
	}
	var out []string
	for _, n := range fixing {
		c, _, ok := strings.Cut(n, ":")
		if !ok || !still[c] || hasNote(after.Notes, n) || slices.Contains(after.Fixed, n) || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// hasNote reports whether notes hold n, numbers aside: a note that counts
// time or rows ("live: last fetch 38s ago") reads differently in the
// re-check a second later and is the same note, never a repair that
// could not fix (owner item 44: such a note sent a repo to --all's second
// run, alone, for nothing).
func hasNote(notes []string, n string) bool {
	key := noteKey(n)
	return slices.ContainsFunc(notes, func(o string) bool { return noteKey(o) == key })
}

// noteKey is a note with every run of digits replaced by "#".
func noteKey(n string) string {
	var b strings.Builder
	digit := false
	for _, c := range n {
		if c >= '0' && c <= '9' {
			if !digit {
				b.WriteByte('#')
			}
			digit = true
			continue
		}
		digit = false
		b.WriteRune(c)
	}
	return b.String()
}

// repairRepo is the checks' fixing run and the re-check after it: the
// report shows the state now, and what the fixes changed. afterFix (nil:
// none) sees the re-check; when it changed something (render again), the
// check runs once more and that is the state now.
func repairRepo(ctx context.Context, env Env, afterFix func(Report) bool) (Report, error) {
	r, err := runRepo(ctx, env, true)
	if err != nil {
		return Report{}, err
	}
	after, err := runRepo(ctx, env, false)
	if err != nil {
		return Report{}, err
	}
	if afterFix != nil && afterFix(after) {
		if after, err = runRepo(ctx, env, false); err != nil {
			return Report{}, err
		}
	}
	after.Fixed = fixedBetween(r, after)
	after.fixNotes = fixOnlyNotes(r.Notes, after)
	after.Notes = withFixNotes(after.Notes, r.Notes)
	return after, nil
}

// fixableLines are the warnings and errors whose source named a fix.
func fixableLines(r Report) []string {
	var out []string
	for _, l := range append(append([]string(nil), r.Errors...), r.Warnings...) {
		if m, ok := r.meta[l]; ok && m.fix != "" {
			out = append(out, l)
		}
	}
	return out
}

// Changed reports whether any step changed something.
func Changed(steps []FixStep) bool {
	for _, s := range steps {
		if s.Changed {
			return true
		}
	}
	return false
}

// FailedSteps names the steps that could not run.
func FailedSteps(steps []FixStep) []string {
	var out []string
	for _, s := range steps {
		if s.Failed {
			out = append(out, s.Step)
		}
	}
	return out
}
