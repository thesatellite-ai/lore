package live

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/logic/clock"
)

// Ref is one live ref in one local repository (a code clone, or a bare
// store clone) plus its remote: the executor of F2 (write), F3 (fetch and
// integrate) and the push half of both.
//
// Invariants:
//   - nothing is ever force-written into the local ref: it only moves by
//     compare-and-swap to a commit that contains everything it held
//     (fast-forward, or a merge of its changes onto the remote tip);
//   - a remote ref that was deleted or rewritten without a higher epoch is
//     never merged blindly (ErrRemoteRewritten);
//   - every git call goes through Repo, so a tracing gitref.Git sees all.
type Ref struct {
	// Repo is the local repository.
	Repo *gitref.Repo
	// Name is the live ref (RefLive, or StoreRef(branch)).
	Name string
	// Remote is where the ref is shared; nil means "no remote" (solo,
	// cookbook 1): writes stay on the local ref and nothing is pushed.
	Remote gitremote.Remote
	// RemoteName names the remote for tracking refs ("origin").
	RemoteName string
	// StateDir is the per-repo machine state folder (.git/lore, or
	// <bare clone>/lore): fetch stamp, seen marker, pending list.
	StateDir string
	// Clock stamps commits and decides fetch ages.
	Clock clock.Clock
	// Signature is the author of live commits (the org identity); When is
	// filled from Clock.
	Signature gitref.Signature
	// Strategies give per-table field strategies for merges.
	Strategies StrategySource
	// IgnoreKeys are body keys a no-op check ignores (updated_at: a bump
	// that changes nothing else is not a write).
	IgnoreKeys []string
	// PushRetries bounds fetch-merge-retry rounds after a rejected push.
	PushRetries int
	// Backoff returns the pause before retry n (1-based); nil = no pause.
	Backoff func(n int) time.Duration
}

// ErrRemoteRewritten means the remote live ref was deleted or replaced by
// history that does not contain what this clone last saw, and its epoch is
// not higher (so it is not a compaction): §6.6, side effect 28. Writes stop
// until `lore live restore`.
var ErrRemoteRewritten = errors.New("live: the remote live ref was deleted or rewritten")

// ErrBelowFloor means this binary is older than the live ref's
// min_lore_version (E_OLD_LORE): it may read, never write.
var ErrBelowFloor = errors.New("live: this lore is older than the live ref's min_lore_version")

// Tracking is the local tracking ref of the live ref.
func (r *Ref) Tracking() string {
	t, ok := gitremote.TrackingRef(r.remoteName(), r.Name)
	if !ok {
		return ""
	}
	return t
}

func (r *Ref) remoteName() string {
	if r.RemoteName == "" {
		return defaultRemote
	}
	return r.RemoteName
}

// defaultRemote is used when RemoteName is empty.
const defaultRemote = "origin"

// state file names inside StateDir, per live ref (a bare store clone holds
// one live ref per store branch, so the ref name is part of the file name).
const (
	seenSuffix    = ".seen"
	pendingSuffix = ".pending.json"
	stateDirMode  = 0o755
	stateFileMode = 0o644
)

func (r *Ref) stateFile(suffix string) string {
	name := strings.ReplaceAll(strings.TrimPrefix(r.Name, "refs/"), "/", "-")
	return filepath.Join(r.StateDir, name+suffix)
}

// Snapshot is the state of the local and tracking refs at one moment.
type Snapshot struct {
	Local, Tracking string // "" = absent
	Seen            string // last remote commit integrated
}

// Snapshot reads both refs and the seen marker.
func (r *Ref) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	var err error
	if s.Local, _, err = r.Repo.ResolveRef(ctx, r.Name); err != nil {
		return s, err
	}
	if trk := r.Tracking(); trk != "" {
		if s.Tracking, _, err = r.Repo.ResolveRef(ctx, trk); err != nil {
			return s, err
		}
	}
	s.Seen = readTrim(r.stateFile(seenSuffix))
	return s, nil
}

