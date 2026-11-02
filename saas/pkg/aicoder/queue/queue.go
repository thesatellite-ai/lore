// Package queue is the ready queue of lore for teams: which open tasks an
// agent may take next, in what order, and why every other task is not ready
// (LORE_TEAM_SPEC.md §7.3, §7.2 dependencies, F9 `--after` done/merged).
//
// Why a package of its own: `lore task next`, `lore mission status`,
// `lore inbox` and the `--after` cycle check all need the same answer to
// "is this task ready, and if not, why?". Written once as pure functions
// over plain values, the answer is testable as an input-space matrix with no
// git, no database and no clock, and every view explains a waiting task the
// same way.
//
// The package decides nothing about storage: callers load tasks from every
// source (live rows, branch data, store projects), resolve the claim state
// from the signals ref and the merged state from the merged check, and pass
// plain values in.
package queue

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status is a task status (dbent/schema/enums.go TaskStatus).
type Status string

// Task statuses. Closed set mirrored from the schema so this package does
// not import the generated code.
const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
	StatusBlocked    Status = "blocked"
)

// Open reports whether s is a status work can still happen in.
func (s Status) Open() bool { return s == StatusTodo || s == StatusInProgress }

// Until says which upstream state satisfies a dependency edge
// (schema.DependencyUntil).
type Until string

// Dependency edge kinds.
const (
	UntilDone   Until = "done"
	UntilMerged Until = "merged"
)

// Dep is one `--after` edge.
type Dep struct {
	TaskID string
	Until  Until
}

// ClaimState is what the signals ref says about a task right now.
type ClaimState int

const (
	// Unclaimed means no claim file, or one this reader treats as absent.
	Unclaimed ClaimState = iota
	// Claimed means an active claim by someone.
	Claimed
	// ClaimExpired means a claim file whose lease has expired for this
	// reader (a reclaim candidate).
	ClaimExpired
)

// Priority ranks (dbent/schema TaskPriority low < medium < high < urgent).
const (
	PriorityLow    = 1
	PriorityMedium = 2
	PriorityHigh   = 3
	PriorityUrgent = 4
)

// PriorityRank maps a schema priority name to its rank; unknown names rank
// as medium (the schema default).
func PriorityRank(name string) int {
	switch name {
	case "low":
		return PriorityLow
	case "high":
		return PriorityHigh
	case "urgent":
		return PriorityUrgent
	}
	return PriorityMedium
}

// Task is what the queue needs to know about one task.
type Task struct {
	ID    string
	Title string
	// Status is the row status.
	Status Status
	// Priority is a rank (PriorityRank).
	Priority  int
	CreatedAt time.Time
	MissionID string
	// Repo is the task's repo name ("" = no repo).
	Repo string
	// Role is the plan role ("" = none).
	Role string
	// Gated is true while a gate is required or requested, or approved
	// with a hash that no longer matches title + body (§7.4).
	Gated bool
	// Attempts is the row's attempts.
	Attempts int
	// After are the task's dependencies.
	After []Dep
	// Merged is true once merged_at is set; DonePR / DoneCommit help the
	// waiting reason name the code.
	Merged     bool
	DonePR     int
	DoneCommit string
	// Claim is the signals state; ClaimedBy names the holder for reasons.
	Claim     ClaimState
	ClaimedBy string
}

// Filter is the caller's `task next` filter (§7.3).
type Filter struct {
	// Mission limits to one mission ("" = any).
	Mission string
	// Repo is `--repo` ("" = none given).
	Repo string
	// Here is the caller's own repo name, used when Repo is empty: a
	// caller without --repo is offered tasks with no repo or of its repo.
	Here string
	// Role is `--role` ("" = none).
	Role string
	// AnyRepoRole makes views (mission status, inbox) see every task of
	// the mission whatever its repo and role; `task next` never sets it.
	AnyRepoRole bool
}

// Input is everything one ready-queue decision reads.
type Input struct {
	// Tasks are every task the caller can see, including dependencies in
	// other missions or sources; dependencies missing here are "not found".
	Tasks []Task
	Filter
	Now time.Time
	// MaxAttempts is agents.max_attempts (0 = unlimited).
	MaxAttempts int
}

// Waiting is a task the filter matched that is not ready, with the reason.
type Waiting struct {
	Task   Task
	Reason string
	// Kind classifies the reason for views (mission status columns).
	Kind WaitKind
}

