package coord

import (
	"reflect"
	"testing"
	"time"
)

// Summarize reads every field `task show` prints from the row document,
// and an empty row gives the zero summary (no gate, no timebox, no outcome).
func TestSummarize(t *testing.T) {
	merged := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	d := Doc{keyID: str("tsk_1"), keyTitle: str("invite route"), keyBody: str("b"), keyStatus: str("done"), keyPriority: str("high"),
		keyCommitment: str("accepted"), keyMissionID: str("msn_1"), keyRepoID: str("rep_1"), keyRole: str("coordinator"),
		keyAssignee: str("act_a"), keyDoneBy: str("act_b"), keyGate: str(gateRequired), keyDoneCommit: str("abc"), keyWorkBranch: str("agent/x")}
	d.SetInt(keyTimebox, 3600)
	d.SetInt(keyAttempts, 2)
	d.SetInt(keyDonePR, 412)
	d.SetTime(keyMergedAt, merged)
	deps := []TaskDependency{{TaskID: "tsk_0", Until: DependencyUntilMerged}}
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
