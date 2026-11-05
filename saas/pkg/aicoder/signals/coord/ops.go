package coord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbent/schema"
	"saas/pkg/aicoder/signals"

	"github.com/thesatellite-ai/lore/logic/urgency"
)

// Rows is the row store of one project as the CLI sees it: live wins over
// branch data (F1), and writes go out as companions of a signals push.
type Rows interface {
	// Get returns the current version of a task or mission row, read fresh
	// (after the per-command fetch). ok=false when the id is unknown.
	Get(ctx context.Context, id string) (Doc, bool, error)
	// Stage returns the companions that write every row as its next
	// version — one commit per ref the rows go to (one ref may appear only
	// once in a push; a client task's row and its internal note go to two
	// refs, D8); nothing is applied until each companion's Finalize runs
	// after a confirmed push.
	Stage(ctx context.Context, rows ...RowWrite) ([]signals.Companion, error)
}

// RowWrite is one row version to stage. Table "" means the table of the
// id's kind (tasks or missions).
type RowWrite struct {
	Table string
	ID    string
	Doc   Doc
}

// Agents are the agents.* settings (§1).
type Agents struct {
	MaxClaims   int
	MaxAttempts int
}

// Env is one project's coordination context.
type Env struct {
	Signals *signals.Store
	// Folder is the project folder on the signals ref (`_root`, `web`, a
	// store folder for a client task, D8).
	Folder string
	Rows   Rows
	Caller signals.Caller
	Agents Agents
	Held   HeldStore
	// Urgency are the tasks.urgency.* weights the host read from policy
	// (OV15B-URG); the zero value means the TASK 13.2 defaults.
	Urgency urgency.Weights
	// Time is time.long_span / time.coalesce_gap for the claim spans the
	// writer closes (OV7-6); the zero value means the built-in defaults.
	Time TimeSpans
}

// FenceKind is the kind of fencing failure (§7.4), mapped by the CLI to
// E_LOST_CLAIM, E_CANCELLED and E_REASSIGNED.
type FenceKind string

const (
	// FenceLost: the claim expired or was revoked (E_LOST_CLAIM).
	FenceLost FenceKind = "lost"
	// FenceCancelled: the task was cancelled while held (E_CANCELLED).
	FenceCancelled FenceKind = "cancelled"
	// FenceReassigned: the task now has another assignee (E_REASSIGNED).
	FenceReassigned FenceKind = "reassigned"
)

// FencedError says the caller no longer holds the task.
type FencedError struct {
	Kind   FenceKind
	Task   string
	Detail string
}

func (e *FencedError) Error() string {
	switch e.Kind {
	case FenceCancelled:
		return fmt.Sprintf("%s was cancelled while you held it; stop work on it", e.Task)
	case FenceReassigned:
		return fmt.Sprintf("%s was reassigned while you held it; stop work on it (%s)", e.Task, e.Detail)
	}
	return e.Detail
}

// Unwrap lets errors.Is(err, signals.ErrNotHeld) match every fencing error.
func (e *FencedError) Unwrap() error { return signals.ErrNotHeld }

// Sentinels.
var (
	// ErrUnknownTask means the id is not a task or mission of this project.
	ErrUnknownTask = errors.New("coord: no such task")
	// ErrNotOpen means the task is done, cancelled or blocked.
	ErrNotOpen = errors.New("coord: task is not open")
	// ErrPoisoned means the claim was not taken: the task failed
	// agents.max_attempts times and is now blocked (F7 poison task).
	ErrPoisoned = errors.New("coord: task failed too many times and is blocked")
)

// PoisonedError carries the attempt count.
type PoisonedError struct {
	Task     string
	Attempts int
}

func (e *PoisonedError) Error() string {
	return fmt.Sprintf("%s failed %d times; it is now blocked (see `lore inbox`)", e.Task, e.Attempts)
}

// Unwrap lets errors.Is(err, ErrPoisoned) match.
func (e *PoisonedError) Unwrap() error { return ErrPoisoned }

// Statuses this package writes (dbent/schema TaskStatus).
const (
	statusTodo       = string(schema.TaskStatusTodo)
	statusInProgress = string(schema.TaskStatusInProgress)
	statusDone       = string(schema.TaskStatusDone)
	statusCancelled  = string(schema.TaskStatusCancelled)
	statusBlocked    = string(schema.TaskStatusBlocked)
)

