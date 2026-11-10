package setup

import (
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestCheckKeepsSavedRows (realmac #9b/#11, item 1b): setup.check never
// changes a saved or skipped step row unless the checked answers are the
// saved ones, and then only refreshes its results; a draft row (never
// saved) takes the checked answers and results as before.
func TestCheckKeepsSavedRows(t *testing.T) {
	other := onboard.Answers{"email": text("other@agency.example")}
	marker := []onboard.CheckResult{{Check: "marker.old", Status: onboard.StatusFail, Level: onboard.LevelError}}
	type want struct {
		out     onboard.StepStatus
		row     onboard.StepStatus
		answers onboard.Answers
		fresh   bool // the row's checks are this check's results (else the marker is kept)
	}
	for _, c := range []struct {
		name  string
		prev  onboard.StepStatus // "" = no row
		check onboard.Answers
		want  want
	}{
		{"done, same answers", onboard.StepDone, emailAnswers, want{onboard.StepDone, onboard.StepDone, emailAnswers, true}},
		{"done, other answers", onboard.StepDone, other, want{onboard.StepReady, onboard.StepDone, emailAnswers, false}},
		{"skipped, same answers", onboard.StepSkipped, emailAnswers, want{onboard.StepSkipped, onboard.StepSkipped, emailAnswers, true}},
		{"skipped, other answers", onboard.StepSkipped, other, want{onboard.StepReady, onboard.StepSkipped, emailAnswers, false}},
		{"draft ready, same answers", onboard.StepReady, emailAnswers, want{onboard.StepReady, onboard.StepReady, emailAnswers, true}},
		{"draft ready, other answers", onboard.StepReady, other, want{onboard.StepReady, onboard.StepReady, other, true}},
		{"draft failed, same answers", onboard.StepFailed, emailAnswers, want{onboard.StepReady, onboard.StepReady, emailAnswers, true}},
		{"draft failed, other answers", onboard.StepFailed, other, want{onboard.StepReady, onboard.StepReady, other, true}},
		{"todo row, other answers", onboard.StepTodo, other, want{onboard.StepReady, onboard.StepReady, other, true}},
		{"no row", "", other, want{onboard.StepReady, onboard.StepReady, other, true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, e := started(t)
			if c.prev != "" {
				if err := env.store.PutProgress(bg, onboard.Progress{Setup: "personal", Step: onboard.StepIdentity, Answers: emailAnswers, Checks: marker, Status: c.prev}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: c.check})
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != c.want.out {
				t.Fatalf("out.Status = %s, want %s", out.Status, c.want.out)
			}
			row, ok := env.store.progress["personal"][onboard.StepIdentity]
			if !ok {
				t.Fatal("no progress row")
			}
			if row.Status != c.want.row || !reflect.DeepEqual(row.Answers, c.want.answers) {
				t.Fatalf("row = %s %v, want %s %v", row.Status, row.Answers, c.want.row, c.want.answers)
			}
			if got := reflect.DeepEqual(row.Checks, out.Checks) && !reflect.DeepEqual(row.Checks, marker); got != c.want.fresh {
				t.Fatalf("row checks fresh = %v, want %v (row %v)", got, c.want.fresh, row.Checks)
			}
		})
	}
}

// TestApplyWithoutAnswersAfterCheck: apply with no answers saves the
// checked answers of a draft step, and the saved answers of a saved step
// whose draft was only checked.
func TestApplyWithoutAnswersAfterCheck(t *testing.T) {
	other := onboard.Answers{"email": text("other@agency.example")}
	t.Run("draft step: the checked answers", func(t *testing.T) {
		env, e := started(t)
		if _, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: other}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity"}); err != nil {
			t.Fatal(err)
		}
		if row := env.store.progress["personal"][onboard.StepIdentity]; row.Status != onboard.StepDone || !reflect.DeepEqual(row.Answers, other) {
			t.Fatalf("row = %+v", row)
		}
	})
	t.Run("saved step, draft checked: the saved answers", func(t *testing.T) {
		env, e := started(t)
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: emailAnswers}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: other}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity"}); err != nil {
			t.Fatal(err)
		}
		if row := env.store.progress["personal"][onboard.StepIdentity]; row.Status != onboard.StepDone || !reflect.DeepEqual(row.Answers, emailAnswers) {
			t.Fatalf("row = %+v", row)
		}
	})
}