// WaitKind is a closed set of reasons a matched task is not ready.
type WaitKind string

const (
	WaitDependency WaitKind = "waiting"
	WaitGate       WaitKind = "gated"
	WaitClaimed    WaitKind = "claimed"
	WaitBlocked    WaitKind = "blocked"
	WaitCycle      WaitKind = "cycle"
	WaitPoison     WaitKind = "poison"
)

// Result is a ready-queue decision.
type Result struct {
	// Ready is in claim order: effective priority, then age, then id.
	Ready []Task
	// Waiting lists every other matched open task, in the same order.
	Waiting []Waiting
	// Poisoned are matched tasks whose expired claim cannot be reclaimed
	// because attempts reached MaxAttempts; `task next` blocks them
	// ("failed N times", F7 poison task).
	Poisoned []Task
}

// agingStep is the age after which a task gains one priority step (§7.3
// step 4), so old low-priority work is not starved forever.
const agingStep = 24 * time.Hour

// Next computes the ready queue.
func Next(in Input) Result {
	byID := make(map[string]Task, len(in.Tasks))
	for _, t := range in.Tasks {
		byID[t.ID] = t
	}
	eff := effectivePriorities(in.Tasks, byID, in.Now)
	var res Result
	for _, t := range in.Tasks {
		if !t.Status.Open() || !matches(t, in.Filter) {
			continue
		}
		if cyc := cycleThrough(byID, t.ID); cyc != nil {
			res.Waiting = append(res.Waiting, Waiting{Task: t, Kind: WaitCycle, Reason: "cycle: " + strings.Join(cyc, " → ")})
			continue
		}
		if reason, ok := depsSatisfied(t, byID); !ok {
			res.Waiting = append(res.Waiting, Waiting{Task: t, Kind: WaitDependency, Reason: reason})
			continue
		}
		if t.Gated {
			res.Waiting = append(res.Waiting, Waiting{Task: t, Kind: WaitGate, Reason: fmt.Sprintf("gate needs approval (lore gate approve %s)", t.ID)})
			continue
		}
		switch t.Claim {
		case Claimed:
			res.Waiting = append(res.Waiting, Waiting{Task: t, Kind: WaitClaimed, Reason: "claimed by " + orUnknown(t.ClaimedBy)})
			continue
		case ClaimExpired:
			if in.MaxAttempts > 0 && t.Attempts >= in.MaxAttempts {
				res.Poisoned = append(res.Poisoned, t)
				res.Waiting = append(res.Waiting, Waiting{Task: t, Kind: WaitPoison, Reason: fmt.Sprintf("failed %d times", t.Attempts)})
				continue
			}
		}
		res.Ready = append(res.Ready, t)
	}
	less := func(a, b Task) bool {
		if eff[a.ID] != eff[b.ID] {
			return eff[a.ID] > eff[b.ID]
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	}
	sort.SliceStable(res.Ready, func(i, j int) bool { return less(res.Ready[i], res.Ready[j]) })
	sort.SliceStable(res.Waiting, func(i, j int) bool { return less(res.Waiting[i].Task, res.Waiting[j].Task) })
	return res
}

func orUnknown(s string) string {
	if s == "" {
		return "another holder"
	}
	return s
}

// matches applies the §7.3 step 3 filter.
func matches(t Task, f Filter) bool {
	if f.Mission != "" && t.MissionID != f.Mission {
		return false
	}
	if f.AnyRepoRole {
		return true
	}
	if t.Role != "" && t.Role != f.Role {
		return false
	}
	switch {
	case f.Repo != "":
		return t.Repo == f.Repo || (t.Repo == "" && t.Role != "" && t.Role == f.Role)
	default:
		return t.Repo == "" || t.Repo == f.Here
	}
}

// depsSatisfied checks every --after edge.
func depsSatisfied(t Task, byID map[string]Task) (string, bool) {
	for _, d := range t.After {
		up, ok := byID[d.TaskID]
		switch {
		case !ok:
			return fmt.Sprintf("waits for %s, which was not found", d.TaskID), false
		case up.Status == StatusCancelled:
			return fmt.Sprintf("waits for %s, which was cancelled", d.TaskID), false
		case up.Status != StatusDone:
			return fmt.Sprintf("waits for %s to be %s (%s)", d.TaskID, untilWord(d.Until), describe(up)), false
		case d.Until == UntilMerged && !up.Merged:
			return fmt.Sprintf("waits for %s to be merged (%s)", d.TaskID, describe(up)), false
		}
	}
	return "", true
}

func untilWord(u Until) string {
	if u == UntilMerged {
		return "merged"
	}
	return "done"
}

// describe summarises an upstream task's state for a waiting reason.
func describe(t Task) string {
	switch {
	case t.Status == StatusDone && t.DonePR > 0:
		return fmt.Sprintf("done, pr #%d not merged yet", t.DonePR)
	case t.Status == StatusDone && t.DoneCommit != "":
		return fmt.Sprintf("done, %s not on the default branch yet", shortSHA(t.DoneCommit))
	case t.Status == StatusDone:
		return "done, merge unknown"
	case t.Claim == Claimed:
		return "claimed by " + orUnknown(t.ClaimedBy)
	}
	return string(t.Status)
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// effectivePriorities returns max(own priority, priority of any open task
// waiting on it, directly or transitively) plus one aging step for tasks
// older than a day, capped at urgent (§7.3 step 4).
func effectivePriorities(tasks []Task, byID map[string]Task, now time.Time) map[string]int {
	eff := make(map[string]int, len(tasks))
	for _, t := range tasks {
		p := t.Priority
		if p == 0 {
			p = PriorityMedium
		}
		if !t.CreatedAt.IsZero() && now.Sub(t.CreatedAt) > agingStep && p < PriorityUrgent {
			p++
		}
		eff[t.ID] = p
	}
	// Propagate a waiter's priority to everything it waits on, transitively.
	// Bounded by the number of tasks: each pass can only raise values.
	for range tasks {
		changed := false
		for _, t := range tasks {
			if !t.Status.Open() {
				continue
			}
			for _, d := range t.After {
				if _, ok := byID[d.TaskID]; ok && eff[d.TaskID] < eff[t.ID] {
					eff[d.TaskID] = eff[t.ID]
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return eff
}

// cycleThrough returns a dependency cycle that passes through id (as ids,
// first == last), or nil.
func cycleThrough(byID map[string]Task, id string) []string {
	return findPath(byID, nil, id, id)
}

// findPath searches for a path from `from` back to target over After edges;
// extra adds edges not stored yet (a proposed `--after`).
func findPath(byID map[string]Task, extra map[string][]string, from, target string) []string {
	seen := map[string]bool{}
	var walk func(cur string, path []string) []string
	walk = func(cur string, path []string) []string {
		for _, next := range edges(byID, extra, cur) {
			if next == target {
				return append(append([]string(nil), path...), next)
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if p := walk(next, append(path, next)); p != nil {
				return p
			}
		}
		return nil
	}
	return walk(from, []string{from})
}

func edges(byID map[string]Task, extra map[string][]string, id string) []string {
	var out []string
	for _, d := range byID[id].After {
		out = append(out, d.TaskID)
	}
	return append(out, extra[id]...)
}

// CheckCycle reports the cycle that adding edges `id --after deps` would
// create (E_CYCLE at add and edit, §7.2), or nil. tasks are the known tasks;
// id may be new (not in tasks yet).
func CheckCycle(tasks []Task, id string, deps []string) []string {
	byID := make(map[string]Task, len(tasks)+1)
	for _, t := range tasks {
		byID[t.ID] = t
	}
	// The proposed edges replace the task's stored ones (edit sets them).
	cur := byID[id]
	cur.ID = id
	cur.After = nil
	byID[id] = cur
	extra := map[string][]string{id: deps}
	for _, d := range deps {
		if d == id {
			return []string{id, id}
		}
	}
	return findPath(byID, extra, id, id)
}

// PickAfterLoss chooses the next candidate after a lost claim race (§7.3
// step 5): a random one among the top three not tried yet. pick(n) returns
// a number in [0, n).
func PickAfterLoss(ready []Task, tried map[string]bool, pick func(n int) int) (Task, bool) {
	const topK = 3
	var cands []Task
	for _, t := range ready {
		if !tried[t.ID] {
			cands = append(cands, t)
			if len(cands) == topK {
				break
			}
		}
	}
	if len(cands) == 0 {
		return Task{}, false
	}
	return cands[pick(len(cands))], true
}