// Mission statuses that end coordination (dbent/schema MissionStatus).
const (
	missionDone      = string(schema.MissionStatusDone)
	missionCancelled = string(schema.MissionStatusCancelled)
)

// claimable: a task must be open; a mission may be coordinated until it is
// done or cancelled.
func claimable(mission bool, status string) bool {
	if mission {
		return status != missionDone && status != missionCancelled
	}
	return isOpen(status)
}

func isOpen(status string) bool {
	return status == statusTodo || status == statusInProgress || status == ""
}

// missionPrefix marks mission ids: a mission claim is a coordinator lease
// with no row change (missions carry no claim_epoch, §7.4).
const missionPrefix = "msn_"

// IsMission reports whether id names a mission.
func IsMission(id string) bool { return strings.HasPrefix(id, missionPrefix) }

// ClaimOptions are the extra inputs of a claim.
type ClaimOptions struct {
	Owns       []string
	WorkBranch string
	// Repo is the task's repo name, remembered for the commit-msg trailer.
	Repo string
	// InRepo: the task row lives in the caller's checkout repo (see
	// HeldRecord.InRepo).
	InRepo bool
	// OpenChildren lists a task's open sub-tasks (TASK 6.9); nil = the
	// caller has no tree (every task is a leaf).
	OpenChildren func(id string) []string
	// Coordinate takes a coordinator lease on a parent with open
	// sub-tasks (`claim --coordinate`, 6.9): it does not count toward WIP
	// and never stops others from claiming the children.
	Coordinate bool
}

// HasOpenChildrenError is E_HAS_OPEN_CHILDREN: a claim of a parent with
// open sub-tasks without --coordinate (TASK 6.9, 24). Children are the
// open ones, so the message can list what to claim instead.
type HasOpenChildrenError struct {
	Task     string
	Children []string
}

func (e *HasOpenChildrenError) Error() string {
	return fmt.Sprintf("%s has %d open sub-task(s): %s; claim a sub-task, or coordinate with --coordinate", e.Task, len(e.Children), strings.Join(e.Children, ", "))
}

// coordinating reports a held task that is a coordinator lease (an open
// parent): exempt from WIP (6.9).
func (o ClaimOptions) coordinating(task string) bool {
	return o.OpenChildren != nil && len(o.OpenChildren(task)) > 0
}

// ClaimResult is a successful claim.
type ClaimResult struct {
	Plan signals.ClaimPlan
	// Doc is the row after the claim (nil for a mission).
	Doc Doc
	// Overlaps names other active claims whose --owns overlap ours.
	Overlaps []string
}

