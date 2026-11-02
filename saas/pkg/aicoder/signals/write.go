package signals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
)

// Companion is another ref that must change together with a signals write:
// the task row on a live ref (claim, release, holder done, revoke …; F7
// "Writes" column). The signals lease and the companion are pushed in ONE
// `git push --atomic`, so a holder that lost its claim cannot change the row.
//
// Prepare is called once per attempt, after the signals decision, and must
// build the companion commit on the freshest state it can see without
// pushing it or moving any local ref; it is called again after a lost race.
// Finalize runs only after the push was confirmed (move local refs, refresh
// caches, start landing).
type Companion interface {
	Prepare(ctx context.Context) (gitremote.RefUpdate, error)
	Finalize(ctx context.Context) error
}

// RemoteCompanion is a companion on ANOTHER remote than the signals ref —
// a client repo's live ref when its claims live in a store (D8). It cannot
// join the atomic push, so the executor pushes it right after the signals
// push was confirmed (the decision was fenced by the lease already) and
// reports ErrPartial if that second push fails: the documented window of
// TEAM-036, the same as the ordered fallback. PushSelf must push exactly
// what Prepare built.
type RemoteCompanion interface {
	Companion
	PushSelf(ctx context.Context) error
}

// Change is what one decision wants written.
type Change struct {
	// Put adds or replaces signal files (path → signal).
	Put map[string]Signal
	// Delete removes signal files. Expired and malformed files are dropped
	// by the executor on every write anyway (fixed rule 10).
	Delete []string
	// Companions are pushed atomically with the signals ref.
	Companions []Companion
}

// Decide is one decision run against a fresh view. It is re-run from a new
// fetch after every lost race, so it must not have side effects beyond
// capturing its result in the caller's variables.
type Decide func(v View) (Change, error)

// Tokens makes claim tokens.
type Tokens interface {
	// New returns a fresh, unique token ("ct_" + 32 hex characters).
	New() (string, error)
}

// CryptoTokens draws tokens from crypto/rand.
type CryptoTokens struct{}

// tokenPrefix marks claim tokens; tokenBytes of randomness make a collision
// between two claims practically impossible.
const (
	tokenPrefix = "ct_"
	tokenBytes  = 16
)

// New implements Tokens.
func (CryptoTokens) New() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("signals: claim token: %w", err)
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

// Jitter draws retry delays. Production uses a random source; tests pass a
// fixed one.
type Jitter interface {
	// Int63n returns a number in [0, n).
	Int63n(n int64) int64
}

// CryptoJitter draws retry jitter from crypto/rand, so agents started at
// the same instant on one machine never share a sequence.
type CryptoJitter struct{}

// Int63n implements Jitter.
func (CryptoJitter) Int63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return n / 2
	}
	return v.Int64()
}

// Errors of the executor. Callers map them to error codes: ErrOffline →
// E_OFFLINE (exit 15); ErrRejected is a generic failure naming the reason.
var (
	// ErrOffline means the write could not be confirmed: the remote was
	// unreachable, autopush is off, or the retries ran out. Nothing is
	// queued (fixed rule 11).
	ErrOffline = errors.New("signals: coordination write not confirmed")
	// ErrRejected means the remote refused the write for a reason a retry
	// cannot fix (protected ref, hook, permission).
	ErrRejected = errors.New("signals: remote refused the signals write")
	// ErrPartial means the remote cannot push atomically, the signals write
	// was applied and the companion write failed (the documented window of
	// TEAM-036). The caller reports it; a re-run converges.
	ErrPartial = errors.New("signals: signals written but the companion write failed")
)

// OfflineError explains an ErrOffline.
type OfflineError struct {
	// Reason is "autopush is off", "remote unreachable", "gave up after N
	// lost races".
	Reason string
	Err    error
}

func (e *OfflineError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("could not confirm the coordination write (%s): %v", e.Reason, e.Err)
	}
	return "could not confirm the coordination write (" + e.Reason + ")"
}

// Unwrap lets errors.Is(err, ErrOffline) match.
func (e *OfflineError) Unwrap() error { return ErrOffline }

// Store is one signals ref of one repository plus everything a write needs.
//
// Invariant: every write is one parentless commit holding the full state,
// pushed with a lease on the value read in the same attempt; the local copy
// lives only in the tracking ref (refs/lore-remote/<remote>/…), so there is
// never local signals state that could be pushed later.
type Store struct {
	Repo   *gitref.Repo
	Remote gitremote.Remote
	// RemoteName names the tracking namespace ("origin").
	RemoteName string
	// Ref is the signals ref (CodeRepoRef or StoreRef(branch)).
	Ref   string
	Seen  SeenStore
	Clock clock.Clock
	Lease Lease
	// Signature returns the commit author for a write at t.
	Signature func(t time.Time) gitref.Signature
	// Autopush false makes every write fail closed without network.
	Autopush bool
	Tokens   Tokens
	Jitter   Jitter
	// Sleep waits between retries (nil = real sleep honouring ctx).
	Sleep func(ctx context.Context, d time.Duration) error
	// NoAtomic forces the ordered fallback (signals first, then the
	// companions). Set it when doctor found the host lacks atomic push;
	// the executor also switches by itself on ErrAtomicUnsupported.
	NoAtomic bool
	// Trace, when set, receives one line per fetch, push and retry.
	Trace func(format string, args ...any)
}

