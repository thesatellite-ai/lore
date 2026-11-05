// Package queue is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/logic/queue (P3-SIGCOORD-1). Every
// exported name is an alias, so the old importers need no edit; new code
// imports the root package directly.
package queue

import root "github.com/thesatellite-ai/lore/logic/queue"

// Status forwards to the root package.
type Status = root.Status

// Task statuses.
const (
	StatusTodo       = root.StatusTodo
	StatusInProgress = root.StatusInProgress
	StatusDone       = root.StatusDone
	StatusCancelled  = root.StatusCancelled
	StatusBlocked    = root.StatusBlocked
)

// Until forwards to the root package.
type Until = root.Until

// Dependency modes.
const (
	UntilDone   = root.UntilDone
	UntilMerged = root.UntilMerged
)

// Dep forwards to the root package.
type Dep = root.Dep

// ClaimState forwards to the root package.
type ClaimState = root.ClaimState

// Claim states.
const (
	Unclaimed    = root.Unclaimed
	Claimed      = root.Claimed
	ClaimExpired = root.ClaimExpired
)

// Priority ranks.
const (
	PriorityLow    = root.PriorityLow
	PriorityMedium = root.PriorityMedium
	PriorityHigh   = root.PriorityHigh
	PriorityUrgent = root.PriorityUrgent
)

// PriorityRank forwards to the root package.
func PriorityRank(name string) int { return root.PriorityRank(name) }

// Task forwards to the root package.
type Task = root.Task

// Filter forwards to the root package.
type Filter = root.Filter

// Input forwards to the root package.
type Input = root.Input

// Waiting forwards to the root package.
type Waiting = root.Waiting

// WaitKind forwards to the root package.
type WaitKind = root.WaitKind

// Wait kinds.
const (
	WaitDependency = root.WaitDependency
	WaitGate       = root.WaitGate
	WaitClaimed    = root.WaitClaimed
	WaitBlocked    = root.WaitBlocked
	WaitCycle      = root.WaitCycle
	WaitPoison     = root.WaitPoison
)

// Result forwards to the root package.
type Result = root.Result

// Next forwards to the root package.
func Next(in Input) Result { return root.Next(in) }

// CheckCycle forwards to the root package.
func CheckCycle(tasks []Task, id string, deps []string) []string {
	return root.CheckCycle(tasks, id, deps)
}

// PickAfterLoss forwards to the root package.
func PickAfterLoss(ready []Task, tried map[string]bool, pick func(n int) int) (Task, bool) {
	return root.PickAfterLoss(ready, tried, pick)
}