// Claim takes a claim on a task or mission (F7 "claim").
func Claim(ctx context.Context, env Env, id string, opts ClaimOptions) (ClaimResult, error) {
	var out ClaimResult
	var poisoned *PoisonedError
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		out, poisoned = ClaimResult{}, nil
		mission := IsMission(id)
		doc, ok, err := env.Rows.Get(ctx, id)
		if err != nil {
			return signals.Change{}, err
		}
		if !ok {
			return signals.Change{}, fmt.Errorf("%w: %s", ErrUnknownTask, id)
		}
		if !claimable(mission, doc.Status()) {
			return signals.Change{}, fmt.Errorf("%w: %s is %s", ErrNotOpen, id, doc.Status())
		}
		// 6.9: a parent with open children is coordinated, never worked.
		lease := false
		if !mission && opts.OpenChildren != nil {
			if kids := opts.OpenChildren(id); len(kids) > 0 {
				if !opts.Coordinate {
					return signals.Change{}, &HasOpenChildrenError{Task: id, Children: kids}
				}
				lease = true
			}
		}
		// A claim records the caller's branch only while the row has none: a
		// reclaim (or a claim after a release) keeps the work branch the
		// earlier holder recorded and pushed (§7.4 "Work branch"), so the
		// replacement is pointed at that work, never at its own checkout.
		workBranch := opts.WorkBranch
		if recorded := doc.Str(keyWorkBranch); recorded != "" {
			workBranch = recorded
		}
		tok, err := env.Signals.Tokens.New()
		if err != nil {
			return signals.Change{}, err
		}
		plan, err := signals.DecideClaim(v, signals.ClaimRequest{
			Caller: env.Caller, Folder: env.Folder, ID: id, CountsTowardWIP: !mission && !lease, Exempt: opts.coordinating,
			MaxClaims: env.Agents.MaxClaims, MaxAttempts: env.Agents.MaxAttempts,
			Row: signals.RowState{Epoch: doc.Epoch(), Attempts: doc.Int(keyAttempts)}, Token: tok,
			WorkBranch: workBranch, Owns: opts.Owns,
		})
		if err != nil {
			return signals.Change{}, err
		}
		out.Plan = plan
		out.Overlaps = overlaps(v, plan.Path, opts.Owns)
		if plan.Resumed {
			out.Doc = doc
			return signals.Change{}, nil
		}
		if mission {
			return signals.Change{Put: map[string]signals.Signal{plan.Path: plan.Signal}}, nil
		}
		next := doc.Clone()
		now := v.Now
		if plan.Poisoned {
			next[keyStatus] = statusBlocked
			next.touch(now)
			poisoned = &PoisonedError{Task: id, Attempts: plan.Attempts}
			comps, err := env.Rows.Stage(ctx, RowWrite{ID: id, Doc: next})
			if err != nil {
				return signals.Change{}, err
			}
			return signals.Change{Delete: []string{plan.Path}, Companions: comps}, nil
		}
		next.SetInt(keyEpoch, plan.Epoch)
		next.SetInt(keyAttempts, plan.Attempts)
		if doc.Status() != statusInProgress {
			next[keyStatus] = statusInProgress
			if doc.Time(keyStartedAt).IsZero() {
				next.SetTime(keyStartedAt, now)
			}
		}
		if workBranch != "" {
			next.SetStr(keyWorkBranch, workBranch)
		}
		entry := schema.ClaimLogEntry{At: now.UTC().Truncate(time.Second), Event: schema.ClaimEventClaim,
			ActorID: env.Caller.ActorID, Session: env.Caller.Session, Epoch: plan.Epoch}
		if plan.Reclaim && plan.Prev != nil {
			entry.Event = schema.ClaimEventReclaim
			entry.PrevActorID = plan.Prev.Holder
			// D3: the previous holder's span ends at its last heartbeat.
			if err = next.closeClaimSpan(*plan.Prev, now, v.Lease.TTL, env.Time); err != nil {
				return signals.Change{}, err
			}
		}
		if err := next.AppendClaimLog(entry); err != nil {
			return signals.Change{}, err
		}
		next.touch(now)
		out.Doc = next
		comps, err := env.Rows.Stage(ctx, RowWrite{ID: id, Doc: next})
		if err != nil {
			return signals.Change{}, err
		}
		return signals.Change{Put: map[string]signals.Signal{plan.Path: plan.Signal}, Companions: comps}, nil
	})
	if err != nil {
		return ClaimResult{}, err
	}
	if poisoned != nil {
		return out, poisoned
	}
	if !out.Plan.Resumed {
		s := out.Plan.Signal
		rec := HeldRecord{Task: id, Folder: env.Folder, Ref: env.Signals.Ref, Token: s.ClaimToken, Epoch: s.ClaimEpoch,
			Repo: opts.Repo, InRepo: opts.InRepo, Claimed: s.Since, Expires: s.Expires}
		if out.Doc != nil {
			rec.Assignee = out.Doc.Str(keyAssignee)
		}
		if err := env.Held.Put(env.Caller.Session, rec, s.Since); err != nil {
			return out, fmt.Errorf("coord: claim pushed, local record failed: %w", err)
		}
	}
	return out, nil
}

// overlaps lists other active claims whose --owns paths overlap owns
// (directory prefixes and exact files, §7.4 file ownership).
func overlaps(v signals.View, self string, owns []string) []string {
	if len(owns) == 0 {
		return nil
	}
	var out []string
	for p, s := range v.Board {
		if p == self || s.Kind != signals.KindClaim {
			continue
		}
		if _, ok := v.Active(p); !ok {
			continue
		}
		for _, a := range owns {
			for _, b := range s.Owns {
				if pathsOverlap(a, b) {
					out = append(out, fmt.Sprintf("%s (%s) owns %s", s.Task, holderName(s), b))
				}
			}
		}
	}
	return out
}

// pathsOverlap: equal, or one is a directory prefix ("dir/") of the other.
func pathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return (strings.HasSuffix(a, "/") && strings.HasPrefix(b, a)) || (strings.HasSuffix(b, "/") && strings.HasPrefix(a, b))
}

func holderName(s signals.Signal) string {
	if s.HolderLabel != "" {
		return s.HolderLabel
	}
	return s.Holder
}