// Result reports a finished write.
type Result struct {
	// Old and New are the signals commits before and after ("" = none).
	Old, New string
	// Pushed is false when nothing changed and nothing was pushed.
	Pushed bool
	// Attempts is the number of decide+push rounds (1 = no lost race).
	Attempts int
	// Atomic tells whether the companions went in one atomic push.
	Atomic bool
}

func (s *Store) tracef(format string, args ...any) {
	if s.Trace != nil {
		s.Trace(format, args...)
	}
}

func (s *Store) tracking() (string, error) {
	t, ok := gitremote.TrackingRef(s.RemoteName, s.Ref)
	if !ok {
		return "", fmt.Errorf("signals: %s is not under refs/lore/", s.Ref)
	}
	return t, nil
}

// fetchState fetches the signals ref into its tracking ref and returns the
// commit ("" when the remote has none) and its tree.
func (s *Store) fetchState(ctx context.Context) (string, map[string][]byte, error) {
	track, err := s.tracking()
	if err != nil {
		return "", nil, err
	}
	s.tracef("signals: fetch %s", s.Ref)
	err = s.Remote.Fetch(ctx, gitremote.FetchRequest{Refspecs: []string{"+" + s.Ref + ":" + track}})
	switch {
	case errors.Is(err, gitremote.ErrRemoteRefMissing):
		if old, ok, rerr := s.Repo.ResolveRef(ctx, track); rerr == nil && ok {
			_ = s.Repo.DeleteRef(ctx, track, old)
		}
		return "", map[string][]byte{}, nil
	case errors.Is(err, gitremote.ErrUnconfirmed):
		return "", nil, &OfflineError{Reason: "remote unreachable", Err: err}
	case err != nil:
		return "", nil, fmt.Errorf("signals: fetch %s: %w", s.Ref, err)
	}
	return s.readTracking(ctx)
}

func (s *Store) readTracking(ctx context.Context) (string, map[string][]byte, error) {
	track, err := s.tracking()
	if err != nil {
		return "", nil, err
	}
	oid, ok, err := s.Repo.ResolveRef(ctx, track)
	if err != nil {
		return "", nil, fmt.Errorf("signals: resolve %s: %w", track, err)
	}
	if !ok {
		return "", map[string][]byte{}, nil
	}
	tree, err := s.Repo.ReadTree(ctx, oid, "")
	if err != nil {
		return "", nil, fmt.Errorf("signals: read %s: %w", track, err)
	}
	return oid, tree, nil
}

// view builds this reader's view of tree and records the observations.
func (s *Store) view(ctx context.Context, tree map[string][]byte) (View, []string) {
	b, bad := ParseBoard(tree)
	now := s.Clock.Now()
	seen := s.Seen.Load(ctx, s.Ref).Observe(b, now)
	if err := s.Seen.Save(ctx, s.Ref, seen); err != nil {
		s.tracef("signals: %v", err)
	}
	return View{Board: b, Seen: seen, Now: now, Lease: s.Lease}, bad
}

// Read fetches the signals ref (when online) and returns this reader's view.
// Offline it falls back to the last fetched copy: reads never fail closed,
// a stale view only costs a lost race later (§7.3 step 1).
func (s *Store) Read(ctx context.Context) (View, error) {
	var tree map[string][]byte
	var err error
	if s.Autopush {
		_, tree, err = s.fetchState(ctx)
	}
	if !s.Autopush || errors.Is(err, ErrOffline) {
		_, tree, err = s.readTracking(ctx)
	}
	if err != nil {
		return View{}, err
	}
	v, _ := s.view(ctx, tree)
	return v, nil
}

// Local returns the view of the last fetched copy without any network.
func (s *Store) Local(ctx context.Context) (View, error) {
	_, tree, err := s.readTracking(ctx)
	if err != nil {
		return View{}, err
	}
	v, _ := s.view(ctx, tree)
	return v, nil
}

// retry backoff: base * 2^attempt, capped, times a jitter factor in
// [0.5, 1.5) so racing agents spread out instead of colliding again.
const (
	backoffBase = 40 * time.Millisecond
	backoffCap  = 800 * time.Millisecond
)

