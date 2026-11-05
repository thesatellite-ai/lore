// Package coord is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/adapters/signals/coord (P3-SIGCOORD-1),
// where a row document is a closed canonjson.Object. This shell keeps the
// v0.1.17 map form (Doc map[string]any, dbent/schema claim-log types) for
// the old binary: names without a document are aliases, and every
// operation that takes or returns a document converts at the edge and runs
// in the root package. New code imports the root package directly.
package coord

import (
	"context"
	"fmt"
	"time"

	"dbent/schema"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"

	root "github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/timeentry"
)

// Agents forwards to the root package.
type Agents = root.Agents

// FenceKind forwards to the root package.
type FenceKind = root.FenceKind

// Fencing failure kinds.
const (
	FenceLost       = root.FenceLost
	FenceCancelled  = root.FenceCancelled
	FenceReassigned = root.FenceReassigned
)

// FencedError forwards to the root package.
type FencedError = root.FencedError

// PoisonedError forwards to the root package.
type PoisonedError = root.PoisonedError

// HasOpenChildrenError forwards to the root package.
type HasOpenChildrenError = root.HasOpenChildrenError

// Sentinels (the root package's values, so errors.Is matches both).
var (
	ErrUnknownTask = root.ErrUnknownTask
	ErrNotOpen     = root.ErrNotOpen
	ErrPoisoned    = root.ErrPoisoned
	ErrNotGated    = root.ErrNotGated
	ErrContended   = root.ErrContended
)

// ClaimOptions forwards to the root package.
type ClaimOptions = root.ClaimOptions

// HeldRecord forwards to the root package.
type HeldRecord = root.HeldRecord

// HeldStore forwards to the root package.
type HeldStore = root.HeldStore

// OverrideKind forwards to the root package.
type OverrideKind = root.OverrideKind

// Override kinds.
const (
	OverrideRevoke   = root.OverrideRevoke
	OverrideCancel   = root.OverrideCancel
	OverrideReassign = root.OverrideReassign
)

// ApproveRequest forwards to the root package.
type ApproveRequest = root.ApproveRequest

// Approval forwards to the root package.
type Approval = root.Approval

// UrgencyContext forwards to the root package.
type UrgencyContext = root.UrgencyContext

// NoWorkError forwards to the root package.
type NoWorkError = root.NoWorkError

// TimeSpans forwards to the root package.
type TimeSpans = root.TimeSpans

// HolderTime forwards to the root package.
type HolderTime = root.HolderTime

// TokenPrefixLen forwards to the root package.
const TokenPrefixLen = root.TokenPrefixLen

// ClaimEventExpire is the claim_log event of an expiry (root
// ClaimEventExpire, in the dbent/schema type the old callers compare with).
const ClaimEventExpire = schema.ClaimEvent(root.ClaimEventExpire)

// IsMission forwards to the root package.
func IsMission(id string) bool { return root.IsMission(id) }

// GateHash forwards to the root package.
func GateHash(title, body string) string { return root.GateHash(title, body) }

// ClaimStateOf forwards to the root package.
func ClaimStateOf(v signals.View, folder, id string) (queue.ClaimState, string) {
	return root.ClaimStateOf(v, folder, id)
}

// LeaseTimeSpans forwards to the root package.
func LeaseTimeSpans(entries []timeentry.Entry, open *signals.Signal, now time.Time, ttl time.Duration) (time.Duration, []HolderTime) {
	return root.LeaseTimeSpans(entries, open, now, ttl)
}

// Rows is the map-form row store (root Rows with Doc map[string]any).
type Rows interface {
	Get(ctx context.Context, id string) (Doc, bool, error)
	Stage(ctx context.Context, rows ...RowWrite) ([]signals.Companion, error)
}

// RowWrite is one map-form row version to stage (root RowWrite).
type RowWrite struct {
	Table string
	ID    string
	Doc   Doc
}

// Env is one project's coordination context (root Env with map-form Rows).
type Env struct {
	Signals *signals.Store
	Folder  string
	Rows    Rows
	Caller  signals.Caller
	Agents  Agents
	Held    HeldStore
	Urgency urgency.Weights
	Time    TimeSpans
}

// rowsAdapter is the root Rows over a map-form Rows.
type rowsAdapter struct{ rows Rows }

// Get implements root.Rows.
func (r rowsAdapter) Get(ctx context.Context, id string) (root.Doc, bool, error) {
	d, ok, err := r.rows.Get(ctx, id)
	if err != nil || !ok {
		return nil, ok, err
	}
	rd, err := toRoot(d)
	return rd, err == nil, err
}

// Stage implements root.Rows.
func (r rowsAdapter) Stage(ctx context.Context, rows ...root.RowWrite) ([]signals.Companion, error) {
	return r.rows.Stage(ctx, fromRootWrites(rows)...)
}

