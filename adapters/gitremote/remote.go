// Package gitremote moves refs between a local repository and a remote:
// fetch into tracking refs, push with a per-ref lease and optionally
// atomically, and list the remote's refs. It is the network half of the git
// seams in LORE_TEAM_SPEC.md §9.1 (package gitref is the local half).
//
// Why it exists: every team flow (live writes, signals, landing, compaction)
// ends in a push that may lose a race, hit a protected branch or not reach
// the remote at all, and each case needs a different reaction — merge and
// retry, ride along on a code branch, or fail closed. git reports those
// cases only as text; this package turns them into a typed result per ref,
// so callers decide on Status values instead of parsing messages, and tests
// can produce every case on demand with FaultRemote.
//
// Rules it enforces (mechanism, not policy):
//   - fetches write only into the tracking namespace the caller names and
//     never apply the remote's configured fetch refspecs as a side effect
//     (`--refmap=`), so a forced refspec in .git/config can never overwrite
//     a local refs/lore/* ref that holds unpushed commits (F3);
//   - a push either yields a verdict for every ref or an error wrapping
//     ErrUnconfirmed — never a guess (fixed rule 11: coordination writes
//     fail closed).
package gitremote

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// Remote is one remote repository as seen from one local repository.
//
// Implementations: Git (the real one) and FaultRemote (wraps any Remote and
// injects failures). All methods must be safe for concurrent use.
type Remote interface {
	// Fetch runs one fetch of the given refspecs. Remote refs that no longer
	// exist are pruned from the destinations when Prune is set.
	Fetch(ctx context.Context, req FetchRequest) error
	// Push sends updates and reports a verdict per ref (see PushResult).
	Push(ctx context.Context, req PushRequest) (PushResult, error)
	// List returns the remote's refs matching patterns (all when none),
	// keyed by full name.
	List(ctx context.Context, patterns ...string) (map[string]string, error)
}

// FetchRequest is one fetch.
type FetchRequest struct {
	// Refspecs say what to fetch and where to store it, e.g. the result of
	// TrackingRefspecs.
	Refspecs []string
	// Prune removes destination refs whose source no longer exists on the
	// remote (only refs matching these refspecs' destinations are touched),
	// so a ref deleted on the remote disappears from its tracking copy.
	Prune bool
}

// UpdateMode says what the remote must hold for a ref update to be applied.
type UpdateMode int

const (
	// ModeFastForward is a plain push: applied only when the new commit
	// descends from the remote's current one (or the ref is new). Live
	// writes use it: a concurrent writer makes it fail, never vanish (F2).
	ModeFastForward UpdateMode = iota
	// ModeLease is `--force-with-lease=<dst>:<Expect>`: applied only when
	// the remote ref is exactly Expect ("" = it must not exist), even when
	// the update is not a fast-forward. Signals and compaction use it
	// (fixed rule 10, F8).
	ModeLease
)

// RefUpdate is one ref in a push.
type RefUpdate struct {
	// Src is the local commit id to send; "" deletes Dst on the remote.
	Src string
	// Dst is the full remote ref name.
	Dst string
	// Mode is ModeFastForward or ModeLease.
	Mode UpdateMode
	// Expect is the remote commit the lease requires (ModeLease only);
	// "" means the ref must not exist.
	Expect string
}

// PushRequest is one push of one or more refs.
type PushRequest struct {
	// Updates are sent in one `git push`.
	Updates []RefUpdate
	// Atomic asks the remote to apply all updates or none (`--atomic`).
	// A remote that cannot do that fails the whole push with
	// ErrAtomicUnsupported and changes nothing.
	Atomic bool
}

// Status is the remote's verdict on one ref.
type Status int

const (
	// StatusOK means the ref was created, updated, forced or deleted.
	StatusOK Status = iota
	// StatusUpToDate means the remote already held that value.
	StatusUpToDate
	// StatusNonFastForward means a plain push lost to a commit the caller
	// has not seen ("fetch first" / "non-fast-forward"): fetch, merge,
	// retry.
	StatusNonFastForward
	// StatusStale means a lease failed: the remote ref was not at Expect.
	// Re-read and re-decide (signals never merge).
	StatusStale
	// StatusProtected means the host refused because the ref is protected
	// (GitHub GH006, GitLab protected branches): landing switches to the
	// ride-along path (F6).
	StatusProtected
	// StatusRemoteRejected is any other refusal by the remote (a hook, a
	// permission, a size limit); Message has the reason.
	StatusRemoteRejected
	// StatusAtomicFailed means this ref was fine but another ref of the
	// same atomic push failed, so nothing was applied.
	StatusAtomicFailed
)

