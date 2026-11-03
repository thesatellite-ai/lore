package stores

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/processlock"
)

// RemoteName is the remote every bare clone uses for its store.
const RemoteName = "origin"

// Refspecs a bare store clone fetches (§5.3): branches into
// refs/remotes/origin/*, lore's hidden refs into refs/lore-remote/origin/*
// (F3). Local refs/heads/* and refs/lore/* are lore's own working state and
// are never written by a fetch.
var fetchRefspecs = []string{
	"+refs/heads/*:refs/remotes/" + RemoteName + "/*",
	"+refs/lore/*:refs/lore-remote/" + RemoteName + "/*",
}

// File names inside a clone's state folder (<clone>/lore/).
const (
	lockFile          = "lock"
	fetchStampFile    = "fetch-stamp"
	defaultBranchFile = "default-branch"
	checkedFile       = "checked"
)

// Lock timing: processlock is non-blocking, and several lore processes (two
// terminals, a hook, an agent) share one clone (side effect 32), so a busy
// clone is waited for rather than refused.
const (
	lockPoll = 25 * time.Millisecond
	// DefaultLockWait bounds the wait; past it the command fails clearly
	// instead of hanging behind a stuck process.
	DefaultLockWait = 60 * time.Second
)

// stateDirMode / stateFileMode: machine state readable only by the user —
// a clone of an internal store is internal.
const (
	stateDirMode  = 0o700
	stateFileMode = 0o600
)

// Manager opens bare store clones under one lore home. Every field is a
// seam; the zero values of Git, Clock and RemoteFor are the real ones.
type Manager struct {
	// Layout says where clones live.
	Layout Layout
	// Git runs git (nil = gitref.Exec{}).
	Git gitref.Git
	// Clock stamps fetches and commits (nil = clock.Real{}).
	Clock clock.Clock
	// FetchTTL: a store fetched less than this long ago is not fetched
	// again by Fetch(ctx, false) (live.fetch_ttl, TEAM-025). 0 = always.
	FetchTTL time.Duration
	// RemoteFor builds the Remote for a clone; nil = the real git remote.
	// Tests wrap it in gitremote.FaultRemote.
	RemoteFor func(r *gitref.Repo) gitremote.Remote
	// LockWait bounds the wait for a busy clone (0 = DefaultLockWait).
	LockWait time.Duration
}

func (m *Manager) git() gitref.Git {
	if m.Git == nil {
		return gitref.Exec{}
	}
	return m.Git
}

func (m *Manager) now() time.Time {
	if m.Clock == nil {
		return time.Now()
	}
	return m.Clock.Now()
}

// Store is one opened bare clone.
type Store struct {
	// URL is the store URL as configured (what git is told to fetch).
	URL string
	// Norm is the normalised host/owner/repo.
	Norm string
	// Dir is the bare clone folder.
	Dir string
	// Repo is the clone for gitref reads and writes.
	Repo *gitref.Repo
	// Remote is the store's remote.
	Remote gitremote.Remote
	m      *Manager
}

// ErrNoClone is returned by Lookup when a store has never been cloned.
var ErrNoClone = errors.New("stores: store is not cloned yet")

// Lookup opens an existing clone without creating one (doctor, forget,
// the commit hook's cache lookup).
func (m *Manager) Lookup(ctx context.Context, url string) (*Store, error) {
	dir, err := m.Layout.CloneDir(url)
	if err != nil {
		return nil, err
	}
	if !isBareRepo(dir) {
		return nil, fmt.Errorf("%w: %s", ErrNoClone, dir)
	}
	return m.open(ctx, url, dir)
}

// Open returns the bare clone for url, creating it (empty, no fetch) when
// it does not exist yet. A missing clone is simply made again (§5.3:
// deleting a clone is safe). Creating is serialised with the clone's lock,
// so two processes opening one new store make one clone.
func (m *Manager) Open(ctx context.Context, url string) (*Store, error) {
	dir, err := m.Layout.CloneDir(url)
	if err != nil {
		return nil, err
	}
	if !isBareRepo(dir) {
		if err := os.MkdirAll(filepath.Join(dir, stateDir), stateDirMode); err != nil {
			return nil, fmt.Errorf("stores: create clone dir: %w", err)
		}
		unlock, err := m.lock(dir)
		if err != nil {
			return nil, err
		}
		err = m.initBare(ctx, url, dir)
		unlock()
		if err != nil {
			return nil, err
		}
	}
	return m.open(ctx, url, dir)
}

func (m *Manager) open(ctx context.Context, url, dir string) (*Store, error) {
	norm, err := normalizeStoreURL(url)
	if err != nil {
		return nil, err
	}
	r, err := gitref.Open(ctx, dir, m.git())
	if err != nil {
		return nil, fmt.Errorf("stores: open clone %s: %w", dir, err)
	}
	if !r.Bare() {
		return nil, fmt.Errorf("stores: %s is not a bare repository; remove it and lore clones it again", dir)
	}
	var rem gitremote.Remote = &gitremote.Git{Repo: r, Name: RemoteName}
	if m.RemoteFor != nil {
		rem = m.RemoteFor(r)
	}
	return &Store{URL: url, Norm: norm, Dir: dir, Repo: r, Remote: rem, m: m}, nil
}