// classify turns a fencing failure into the §7.4 error the holder sees,
// using the fresh row and the holder's own record.
func classify(ctx context.Context, env Env, id string, cause error) error {
	var nh *signals.NotHeldError
	if !errors.As(cause, &nh) {
		return cause
	}
	doc, ok, err := env.Rows.Get(ctx, id)
	if err != nil {
		return err
	}
	if ok && doc.Status() == statusCancelled {
		return &FencedError{Kind: FenceCancelled, Task: id}
	}
	if rec, had := env.Held.Get(env.Caller.Session, id); had && ok && doc.Str(keyAssignee) != rec.Assignee {
		return &FencedError{Kind: FenceReassigned, Task: id, Detail: "now assigned to " + orNobody(doc.Str(keyAssignee))}
	}
	detail := nh.Error()
	if rec, had := env.Held.Get(env.Caller.Session, id); had {
		if ev, ok := lastRevokeSince(doc, rec.Claimed); ok && nh.Current == nil {
			detail = fmt.Sprintf("%s: your claim (token %s…) was revoked by %s at %s", id, prefix(rec.Token), ev.ActorID, ev.At.UTC().Format(time.Kitchen))
		} else {
			detail += fmt.Sprintf("; your token %s… expired %s", prefix(rec.Token), rec.Expires.UTC().Format(time.Kitchen))
		}
	}
	return &FencedError{Kind: FenceLost, Task: id, Detail: detail}
}

// lastRevokeSince returns the newest revoke in the row's claim log at or
// after since (the caller's claim time).
func lastRevokeSince(doc Doc, since time.Time) (schema.ClaimLogEntry, bool) {
	if doc == nil {
		return schema.ClaimLogEntry{}, false
	}
	log := doc.ClaimLog()
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		if e.At.Before(since.Truncate(time.Second)) {
			break
		}
		if e.Event == schema.ClaimEventRevoke {
			return e, true
		}
	}
	return schema.ClaimLogEntry{}, false
}

func orNobody(s string) string {
	if s == "" {
		return "nobody"
	}
	return s
}

// TokenPrefixLen is how much of a claim token views show.
const TokenPrefixLen = 8

func prefix(tok string) string {
	if len(tok) > TokenPrefixLen {
		return tok[:TokenPrefixLen]
	}
	return tok
}

// Renew is the heartbeat (F7 "renew"): signals only, never the row.
func Renew(ctx context.Context, env Env, id string) (signals.Signal, error) {
	var renewed signals.Signal
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		s, err := signals.DecideRenew(v, env.Caller, env.Folder, id)
		if err != nil {
			return signals.Change{}, err
		}
		renewed = s
		return signals.Change{Put: map[string]signals.Signal{signals.ClaimPath(env.Folder, id): s}}, nil
	})
	if errors.Is(err, signals.ErrNotHeld) {
		return signals.Signal{}, classify(ctx, env, id, err)
	}
	if err == nil {
		if rec, ok := env.Held.Get(env.Caller.Session, id); ok {
			rec.Expires = renewed.Expires
			// Held is a cache of the holder's own past, never the source of
			// truth (HeldRecord): the renewal is already on the signals ref.
			_ = env.Held.Put(env.Caller.Session, rec, renewed.Expires)
		}
	}
	return renewed, err
}

// Release gives a claim back (F7 "release"): the claim leaves the signals
// ref and the row gets one claim_log entry, atomically.
func Release(ctx context.Context, env Env, id string) error {
	now := env.Signals.Clock.Now()
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		s, err := signals.DecideRelease(v, env.Caller, env.Folder, id)
		if err != nil {
			return signals.Change{}, err
		}
		ch := signals.Change{Delete: []string{signals.ClaimPath(env.Folder, id)}}
		if IsMission(id) {
			return ch, nil
		}
		doc, ok, err := env.Rows.Get(ctx, id)
		if err != nil || !ok {
			return signals.Change{}, errors.Join(err, errIf(!ok, fmt.Errorf("%w: %s", ErrUnknownTask, id)))
		}
		next := doc.Clone()
		if err = next.closeClaimSpan(s, v.Now, v.Lease.TTL, env.Time); err != nil { // D4: same write
			return signals.Change{}, err
		}
		if err := next.AppendClaimLog(schema.ClaimLogEntry{At: v.Now.UTC().Truncate(time.Second), Event: schema.ClaimEventRelease,
			ActorID: env.Caller.ActorID, Session: env.Caller.Session, Epoch: s.ClaimEpoch}); err != nil {
			return signals.Change{}, err
		}
		next.touch(v.Now)
		comps, err := env.Rows.Stage(ctx, RowWrite{ID: id, Doc: next})
		if err != nil {
			return signals.Change{}, err
		}
		ch.Companions = comps
		return ch, nil
	})
	if errors.Is(err, signals.ErrNotHeld) {
		return classify(ctx, env, id, err)
	}
	if err == nil {
		_ = env.Held.Drop(env.Caller.Session, id, now) // cache only (HeldRecord); the release is already on the signals ref
	}
	return err
}

