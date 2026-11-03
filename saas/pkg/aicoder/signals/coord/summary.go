package coord

import (
	"time"

	"dbent/schema"
)

// TaskSummary is the part of a task row a reader needs to pick the work
// up (§7.5 step 5: "read its notes: lore task show"): who it is for, what
// it waits on, its gate and its outcome. It is read from the row document
// alone, so a store task shown from any linked repo prints the same fields
// as one in lore.db, without importing the row into it.
type TaskSummary struct {
	ID, Title, Body, Status, Priority, Commitment string
	MissionID, RepoID, Role                       string
	// Assignee and DoneBy are actor ids ("" = none).
	Assignee, DoneBy string
	// After is the --after list (blocked_by edges).
	After []schema.TaskDependency
	// Gate is GateState's text ("" = no gate).
	Gate string
	// Timebox is 0 when the task has none.
	Timebox  time.Duration
	Attempts int
	// DonePR, DoneCommit and MergedAt are the done → merged outcome (F9).
	DonePR     int
	DoneCommit string
	MergedAt   time.Time
	WorkBranch string
}

// Summarize reads a task row into its TaskSummary.
func Summarize(d Doc) TaskSummary {
	return TaskSummary{
		ID: d.Str(keyID), Title: d.Str(keyTitle), Body: d.Str(keyBody), Status: d.Status(),
		Priority: d.Str(keyPriority), Commitment: d.Str(keyCommitment),
		MissionID: d.Str(keyMissionID), RepoID: d.Str(keyRepoID), Role: d.Str(keyRole),
		Assignee: d.Str(keyAssignee), DoneBy: d.Str(keyDoneBy),
		After: d.BlockedBy(), Gate: GateState(d),
		Timebox:  time.Duration(d.Int(keyTimebox)) * time.Second,
		Attempts: d.Int(keyAttempts),
		DonePR:   d.Int(keyDonePR), DoneCommit: d.Str(keyDoneCommit), MergedAt: d.Time(keyMergedAt),
		WorkBranch: d.Str(keyWorkBranch),
	}
}
