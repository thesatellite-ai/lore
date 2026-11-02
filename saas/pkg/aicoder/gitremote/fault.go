package gitremote

import (
	"context"
	"sync"
)

// FaultRemote wraps a Remote and injects the failures every team flow must
// survive (LORE_TEAM_SPEC.md §9.1): a rejected nth push, a protection
// refusal with GitHub's GH006 text, offline, a remote without atomic push,
// and arbitrary actions (a concurrent writer, a remote ref deletion) run
// right before the nth push reaches the remote.
//
// Configure it before use; the setters are safe to call concurrently with
// operations, and counters are shared by all goroutines using the value.
// Pushes and fetches are numbered from 1 in the order they are called.
//
//	f := gitremote.NewFaultRemote(&gitremote.Git{Repo: repo, Name: "origin"})
//	f.RejectPush(1, gitremote.StatusNonFastForward) // first push loses a race
//	f.BeforePush(2, func(ctx context.Context) error { return otherClone.WriteAndPush(ctx) })
//	f.SetOffline(true)                              // every call fails with ErrUnconfirmed
type FaultRemote struct {
	inner Remote

	mu        sync.Mutex
	offline   bool
	noAtomic  bool
	protected map[string]bool
	rejects   map[int]Status
	before    map[int][]func(context.Context) error
	pushes    int
	fetches   int
}

// compile-time check.
var _ Remote = (*FaultRemote)(nil)

// NewFaultRemote wraps inner with no faults configured.
func NewFaultRemote(inner Remote) *FaultRemote {
	return &FaultRemote{inner: inner, protected: map[string]bool{}, rejects: map[int]Status{}, before: map[int][]func(context.Context) error{}}
}

// faultStderr is what an injected offline failure reports.
const faultStderr = "fatal: unable to access remote: injected offline (FaultRemote)"

// gh006Format is GitHub's protection refusal, as the remote prints it.
const gh006Format = "remote: error: GH006: Protected branch update failed for "

// SetOffline makes every later call fail as an unreachable remote does.
func (f *FaultRemote) SetOffline(off bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offline = off
}

// SetNoAtomic makes atomic pushes fail with ErrAtomicUnsupported, as on a
// host without the capability; nothing is sent.
func (f *FaultRemote) SetNoAtomic(no bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noAtomic = no
}

// Protect makes every push that updates ref fail with StatusProtected and
// GitHub's GH006 message, as a protected branch does. Like the real host,
// an atomic push containing it applies nothing; a non-atomic push still
// applies its other refs.
func (f *FaultRemote) Protect(ref string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.protected[ref] = true
}

// RejectPush makes the nth push (1-based) fail with status for every ref
// without reaching the remote, as if another writer won the race.
func (f *FaultRemote) RejectPush(n int, status Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejects[n] = status
}

// BeforePush runs fn right before the nth push (1-based) is sent, after the
// caller decided what to push: the place a concurrent writer or a remote
// ref deletion does the most damage. An error from fn fails that push with
// the error.
func (f *FaultRemote) BeforePush(n int, fn func(context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.before[n] = append(f.before[n], fn)
}

// Pushes returns how many pushes were attempted.
func (f *FaultRemote) Pushes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pushes
}

// Fetches returns how many fetches were attempted.
func (f *FaultRemote) Fetches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches
}

func (f *FaultRemote) offlineErr(op string) error {
	return &TransportError{Op: op, Err: ErrUnconfirmed, Stderr: faultStderr}
}

// Fetch implements Remote.
func (f *FaultRemote) Fetch(ctx context.Context, req FetchRequest) error {
	f.mu.Lock()
	f.fetches++
	off := f.offline
	f.mu.Unlock()
	if off {
		return f.offlineErr(opFetch)
	}
	return f.inner.Fetch(ctx, req)
}

// List implements Remote.
func (f *FaultRemote) List(ctx context.Context, patterns ...string) (map[string]string, error) {
	f.mu.Lock()
	off := f.offline
	f.mu.Unlock()
	if off {
		return nil, f.offlineErr(opList)
	}
	return f.inner.List(ctx, patterns...)
}

// Push implements Remote.
func (f *FaultRemote) Push(ctx context.Context, req PushRequest) (PushResult, error) {
	if err := validate(req); err != nil {
		return PushResult{}, err
	}
	f.mu.Lock()
	f.pushes++
	n := f.pushes
	off, noAtomic := f.offline, f.noAtomic
	reject, hasReject := f.rejects[n]
	hooks := append([]func(context.Context) error(nil), f.before[n]...)
	protected := map[string]bool{}
	for _, u := range req.Updates {
		if f.protected[u.Dst] {
			protected[u.Dst] = true
		}
	}
	f.mu.Unlock()

	if off {
		return PushResult{}, f.offlineErr(opPush)
	}
	if req.Atomic && noAtomic {
		return PushResult{}, &TransportError{Op: opPush, Err: ErrAtomicUnsupported, Stderr: "fatal: the receiving end does not support --atomic push"}
	}
	for _, h := range hooks {
		if err := h(ctx); err != nil {
			return PushResult{}, err
		}
	}
	if hasReject {
		return synthetic(req, func(RefUpdate) (Status, bool) { return reject, true }), nil
	}
	if len(protected) == 0 {
		return f.inner.Push(ctx, req)
	}
	if req.Atomic {
		return synthetic(req, func(u RefUpdate) (Status, bool) { return StatusProtected, protected[u.Dst] }), nil
	}
	// Non-atomic: the protected refs are refused, the others go through.
	var rest PushRequest
	for _, u := range req.Updates {
		if !protected[u.Dst] {
			rest.Updates = append(rest.Updates, u)
		}
	}
	var applied PushResult
	if len(rest.Updates) > 0 {
		var err error
		if applied, err = f.inner.Push(ctx, rest); err != nil {
			return PushResult{}, err
		}
	}
	out := PushResult{}
	for _, u := range req.Updates {
		if protected[u.Dst] {
			out.Refs = append(out.Refs, RefResult{Dst: u.Dst, Status: StatusProtected, Message: "[remote rejected] (protected branch hook declined); " + gh006Format + u.Dst + "."})
			continue
		}
		r, _ := applied.Ref(u.Dst)
		out.Refs = append(out.Refs, r)
	}
	return out, nil
}

// synthetic builds a failed result without contacting the remote. pick
// returns the status of an update and whether that update is one of the
// failing refs. Refs that are not failing get StatusAtomicFailed in an
// atomic push and the first failing status otherwise: nothing was sent, so
// no ref may be reported as applied.
func synthetic(req PushRequest, pick func(RefUpdate) (Status, bool)) PushResult {
	out := PushResult{}
	var failing Status
	for _, u := range req.Updates {
		if s, ok := pick(u); ok {
			failing = s
			break
		}
	}
	for _, u := range req.Updates {
		s, ok := pick(u)
		switch {
		case ok:
		case req.Atomic:
			s = StatusAtomicFailed
		default:
			s = failing
		}
		msg := "[rejected] (injected by FaultRemote)"
		if s == StatusProtected {
			msg = "[remote rejected] (protected branch hook declined); " + gh006Format + u.Dst + "."
		}
		out.Refs = append(out.Refs, RefResult{Dst: u.Dst, Status: s, Message: msg})
	}
	return out
}
