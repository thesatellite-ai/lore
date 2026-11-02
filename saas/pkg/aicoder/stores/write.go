package stores

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
)

// DefaultPushRetries is how many times a write re-reads the store and
// re-applies itself after a concurrent writer won the push race
// (lease.push_retries' default, §1).
const DefaultPushRetries = 5

// ErrNoBranch means the store branch exists neither locally nor on the
// store. Only `lore store init <url> --branch <b>` creates one (F12; a typo
// must never create a new lore silently, coordinator answer Q8).
var ErrNoBranch = errors.New("stores: store branch does not exist")

// Snapshot is a read-only view of one store commit, handed to a write's
// Apply so it decides on what is really there.
type Snapshot struct {
	repo *gitref.Repo
	// Commit is the commit id ("" for a branch with no commit yet).
	Commit string
}

// ReadFile returns one file's content; ok is false when absent.
func (s Snapshot) ReadFile(ctx context.Context, path string) ([]byte, bool, error) {
	if s.Commit == "" {
		return nil, false, nil
	}
	return s.repo.ReadFile(ctx, s.Commit, path)
}

// ReadTree returns every file under prefix ("" = all), root-relative.
func (s Snapshot) ReadTree(ctx context.Context, prefix string) (map[string][]byte, error) {
	if s.Commit == "" {
		return map[string][]byte{}, nil
	}
	return s.repo.ReadTree(ctx, s.Commit, prefix)
}

// WriteRequest is one write to a store branch.
type WriteRequest struct {
	// Branch is the store branch (no "/", fixed rule 3).
	Branch string
	// Apply decides the changes against the current state. It is called
	// again after every lost push race, on the new state: re-read,
	// re-decide, never blindly re-send.
	Apply func(ctx context.Context, base Snapshot) (gitref.Changes, error)
	// Message is the commit message: fixed text with counts and ids only
	// (§6.5 commit metadata).
	Message string
	// Signature is the org identity (§6.5: never a personal git email).
	Signature gitref.Signature
	// Retries bounds lost push races (0 = DefaultPushRetries).
	Retries int
	// NoPush commits locally only (autopush off, LORE_LIVE_AUTOPUSH=0).
	NoPush bool
	// Create allows a branch that exists nowhere: the write becomes its
	// first, parentless commit (C2). Only `lore store init` sets it.
	Create bool
}

// WriteResult reports a store write.
type WriteResult struct {
	// Commit is the new local branch tip ("" when nothing changed).
	Commit string
	// Changed is false when Apply's changes left the tree as it was.
	Changed bool
	// Pushed is true when the store confirmed the branch.
	Pushed bool
	// Offline is true when the push could not reach the store: the commit
	// waits in the clone and the next command pushes it (PushPending).
	Offline bool
	// Attempts counts push attempts.
	Attempts int
}

// Write commits req's changes on the clone's local store branch and pushes
// it (F11 step 5, §3.2 "committed and pushed at once"). Under the clone's
// lock: fetch, reconcile local work with the store's, apply, commit, push;
// a lost race repeats from the fetch. Offline, the commit stays local and
// Result.Offline is set — the write is not lost and not reported as pushed.
func (s *Store) Write(ctx context.Context, req WriteRequest) (WriteResult, error) {
	unlock, err := s.Lock()
	if err != nil {
		return WriteResult{}, err
	}
	defer unlock()
	retries := req.Retries
	if retries <= 0 {
		retries = DefaultPushRetries
	}
	var res WriteResult
	for attempt := 0; attempt <= retries; attempt++ {
		fr, err := s.Fetch(ctx, attempt > 0)
		if err != nil {
			return res, err
		}
		base, err := s.reconcile(ctx, req.Branch, req.Signature)
		if errors.Is(err, ErrNoBranch) && req.Create {
			base, err = "", nil
		}
		if err != nil {
			return res, err
		}
		changes, err := req.Apply(ctx, Snapshot{repo: s.Repo, Commit: base})
		if err != nil {
			return res, err
		}
		c, err := s.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: base, Changes: changes, Message: req.Message, Signature: req.Signature})
		if err != nil {
			return res, fmt.Errorf("stores: commit on %s: %w", req.Branch, err)
		}
		if c.Changed {
			if err := s.moveLocal(ctx, req.Branch, c.OID); err != nil {
				return res, err
			}
			res.Changed, res.Commit = true, c.OID
		}
		if req.NoPush || fr.Offline {
			res.Offline = fr.Offline
			return res, nil
		}
		pushed, offline, err := s.pushBranch(ctx, req.Branch)
		res.Attempts++
		if err != nil {
			return res, err
		}
		if offline {
			res.Offline = true
			return res, nil
		}
		if pushed {
			res.Pushed = true
			return res, nil
		}
		// Lost the race: drop nothing, re-read the store, apply again.
		// The local commit is reconciled (replayed) on the next round.
	}
	return res, fmt.Errorf("stores: %s %s: still rejected after %d attempts (another writer keeps winning)", s.Norm, req.Branch, retries+1)
}

