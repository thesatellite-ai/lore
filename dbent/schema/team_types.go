package schema

import "time"

// Typed shapes of the JSON columns added for lore for teams
// (LORE_TEAM_SPEC.md §7, §10 C14). They live in the schema package so the
// generated ent code, the lsync exporter and the CLI all share ONE
// definition: a JSON column whose shape is only known by convention drifts
// the first time two writers disagree.
//
// Every struct is written into `.lore/data/<table>/<id>.json` and travels
// through git, so field names are the wire format. Renaming a json tag is a
// format change: older binaries keep the old key as an unknown field (E24)
// and the two never reconcile.

// DependencyUntil says which state of an upstream task satisfies a
// dependency edge (`--after tsk_30:done` / `:merged`). Closed set.
type DependencyUntil string

const (
	// DependencyUntilDone is satisfied as soon as the upstream task is done.
	// Default within one repo: the dependent works on the same code line.
	DependencyUntilDone DependencyUntil = "done"
	// DependencyUntilMerged is satisfied once the upstream task has
	// merged_at set, i.e. its code reached the default branch. Default
	// across repos: another repo's work is only usable once it merged (F9).
	DependencyUntilMerged DependencyUntil = "merged"
)

// TaskDependency is one `--after` edge, stored in tasks.blocked_by.
//
// Invariant: TaskID is a task id (tsk_ prefix). ProjectID is empty when the
// upstream task lives in the same project as the dependent; it is set only
// for a cross-project edge (F15 reference direction rules apply). Cycles are
// refused by the writer (E_CYCLE), never stored.
type TaskDependency struct {
	TaskID    string          `json:"task_id"`
	ProjectID string          `json:"project_id,omitempty"`
	Until     DependencyUntil `json:"until"`
}

// ClaimEvent is the kind of one claim_log entry. Closed set; the spec
// (§7.4 "Claim log") names exactly these.
type ClaimEvent string

const (
	ClaimEventClaim   ClaimEvent = "claim"
	ClaimEventReclaim ClaimEvent = "reclaim"
	ClaimEventRelease ClaimEvent = "release"
	ClaimEventRevoke  ClaimEvent = "revoke"
	ClaimEventApprove ClaimEvent = "approve"
)

// ClaimLogMax bounds tasks.claim_log: the writer keeps only the newest
// ClaimLogMax entries (design principle 1: nothing grows without bound).
const ClaimLogMax = 20

// ClaimLogEntry is one coordination event on a task, stored in
// tasks.claim_log (newest last, at most ClaimLogMax entries).
//
// ActorID/Session identify who acted; PrevActorID is set on a reclaim (the
// holder whose lease expired) and on a revoke (the holder removed). Epoch is
// the task's claim_epoch after the event, so a reader can line the log up
// with fencing decisions.
type ClaimLogEntry struct {
	At          time.Time  `json:"at"`
	Event       ClaimEvent `json:"event"`
	ActorID     string     `json:"actor_id"`
	Session     string     `json:"session,omitempty"`
	PrevActorID string     `json:"prev_actor_id,omitempty"`
	Epoch       int        `json:"epoch"`
}
