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
//	            ─▶ Report (finalized) with Steps; Status is after the fixes
//
// doctor.run --fix calls the same function, so the two can never drift:
// its text says it ran lore fix's steps. A dry run changes nothing:
// each step says what it would do, and the doctor step lists the findings
// a fix would repair.

import (
	"context"
	"fmt"
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
	r, err := repairRepo(ctx, env)
	if err != nil {
		return Report{}, err
	}
	st := FixStep{Step: StepDoctorFix, Result: ResultCurrent}
	if len(r.Fixed) > 0 {
		st.Result, st.Detail, st.Changed = fmt.Sprintf("fixed %d", len(r.Fixed)), r.Fixed, true
	}
	r.Steps = append(steps, st)
	return r, nil
}

// repairRepo is the checks' fixing run and the re-check after it: the
// report shows the state now, and what the fixes changed.
func repairRepo(ctx context.Context, env Env) (Report, error) {
	r, err := runRepo(ctx, env, true)
	if err != nil {
		return Report{}, err
	}
	after, err := runRepo(ctx, env, false)
	if err != nil {
		return Report{}, err
	}
	after.Fixed = fixedBetween(r, after)
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
