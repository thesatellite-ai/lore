package coord

// revoked.go — the revoke marker (devco:S-12, S-13): a person's revoke
// fences the holder out of the task, not only out of its token.
//
// A revoke's row change (claim log, epoch) stays in the revoker's clone
// until a sync, while the holder reads only the signals ref on every
// write. So the holder used to (1) report its lease as "expired" at a time
// still in the future, never naming the person, and (2) claim the very
// task again with its next `task next`. Now the revoke puts a marker on
// the signals ref in the same push that drops the claim:
//
//	person's revoke
//	   └─► Change.Put[<folder>/revoked/<task>.json] (holder, session, token, by, at)
//	holder's next write ─► E_LOST_CLAIM "revoked by <person> at <time>"
//	holder's task next  ─► the task waits ("revoked from you by …")
//	holder's task claim ─► refused, names the reassign that lifts it
//	person's reassign   ─► marker dropped; the assignee may claim again

import (
	"errors"
	"fmt"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// RevokeKeep is how long a revoke marker stays on the signals ref when no
// person reassigns the task: long enough to keep the revoked holder off it
// for the working week, short enough that the board never grows with
// history (an expired marker is dropped by the next writer).
const RevokeKeep = 7 * 24 * time.Hour

// RevokedError is a claim of a task a person revoked from this caller
// (the same actor): the person decides who works on it next. It unwraps to
// ErrNotOpen, so task next moves on to the next ready task.
type RevokedError struct {
	Task string
	// By names the person who revoked the claim (label, else actor id);
	// At is when.
	By string
	At time.Time
}

func (e *RevokedError) Error() string {
	return fmt.Sprintf("%s was revoked from you by %s at %s; a person decides who works on it next", e.Task, e.By, e.At.UTC().Format(time.Kitchen))
}

// Unwrap makes a task revoked from the caller one it may not claim.
func (e *RevokedError) Unwrap() error { return ErrNotOpen }

// IsRevoked reports a RevokedError and returns it.
func IsRevoked(err error) (*RevokedError, bool) {
	var re *RevokedError
	ok := errors.As(err, &re)
	return re, ok
}

// RevokeMarkerOf is the live revoke marker of task id, if any.
func RevokeMarkerOf(v signals.View, folder, id string) (signals.Signal, bool) {
	m, ok := v.Active(signals.RevokedPath(folder, id))
	if !ok || m.Kind != signals.KindRevoke {
		return signals.Signal{}, false
	}
	return m, true
}

// revokerName is how messages name the person in a revoke marker.
func revokerName(m signals.Signal) string {
	if m.ByLabel != "" {
		return m.ByLabel
	}
	return m.By
}

// RevokedFrom is the RevokedError a claim of task id by actor gets when a
// person revoked that actor's claim on it (every session of the actor:
// a second window of the same agent must not pick it up either); nil
// otherwise.
func RevokedFrom(v signals.View, folder, id, actor string) error {
	m, ok := RevokeMarkerOf(v, folder, id)
	if !ok || m.Holder != actor {
		return nil
	}
	return &RevokedError{Task: id, By: revokerName(m), At: m.Since}
}

// revokeMarker is the marker a revoke of removed (the claim it drops)
// puts on the signals ref. The revoker's own claim (a person revoking
// themselves) gets none: nobody else decided anything.
func revokeMarker(env Env, v signals.View, id string, removed signals.Signal) (signals.Signal, bool) {
	if removed.Holder == env.Caller.ActorID {
		return signals.Signal{}, false
	}
	now := v.Now
	return signals.Signal{
		Kind: signals.KindRevoke, Task: id, Project: env.Folder,
		Holder: removed.Holder, HolderLabel: removed.HolderLabel, Session: removed.Session, ClaimToken: removed.ClaimToken,
		Since: now, Expires: now.Add(RevokeKeep), RenewedAt: now, Host: env.Caller.Host,
		By: env.Caller.ActorID, ByLabel: env.Caller.Label,
	}, true
}