func (s *Store) backoff(ctx context.Context, attempt int) error {
	d := backoffBase << min(attempt, 5)
	if d > backoffCap {
		d = backoffCap
	}
	if s.Jitter != nil {
		d = d/2 + time.Duration(s.Jitter.Int63n(int64(d)))
	}
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Write runs decide against a fresh view and pushes the outcome; on a lost
// lease (another writer was first) it fetches again and re-runs decide —
// never merging — up to Lease.PushRetries times (F7 contract, steps 1–4).
//
// Errors from decide are returned unchanged. Offline, autopush off or
// retries used up return an *OfflineError (fixed rule 11).
func (s *Store) Write(ctx context.Context, decide Decide) (Result, error) {
	if !s.Autopush {
		return Result{}, &OfflineError{Reason: "autopush is off (live.autopush / LORE_LIVE_AUTOPUSH=0)"}
	}
	retries := s.Lease.PushRetries
	if retries <= 0 {
		retries = 1
	}
	var last string
	for attempt := 1; ; attempt++ {
		res, again, err := s.attempt(ctx, decide)
		res.Attempts = attempt
		if err != nil || !again {
			return res, err
		}
		last = res.Old
		if attempt > retries {
			return res, &OfflineError{Reason: fmt.Sprintf("gave up after %d lost races on %s (last seen %s)", attempt, s.Ref, short(last))}
		}
		s.tracef("signals: lost race on %s (attempt %d), re-deciding", s.Ref, attempt)
		if err := s.backoff(ctx, attempt); err != nil {
			return res, err
		}
	}
}

func short(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	if oid == "" {
		return "none"
	}
	return oid
}

// attempt is one fetch → decide → commit → push round. again is true when a
// race was lost and the caller should retry.
func (s *Store) attempt(ctx context.Context, decide Decide) (Result, bool, error) {
	old, tree, err := s.fetchState(ctx)
	if err != nil {
		return Result{}, false, err
	}
	v, bad := s.view(ctx, tree)
	ch, err := decide(v)
	if err != nil {
		return Result{Old: old}, false, err
	}
	changes, err := s.changes(v, bad, ch)
	if err != nil {
		return Result{Old: old}, false, err
	}
	commit, err := s.Repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: old, History: gitref.HistoryReplace, Changes: changes,
		Message: CommitMessage, Signature: s.Signature(s.Clock.Now()),
	})
	if err != nil {
		return Result{Old: old}, false, fmt.Errorf("signals: build commit: %w", err)
	}
	if old == "" && !commit.Changed {
		// No ref and nothing to write: an empty parentless commit would be
		// a pointless push. A companion still needs a lease on "absent".
		commit.OID = ""
	}
	res := Result{Old: old, New: commit.OID}
	if !commit.Changed && len(ch.Companions) == 0 {
		return res, false, nil
	}
	sigUpdate := gitremote.RefUpdate{Src: commit.OID, Dst: s.Ref, Mode: gitremote.ModeLease, Expect: old}
	if commit.OID == "" {
		// Lease on absence: deleting a missing ref is how git expresses
		// "must still not exist" without writing anything.
		sigUpdate = gitremote.RefUpdate{}
	}
	updates := []gitremote.RefUpdate{}
	if sigUpdate.Dst != "" {
		updates = append(updates, sigUpdate)
	}
	var local, remote []Companion
	for _, c := range ch.Companions {
		if _, ok := c.(RemoteCompanion); ok {
			remote = append(remote, c)
		} else {
			local = append(local, c)
		}
	}
	for _, c := range remote {
		if _, err := c.Prepare(ctx); err != nil {
			return res, false, fmt.Errorf("signals: prepare companion: %w", err)
		}
	}
	for _, c := range local {
		u, err := c.Prepare(ctx)
		if err != nil {
			return res, false, fmt.Errorf("signals: prepare companion: %w", err)
		}
		updates = append(updates, u)
	}
	ch.Companions = append(local, remote...)
	if len(updates) == 0 {
		// Only other-remote companions and no ref to lease on: nothing to
		// fence with here, push them as they are.
		return s.finish(ctx, res, sigUpdate, ch)
	}
	atomic := len(updates) > 1 && !s.NoAtomic
	res.Atomic = atomic
	if atomic || len(updates) == 1 {
		again, err := s.push(ctx, updates, atomic)
		if errors.Is(err, gitremote.ErrAtomicUnsupported) {
			s.NoAtomic = true
			res.Atomic = false
			return s.ordered(ctx, res, sigUpdate, ch, updates)
		}
		if err != nil || again {
			return res, again, err
		}
	} else {
		return s.ordered(ctx, res, sigUpdate, ch, updates)
	}
	return s.finish(ctx, res, sigUpdate, ch)
}