func (e Env) root() root.Env {
	var rows root.Rows
	if e.Rows != nil {
		rows = rowsAdapter{rows: e.Rows}
	}
	return root.Env{Signals: e.Signals, Folder: e.Folder, Rows: rows, Caller: e.Caller,
		Agents: e.Agents, Held: e.Held, Urgency: e.Urgency, Time: e.Time}
}

func fromRootWrites(rows []root.RowWrite) []RowWrite {
	out := make([]RowWrite, len(rows))
	for i, w := range rows {
		out[i] = RowWrite{Table: w.Table, ID: w.ID, Doc: fromRoot(w.Doc)}
	}
	return out
}

func toRootWrites(rows []RowWrite) ([]root.RowWrite, error) {
	out := make([]root.RowWrite, len(rows))
	for i, w := range rows {
		d, err := toRoot(w.Doc)
		if err != nil {
			return nil, err
		}
		out[i] = root.RowWrite{Table: w.Table, ID: w.ID, Doc: d}
	}
	return out, nil
}

// mapMutate runs a map-form mutation on a root document in place.
func mapMutate(rd root.Doc, f func(Doc) error) error {
	d := fromRoot(rd)
	if d == nil {
		d = Doc{}
	}
	if err := f(d); err != nil {
		return err
	}
	nd, err := toRoot(d)
	if err != nil {
		return err
	}
	refill(rd, nd)
	return nil
}

// ClaimResult is a successful claim (root ClaimResult, map-form Doc).
type ClaimResult struct {
	Plan     signals.ClaimPlan
	Doc      Doc
	Overlaps []string
}

func fromRootClaim(r root.ClaimResult) ClaimResult {
	return ClaimResult{Plan: r.Plan, Doc: fromRoot(r.Doc), Overlaps: r.Overlaps}
}

// Claim forwards to the root package.
func Claim(ctx context.Context, env Env, id string, opts ClaimOptions) (ClaimResult, error) {
	r, err := root.Claim(ctx, env.root(), id, opts)
	return fromRootClaim(r), err
}

// Renew forwards to the root package.
func Renew(ctx context.Context, env Env, id string) (signals.Signal, error) {
	return root.Renew(ctx, env.root(), id)
}

// Release forwards to the root package.
func Release(ctx context.Context, env Env, id string) error {
	return root.Release(ctx, env.root(), id)
}

// HolderWrite forwards to the root package; mutate sees the map form.
func HolderWrite(ctx context.Context, env Env, id string, dropClaim bool, mutate func(doc Doc, now time.Time) ([]RowWrite, error)) (Doc, error) {
	d, err := root.HolderWrite(ctx, env.root(), id, dropClaim, func(rd root.Doc, now time.Time) ([]root.RowWrite, error) {
		var extra []RowWrite
		if err := mapMutate(rd, func(d Doc) error {
			var err error
			extra, err = mutate(d, now)
			return err
		}); err != nil {
			return nil, err
		}
		return toRootWrites(extra)
	})
	return fromRoot(d), err
}

// MarkDone is root MarkDone for a map-form document.
func MarkDone(by, commit string, pr int) func(Doc, time.Time) error {
	f := root.MarkDone(by, commit, pr)
	return func(d Doc, now time.Time) error {
		rd, err := toRoot(d)
		if err != nil {
			return err
		}
		if rd == nil {
			rd = root.Doc{}
		}
		if err := f(rd, now); err != nil {
			return err
		}
		refill(d, fromRoot(rd))
		return nil
	}
}

// Override forwards to the root package; mutate sees the map form.
func Override(ctx context.Context, env Env, id string, kind OverrideKind, mutate func(Doc)) (*signals.Signal, error) {
	return OverrideChecked(ctx, env, id, kind, func(d Doc, _ time.Time) error {
		if mutate != nil {
			mutate(d)
		}
		return nil
	})
}

// OverrideChecked forwards to the root package; mutate sees the map form.
func OverrideChecked(ctx context.Context, env Env, id string, kind OverrideKind, mutate func(Doc, time.Time) error) (*signals.Signal, error) {
	var rm func(root.Doc, time.Time) error
	if mutate != nil {
		rm = func(rd root.Doc, now time.Time) error {
			return mapMutate(rd, func(d Doc) error { return mutate(d, now) })
		}
	}
	return root.OverrideChecked(ctx, env.root(), id, kind, rm)
}

// Update forwards to the root package; mutate sees the map form.
func Update(ctx context.Context, env Env, id string, mutate func(Doc, time.Time) error) (Doc, error) {
	d, err := root.Update(ctx, env.root(), id, func(rd root.Doc, now time.Time) error {
		return mapMutate(rd, func(d Doc) error { return mutate(d, now) })
	})
	return fromRoot(d), err
}