// moveLocal points the local branch at oid (CAS against its current value).
func (s *Store) moveLocal(ctx context.Context, branch, oid string) error {
	cur, ok, err := s.Repo.ResolveRef(ctx, LocalBranch(branch))
	if err != nil {
		return err
	}
	old := ""
	if ok {
		old = cur
	}
	if err := s.Repo.UpdateRef(ctx, LocalBranch(branch), oid, old); err != nil {
		return fmt.Errorf("stores: move %s: %w", LocalBranch(branch), err)
	}
	return nil
}

// pushBranch pushes the local branch as a fast-forward. pushed=false with
// no error and offline=false means a lost race (non-fast-forward).
func (s *Store) pushBranch(ctx context.Context, branch string) (pushed, offline bool, err error) {
	local, ok, err := s.Repo.ResolveRef(ctx, LocalBranch(branch))
	if err != nil || !ok {
		return false, false, err
	}
	res, err := s.Remote.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: local, Dst: LocalBranch(branch), Mode: gitremote.ModeFastForward}}})
	if err != nil {
		var te *gitremote.TransportError
		if errors.As(err, &te) {
			return false, true, nil
		}
		return false, false, fmt.Errorf("stores: push %s: %w", branch, err)
	}
	rr, _ := res.Ref(LocalBranch(branch))
	switch {
	case rr.Status.Applied():
		// Record what the store now holds, so Tip and Unpushed agree
		// without another fetch.
		cur, tok, err := s.Repo.ResolveRef(ctx, TrackingBranch(branch))
		if err != nil {
			return false, false, err
		}
		old := ""
		if tok {
			old = cur
		}
		if cur != local {
			if err := s.Repo.UpdateRef(ctx, TrackingBranch(branch), local, old); err != nil && !errors.Is(err, gitref.ErrRefMoved) {
				return false, false, err
			}
		}
		return true, false, nil
	case rr.Status == gitremote.StatusNonFastForward || rr.Status == gitremote.StatusStale:
		return false, false, nil
	case rr.Status == gitremote.StatusProtected:
		return false, false, fmt.Errorf("stores: %s refuses direct pushes to %s (a store branch must accept them; `lore store doctor`): %s", s.Norm, branch, rr.Message)
	default:
		return false, false, fmt.Errorf("stores: push %s to %s: %s %s", branch, s.Norm, rr.Status, rr.Message)
	}
}

// reconcile returns the commit a new write builds on, after bringing the
// local branch and the store's branch together:
//
//	local missing            → the store's tip
//	local behind or equal    → the store's tip (local is moved up to it)
//	local ahead              → local (unpushed writes are kept)
//	diverged                 → a merge commit: the store's tip plus local's
//	                           own changes replayed file by file, with both
//	                           as parents so the next push fast-forwards
//
// Neither existing → ErrNoBranch.
func (s *Store) reconcile(ctx context.Context, branch string, sig gitref.Signature) (string, error) {
	local, lok, err := s.Repo.ResolveRef(ctx, LocalBranch(branch))
	if err != nil {
		return "", err
	}
	track, tok, err := s.Repo.ResolveRef(ctx, TrackingBranch(branch))
	if err != nil {
		return "", err
	}
	switch {
	case !lok && !tok:
		return "", fmt.Errorf("%w: %s has no branch %s; create it with `lore store init %s --branch %s`", ErrNoBranch, s.Norm, branch, s.URL, branch)
	case !tok:
		return local, nil
	case !lok:
		if err := s.Repo.UpdateRef(ctx, LocalBranch(branch), track, ""); err != nil {
			return "", err
		}
		return track, nil
	case local == track:
		return local, nil
	}
	behind, err := s.Repo.IsAncestor(ctx, local, track)
	if err != nil {
		return "", err
	}
	if behind {
		if err := s.Repo.UpdateRef(ctx, LocalBranch(branch), track, local); err != nil {
			return "", err
		}
		return track, nil
	}
	ahead, err := s.Repo.IsAncestor(ctx, track, local)
	if err != nil {
		return "", err
	}
	if ahead {
		return local, nil
	}
	merged, err := s.replay(ctx, local, track, sig)
	if err != nil {
		return "", err
	}
	if err := s.Repo.UpdateRef(ctx, LocalBranch(branch), merged, local); err != nil {
		return "", err
	}
	return merged, nil
}

