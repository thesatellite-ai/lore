package coord

// done.go — the done marker (G-10): a task is finished once.
//
// Task rows reach another clone only with a sync, while every claim
// decision reads the signals ref atomically. A done used to delete the
// claim and nothing else, so a clone whose row still said todo saw an
// unclaimed task and claimed it again (10 agents: 37 tasks finished 104
// times). Now every coordinated write that leaves a task done also puts a
// done marker on the signals ref, in the same push:
//
//	holder done / person's done over an expired claim
//	   └─► Change.Put[<folder>/done/<task>.json] (kept DoneKeep)
//	claim / task next
//	   └─► marker on the board and the row not edited since ─► AlreadyDoneError
//	       (task next skips it; the queue ranks it as done)
//	claim of a task reopened after the marker ─► allowed, the marker dropped

import (
	"errors"
	"fmt"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// DoneKeep is how long a done marker stays on the signals ref: long enough
// for every clone to sync the done row, short enough that the board never
// grows with history (expired markers are dropped by the next writer).
const DoneKeep = 7 * 24 * time.Hour

// AlreadyDoneError is a claim of a task the signals ref says was finished:
// the caller's row is older than the done. It unwraps to ErrNotOpen, so
// task next moves on to the next ready task.
type AlreadyDoneError struct {
	Task string
	// By names who finished it (holder label, else actor id); At is when.
	By string
	At time.Time
}

func (e *AlreadyDoneError) Error() string {
	return fmt.Sprintf("%s was already done by %s at %s", e.Task, e.By, e.At.UTC().Format(time.RFC3339))
}

// Unwrap makes an already-done task a task that is not open.
func (e *AlreadyDoneError) Unwrap() error { return ErrNotOpen }

// DoneMarkerOf is the done marker of task id that applies to doc: on the
// board, not expired, and the row not edited after it (a row edited later
// was reopened; a row edited before is a copy that has not seen the done).
func DoneMarkerOf(v signals.View, folder, id string, doc Doc) (signals.Signal, bool) {
	m, ok := v.Active(signals.DonePath(folder, id))
	if !ok || m.Kind != signals.KindDone {
		return signals.Signal{}, false
	}
	if doc != nil && doc.Time(keyUpdatedAt).UTC().Truncate(time.Second).After(m.Since) {
		return signals.Signal{}, false
	}
	return m, true
}

// AlreadyDone is the error a claim of task id gets when a done marker
// applies to doc; nil otherwise.
func AlreadyDone(v signals.View, folder, id string, doc Doc) error {
	m, ok := DoneMarkerOf(v, folder, id, doc)
	if !ok {
		return nil
	}
	by := m.HolderLabel
	if by == "" {
		by = m.Holder
	}
	return &AlreadyDoneError{Task: id, By: by, At: m.Since}
}

// IsAlreadyDone reports an AlreadyDoneError and returns it.
func IsAlreadyDone(err error) (*AlreadyDoneError, bool) {
	var ad *AlreadyDoneError
	ok := errors.As(err, &ad)
	return ad, ok
}

// withDoneMarker adds the done marker of task id to ch when next leaves the
// task done (the caller finished it in this write).
func withDoneMarker(ch signals.Change, env Env, v signals.View, id string, next Doc) (signals.Change, error) {
	if IsMission(id) || next.Status() != statusDone {
		return ch, nil
	}
	tok, err := env.Signals.Tokens.New()
	if err != nil {
		return ch, err
	}
	if ch.Put == nil {
		ch.Put = map[string]signals.Signal{}
	}
	ch.Put[signals.DonePath(env.Folder, id)] = signals.Signal{
		Kind: signals.KindDone, Task: id, Project: env.Folder,
		Holder: env.Caller.ActorID, HolderLabel: env.Caller.Label, Session: env.Caller.Session,
		ClaimToken: tok, Since: v.Now, Expires: v.Now.Add(DoneKeep), RenewedAt: v.Now, Host: env.Caller.Host,
	}
	return ch, nil
}
