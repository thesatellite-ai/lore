package coord

import (
	"reflect"
	"testing"
	"time"

	"dbent/schema"
)

// Summarize reads every field `task show` prints from the row document,
// and an empty row gives the zero summary (no gate, no timebox, no outcome).
func TestSummarize(t *testing.T) {
	merged := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	d := Doc{keyID: "tsk_1", keyTitle: "invite route", keyBody: "b", keyStatus: "done", keyPriority: "high",
		keyCommitment: "accepted", keyMissionID: "msn_1", keyRepoID: "rep_1", keyRole: "coordinator",
		keyAssignee: "act_a", keyDoneBy: "act_b", keyGate: gateRequired, keyDoneCommit: "abc", keyWorkBranch: "agent/x"}
	d.SetInt(keyTimebox, 3600)
	d.SetInt(keyAttempts, 2)
	d.SetInt(keyDonePR, 412)
	d.SetTime(keyMergedAt, merged)
	deps := []schema.TaskDependency{{TaskID: "tsk_0", Until: schema.DependencyUntilMerged}}
	if err := d.SetBlockedBy(deps); err != nil {
		t.Fatal(err)
	}
	s := Summarize(d)
	want := TaskSummary{ID: "tsk_1", Title: "invite route", Body: "b", Status: "done", Priority: "high", Commitment: "accepted",
		MissionID: "msn_1", RepoID: "rep_1", Role: "coordinator", Assignee: "act_a", DoneBy: "act_b",
		Gate: "needs approval", Timebox: time.Hour, Attempts: 2, DonePR: 412, DoneCommit: "abc", MergedAt: merged, WorkBranch: "agent/x"}
	if len(s.After) != 1 || s.After[0] != deps[0] {
		t.Fatalf("after: %+v", s.After)
	}
	s.After = nil
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("Summarize:\n got %+v\nwant %+v", s, want)
	}
	if z := Summarize(Doc{}); z.Gate != "" || z.Timebox != 0 || !z.MergedAt.IsZero() || len(z.After) != 0 {
		t.Fatalf("empty row: %+v", z)
	}
}
