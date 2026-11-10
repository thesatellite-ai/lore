package setup

// repair.go — setup.repair (ONBOARDING_DESIGN 6.5, 7.1): run every check
// of a setup fresh, map each failing one to its home step, and answer
// those steps with their results; the page re-asks only them (RepairOut.Web,
// cmd/lore opens it). --yes runs the fixes that are Runnable and not
// Outward (an outward fix is always confirmed on the page or listed);
// --dry-run only lists; --all repairs every setup with a failure.
//
//	setups ─▶ per setup: KindChecks ─▶ probe (one batch) ─▶ failing ─▶ HomeStep
//	       └▶ --yes: run runnable, non-outward fixes ─▶ probe again ─▶ what still fails

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// repairExitFailing is the exit status when a failure remains (6.5 step
// 3: "prints the steps and their fixes and exits 1").
const repairExitFailing = 1

// ExitStatus implements op.ExitStatuser: 1 while any setup has a step to
// repair, 0 when everything passes.
func (o RepairOut) ExitStatus() int {
	for _, s := range o.Setups {
		if len(s.Steps) > 0 {
			return repairExitFailing
		}
	}
	return 0
}

// Repair text marks (the doctor --all marks: ✗ an error-level fail, ⚠
// a warn-level one).
const (
	repairMarkFail = "✗"
	repairMarkWarn = "⚠"
	repairIndent   = "  "
)

// Text implements op.Texter: per setup a header, then each step to redo
// with its failing checks, the detail, the fix text and the command a
// fix runs (6.5 step 3 "prints the steps and their fixes"). Before this
// the CLI fell back to the generic printer, which put each step's checks
// as JSON inside a table cell.
//
//	agency: 1 step to redo
//	  team
//	    ✗ store.write  agency: no push access
//	      fix: ask the org admin for push access
//	      run: lore setup edit agency --host-alias github-agency
//	personal: nothing to repair
func (o RepairOut) Text() string {
	var b strings.Builder
	if len(o.Setups) == 0 {
		b.WriteString("nothing to repair\n")
	}
	for _, s := range o.Setups {
		switch n := len(s.Steps); n {
		case 0:
			b.WriteString(s.Name + ": nothing to repair\n")
			continue
		case 1:
			b.WriteString(s.Name + ": 1 step to redo\n")
		default:
			b.WriteString(s.Name + ": " + itoa(n) + " steps to redo\n")
		}
		for _, st := range s.Steps {
			b.WriteString(repairIndent + string(st.Step) + "\n")
			for _, c := range st.Checks {
				mark := repairMarkFail
				if c.Level == onboard.LevelWarn {
					mark = repairMarkWarn
				}
				line := repairIndent + repairIndent + mark + " " + string(c.Check)
				if c.Detail != "" {
					line += "  " + c.Detail
				}
				b.WriteString(line + "\n")
				if c.Fix.Text != "" {
					b.WriteString(repairIndent + repairIndent + repairIndent + "fix: " + c.Fix.Text + "\n")
				}
				if len(c.Fix.Command) > 0 {
					b.WriteString(repairIndent + repairIndent + repairIndent + "run: " + strings.Join(c.Fix.Command, " ") + "\n")
				}
				for _, ch := range c.Fix.Choices {
					b.WriteString(repairIndent + repairIndent + repairIndent + "or run: " + strings.Join(ch.Command, " ") + "  (" + ch.Label + ")\n")
				}
			}
		}
	}
	return b.String()
}

// Repair is setup.repair. The setup is the name argument, else the global
// --setup / active setup (3.3); --all takes every setup.
func (e engine) Repair(ctx context.Context, in RepairIn) (RepairOut, error) {
	if in.All && in.Name != "" {
		return RepairOut{}, errs.New(errs.InvalidInput, "--all repairs every setup; give a name or --all, not both").
			WithNext("lore", "setup", "repair", "--all")
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return RepairOut{}, err
	}
	names, err := e.repairNames(ctx, st, in)
	if err != nil {
		return RepairOut{}, err
	}
	out := RepairOut{Setups: []RepairSetup{}}
	for _, name := range names {
		rs, err := e.repairOne(ctx, st, name, (in.Yes || op.Confirmed(ctx)) && !in.DryRun)
		if err != nil {
			return RepairOut{}, err
		}
		if in.All && len(rs.Steps) == 0 {
			continue // --all lists only the setups with a failure
		}
		out.Setups = append(out.Setups, rs)
	}
	if !in.DryRun {
		for _, rs := range out.Setups {
			if len(rs.Steps) > 0 {
				out.Web = WebStart{Setup: rs.Name}
				break
			}
		}
	}
	return out, nil
}