// String names the status for logs and --trace.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusUpToDate:
		return "up-to-date"
	case StatusNonFastForward:
		return "non-fast-forward"
	case StatusStale:
		return "stale-lease"
	case StatusProtected:
		return "protected"
	case StatusRemoteRejected:
		return "remote-rejected"
	case StatusAtomicFailed:
		return "atomic-failed"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Applied reports whether the remote now holds what the update asked for.
func (s Status) Applied() bool { return s == StatusOK || s == StatusUpToDate }

// RefResult is the verdict on one ref.
type RefResult struct {
	// Dst is the remote ref.
	Dst string
	// Status is the verdict.
	Status Status
	// Message is git's summary for the ref ("[rejected] (stale info)"),
	// plus any matching remote message for a protection refusal.
	Message string
}

// PushResult holds one RefResult per requested update, in request order.
//
// Invariant: when Push returns a nil error, Refs has exactly one entry per
// update; a rejection is a Status, not an error.
type PushResult struct {
	Refs []RefResult
}

// OK reports whether every update was applied.
func (p PushResult) OK() bool {
	for _, r := range p.Refs {
		if !r.Status.Applied() {
			return false
		}
	}
	return len(p.Refs) > 0
}

// Ref returns the verdict for dst.
func (p PushResult) Ref(dst string) (RefResult, bool) {
	for _, r := range p.Refs {
		if r.Dst == dst {
			return r, true
		}
	}
	return RefResult{}, false
}

// Sentinels.
var (
	// ErrUnconfirmed means git gave no verdict for the refs (the remote
	// could not be reached, refused the connection, or the command failed
	// before any ref was sent). Nothing may be assumed applied: coordination
	// writes report E_OFFLINE. Wrapped by *TransportError.
	ErrUnconfirmed = errors.New("gitremote: remote did not confirm the push")
	// ErrAtomicUnsupported means the remote cannot apply an atomic push;
	// nothing was changed. Callers fall back to ordered writes (F7).
	ErrAtomicUnsupported = errors.New("gitremote: remote does not support atomic push")
	// ErrRemoteRefMissing means a fetch named a ref the remote lacks.
	ErrRemoteRefMissing = errors.New("gitremote: remote ref not found")
	// ErrBadRequest means the request itself is malformed (no updates, a
	// ref twice, a lease on a plain update).
	ErrBadRequest = errors.New("gitremote: invalid request")
)

// TransportError is a fetch, push or list that did not complete; Stderr is
// git's message. errors.Is(err, ErrUnconfirmed) is true for pushes and
// fetches that got no answer.
type TransportError struct {
	// Op is "push", "fetch" or "ls-remote".
	Op string
	// Err is the sentinel (ErrUnconfirmed, ErrAtomicUnsupported, …).
	Err error
	// Stderr is git's standard error.
	Stderr string
}

// Error renders the operation and git's message.
func (e *TransportError) Error() string {
	return fmt.Sprintf("gitremote: %s: %v: %s", e.Op, e.Err, e.Stderr)
}

// Unwrap exposes the sentinel.
func (e *TransportError) Unwrap() error { return e.Err }

// Tracking namespaces (glossary "Refs", F3, C7).
const (
	// LoreRefsPrefix holds lore's shared refs on every remote.
	LoreRefsPrefix = "refs/lore/"
	// TrackingPrefix is the local-only namespace fetch copies refs/lore/*
	// into: refs/lore-remote/<remote>/.
	TrackingPrefix = "refs/lore-remote/"
	// headsPrefix holds branches.
	headsPrefix = "refs/heads/"
	// remotesPrefix holds remote-tracking branches.
	remotesPrefix = "refs/remotes/"
)

