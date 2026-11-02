package signals

import (
	"errors"
	"fmt"
	"time"
)

// Caller is who asks for a signals write: an actor in one session.
//
// Invariant: ActorID and Session are set. Two sessions of the same actor are
// two different holders: the second one is refused, never merged into the
// first (F7 edge cases).
type Caller struct {
	ActorID string
	// Label names the caller in messages ("agent:frontend-1", "@alice").
	Label   string
	Session string
	Host    string
}

// HeldError says the subject is held by someone else right now.
type HeldError struct {
	Path string
	// By is the current holder's signal.
	By Signal
	// SameActor is true when the holder is the caller's own actor in
	// another session: the fix is a different LORE_ACTOR, not waiting.
	SameActor bool
}

func (e *HeldError) Error() string {
	subj := e.By.Task
	if e.By.Kind == KindResource {
		subj = e.By.Name
	}
	who := e.By.HolderLabel
	if who == "" {
		who = e.By.Holder
	}
	if e.SameActor {
		host := ""
		if e.By.Host != "" {
			host = " (" + e.By.Host + ")"
		}
		return fmt.Sprintf("%s is held by %s in another session %s%s; set a different LORE_ACTOR for this session",
			subj, who, e.By.Session, host)
	}
	return fmt.Sprintf("%s is held by %s since %s (lease until %s)", subj, who,
		e.By.Since.UTC().Format(time.Kitchen), e.By.Expires.UTC().Format(time.Kitchen))
}

// ErrWIPLimit means the caller already holds agents.max_claims_per_agent
// task claims.
var ErrWIPLimit = errors.New("signals: work-in-progress limit reached")

// WIPError carries the claims the caller already holds.
type WIPError struct {
	Held []Held
	Max  int
}

func (e *WIPError) Error() string {
	ids := ""
	for i, h := range e.Held {
		if i > 0 {
			ids += ", "
		}
		ids += h.Signal.Task
	}
	return fmt.Sprintf("you already hold %s (max_claims_per_agent %d); finish or `lore task release` it first", ids, e.Max)
}

// Unwrap lets errors.Is(err, ErrWIPLimit) match.
func (e *WIPError) Unwrap() error { return ErrWIPLimit }

// ErrNotHeld means the caller's claim (or hold) is not on the ref, or no
// longer valid: the caller was fenced out. Callers classify it further
// (cancelled, reassigned, lost) from the row (§7.4).
var ErrNotHeld = errors.New("signals: your claim is no longer on the signals ref")

// NotHeldError explains a fencing failure.
type NotHeldError struct {
	Path string
	// Current is the signal now at Path, if any (another holder).
	Current *Signal
	// Reason is a short explanation ("no claim", "lease expired at …",
	// "claim epoch 3 ≠ row epoch 4").
	Reason string
}

func (e *NotHeldError) Error() string {
	if e.Current != nil {
		who := e.Current.HolderLabel
		if who == "" {
			who = e.Current.Holder
		}
		return fmt.Sprintf("%s is held by %s since %s (%s)", subjectOf(*e.Current), who,
			e.Current.Since.UTC().Format(time.Kitchen), e.Reason)
	}
	return e.Reason
}

// Unwrap lets errors.Is(err, ErrNotHeld) match.
func (e *NotHeldError) Unwrap() error { return ErrNotHeld }

func subjectOf(s Signal) string {
	if s.Kind == KindResource {
		return s.Name
	}
	return s.Task
}

// RowState is the part of a task (or mission) row a claim decision reads.
type RowState struct {
	// Epoch is the row's claim_epoch.
	Epoch int
	// Attempts is the row's attempts.
	Attempts int
}

// ClaimRequest asks to claim one task or mission (F7 "claim").
//
// The caller has already decided the subject is ready (§7.3: open, not
// gated, dependencies satisfied); this decision only arbitrates holders.
type ClaimRequest struct {
	Caller Caller
	Folder string
	ID     string
	// CountsTowardWIP is false for a mission (a coordinator's claim on a
	// mission does not stop it from claiming a task, §7.5 step 6).
	CountsTowardWIP bool
	// MaxClaims is agents.max_claims_per_agent (0 = unlimited).
	MaxClaims int
	// MaxAttempts is agents.max_attempts (0 = unlimited).
	MaxAttempts int
	Row         RowState
	// Token is a fresh claim token (Tokens.New).
	Token      string
	WorkBranch string
	Owns       []string
}

