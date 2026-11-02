package coord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"
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
}

// NextResult is a successful `task next`.
type NextResult struct {
	// Resumed: the caller already held Task; nothing was claimed.
	Resumed bool
	Task    string
	Claim   ClaimResult
	// Poisoned are tasks this call blocked after max_attempts.
	Poisoned []string
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
	}
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
	tasks := make([]queue.Task, 0, len(docs))
	for _, d := range docs {
		st, by := ClaimStateOf(v, env.Folder, d.Str(keyID))
		tasks = append(tasks, ToQueueTask(d, req.RepoName, st, by))
	}
	return queue.Next(queue.Input{Tasks: tasks, Filter: req.Filter, Now: v.Now, MaxAttempts: env.Agents.MaxAttempts}), nil
}

// Next runs §7.3: resume first, block poisoned tasks, then claim the first
// ready task, re-picking at random among the top three after a lost race.
func Next(ctx context.Context, env Env, req NextRequest) (NextResult, error) {
	v, err := env.Signals.Read(ctx)
	if err != nil {
		return NextResult{}, err
	}
	for _, h := range v.ActiveClaimsOf(env.Caller.ActorID) {
		if h.Signal.Session == env.Caller.Session && !IsMission(h.Signal.Task) {
			return NextResult{Resumed: true, Task: h.Signal.Task}, nil
		}
	}
	res, err := Queue(ctx, env, v, req)
	if err != nil {
		return NextResult{}, err
	}
	var out NextResult
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
