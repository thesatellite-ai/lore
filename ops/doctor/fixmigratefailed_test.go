package doctor

import (
	"context"
	"slices"
	"testing"
)

// migrateFailedEnv is a repo whose migrate step failed: its rows are half
// in the lore 0.1.17 format.
type migrateFailedEnv struct{ recheckEnv }

func (e *migrateFailedEnv) FixSteps(context.Context, bool) []FixStep {
	return []FixStep{{Step: StepMigrate, Result: "[E_MIGRATE_LOCKED] migrate: another migration holds the lock", Failed: true}}
}

// TestFixNoRepairsAfterFailedMigrate (B1-12): a stopped lore fix left the
// repo half migrated and the next run's migrate step failed; the doctor
// repairs then rewrote hundreds of rows the migration had still to finish.
// Now they are skipped (the checks still run and report), and the next
// lore fix migrates first.
func TestFixNoRepairsAfterFailedMigrate(t *testing.T) {
	t.Parallel()
	lead := Finding{Check: "task_lead", Level: LevelWarning, Line: "task_lead: task t1: lead is not in its assignees", Fix: "lore task edit --id t1"}
	env := &migrateFailedEnv{recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "task_lead", Findings: []Finding{lead}}}}}
	r, err := FixProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(env.calls, true) {
		t.Fatalf("checks %v: no repair may run after a failed migrate step", env.calls)
	}
	last := r.Steps[len(r.Steps)-1]
	if last.Step != StepDoctorFix || last.Result != ResultSkippedMigrateFailed || last.Changed {
		t.Fatalf("doctor fixes step %+v", last)
	}
	if len(r.Fixed) != 0 {
		t.Fatalf("fixed %q", r.Fixed)
	}
}
