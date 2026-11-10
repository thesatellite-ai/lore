package coord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/logic/queue"

	"github.com/thesatellite-ai/lore/logic/urgency"
)

// NextRequest is one `lore task next` (§7.3).
type NextRequest struct {
	Filter queue.Filter
	Opts   ClaimOptions
	// List returns every task row the caller can see (all sources),
	// including dependencies outside the filter.
	List func(ctx context.Context) ([]Doc, error)
	// RepoName maps a row's repo_id to the repo name --repo matches; nil
	// uses the id as the name.
	RepoName func(repoID string) string
	// Pick returns a number in [0, n) for the random pick after a lost
	// race (the Rand seam).
	Pick func(n int) int
	// Urgency is what the score reads beyond the task row (TASK 13.2):
	// tag names and mission target dates; zero value = none known.
	Urgency UrgencyContext
}

// UrgencyContext resolves the urgency inputs that live on other rows.
type UrgencyContext struct {
	// NextTag reports whether a tag id names the `next` tag.
	NextTag func(tagID string) bool
	// MissionTarget is a mission's target_date (zero = none).
	MissionTarget func(missionID string) time.Time
}

// NextResult is a successful `task next`.
type NextResult struct {
	// Resumed: the caller already held Task; nothing was claimed.
	Resumed bool
	Task    string
	Claim   ClaimResult
	// Poisoned are tasks this call blocked after max_attempts.
	Poisoned []string
	// Scores are the urgency of every open task ranked (TASK 13; --why).
	Scores map[string]urgency.Score
}

// NoWorkError is E_NO_WORK: nothing is ready; Waiting explains each
// matched task.
type NoWorkError struct {
	Waiting  []queue.Waiting
	Poisoned []string
}

func (e *NoWorkError) Error() string {
	if len(e.Waiting) == 0 {
		return "nothing ready: no open task matches"
	}
	var b strings.Builder
	b.WriteString("nothing ready:")
	for _, w := range e.Waiting {
		fmt.Fprintf(&b, "\n  %s %s: %s", w.Task.ID, w.Task.Title, w.Reason)
	}
	return b.String()
}

// ErrContended is E_CONTENDED: ready work exists but every attempt lost.
var ErrContended = errors.New("coord: every claim attempt lost a race")

// ToQueueTask converts a row to the queue's view; claim is its signals state.
func ToQueueTask(d Doc, repoName func(string) string, claim queue.ClaimState, claimedBy string) queue.Task {
	repo := d.Str(keyRepoID)
	if repoName != nil && repo != "" {
		repo = repoName(repo)
	}
	var after []queue.Dep
	for _, dep := range d.BlockedBy() {
		after = append(after, queue.Dep{TaskID: dep.TaskID, Until: queue.Until(dep.Until)})
	}
	return queue.Task{
		ID: d.Str(keyID), Title: d.Str(keyTitle), Status: queue.Status(d.Status()),
		Priority: queue.PriorityRank(d.Str(keyPriority)), CreatedAt: d.Time(keyCreatedAt),
		MissionID: d.Str(keyMissionID), Repo: repo, Role: d.Str(keyRole), Gated: Gated(d),
		Attempts: d.Int(keyAttempts), After: after,
		Merged: !d.Time(keyMergedAt).IsZero(), DonePR: d.Int(keyDonePR), DoneCommit: d.Str(keyDoneCommit),
		Claim: claim, ClaimedBy: claimedBy,
		ParentID: d.Str(keyParentID), Due: d.Time(keyDueAt), Proposed: d.Str(keyCommitment) == commitmentProposed,
	}
}

// commitmentProposed is the commitment the proposed term reads (13.2).
const commitmentProposed = "proposed"

// withUrgency fills the inputs that live on other rows (tags, missions).
func withUrgency(t queue.Task, d Doc, u UrgencyContext) queue.Task {
	if u.NextTag != nil {
		for _, id := range d.Strs(keyTagIDs) {
			if u.NextTag(id) {
				t.NextTag = true
				break
			}
		}
	}
	if u.MissionTarget != nil && t.MissionID != "" {
		t.MissionTarget = u.MissionTarget(t.MissionID)
	}
	return t
}