// Approve forwards to the root package.
func Approve(ctx context.Context, env Env, req ApproveRequest) (Approval, error) {
	return root.Approve(ctx, env.root(), req)
}

// mustRoot converts for a pure read; a document the encoder cannot hold
// reads as empty, which every caller below treats as "no such field".
func mustRoot(d Doc) root.Doc {
	rd, err := toRoot(d)
	if err != nil || rd == nil {
		return root.Doc{}
	}
	return rd
}

// RequestGate is root RequestGate for a map-form document.
func RequestGate(d Doc, now time.Time) ([]RowWrite, error) {
	rd, err := toRoot(d)
	if err != nil {
		return nil, err
	}
	if rd == nil {
		rd = root.Doc{}
	}
	extra, err := root.RequestGate(rd, now)
	if err != nil {
		return nil, err
	}
	refill(d, fromRoot(rd))
	return fromRootWrites(extra), nil
}

// GateState forwards to the root package.
func GateState(d Doc) string { return root.GateState(mustRoot(d)) }

// Gated forwards to the root package.
func Gated(d Doc) bool { return root.Gated(mustRoot(d)) }

// TaskSummary is root TaskSummary with After in the dbent/schema type.
type TaskSummary struct {
	ID, Title, Body, Status, Priority, Commitment string
	MissionID, RepoID, Role                       string
	Assignee, DoneBy                              string
	After                                         []schema.TaskDependency
	Gate                                          string
	Timebox                                       time.Duration
	Attempts                                      int
	DonePR                                        int
	DoneCommit                                    string
	MergedAt                                      time.Time
	WorkBranch                                    string
}

// Summarize forwards to the root package.
func Summarize(d Doc) TaskSummary {
	s := root.Summarize(mustRoot(d))
	return TaskSummary{ID: s.ID, Title: s.Title, Body: s.Body, Status: s.Status, Priority: s.Priority,
		Commitment: s.Commitment, MissionID: s.MissionID, RepoID: s.RepoID, Role: s.Role,
		Assignee: s.Assignee, DoneBy: s.DoneBy, After: d.BlockedBy(), Gate: s.Gate, Timebox: s.Timebox,
		Attempts: s.Attempts, DonePR: s.DonePR, DoneCommit: s.DoneCommit, MergedAt: s.MergedAt, WorkBranch: s.WorkBranch}
}

// ToQueueTask forwards to the root package.
func ToQueueTask(d Doc, repoName func(string) string, claim queue.ClaimState, claimedBy string) queue.Task {
	return root.ToQueueTask(mustRoot(d), repoName, claim, claimedBy)
}

// OpenChildrenOf forwards to the root package.
func OpenChildrenOf(docs []Doc) func(id string) []string {
	return root.OpenChildrenOf(rootDocs(docs))
}

func rootDocs(docs []Doc) []root.Doc {
	out := make([]root.Doc, len(docs))
	for i, d := range docs {
		out[i] = mustRoot(d)
	}
	return out
}

// NextRequest is root NextRequest with a map-form List.
type NextRequest struct {
	Filter   queue.Filter
	Opts     ClaimOptions
	List     func(ctx context.Context) ([]Doc, error)
	RepoName func(repoID string) string
	Pick     func(n int) int
	Urgency  UrgencyContext
}

func (r NextRequest) root() root.NextRequest {
	out := root.NextRequest{Filter: r.Filter, Opts: r.Opts, RepoName: r.RepoName, Pick: r.Pick, Urgency: r.Urgency}
	if r.List != nil {
		out.List = func(ctx context.Context) ([]root.Doc, error) {
			docs, err := r.List(ctx)
			if err != nil {
				return nil, err
			}
			rd := make([]root.Doc, len(docs))
			for i, d := range docs {
				if rd[i], err = toRoot(d); err != nil {
					return nil, fmt.Errorf("coord: task list: %w", err)
				}
			}
			return rd, nil
		}
	}
	return out
}

// NextResult is root NextResult with a map-form claim.
type NextResult struct {
	Resumed  bool
	Task     string
	Claim    ClaimResult
	Poisoned []string
	Scores   map[string]urgency.Score
}

// Next forwards to the root package.
func Next(ctx context.Context, env Env, req NextRequest) (NextResult, error) {
	r, err := root.Next(ctx, env.root(), req.root())
	return NextResult{Resumed: r.Resumed, Task: r.Task, Claim: fromRootClaim(r.Claim), Poisoned: r.Poisoned, Scores: r.Scores}, err
}

// Queue forwards to the root package.
func Queue(ctx context.Context, env Env, v signals.View, req NextRequest) (queue.Result, error) {
	return root.Queue(ctx, env.root(), v, req.root())
}