func errIf(cond bool, err error) error {
	if cond {
		return err
	}
	return nil
}

// HolderWrite is a write by the holder (done, note, gate request; F7
// table): fenced by token, expiry and epoch, pushed atomically with the row
// change. mutate edits a copy of the row and may return further rows for
// the same commit (a note's comment). dropClaim removes the claim (done).
func HolderWrite(ctx context.Context, env Env, id string, dropClaim bool, mutate func(doc Doc, now time.Time) ([]RowWrite, error)) (Doc, error) {
	var written Doc
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		doc, ok, err := env.Rows.Get(ctx, id)
		if err != nil {
			return signals.Change{}, err
		}
		if !ok {
			return signals.Change{}, fmt.Errorf("%w: %s", ErrUnknownTask, id)
		}
		held, err := signals.CheckHolder(v, env.Caller, env.Folder, id, doc.Epoch())
		if err != nil {
			return signals.Change{}, err
		}
		next := doc.Clone()
		if dropClaim { // holder done: the claim span closes in the same write (D4)
			if err = next.closeClaimSpan(held, v.Now, v.Lease.TTL, env.Time); err != nil {
				return signals.Change{}, err
			}
		}
		extra, err := mutate(next, v.Now)
		if err != nil {
			return signals.Change{}, err
		}
		next.touch(v.Now)
		written = next
		comps, err := env.Rows.Stage(ctx, append([]RowWrite{{ID: id, Doc: next}}, extra...)...)
		if err != nil {
			return signals.Change{}, err
		}
		ch := signals.Change{Companions: comps}
		if dropClaim {
			ch.Delete = []string{signals.ClaimPath(env.Folder, id)}
		}
		return ch, nil
	})
	if errors.Is(err, signals.ErrNotHeld) {
		return nil, classify(ctx, env, id, err)
	}
	if err == nil && dropClaim {
		_ = env.Held.Drop(env.Caller.Session, id, env.Signals.Clock.Now()) // cache only (HeldRecord); the write is already on the signals ref
	}
	return written, err
}

// MarkDone is the holder's `task done` mutation: status done, completed_at,
// done_by, done_commit / done_pr (content that lands, §3.5).
func MarkDone(by, commit string, pr int) func(Doc, time.Time) error {
	return func(d Doc, now time.Time) error {
		d[keyStatus] = statusDone
		d.SetTime(keyCompletedAt, now)
		d[keyCommitment] = commitmentAccepted
		d[keyDeferredUntil] = nil
		d.SetStr(keyDoneBy, by)
		if commit != "" {
			d.SetStr(keyDoneCommit, commit)
		}
		if pr > 0 {
			d.SetInt(keyDonePR, pr)
		}
		return nil
	}
}

// OverrideKind is a human override (§7.4).
type OverrideKind string

const (
	// OverrideRevoke drops the claim; the task stays open for others.
	OverrideRevoke OverrideKind = "revoke"
	// OverrideCancel drops the claim and cancels the task.
	OverrideCancel OverrideKind = "cancel"
	// OverrideReassign drops the claim and hands the task to a new assignee.
	OverrideReassign OverrideKind = "reassign"
)

// Override is a human revoke, cancel or reassign: the claim (if any) leaves
// the signals ref and the row's claim_epoch rises, atomically, so the
// holder's next write is fenced out. mutate applies the kind's own change
// (status cancelled, a new assignee). It returns the removed claim, if any.
// A change that can fail goes through OverrideChecked instead.
func Override(ctx context.Context, env Env, id string, kind OverrideKind, mutate func(Doc)) (*signals.Signal, error) {
	return OverrideChecked(ctx, env, id, kind, func(d Doc, _ time.Time) error {
		if mutate != nil {
			mutate(d)
		}
		return nil
	})
}