// ClaimStateOf reads a task's claim state from a view.
func ClaimStateOf(v signals.View, folder, id string) (queue.ClaimState, string) {
	p := signals.ClaimPath(folder, id)
	s, ok := v.Board[p]
	if !ok {
		return queue.Unclaimed, ""
	}
	if _, active := v.Active(p); active {
		return queue.Claimed, holderName(s)
	}
	return queue.ClaimExpired, holderName(s)
}

// Queue builds the ready queue as this reader sees it.
func Queue(ctx context.Context, env Env, v signals.View, req NextRequest) (queue.Result, error) {
	docs, err := req.List(ctx)
	if err != nil {
		return queue.Result{}, err
	}
	return queueOf(env, v, req, docs), nil
}

// OpenChildrenOf indexes the open sub-tasks of every parent in docs
// (TASK 6.9: a claim of such a parent is a coordinator lease).
func OpenChildrenOf(docs []Doc) func(id string) []string {
	kids := map[string][]string{}
	for _, d := range docs {
		if p := d.Str(keyParentID); p != "" && isOpen(d.Status()) {
			kids[p] = append(kids[p], d.Str(keyID))
		}
	}
	return func(id string) []string { return kids[id] }
}

func queueOf(env Env, v signals.View, req NextRequest, docs []Doc) queue.Result {
	tasks := make([]queue.Task, 0, len(docs))
	for _, d := range docs {
		st, by := ClaimStateOf(v, env.Folder, d.Str(keyID))
		tasks = append(tasks, withUrgency(ToQueueTask(d, req.RepoName, st, by), d, req.Urgency))
	}
	return queue.Next(queue.Input{Tasks: tasks, Filter: req.Filter, Now: v.Now, MaxAttempts: env.Agents.MaxAttempts, Weights: env.Urgency})
}

// Next runs §7.3: resume first, block poisoned tasks, then claim the first
// ready task, re-picking at random among the top three after a lost race.
func Next(ctx context.Context, env Env, req NextRequest) (NextResult, error) {
	v, err := env.Signals.Read(ctx)
	if err != nil {
		return NextResult{}, err
	}
	docs, err := req.List(ctx)
	if err != nil {
		return NextResult{}, err
	}
	if req.Opts.OpenChildren == nil {
		req.Opts.OpenChildren = OpenChildrenOf(docs)
	}
	// Resume first (§7.3 step 2), but a coordinator lease on a parent is
	// not work to resume (TASK 6.9): the caller is offered a leaf.
	for _, h := range v.ActiveClaimsOf(env.Caller.ActorID) {
		if h.Signal.Session == env.Caller.Session && !IsMission(h.Signal.Task) && len(req.Opts.OpenChildren(h.Signal.Task)) == 0 {
			return NextResult{Resumed: true, Task: h.Signal.Task}, nil
		}
	}
	res := queueOf(env, v, req, docs)
	out := NextResult{Scores: res.Scores}
	for _, t := range res.Poisoned {
		_, err := Claim(ctx, env, t.ID, req.Opts)
		var pe *PoisonedError
		switch {
		case errors.As(err, &pe):
			out.Poisoned = append(out.Poisoned, t.ID)
		case errors.Is(err, signals.ErrOffline):
			return out, err
		}
	}
	if len(res.Ready) == 0 {
		return out, &NoWorkError{Waiting: res.Waiting, Poisoned: out.Poisoned}
	}
	pick := req.Pick
	if pick == nil {
		pick = func(int) int { return 0 }
	}
	tried := map[string]bool{}
	cur := res.Ready[0]
	limit := len(res.Ready) + env.Signals.Lease.PushRetries
	for i := 0; i < limit; i++ {
		opts := req.Opts
		opts.Repo = cur.Repo
		cr, err := Claim(ctx, env, cur.ID, opts)
		if err == nil {
			out.Task, out.Claim = cur.ID, cr
			return out, nil
		}
		var he *signals.HeldError
		var pe *PoisonedError
		switch {
		case errors.As(err, &he) && he.SameActor:
			return out, err
		case errors.As(err, &he), errors.Is(err, ErrNotOpen), errors.As(err, &pe):
			tried[cur.ID] = true
		default:
			return out, err
		}
		next, ok := queue.PickAfterLoss(res.Ready, tried, pick)
		if !ok {
			break
		}
		cur = next
	}
	return out, ErrContended
}