// TrackingRef maps a remote ref to its local tracking copy:
// refs/lore/live → refs/lore-remote/origin/live, refs/heads/main →
// refs/remotes/origin/main. ok is false for refs outside those two
// namespaces.
func TrackingRef(remote, ref string) (string, bool) {
	switch {
	case strings.HasPrefix(ref, LoreRefsPrefix):
		return TrackingPrefix + remote + "/" + strings.TrimPrefix(ref, LoreRefsPrefix), true
	case strings.HasPrefix(ref, headsPrefix):
		return remotesPrefix + remote + "/" + strings.TrimPrefix(ref, headsPrefix), true
	}
	return "", false
}

// TrackingRefspecs returns the refspecs of the one fetch lore runs (F3 step
// 2): all of refs/lore/* into refs/lore-remote/<remote>/*, plus each named
// branch into refs/remotes/<remote>/<branch>, so live, signals and the
// default branch are seen at the same moment. They are forced ("+") because
// tracking refs are mirrors of the remote; local refs/lore/* refs are never
// a destination.
func TrackingRefspecs(remote string, branches ...string) []string {
	specs := []string{"+" + LoreRefsPrefix + "*:" + TrackingPrefix + remote + "/*"}
	for _, b := range branches {
		specs = append(specs, "+"+headsPrefix+b+":"+remotesPrefix+remote+"/"+b)
	}
	return specs
}

// protectionMarkers are texts hosts use when a protected ref refuses a
// push, matched case-insensitively against the ref's reason and against the
// remote's message lines that name the ref. Sources: GitHub "GH006:
// Protected branch update failed for refs/heads/main", GitLab "You are not
// allowed to push code to protected branches on this project", GitLab and
// Gitea "protected branch hook declined".
var protectionMarkers = []string{"gh006", "protected branch", "protected ref", "not allowed to push code to protected"}

// isProtection reports whether a remote rejection of dst is a protection
// refusal: its own reason says so, or a remote message line names dst and
// says so.
func isProtection(reason, stderr, dst string) bool {
	if hasMarker(reason) {
		return true
	}
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.Contains(line, dst) && hasMarker(line) {
			return true
		}
	}
	return false
}

func hasMarker(s string) bool {
	s = strings.ToLower(s)
	for _, m := range protectionMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// validate checks a push request before anything is sent.
func validate(req PushRequest) error {
	if len(req.Updates) == 0 {
		return fmt.Errorf("%w: no updates", ErrBadRequest)
	}
	seen := map[string]bool{}
	for _, u := range req.Updates {
		if !strings.HasPrefix(u.Dst, "refs/") {
			return fmt.Errorf("%w: destination %q is not a full ref", ErrBadRequest, u.Dst)
		}
		if seen[u.Dst] {
			return fmt.Errorf("%w: %s appears twice", ErrBadRequest, u.Dst)
		}
		seen[u.Dst] = true
		if u.Mode == ModeFastForward && u.Expect != "" {
			return fmt.Errorf("%w: %s: Expect is only for ModeLease", ErrBadRequest, u.Dst)
		}
		if u.Src == "" && (u.Mode != ModeLease || u.Expect == "") {
			return fmt.Errorf("%w: deleting %s needs a lease on its current value", ErrBadRequest, u.Dst)
		}
	}
	return nil
}

// compile-time check.
var _ Remote = (*Git)(nil)

// Git is the real Remote: it runs git in Repo against the remote Name (a
// configured remote name or a URL).
type Git struct {
	// Repo is the local repository.
	Repo *gitref.Repo
	// Name is the remote name ("origin") or URL.
	Name string
	// RouteArgs and RouteEnv are the per-call git options and environment
	// that reach this remote as its setup's account (ONBOARDING_DESIGN
	// 3.4, Q5): global options placed before every network subcommand
	// (`-c url.git@<alias>:.insteadOf=git@github.com:`) and extra
	// KEY=VALUE entries (GIT_SSH_COMMAND). Set only for a store clone a
	// setup owns (wire's storeRoute); empty for a code repo's own remote.
	RouteArgs []string
	RouteEnv  []string
}

// invocation is one network call of g: args after g's route options.
func (g *Git) invocation(args []string) gitref.Invocation {
	if len(g.RouteArgs) == 0 && len(g.RouteEnv) == 0 {
		return gitref.Invocation{Args: args}
	}
	return gitref.Invocation{Args: append(append([]string(nil), g.RouteArgs...), args...), Env: g.RouteEnv}
}