// initBare makes an empty bare repo with origin and lore's fetch refspecs.
// Idempotent under the lock: a repo that appeared meanwhile is kept.
func (m *Manager) initBare(ctx context.Context, url, dir string) error {
	if isBareRepo(dir) {
		return nil
	}
	g := m.git()
	run := func(args ...string) error {
		if _, err := g.Run(ctx, gitref.Invocation{Dir: dir, Args: args}); err != nil {
			return fmt.Errorf("stores: git %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	if err := run("init", "--quiet", "--bare", "."); err != nil {
		return err
	}
	if err := run("remote", "add", RemoteName, url); err != nil {
		return err
	}
	// Replace git's default fetch refspec with lore's two, so a person
	// running `git fetch` inside the clone gets the same layout.
	if err := run("config", "--unset-all", "remote."+RemoteName+".fetch"); err != nil && !isConfigUnsetMiss(err) {
		return err
	}
	for _, rs := range fetchRefspecs {
		if err := run("config", "--add", "remote."+RemoteName+".fetch", rs); err != nil {
			return err
		}
	}
	return nil
}

// isConfigUnsetMiss: `git config --unset-all` exits 5 when the key is absent.
func isConfigUnsetMiss(err error) bool {
	code, ok := gitref.ExitCode(err)
	return ok && code == gitConfigUnsetMissing
}

// gitConfigUnsetMissing is git config's exit status for "nothing to unset".
const gitConfigUnsetMissing = 5

// isBareRepo reports whether dir looks like an initialised bare repo.
func isBareRepo(dir string) bool {
	for _, f := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	return true
}

// lock takes the clone's lock, waiting while another lore process holds it.
func (m *Manager) lock(dir string) (func(), error) {
	path := filepath.Join(dir, stateDir, lockFile)
	wait := m.LockWait
	if wait <= 0 {
		wait = DefaultLockWait
	}
	deadline := time.Now().Add(wait)
	for {
		l, err := processlock.Acquire(path)
		switch {
		case err == nil:
			return func() { _ = l.Release() }, nil // release errors are not actionable; the OS drops the flock at exit
		case errors.Is(err, processlock.ErrReadOnly):
			return func() {}, nil
		case !errors.Is(err, processlock.ErrLockHeld):
			return nil, fmt.Errorf("stores: lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("stores: another lore process has held %s for over %s", path, wait)
		}
		time.Sleep(lockPoll)
	}
}

// Lock takes the clone's lock for a multi-step operation (fetch, write,
// push). The returned func releases it.
func (s *Store) Lock() (func(), error) { return s.m.lock(s.Dir) }

// FetchResult reports one Fetch.
type FetchResult struct {
	// Fetched is true when the remote was contacted successfully.
	Fetched bool
	// Cached is true when the TTL skipped the fetch.
	Cached bool
	// Offline is true when the remote could not be reached; the clone's
	// last fetched state is used and must be marked as such.
	Offline bool
	// Err is the transport error behind Offline.
	Err error
	// At is when the clone was last fetched (zero = never).
	At time.Time
}

// Fetch updates the clone's tracking refs from the store, unless force is
// false and the last fetch is younger than FetchTTL. An unreachable store
// is not an error: it is reported as Offline so callers can fail closed
// where it matters (F10, F16) and carry on where it does not (reads).
// The caller holds the lock (Lock) when it also writes.
func (s *Store) Fetch(ctx context.Context, force bool) (FetchResult, error) {
	last, _ := s.lastFetch()
	if !force && s.m.FetchTTL > 0 && !last.IsZero() && s.m.now().Sub(last) < s.m.FetchTTL {
		return FetchResult{Cached: true, At: last}, nil
	}
	if err := s.Remote.Fetch(ctx, gitremote.FetchRequest{Refspecs: fetchRefspecs, Prune: true}); err != nil {
		var te *gitremote.TransportError
		if errors.As(err, &te) {
			return FetchResult{Offline: true, Err: err, At: last}, nil
		}
		return FetchResult{}, fmt.Errorf("stores: fetch %s: %w", s.Norm, err)
	}
	now := s.m.now()
	s.recordDefaultBranch(ctx)
	if err := s.writeState(fetchStampFile, []byte(now.UTC().Format(time.RFC3339Nano))); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Fetched: true, At: now}, nil
}

// lastFetch reads the fetch stamp (zero when never fetched).
func (s *Store) lastFetch() (time.Time, error) {
	b, err := os.ReadFile(s.statePath(fetchStampFile))
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
}

// LastFetch is when the clone last reached the store (zero = never).
func (s *Store) LastFetch() time.Time {
	t, _ := s.lastFetch()
	return t
}

// recordDefaultBranch asks the store which branch HEAD names and keeps it
// for DefaultBranch (F10 staging rule). Best effort: an empty store has no
// HEAD, and then the default stays DefaultBranchFallback.
func (s *Store) recordDefaultBranch(ctx context.Context) {
	out, err := s.Repo.Run(ctx, gitref.Invocation{Args: []string{"ls-remote", "--symref", "--", RemoteName, "HEAD"}})
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(line, "ref: refs/heads/")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, "\t")
		if name != "" {
			_ = s.writeState(defaultBranchFile, []byte(name)) // best effort, see above
		}
		return
	}
}