// Ahead reports whether the local ref holds commits the remote has not
// confirmed (the "queue" of F2).
func (s Snapshot) Ahead() bool { return s.Local != "" && s.Local != s.Tracking }

// ReadTree reads a commit's tree ("" = empty tree).
func (r *Ref) ReadTree(ctx context.Context, oid string) (Tree, error) {
	if oid == "" {
		return Tree{}, nil
	}
	m, err := r.Repo.ReadTree(ctx, oid, "")
	if err != nil {
		return nil, fmt.Errorf("live: read tree %s: %w", short(oid), err)
	}
	return Tree(m), nil
}

// LocalTree reads the local ref's tree (empty when the ref is absent).
func (r *Ref) LocalTree(ctx context.Context) (string, Tree, error) {
	oid, _, err := r.Repo.ResolveRef(ctx, r.Name)
	if err != nil {
		return "", nil, err
	}
	t, err := r.ReadTree(ctx, oid)
	return oid, t, err
}

// IntegrateResult says what Integrate did.
type IntegrateResult struct {
	Action IntegrateAction
	// Notes are the rows both sides changed (merge only).
	Notes []MergeNote
	// Backup is the local backup ref made before a rewritten remote or an
	// epoch re-apply.
	Backup string
}

// IntegrateAction names Integrate's outcome.
type IntegrateAction string

// Integrate outcomes.
const (
	IntegrateNothing     IntegrateAction = "nothing"      // equal, or local ahead
	IntegrateTookRemote  IntegrateAction = "took-remote"  // local was absent
	IntegrateFastForward IntegrateAction = "fast-forward" // local behind
	IntegrateMerged      IntegrateAction = "merged"       // both moved: F4 onto the remote tip
	IntegrateReapplied   IntegrateAction = "reapplied"    // remote compacted: F8 reader path
)

// Integrate brings the tracking ref into the local ref (F3 step 3).
func (r *Ref) Integrate(ctx context.Context) (IntegrateResult, error) {
	s, err := r.Snapshot(ctx)
	if err != nil {
		return IntegrateResult{}, err
	}
	switch {
	case s.Tracking == "":
		if s.Seen != "" && s.Local != "" {
			return r.rewritten(ctx, s)
		}
		return IntegrateResult{Action: IntegrateNothing}, nil
	case s.Local == "":
		if err = r.Repo.UpdateRef(ctx, r.Name, s.Tracking, ""); err != nil {
			return IntegrateResult{}, err
		}
		return IntegrateResult{Action: IntegrateTookRemote}, r.markSeen(s.Tracking)
	case s.Local == s.Tracking:
		return IntegrateResult{Action: IntegrateNothing}, r.markSeen(s.Tracking)
	}
	if s.Seen != "" && s.Seen != s.Tracking {
		var cont bool
		if cont, err = r.Repo.IsAncestor(ctx, s.Seen, s.Tracking); err != nil {
			return IntegrateResult{}, err
		}
		if !cont {
			return r.historyReplaced(ctx, s)
		}
	}
	localHasRemote, err := r.Repo.IsAncestor(ctx, s.Tracking, s.Local)
	if err != nil {
		return IntegrateResult{}, err
	}
	if localHasRemote {
		return IntegrateResult{Action: IntegrateNothing}, r.markSeen(s.Tracking)
	}
	remoteHasLocal, err := r.Repo.IsAncestor(ctx, s.Local, s.Tracking)
	if err != nil {
		return IntegrateResult{}, err
	}
	if remoteHasLocal {
		if err = r.Repo.UpdateRef(ctx, r.Name, s.Tracking, s.Local); err != nil {
			return IntegrateResult{}, err
		}
		return IntegrateResult{Action: IntegrateFastForward}, r.markSeen(s.Tracking)
	}
	base, _, err := r.Repo.MergeBase(ctx, s.Local, s.Tracking)
	if err != nil {
		return IntegrateResult{}, err
	}
	notes, err := r.mergeOnto(ctx, base, s.Local, s.Tracking)
	if err != nil {
		return IntegrateResult{}, err
	}
	return IntegrateResult{Action: IntegrateMerged, Notes: notes}, r.markSeen(s.Tracking)
}