// ordered is the fallback for hosts without atomic push (TEAM-036): the
// signals ref first (so a fenced-out holder still fails), then each
// companion. A companion failing after the signals write is ErrPartial.
func (s *Store) ordered(ctx context.Context, res Result, sigUpdate gitremote.RefUpdate, ch Change, updates []gitremote.RefUpdate) (Result, bool, error) {
	rest := updates
	if sigUpdate.Dst != "" {
		again, err := s.push(ctx, updates[:1], false)
		if err != nil || again {
			return res, again, err
		}
		rest = updates[1:]
	}
	for i, u := range rest {
		again, err := s.push(ctx, []gitremote.RefUpdate{u}, false)
		if err != nil || again {
			return res, false, fmt.Errorf("%w: companion %d: %v", ErrPartial, i+1, errors.Join(err, errAgain(again)))
		}
	}
	return s.finish(ctx, res, sigUpdate, ch)
}

func errAgain(again bool) error {
	if again {
		return errors.New("lost a race")
	}
	return nil
}

// finish records the pushed signals commit locally and finalizes companions.
func (s *Store) finish(ctx context.Context, res Result, sigUpdate gitremote.RefUpdate, ch Change) (Result, bool, error) {
	res.Pushed = true
	for i, c := range ch.Companions {
		if rc, ok := c.(RemoteCompanion); ok {
			if err := rc.PushSelf(ctx); err != nil {
				s.recordTracking(ctx, sigUpdate)
				return res, false, fmt.Errorf("%w: companion %d on its own remote: %v", ErrPartial, i+1, err)
			}
		}
	}
	s.recordTracking(ctx, sigUpdate)
	for _, c := range ch.Companions {
		if err := c.Finalize(ctx); err != nil {
			return res, false, fmt.Errorf("signals: finalize companion (the remote has the write): %w", err)
		}
	}
	return res, false, nil
}

// recordTracking moves the local tracking copy of the signals ref to the
// pushed commit.
func (s *Store) recordTracking(ctx context.Context, sigUpdate gitremote.RefUpdate) {
	if sigUpdate.Dst != "" && sigUpdate.Src != sigUpdate.Expect {
		track, err := s.tracking()
		if err == nil {
			if uerr := s.Repo.UpdateRef(ctx, track, sigUpdate.Src, sigUpdate.Expect); uerr != nil {
				s.tracef("signals: tracking ref not moved: %v", uerr)
			}
		}
	}
}

// push sends updates and classifies the outcome: again=true for a lost
// race (stale lease, non-fast-forward, or an atomic sibling failing for one
// of those), an error for anything a retry cannot fix.
func (s *Store) push(ctx context.Context, updates []gitremote.RefUpdate, atomic bool) (bool, error) {
	s.tracef("signals: push %d ref(s) atomic=%v", len(updates), atomic)
	res, err := s.Remote.Push(ctx, gitremote.PushRequest{Updates: updates, Atomic: atomic})
	switch {
	case errors.Is(err, gitremote.ErrAtomicUnsupported):
		return false, err
	case errors.Is(err, gitremote.ErrUnconfirmed):
		return false, &OfflineError{Reason: "remote unreachable", Err: err}
	case err != nil:
		return false, fmt.Errorf("signals: push: %w", err)
	}
	if res.OK() {
		return false, nil
	}
	again := false
	for _, r := range res.Refs {
		switch {
		case r.Status == gitremote.StatusOK, r.Status == gitremote.StatusUpToDate, r.Status == gitremote.StatusAtomicFailed:
		case r.Status == gitremote.StatusStale, r.Status == gitremote.StatusNonFastForward:
			again = true
		default:
			return false, fmt.Errorf("%w: %s %s: %s", ErrRejected, r.Dst, r.Status, r.Message)
		}
	}
	if !again {
		return false, fmt.Errorf("%w: atomic push failed without a reason", ErrRejected)
	}
	return true, nil
}

// changes turns a decision into tree changes: its puts and deletes, plus
// every expired or malformed file this reader sees (fixed rule 10).
func (s *Store) changes(v View, bad []string, ch Change) (gitref.Changes, error) {
	out := gitref.Changes{Put: map[string][]byte{}}
	for p, sig := range ch.Put {
		b, err := Encode(sig)
		if err != nil {
			return gitref.Changes{}, err
		}
		out.Put[p] = b
	}
	drop := map[string]bool{}
	for _, p := range ch.Delete {
		drop[p] = true
	}
	for _, p := range v.Expired() {
		drop[p] = true
	}
	for _, p := range bad {
		drop[p] = true
	}
	for p := range drop {
		if _, put := out.Put[p]; !put {
			out.Delete = append(out.Delete, p)
		}
	}
	return out, nil
}