// OverrideChecked is Override with a mutation that may refuse, such as
// MarkDone when a human finishes a task whose claim expired. mutate gets
// the signals view's clock, the same instant the claim log records. Why it
// exists: an override that ignored its mutation's error would drop the claim
// and raise the epoch while the row change silently did not happen, and the
// command would still report success. Invariant: when mutate returns an
// error, nothing is written (no claim removed, no epoch raised, no row
// staged) and the error is returned wrapped, so the caller fails closed.
// mutate nil is the plain override.
func OverrideChecked(ctx context.Context, env Env, id string, kind OverrideKind, mutate func(Doc, time.Time) error) (*signals.Signal, error) {
	var removed *signals.Signal
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		removed = nil
		doc, ok, err := env.Rows.Get(ctx, id)
		if err != nil {
			return signals.Change{}, err
		}
		if !ok {
			return signals.Change{}, fmt.Errorf("%w: %s", ErrUnknownTask, id)
		}
		p := signals.ClaimPath(env.Folder, id)
		ch := signals.Change{}
		if s, ok := v.Board[p]; ok {
			c := s
			removed = &c
			ch.Delete = []string{p}
		}
		next := doc.Clone()
		epoch := doc.Epoch() + 1
		next.SetInt(keyEpoch, epoch)
		if mutate != nil {
			if err := mutate(next, v.Now); err != nil {
				removed = nil
				return signals.Change{}, fmt.Errorf("override %s %s: %w", kind, id, err)
			}
		}
		entry := schema.ClaimLogEntry{At: v.Now.UTC().Truncate(time.Second), Event: schema.ClaimEventRevoke,
			ActorID: env.Caller.ActorID, Session: env.Caller.Session, Epoch: epoch}
		if removed != nil {
			entry.PrevActorID = removed.Holder
		}
		if removed != nil { // revoke/reassign/cancel close the holder's span (D4)
			if err = next.closeClaimSpan(*removed, v.Now, v.Lease.TTL, env.Time); err != nil {
				return signals.Change{}, err
			}
		}
		if removed != nil || kind == OverrideRevoke {
			if err := next.AppendClaimLog(entry); err != nil {
				return signals.Change{}, err
			}
		}
		next.touch(v.Now)
		comps, err := env.Rows.Stage(ctx, RowWrite{ID: id, Doc: next})
		if err != nil {
			return signals.Change{}, err
		}
		ch.Companions = comps
		return ch, nil
	})
	return removed, err
}

// GateHash is the approval hash over title and body (§7.4: editing either
// voids the approval).
func GateHash(title, body string) string {
	h := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(h[:])
}

// Gated reports whether the row's gate stops agents: required or requested,
// or approved with a hash that no longer matches.
func Gated(d Doc) bool {
	switch d.Str(keyGate) {
	case "":
		return false
	case string(schema.TaskGateApproved):
		return d.Str(keyApprovedSum) != GateHash(d.Str(keyTitle), d.Str(keyBody))
	}
	return true
}

// Update writes a change to a row that nobody holds, on the coordination
// target (a store project's live ref), with a lease on the signals ref so
// it is confirmed or fails closed. It refuses when the row is claimed: a
// held task changes only through the holder or a human override.
func Update(ctx context.Context, env Env, id string, mutate func(Doc, time.Time) error) (Doc, error) {
	var written Doc
	_, err := writeSignals(ctx, env, func(v signals.View) (signals.Change, error) {
		doc, ok, err := env.Rows.Get(ctx, id)
		if err != nil {
			return signals.Change{}, err
		}
		if !ok {
			return signals.Change{}, fmt.Errorf("%w: %s", ErrUnknownTask, id)
		}
		if s, held := v.Active(signals.ClaimPath(env.Folder, id)); held {
			return signals.Change{}, &signals.HeldError{Path: signals.ClaimPath(env.Folder, id), By: s}
		}
		next := doc.Clone()
		if err := mutate(next, v.Now); err != nil {
			return signals.Change{}, err
		}
		next.touch(v.Now)
		written = next
		comps, err := env.Rows.Stage(ctx, RowWrite{ID: id, Doc: next})
		if err != nil {
			return signals.Change{}, err
		}
		return signals.Change{Companions: comps}, nil
	})
	return written, err
}