// DefaultBranchFallback is the store default branch when the store has not
// told us one (lorecfg.DefaultStoreBranch).
const DefaultBranchFallback = "main"

// DefaultBranch is the store's default branch as last fetched (its remote
// HEAD), DefaultBranchFallback when unknown. An org entry on any other
// branch is a staging entry (TEAM-022).
func (s *Store) DefaultBranch() string {
	b, err := os.ReadFile(s.statePath(defaultBranchFile))
	if err != nil {
		return DefaultBranchFallback
	}
	if n := strings.TrimSpace(string(b)); n != "" {
		return n
	}
	return DefaultBranchFallback
}

func (s *Store) statePath(name string) string { return filepath.Join(s.Dir, stateDir, name) }

func (s *Store) writeState(name string, b []byte) error {
	dir := filepath.Join(s.Dir, stateDir)
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("stores: state dir: %w", err)
	}
	// A unique temp file per writer: several lore processes share a clone
	// (side effect 32) and may stamp it at the same moment.
	f, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("stores: write %s: %w", name, err)
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp) // best effort: the write already failed
		return fmt.Errorf("stores: write %s: %w", name, errors.Join(werr, cerr))
	}
	if err := os.Chmod(tmp, stateFileMode); err != nil {
		_ = os.Remove(tmp) // best effort: the chmod already failed
		return fmt.Errorf("stores: write %s: %w", name, err)
	}
	if err := os.Rename(tmp, s.statePath(name)); err != nil {
		_ = os.Remove(tmp) // best effort: the rename already failed
		return fmt.Errorf("stores: write %s: %w", name, err)
	}
	return nil
}

// LocalBranch is the clone's own ref for a store branch: lore commits here
// first, then pushes it (so an offline write is never lost).
func LocalBranch(branch string) string { return "refs/heads/" + branch }

// TrackingBranch is the fetched copy of a store branch.
func TrackingBranch(branch string) string { return "refs/remotes/" + RemoteName + "/" + branch }

// Tip returns the commit lore should read a store branch at: the local
// branch when it is ahead of (or equal to) the tracking ref — it holds
// writes not pushed yet — else the tracking ref. ok is false when the
// branch exists nowhere (store not initialised on that branch).
func (s *Store) Tip(ctx context.Context, branch string) (oid string, ok bool, err error) {
	local, lok, err := s.Repo.ResolveRef(ctx, LocalBranch(branch))
	if err != nil {
		return "", false, err
	}
	track, tok, err := s.Repo.ResolveRef(ctx, TrackingBranch(branch))
	if err != nil {
		return "", false, err
	}
	switch {
	case !lok && !tok:
		return "", false, nil
	case !tok:
		return local, true, nil
	case !lok:
		return track, true, nil
	case local == track:
		return local, true, nil
	}
	behind, err := s.Repo.IsAncestor(ctx, local, track)
	if err != nil {
		return "", false, err
	}
	if behind {
		return track, true, nil
	}
	return local, true, nil
}

// Paths lists every file path at rev (root-relative, slash-separated).
func (s *Store) Paths(ctx context.Context, rev string) ([]string, error) {
	out, err := s.Repo.Run(ctx, gitref.Invocation{Args: []string{"ls-tree", "-r", "-z", "--name-only", rev}})
	if err != nil {
		return nil, fmt.Errorf("stores: list %s: %w", rev, err)
	}
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
	}
	return paths, nil
}

// Check runs the F12 store check on a branch at its current tip, once per
// commit (the result is cached in the clone's state). A branch that does
// not exist is reported by the caller (ErrNoBranch), not here.
func (s *Store) Check(ctx context.Context, branch string) error {
	tip, ok, err := s.Tip(ctx, branch)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if s.checkedOK(tip) {
		return nil
	}
	paths, err := s.Paths(ctx, tip)
	if err != nil {
		return err
	}
	if err := CheckTree(s.Norm, branch, paths, true); err != nil {
		return err
	}
	if b, rerr := s.Repo.Run(ctx, gitref.Invocation{Args: []string{"cat-file", "blob", tip + ":" + MarkerFile}}); rerr == nil {
		if _, perr := ParseMarker(b); perr != nil {
			return &NotAStoreError{URL: s.Norm, Branch: branch, NoMarker: true}
		}
	}
	return s.writeState(checkedFile, []byte(tip))
}

// checkedOK reports whether tip already passed Check.
func (s *Store) checkedOK(tip string) bool {
	b, err := os.ReadFile(s.statePath(checkedFile))
	return err == nil && strings.TrimSpace(string(b)) == tip
}