// historyReplaced handles a remote whose history no longer contains what
// this clone last saw: a compaction (higher epoch) takes the F8 reader
// path; anything else is a rewrite and stops writes.
func (r *Ref) historyReplaced(ctx context.Context, s Snapshot) (IntegrateResult, error) {
	local, err := r.ReadTree(ctx, s.Local)
	if err != nil {
		return IntegrateResult{}, err
	}
	remote, err := r.ReadTree(ctx, s.Tracking)
	if err != nil {
		return IntegrateResult{}, err
	}
	if remote.Epoch() <= local.Epoch() {
		return r.rewritten(ctx, s)
	}
	backup, err := r.Backup(ctx, s.Local)
	if err != nil {
		return IntegrateResult{}, err
	}
	// F8 reader path: only this clone's own writes since it last saw the
	// remote (seen..local) are re-applied on top of the compacted commit.
	notes, err := r.mergeOnto(ctx, s.Seen, s.Local, s.Tracking)
	if err != nil {
		return IntegrateResult{}, err
	}
	return IntegrateResult{Action: IntegrateReapplied, Notes: notes, Backup: backup}, r.markSeen(s.Tracking)
}

// rewritten backs up the local ref and reports ErrRemoteRewritten.
func (r *Ref) rewritten(ctx context.Context, s Snapshot) (IntegrateResult, error) {
	backup, err := r.ensureBackupOf(ctx, s.Local)
	if err != nil {
		return IntegrateResult{}, err
	}
	return IntegrateResult{Backup: backup}, fmt.Errorf("%w (local copy kept as %s)", ErrRemoteRewritten, backup)
}

// mergeOnto three-way merges local onto the remote tip: base..local are
// this clone's changes, applied to tip. The result is ONE commit whose
// parent is tip (no merge commit: the remote's history stays linear and a
// race of two writers adds exactly two commits, E1), or tip itself when
// local adds nothing.
func (r *Ref) mergeOnto(ctx context.Context, base, local, tip string) ([]MergeNote, error) {
	bt, err := r.ReadTree(ctx, base)
	if err != nil {
		return nil, err
	}
	lt, err := r.ReadTree(ctx, local)
	if err != nil {
		return nil, err
	}
	tt, err := r.ReadTree(ctx, tip)
	if err != nil {
		return nil, err
	}
	merged, notes, err := MergeTrees(bt, lt, tt, r.Strategies)
	if err != nil {
		return nil, err
	}
	ch := diffTrees(tt, merged)
	target := tip
	if !ch.Empty() {
		c, err := r.Repo.MakeCommit(ctx, gitref.CommitRequest{
			Base: tip, Changes: ch, Message: commitMessage(ch), Signature: r.sig(),
		})
		if err != nil {
			return nil, err
		}
		target = c.OID
	}
	if err := r.Repo.UpdateRef(ctx, r.Name, target, local); err != nil {
		return nil, err
	}
	return notes, nil
}

// diffTrees is the change set turning from into to.
func diffTrees(from, to Tree) gitref.Changes {
	ch := gitref.Changes{Put: map[string][]byte{}}
	for p, b := range to {
		if cur, ok := from[p]; !ok || string(cur) != string(b) {
			ch.Put[p] = b
		}
	}
	for p := range from {
		if _, ok := to[p]; !ok {
			ch.Delete = append(ch.Delete, p)
		}
	}
	sort.Strings(ch.Delete)
	return ch
}

