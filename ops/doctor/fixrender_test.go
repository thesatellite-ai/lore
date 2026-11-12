package doctor

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// renderAgainEnv is a repo whose render step ran first and whose doctor
// fixes then changed rows (an actor merge), so the re-check finds LORE.md
// stale until FixRenderAgain renders it.
type renderAgainEnv struct {
	recheckEnv
	rendered int
	render   FixStep // the first render step FixSteps returns
}

func (e *renderAgainEnv) FixSteps(context.Context, bool) []FixStep {
	return []FixStep{{Step: StepMigrate, Result: ResultCurrent}, e.render}
}

func (e *renderAgainEnv) DoctorChecks(ctx context.Context, fix bool) []CheckRun {
	runs := e.recheckEnv.DoctorChecks(ctx, fix)
	if e.rendered > 0 {
		return nil // rendered after the merge: nothing stale
	}
	return runs
}

func (e *renderAgainEnv) FixRenderAgain(context.Context) FixStep {
	e.rendered++
	return FixStep{Step: StepRender, Where: "/w/app/.lore/LORE.md", Result: "again, after the doctor fixes changed rows: rendered", Changed: true}
}

// TestFixRendersAfterRowChangingRepair (owner item 43): render ran before
// the doctor fixes; a repair changed rows after it (the merge), so the
// re-check saw LORE.md stale. lore fix renders again and checks once more:
// the final status has no stale LORE.md, and the second render is a step.
func TestFixRendersAfterRowChangingRepair(t *testing.T) {
	t.Parallel()
	merge := Finding{Check: "actor_stable_key", Level: LevelWarning, Line: "actor_stable_key: 2 actors share human:a@example.com", Fix: "lore actor merge a b"}
	stale := Finding{Check: checkRenderedCopy, Level: LevelWarning, Line: "rendered_copy: .lore/LORE.md is stale: its rows changed; fix: lore render", Fix: "lore render"}
	env := &renderAgainEnv{recheckEnv: recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "actor_stable_key", Findings: []Finding{merge}}},
		after:  []CheckRun{{Check: checkRenderedCopy, Findings: []Finding{stale}}}},
		render: FixStep{Step: StepRender, Result: ResultCurrent}}
	r, err := FixProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if env.rendered != 1 || !slices.Equal(env.calls, []bool{true, false, false}) {
		t.Fatalf("rendered %d, checks %v; want one render again and one more check", env.rendered, env.calls)
	}
	if r.Status != StatusHealthy || slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.HasPrefix(w, "rendered_copy:") }) {
		t.Fatalf("after the fix: status %s warnings %q", r.Status, r.Warnings)
	}
	last := r.Steps[len(r.Steps)-1]
	if last.Step != StepRender || !last.Changed || r.Steps[len(r.Steps)-2].Step != StepDoctorFix {
		t.Fatalf("steps %+v; want the render again after doctor fixes", r.Steps)
	}
	if !slices.Contains(r.Fixed, merge.Line) {
		t.Fatalf("fixed %q lacks the merge", r.Fixed)
	}

	// nothing stale after the repairs: no second render, no extra check
	quiet := &renderAgainEnv{recheckEnv: recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "actor_stable_key", Findings: []Finding{merge}}}}, render: FixStep{Step: StepRender, Result: ResultCurrent}}
	if _, err := FixProject(context.Background(), quiet, false); err != nil || quiet.rendered != 0 || len(quiet.calls) != 2 {
		t.Fatalf("nothing stale: rendered %d checks %v err %v", quiet.rendered, quiet.calls, err)
	}
	// a render step that failed is not run again (it would fail the same way)
	failed := &renderAgainEnv{recheckEnv: recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: env.before, after: env.after}, render: FixStep{Step: StepRender, Result: "boom", Failed: true}}
	if _, err := FixProject(context.Background(), failed, false); err != nil || failed.rendered != 0 {
		t.Fatalf("failed render: rendered again %d times, err %v", failed.rendered, err)
	}
}

// TestFixTimedNoteIsNoFailedRepair (owner item 44): a note that counts
// seconds ("live: last fetch 38s ago") reads "40s ago" in the re-check; it
// is the same note, never a "not fixed:" line that sends the repo to
// --all's second run, and it is kept once.
func TestFixTimedNoteIsNoFailedRepair(t *testing.T) {
	t.Parallel()
	warn := Finding{Check: "live", Level: LevelWarning, Line: "live: 58 live row(s) older than live.max_age not copied to main yet (run `lore sync`)"}
	env := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "live", Findings: []Finding{warn, {Check: "live", Level: LevelNote, Line: "live: last fetch 38s ago"}}}},
		after:  []CheckRun{{Check: "live", Findings: []Finding{warn, {Check: "live", Level: LevelNote, Line: "live: last fetch 40s ago"}}}}}
	r, err := FixProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if RepairFailed(r.Steps) {
		t.Fatalf("steps %+v name a failed repair for a timed note", r.Steps)
	}
	n := 0
	for _, l := range r.Notes {
		if strings.HasPrefix(l, "live: last fetch") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("notes %q; want the last fetch note once", r.Notes)
	}
	// a real reason the re-check does not give stays a not fixed line
	env2 := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "live", Findings: []Finding{warn, {Check: "live", Level: LevelNote, Line: "live: could not copy: the store lock is held"}}}},
		after:  []CheckRun{{Check: "live", Findings: []Finding{warn}}}}
	r2, err := FixProject(context.Background(), env2, false)
	if err != nil || !RepairFailed(r2.Steps) {
		t.Fatalf("a real reason lost: steps %+v err %v", r2.Steps, err)
	}
}