// ClaimPlan is the outcome of DecideClaim.
type ClaimPlan struct {
	Path string
	// Resumed is true when the caller already holds this claim: nothing is
	// written and Signal is the existing claim.
	Resumed bool
	// Signal is the claim to write (or the held one when Resumed).
	Signal Signal
	// Reclaim is true when an expired claim of another holder is replaced;
	// Prev is that claim.
	Reclaim bool
	Prev    *Signal
	// Epoch and Attempts are the row's new claim_epoch and attempts.
	Epoch    int
	Attempts int
	// Poisoned is true when a reclaim would exceed MaxAttempts: nothing is
	// claimed, the expired claim is dropped and the caller blocks the task
	// ("failed N times", F7 poison task).
	Poisoned bool
}

// DecideClaim arbitrates a claim on req.ID against the board.
//
// Rules (F7, §7.4): an active claim by another holder refuses (HeldError);
// the caller's own claim in the same session resumes; the same actor in
// another session refuses with SameActor; an expired claim is reclaimed
// (attempts+1, or Poisoned past MaxAttempts); the caller may hold at most
// MaxClaims task claims. Every new claim gets claim_epoch = row epoch + 1.
//
// attempts counts holders: the first claim sets it to 1 and each reclaim
// after an expiry adds one, so "failed 3 times" means three holders took the
// task and none finished it.
func DecideClaim(v View, req ClaimRequest) (ClaimPlan, error) {
	p := ClaimPath(req.Folder, req.ID)
	plan := ClaimPlan{Path: p}
	if cur, ok := v.Board[p]; ok {
		if active, isActive := v.Active(p); isActive {
			switch {
			case active.Holder == req.Caller.ActorID && active.Session == req.Caller.Session:
				plan.Resumed = true
				plan.Signal = active
				plan.Epoch = req.Row.Epoch
				plan.Attempts = req.Row.Attempts
				return plan, nil
			case active.Holder == req.Caller.ActorID:
				return ClaimPlan{}, &HeldError{Path: p, By: active, SameActor: true}
			default:
				return ClaimPlan{}, &HeldError{Path: p, By: active}
			}
		}
		prev := cur
		plan.Reclaim = true
		plan.Prev = &prev
	}
	if req.CountsTowardWIP && req.MaxClaims > 0 {
		var held []Held
		for _, h := range v.ActiveClaimsOf(req.Caller.ActorID) {
			if h.Signal.Session == req.Caller.Session && h.Path != p && isTaskID(h.Signal.Task) {
				held = append(held, h)
			}
		}
		if len(held) >= req.MaxClaims {
			return ClaimPlan{}, &WIPError{Held: held, Max: req.MaxClaims}
		}
	}
	plan.Attempts = req.Row.Attempts
	switch {
	case plan.Reclaim:
		if req.MaxAttempts > 0 && req.Row.Attempts >= req.MaxAttempts {
			plan.Poisoned = true
			return plan, nil
		}
		plan.Attempts++
	case plan.Attempts == 0:
		plan.Attempts = 1
	}
	plan.Epoch = req.Row.Epoch + 1
	now := normalizeTime(v.Now)
	plan.Signal = Signal{
		Kind: KindClaim, Task: req.ID, Project: req.Folder,
		Holder: req.Caller.ActorID, HolderLabel: req.Caller.Label, Session: req.Caller.Session,
		ClaimToken: req.Token, ClaimEpoch: plan.Epoch,
		Since: now, Expires: now.Add(v.Lease.TTL), RenewedSeq: 0,
		WorkBranch: req.WorkBranch, Owns: append([]string(nil), req.Owns...), Host: req.Caller.Host,
	}
	return plan, nil
}

// taskIDPrefix marks task ids (ids.go prefixes); only task claims count
// toward the WIP limit.
const taskIDPrefix = "tsk_"

func isTaskID(id string) bool {
	return len(id) > len(taskIDPrefix) && id[:len(taskIDPrefix)] == taskIDPrefix
}

// FindOwn returns the caller's own claim on id, whatever its expiry; ok is
// false when the claim at that path belongs to someone else or is absent.
func FindOwn(v View, c Caller, folder, id string) (Signal, bool) {
	s, ok := v.Board[ClaimPath(folder, id)]
	if !ok || s.Holder != c.ActorID || s.Session != c.Session {
		return Signal{}, false
	}
	return s, true
}