// commitMessage is the fixed message of a live commit (F2 step 4).
func commitMessage(ch gitref.Changes) string {
	folders := map[string]bool{}
	n := 0
	for _, p := range append(keysOf(ch.Put), ch.Delete...) {
		if f, _, ok := ParseRowPath(p); ok {
			folders[f] = true
			n++
		}
	}
	names := make([]string, 0, len(folders))
	for f := range folders {
		names = append(names, f)
	}
	sort.Strings(names)
	return fmt.Sprintf("lore: live %d %s (%s)", n, plural(n, "row", "rows"), strings.Join(names, ", "))
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Signed returns the commit signature stamped with the ref's clock, for
// callers that build their own commits (restore, the ride-along).
func (r *Ref) Signed() gitref.Signature { return r.sig() }

func (r *Ref) sig() gitref.Signature {
	s := r.Signature
	s.When = r.now()
	return s
}

func (r *Ref) now() time.Time {
	if r.Clock == nil {
		return time.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// backupStamp names a backup ref; seconds plus a counter keep two backups
// in one second apart.
const backupStamp = "20060102T150405"

// BackupsKept is how many local backups of the live ref survive (§1).
const BackupsKept = 3

// Backup keeps oid as refs/lore-backup/live/<stamp> (local only) and prunes
// all but the newest BackupsKept.
func (r *Ref) Backup(ctx context.Context, oid string) (string, error) {
	if oid == "" {
		return "", nil
	}
	stamp := r.now().Format(backupStamp)
	name := r.backupPrefix() + stamp
	for i := 1; ; i++ {
		_, exists, err := r.Repo.ResolveRef(ctx, name)
		if err != nil {
			return "", err
		}
		if !exists {
			break
		}
		name = fmt.Sprintf("%s%s-%d", r.backupPrefix(), stamp, i)
	}
	if err := r.Repo.UpdateRef(ctx, name, oid, ""); err != nil {
		return "", err
	}
	return name, r.pruneBackups(ctx)
}

// ensureBackupOf returns an existing backup holding oid, or makes one, so a
// command repeated while the remote stays rewritten does not churn backups.
func (r *Ref) ensureBackupOf(ctx context.Context, oid string) (string, error) {
	refs, err := r.Repo.ListRefs(ctx, r.backupPrefix())
	if err != nil {
		return "", err
	}
	for name, v := range refs {
		if v == oid {
			return name, nil
		}
	}
	return r.Backup(ctx, oid)
}

func (r *Ref) backupPrefix() string {
	if r.Name == RefLive {
		return RefBackupPrefix
	}
	return RefBackupPrefix + strings.TrimPrefix(r.Name, RefLive+"/") + "/"
}

// Backups lists backup refs, newest first.
func (r *Ref) Backups(ctx context.Context) ([]string, map[string]string, error) {
	refs, err := r.Repo.ListRefs(ctx, r.backupPrefix())
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		if !strings.Contains(strings.TrimPrefix(n, r.backupPrefix()), "/") {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, refs, nil
}

func (r *Ref) pruneBackups(ctx context.Context) error {
	names, refs, err := r.Backups(ctx)
	if err != nil {
		return err
	}
	for i, n := range names {
		if i < BackupsKept {
			continue
		}
		if err := r.Repo.DeleteRef(ctx, n, refs[n]); err != nil && !errors.Is(err, gitref.ErrRefMoved) {
			return err
		}
	}
	return nil
}

func (r *Ref) markSeen(oid string) error {
	return writeState(r.stateFile(seenSuffix), []byte(oid+"\n"))
}

// ClearSeen forgets the last integrated remote commit (after `lore live
// restore` re-created the remote ref from a backup).
func (r *Ref) ClearSeen() error {
	err := os.Remove(r.stateFile(seenSuffix))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("live: clear seen: %w", err)
	}
	return nil
}

func writeState(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), stateDirMode); err != nil {
		return fmt.Errorf("live: state dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, stateFileMode); err != nil {
		return fmt.Errorf("live: write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("live: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// readTrim reads a small state file under the repo's .git/lore/ (paths
// built by stateFile, never user input).
func readTrim(path string) string {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// shortOIDLen is how much of a commit id messages show.
const shortOIDLen = 7

func short(oid string) string {
	if len(oid) > shortOIDLen {
		return oid[:shortOIDLen]
	}
	return oid
}