// replayMessage is the fixed message of a reconcile merge (§6.5).
const replayMessage = "lore: merge store branch"

// replay builds the merge of local's own changes (since the merge base)
// onto track. A file only local changed takes local's version; a file both
// changed is merged by MergeStoreFile (links union for _meta.json, field
// merge for rows).
func (s *Store) replay(ctx context.Context, local, track string, sig gitref.Signature) (string, error) {
	mb, ok, err := s.Repo.MergeBase(ctx, local, track)
	if err != nil {
		return "", err
	}
	var baseT map[string][]byte
	if ok {
		if baseT, err = s.Repo.ReadTree(ctx, mb, ""); err != nil {
			return "", err
		}
	} else {
		baseT = map[string][]byte{}
	}
	ours, err := s.Repo.ReadTree(ctx, local, "")
	if err != nil {
		return "", err
	}
	theirs, err := s.Repo.ReadTree(ctx, track, "")
	if err != nil {
		return "", err
	}
	ch := gitref.Changes{Put: map[string][]byte{}}
	paths := map[string]bool{}
	for p := range ours {
		paths[p] = true
	}
	for p := range baseT {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		b, inBase := baseT[p]
		o, inOurs := ours[p]
		t, inTheirs := theirs[p]
		if inBase == inOurs && bytes.Equal(b, o) {
			continue // local did not change it: the store's version stands
		}
		switch {
		case !inOurs: // local deleted it
			if inTheirs && inBase && !bytes.Equal(b, t) {
				continue // the store edited it meanwhile: the edit wins over a delete
			}
			ch.Delete = append(ch.Delete, p)
		case !inTheirs || (inBase && bytes.Equal(b, t)) || bytes.Equal(o, t):
			ch.Put[p] = o
		default:
			m, err := MergeStoreFile(p, optional(b, inBase), o, t)
			if err != nil {
				return "", err
			}
			ch.Put[p] = m
		}
	}
	c, err := s.Repo.MakeCommit(ctx, gitref.CommitRequest{Base: track, Changes: ch, MergeParents: []string{local}, Message: replayMessage, Signature: sig})
	if err != nil {
		return "", fmt.Errorf("stores: merge local store work: %w", err)
	}
	return c.OID, nil
}

func optional(b []byte, ok bool) []byte {
	if !ok {
		return nil
	}
	return b
}

// Unpushed lists the clone's local refs holding commits the store has not
// confirmed: store branches ahead of their tracking ref, and lore refs
// (refs/lore/*, e.g. a store live ref) ahead of refs/lore-remote/origin/*.
// `lore store forget` refuses while any exist (§5.3, side effect 20).
func (s *Store) Unpushed(ctx context.Context) ([]string, error) {
	var out []string
	for _, pair := range [][2]string{{"refs/heads/", "refs/remotes/" + RemoteName + "/"}, {"refs/lore/", "refs/lore-remote/" + RemoteName + "/"}} {
		locals, err := s.Repo.ListRefs(ctx, pair[0])
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(locals))
		for n := range locals {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			oid := locals[name]
			track := pair[1] + strings.TrimPrefix(name, pair[0])
			tid, ok, err := s.Repo.ResolveRef(ctx, track)
			if err != nil {
				return nil, err
			}
			if !ok {
				out = append(out, name)
				continue
			}
			if tid == oid {
				continue
			}
			in, err := s.Repo.IsAncestor(ctx, oid, tid)
			if err != nil {
				return nil, err
			}
			if !in {
				out = append(out, name)
			}
		}
	}
	return out, nil
}

// PushPending pushes every store branch with unpushed commits (after an
// offline write). It reconciles each with the store first. Returns the
// branches pushed; offline is set when the store is still unreachable.
func (s *Store) PushPending(ctx context.Context, sig gitref.Signature) (pushed []string, offline bool, err error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	pending, err := s.Unpushed(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, ref := range pending {
		branch, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok {
			continue // lore refs are pushed by their owner (live)
		}
		for attempt := 0; attempt <= DefaultPushRetries; attempt++ {
			fr, err := s.Fetch(ctx, true)
			if err != nil {
				return pushed, false, err
			}
			if fr.Offline {
				return pushed, true, nil
			}
			if _, err := s.reconcile(ctx, branch, sig); err != nil {
				return pushed, false, err
			}
			ok, off, err := s.pushBranch(ctx, branch)
			if err != nil {
				return pushed, false, err
			}
			if off {
				return pushed, true, nil
			}
			if ok {
				pushed = append(pushed, branch)
				break
			}
		}
	}
	return pushed, false, nil
}