// notHeld builds the fencing error for path p.
func notHeld(v View, p, reason string) error {
	e := &NotHeldError{Path: p, Reason: reason}
	if cur, ok := v.Board[p]; ok {
		c := cur
		e.Current = &c
	}
	return e
}

// DecideRenew renews the caller's claim (F7 "renew"): the caller's claim must
// be on the ref and not expired on the caller's clock. It returns the new
// signal: Expires = now + TTL, RenewedSeq + 1. Renewing never touches the row.
func DecideRenew(v View, c Caller, folder, id string) (Signal, error) {
	p := ClaimPath(folder, id)
	s, ok := FindOwn(v, c, folder, id)
	if !ok {
		return Signal{}, notHeld(v, p, "your claim is not on the signals ref")
	}
	if ExpiredForHolder(s, v.Now) {
		return Signal{}, notHeld(v, p, "your lease expired at "+s.Expires.UTC().Format(time.RFC3339))
	}
	s.Expires = normalizeTime(v.Now).Add(v.Lease.TTL)
	s.RenewedSeq++
	return s, nil
}

// CheckHolder is the fencing check for a holder write (done, note, gate
// request by the holder; F7 table): the caller's claim is on the ref, not
// expired on its clock, and its claim_epoch equals the row's.
func CheckHolder(v View, c Caller, folder, id string, rowEpoch int) (Signal, error) {
	p := ClaimPath(folder, id)
	s, ok := FindOwn(v, c, folder, id)
	if !ok {
		return Signal{}, notHeld(v, p, "your claim is not on the signals ref")
	}
	if ExpiredForHolder(s, v.Now) {
		return Signal{}, notHeld(v, p, "your lease expired at "+s.Expires.UTC().Format(time.RFC3339))
	}
	if s.ClaimEpoch != rowEpoch {
		return Signal{}, notHeld(v, p, fmt.Sprintf("claim epoch %d does not match the task's %d", s.ClaimEpoch, rowEpoch))
	}
	return s, nil
}

// DecideRelease checks the caller holds the claim (token on the ref; expiry
// does not matter — giving back an expired claim is always allowed) and
// returns the claim to remove.
func DecideRelease(v View, c Caller, folder, id string) (Signal, error) {
	s, ok := FindOwn(v, c, folder, id)
	if !ok {
		return Signal{}, notHeld(v, ClaimPath(folder, id), "your claim is not on the signals ref")
	}
	return s, nil
}

// HoldRequest asks to hold a resource (§7.2 resource holds).
type HoldRequest struct {
	Caller Caller
	Folder string
	Name   string
	TTL    time.Duration
	Token  string
}

// DecideHold arbitrates a resource hold: exclusive, same lease rules as a
// claim. The caller's own active hold is refreshed (expires = now + TTL,
// renewed_seq + 1) and keeps its token.
func DecideHold(v View, req HoldRequest) (Signal, error) {
	if err := CheckName(req.Name); err != nil {
		return Signal{}, err
	}
	p := ResourcePath(req.Folder, req.Name)
	now := normalizeTime(v.Now)
	if active, ok := v.Active(p); ok {
		switch {
		case active.Holder == req.Caller.ActorID && active.Session == req.Caller.Session:
			active.Expires = now.Add(req.TTL)
			active.RenewedSeq++
			return active, nil
		case active.Holder == req.Caller.ActorID:
			return Signal{}, &HeldError{Path: p, By: active, SameActor: true}
		default:
			return Signal{}, &HeldError{Path: p, By: active}
		}
	}
	return Signal{
		Kind: KindResource, Name: req.Name, Project: req.Folder,
		Holder: req.Caller.ActorID, HolderLabel: req.Caller.Label, Session: req.Caller.Session,
		ClaimToken: req.Token, Since: now, Expires: now.Add(req.TTL), Host: req.Caller.Host,
	}, nil
}

// DecideUnhold checks the caller holds the resource and returns its path.
func DecideUnhold(v View, c Caller, folder, name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	p := ResourcePath(folder, name)
	s, ok := v.Board[p]
	if !ok || s.Holder != c.ActorID || s.Session != c.Session {
		return "", notHeld(v, p, "you do not hold "+name)
	}
	return p, nil
}