// repairNames is the setups to repair, each checked to exist.
func (e engine) repairNames(ctx context.Context, st Store, in RepairIn) ([]string, error) {
	cfg, err := st.Config(ctx)
	if err != nil {
		return nil, err
	}
	if in.All {
		names := make([]string, 0, len(cfg.Setups))
		for _, s := range cfg.Enabled() { // a disabled setup is skipped, like doctor --all
			names = append(names, s.Name)
		}
		return names, nil
	}
	name := setupName(ctx, in.Name)
	if name == "" {
		act, err := st.Active(ctx, "")
		if err != nil {
			return nil, err
		}
		if act.Why == WhyNone || act.Name == "" {
			return nil, errs.New(errs.InvalidInput, "no setup is active here; name the setup to repair, or --all").
				WithNext("lore", "setup", "list")
		}
		name = act.Name
	}
	if _, ok := cfg.Setup(name); !ok {
		return nil, notFound(name, cfg)
	}
	return []string{name}, nil
}

// repairOne runs one setup's checks; runFixes is --yes without --dry-run.
func (e engine) repairOne(ctx context.Context, st Store, name string, runFixes bool) (RepairSetup, error) {
	s, err := e.load(ctx, st, name, nil)
	if err != nil {
		return RepairSetup{}, err
	}
	items := make([]probeItem, 0)
	for _, id := range e.cat.KindChecks(s.kind) {
		home, ok := e.cat.HomeStep(id, s.kind)
		if !ok || (onboard.CreateOnly(id) && s.stepDone(home)) {
			// A create-only check (org.name_free, …) of a saved step would
			// fail on the setup's own org entry or name: not a repair item.
			continue
		}
		items = append(items, probeItem{check: id, step: s.planStep(home)})
	}
	answersOf := func(id onboard.StepID) onboard.Answers { return s.progress[id].Answers }
	ps, err := e.probeItems(ctx, s, items, answersOf, "")
	if err != nil {
		return RepairSetup{}, err
	}
	if runFixes {
		ran, err := e.runSafeFixes(ctx, s, failing(ps), answersOf)
		if err != nil {
			return RepairSetup{}, err
		}
		if ran {
			// What the person sees is the state after the fixes.
			if ps, err = e.probeItems(ctx, s, items, answersOf, ""); err != nil {
				return RepairSetup{}, err
			}
		}
	}
	return RepairSetup{Name: name, Steps: stepsOf(s.plan, failing(ps))}, nil
}

// failing keeps the checks whose status is fail, at either level (6.5:
// "each failing check"; a probe that could not run is not a failure).
func failing(ps []probed) []probed {
	var out []probed
	for _, p := range ps {
		if p.res.Status == onboard.StatusFail {
			out = append(out, p)
		}
	}
	return out
}

// runSafeFixes runs the fixes --yes may run on its own (6.5 step 5):
// Runnable, not Outward, passing re-validation. A refused or outward fix
// stays listed for the page; a fix that runs and exits non-zero is not an
// op error (the re-probe shows what still fails). ran reports whether any
// fix ran.
func (e engine) runSafeFixes(ctx context.Context, s stepState, ps []probed, answersOf func(onboard.StepID) onboard.Answers) (bool, error) {
	var fr FixRunner
	ran := false
	for _, p := range ps {
		if !p.res.Fix.Runnable || p.res.Fix.Outward {
			continue
		}
		f, err := e.runnable(s, p.item, p.res, fixVars(s, answersOf(p.item.step.ID), p.raw), "")
		if err != nil || f.Outward {
			continue
		}
		if fr == nil {
			if fr, err = e.env.SetupFixRunner(ctx); err != nil {
				return ran, err
			}
		}
		if _, err := fr.RunFixIn(WithFixGHAccount(WithFixCheck(ctx, p.item.check), GHAccount(s.entry)), TargetRepo(p.res.Target), f.Command); err != nil {
			return ran, err
		}
		ran = true
	}
	return ran, nil
}

// stepsOf groups the failing results by home step, in plan order; a home
// step outside the plan comes last.
func stepsOf(plan []onboard.Step, ps []probed) []RepairStep {
	by := map[onboard.StepID][]onboard.CheckResult{}
	order := make([]onboard.StepID, 0)
	for _, p := range ps {
		id := p.item.step.ID
		if _, ok := by[id]; !ok {
			order = append(order, id)
		}
		by[id] = append(by[id], p.res)
	}
	rank := func(id onboard.StepID) int {
		for i, st := range plan {
			if st.ID == id {
				return i
			}
		}
		return len(plan)
	}
	slices.SortStableFunc(order, func(a, b onboard.StepID) int { return rank(a) - rank(b) })
	out := make([]RepairStep, 0, len(order))
	for _, id := range order {
		out = append(out, RepairStep{Step: id, Checks: by[id]})
	}
	return out
}
