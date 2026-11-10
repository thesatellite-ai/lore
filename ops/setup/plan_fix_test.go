package setup

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestPlanRefreshesSavedFixes (issue 13): a saved setup_progress row keeps
// the fix it was saved with. setup.plan shows it only when the catalog of
// this binary still gives that fix; a fix the catalog no longer words that
// way (an older lore's text, an old command) opens as the text-only
// "re-check" fix, never runnable, with the row's status and time kept.
func TestPlanRefreshesSavedFixes(t *testing.T) {
	step, _ := onboard.StepByID(onboard.StepIdentity)
	tmpl := onboard.FixFor(onboard.CheckIdentityGitEmail, onboard.StepIdentity, onboard.FixDefault)
	today, err := onboard.FillFix(onboard.CheckIdentityGitEmail, tmpl, map[string]string{onboard.VarGitEmail: "old@agency.example", onboard.VarEmail: "mira@agency.example"}, step)
	if err != nil {
		t.Fatal(err)
	}
	old := onboard.Fix{Text: "git uses old@agency.example; set it with git config user.email mira@agency.example", Command: []string{"git", "config", "user.email", "mira@agency.example"}, Runnable: true}
	for _, c := range []struct {
		name  string
		saved onboard.Fix
		stale bool
	}{
		{"fix the catalog gives today", today, false},
		{"fix an older lore worded", old, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, e := started(t)
			row := onboard.CheckResult{Check: onboard.CheckIdentityGitEmail, Status: onboard.StatusWarn, Level: onboard.LevelWarn, Detail: "git uses old@agency.example", Fix: c.saved}
			if err := env.store.PutProgress(bg, onboard.Progress{Setup: "personal", Step: onboard.StepIdentity, Answers: emailAnswers, Checks: []onboard.CheckResult{row}, Status: onboard.StepDone}); err != nil {
				t.Fatal(err)
			}
			out, err := e.Plan(bg, PlanIn{Setup: "personal"})
			if err != nil {
				t.Fatal(err)
			}
			var got *onboard.CheckResult
			for _, p := range out.Progress {
				if p.Step == onboard.StepIdentity && len(p.Checks) == 1 {
					got = &p.Checks[0]
				}
			}
			if got == nil {
				t.Fatalf("plan has no identity row with its check: %+v", out.Progress)
			}
			if got.Status != row.Status || got.Detail != row.Detail {
				t.Errorf("row = %+v, want status and detail as saved", *got)
			}
			if c.stale {
				if got.Fix.Text != onboard.StaleFix().Text || got.Fix.Runnable || len(got.Fix.Command) != 0 {
					t.Errorf("stale fix shown as %+v, want the re-check text, not runnable", got.Fix)
				}
			} else if got.Fix.Text != today.Text || len(got.Fix.Command) != len(today.Command) {
				t.Errorf("current fix changed to %+v, want %+v", got.Fix, today)
			}
			// The table itself is not rewritten by opening the plan.
			if saved := env.store.progress["personal"][onboard.StepIdentity].Checks[0].Fix.Text; saved != c.saved.Text {
				t.Errorf("plan rewrote the saved row's fix to %q", saved)
			}
		})
	}
}
