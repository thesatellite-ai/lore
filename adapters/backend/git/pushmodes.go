package git

// pushmodes.go — what a store clone needs for the store push modes and
// history compaction (owner decision 2026-11-09):
//
//	SyncWith{NoPush}   fetch and merge only; local commits stay the outbox
//	SyncWith{Squash}   before the push, the unpushed commits become ONE
//	                   batch commit on the remote tip (same tree, so the
//	                   worktree and index stay as they are)
//	adoptEpoch         the remote's lore-store.yaml carries a higher epoch
//	                   (its history was compacted): re-fetch, never merge;
//	                   local work not on the remote is replayed on top
//	Compact            history older than a cutoff becomes one snapshot
//	                   commit, the epoch goes up by one, force-pushed with
//	                   a lease; the old tip is kept in a local backup ref
//
// Every rewrite builds its commits with commit-tree off the branch and
// moves the branch only once the result is safe (pushed, or merged), so a
// crash at any step leaves the branch where it was: nothing is lost.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// SyncOptions shape one clone sync (SyncWith).
type SyncOptions struct {
	// NoPush fetches and merges only: local commits wait for a later push.
	NoPush bool
	// Squash folds the commits not on the remote into one batch commit
	// right before the push (batch and manual modes).
	Squash bool
}

// SyncWith is Sync with options. Repo mode has nothing to do.
func (b *Backend) SyncWith(ctx context.Context, o SyncOptions) (store.SyncReport, error) {
	if err := b.usable(ctx); err != nil {
		return store.SyncReport{}, err
	}
	if b.mode == modeRepo {
		return store.SyncReport{}, nil
	}
	return b.syncClone(ctx, o)
}

// Commit subjects of the rewrites.
const (
	batchSubject    = "lore: batch "
	snapshotSubject = "lore: snapshot of the store history before "
	compactSubject  = "lore: compact store history (epoch "
	replaySubject   = "lore: replay local work onto epoch "
)

// backupRefNS holds the local backup refs a rewrite keeps (never pushed:
// the push refspec names one branch).
const backupRefNS = "refs/lore/backup/"

// maxBatchListed caps the row lines in a batch commit's body.
const maxBatchListed = 100

// emptyTree is git's empty tree object (sha-1 repositories; the clone is
// made by lore with the default object format).
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Unpushed counts the local commits the remote tip does not hold (all of
// them when the remote branch is not known yet); 0 in repo mode.
func (b *Backend) Unpushed(ctx context.Context) (int, error) {
	if err := b.usable(ctx); err != nil {
		return 0, err
	}
	if b.mode == modeRepo {
		return 0, nil
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return 0, err
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return 0, err
	}
	return b.countCommits(ctx, remote, h)
}

// Unpulled counts the commits the clone's tracking ref holds that its
// branch does not: fetched but not merged yet (a plain `git fetch` in the
// clone, or a fetch that updates only the tracking refs). 0 when the
// remote branch is not known yet, and in repo mode.
func (b *Backend) Unpulled(ctx context.Context) (int, error) {
	if err := b.usable(ctx); err != nil {
		return 0, err
	}
	if b.mode == modeRepo {
		return 0, nil
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil || remote == "" {
		return 0, err
	}
	h, err := b.head(ctx)
	if err != nil {
		return 0, err
	}
	return b.countCommits(ctx, h, remote)
}

// OldestUnpushed is the commit date of the oldest local commit the remote
// tip does not hold (zero when nothing waits). Store commits are dated by
// the app clock, so this is when the oldest waiting write was made.
func (b *Backend) OldestUnpushed(ctx context.Context) (time.Time, error) {
	if err := b.usable(ctx); err != nil || b.mode == modeRepo {
		return time.Time{}, err
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return time.Time{}, err
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return time.Time{}, err
	}
	args := []string{"log", "--format=%ct", h}
	if remote != "" {
		args = append(args, "^"+remote)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return time.Time{}, errs.Wrap(errs.Internal, "git: read the waiting commits", err)
	}
	var oldest time.Time
	for line := range strings.SplitSeq(trimmed(out), "\n") {
		sec, perr := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if perr != nil {
			continue
		}
		if t := time.Unix(sec, 0); oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	return oldest, nil
}

// countCommits counts commits reachable from to and not from from ("" =
// every commit of to).
func (b *Backend) countCommits(ctx context.Context, from, to string) (int, error) {
	args := []string{"rev-list", "--count", to}
	if from != "" {
		args = append(args, "^"+from)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "git: count commits", err)
	}
	return strconv.Atoi(trimmed(out))
}

// Hand-edit commits in a batch (brief 2.1: committed as that person). The
// pick-up's commits carry handEditTrailer (clone.go commitBatch, from
// store.PersonAuthored); the batch squash keeps each as a commit of its
// own with its author, committer, dates and message, and squashes only
// lore's own commits between them:
//
//	remote ─ w1 ─ w2 ─ H ─ w3 ─ w4      (H: hand edit by the person)
//	remote ─ batch(w1,w2) ─ H' ─ batch(w3,w4)
//
// Each new commit takes its original's tree, so the result's tree is h's.
// A local history with a merge in it is pushed as it is when it holds a
// hand edit (a tree of a commit before the merge would undo the merge).

// handEditKey is the trailer key that marks a person's own change.
const handEditKey = "Lore-Hand-Edit"

// handEditTrailer is the trailer line of a hand-edit commit.
const handEditTrailer = handEditKey + ": true"

// localCommit is one commit of remote..h for the squash.
type localCommit struct {
	id   string
	hand bool // a hand-edit commit (handEditTrailer)
}

// squashOnto makes the commits of remote..h one commit on remote with h's
// tree and a batch message, and moves the branch there (compare-and-swap
// on h). One commit or none is left as it is. Hand-edit commits stay
// commits of their own (see above).
func (b *Backend) squashOnto(ctx context.Context, remote, h string) (string, error) {
	n, err := b.countCommits(ctx, remote, h)
	if err != nil || n <= 1 {
		return h, err
	}
	local, linear, err := b.localCommits(ctx, remote, h)
	if err != nil {
		return h, err
	}
	hands := 0
	for _, c := range local {
		if c.hand {
			hands++
		}
	}
	if hands > 0 && (!linear || hands == len(local)) {
		return h, nil // nothing lore may fold without losing a person's commit
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return h, err
	}
	next, from := remote, remote // from: the parent of the run of lore commits
	var run string               // the last lore commit of the current run
	flush := func() error {
		if run == "" {
			return nil
		}
		msg, err := b.batchMessage(ctx, from, run)
		if err != nil {
			return err
		}
		if next, err = b.commitTree(ctx, run+"^{tree}", []string{next}, b.cfg.withTrailers(msg), env); err != nil {
			return err
		}
		run = ""
		return nil
	}
	for _, c := range local {
		if !c.hand {
			if run == "" {
				from = c.id + "^"
			}
			run = c.id
			continue
		}
		if err := flush(); err != nil {
			return h, err
		}
		if next, err = b.recommit(ctx, chainCommit{id: c.id, tree: c.id + "^{tree}"}, next); err != nil {
			return h, err
		}
	}
	if err := flush(); err != nil {
		return h, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-m", "lore: batch", "HEAD", next, h}}); err != nil {
		return h, errs.Wrap(errs.Internal, "git: move the branch to the batch commit", err)
	}
	return next, nil
}

// localCommits lists remote..h oldest first, each marked when it is a
// hand-edit commit; linear is false when one of them is a merge or the
// first one does not sit on remote.
func (b *Backend) localCommits(ctx context.Context, remote, h string) ([]localCommit, bool, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--reverse", "--topo-order",
		"--format=%H%x00%P%x00%(trailers:key=" + handEditKey + ",valueonly,separator=%x2C)%x1e", h, "^" + remote}})
	if err != nil {
		return nil, false, errs.Wrap(errs.Internal, "git: list the commits of the batch", err)
	}
	base, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--verify", remote + "^{commit}"}})
	if err != nil {
		return nil, false, errs.Wrap(errs.Internal, "git: read the remote tip of the batch", err)
	}
	var list []localCommit
	linear, prev := true, trimmed(base)
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 3)
		if len(f) != 3 || f[0] == "" {
			continue
		}
		parents := strings.Fields(f[1])
		if len(parents) != 1 || parents[0] != prev {
			linear = false
		}
		prev = f[0]
		list = append(list, localCommit{id: f[0], hand: strings.TrimSpace(f[2]) != ""})
	}
	return list, linear, nil
}

// batchMessage names the rows a batch carries: the subject counts them
// per kind ("lore: batch 3 rows (memories 2, tasks 1)"), the body lists
// each one (up to maxBatchListed).
func (b *Backend) batchMessage(ctx context.Context, from, to string) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "-z", "--no-renames", from, to}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: list the batch rows", err)
	}
	counts := map[string]int{}
	var lines []string
	total := 0
	for _, p := range splitZ(out) {
		rel, ok := rowRel(p)
		if !ok {
			continue
		}
		t, id, ok := rowfs.ParseRowPath(rel)
		if !ok {
			continue
		}
		total++
		counts[string(t)]++
		if len(lines) < maxBatchListed {
			lines = append(lines, string(t)+" "+id.String())
		}
	}
	return BatchMessage(total, counts, lines), nil
}

// BatchMessage is the batch commit message for total rows with counts per
// kind and the listed row lines (exported for tests).
func BatchMessage(total int, counts map[string]int, lines []string) string {
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, k+" "+strconv.Itoa(counts[k]))
	}
	noun := " rows"
	if total == 1 {
		noun = " row"
	}
	subj := batchSubject + strconv.Itoa(total) + noun
	if len(parts) > 0 {
		subj += " (" + strings.Join(parts, ", ") + ")"
	}
	if len(lines) == 0 {
		return subj
	}
	body := strings.Join(lines, "\n")
	if more := total - len(lines); more > 0 {
		body += "\n(+" + strconv.Itoa(more) + " more)"
	}
	return subj + "\n\n" + body
}

// commitTree makes a commit of tree with parents and msg (read from stdin,
// so any length and character is safe) and returns its id.
func (b *Backend) commitTree(ctx context.Context, tree string, parents []string, msg string, env []string) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-")
	out, err := b.git.run(ctx, call{dir: b.work, args: args, env: env, stdin: []byte(msg)})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: make a commit", err)
	}
	return trimmed(out), nil
}

// epochAt is the epoch lore-store.yaml declares at commit (0 when the
// file is absent or unreadable: an unknown epoch never forces a refetch).
func (b *Backend) epochAt(ctx context.Context, commit string) int {
	name := commit + ":" + rowfs.MarkerFile
	blobs, err := b.readBlobs(ctx, []string{name})
	if err != nil {
		return 0
	}
	raw, ok := blobs[name]
	if !ok {
		return 0
	}
	m, err := rowfs.ParseMarker(raw)
	if err != nil {
		return 0
	}
	return m.Epoch
}

// Epoch is the epoch of the clone's HEAD (0 in repo mode or when unborn).
func (b *Backend) Epoch(ctx context.Context) int {
	if b.mode == modeRepo {
		return 0
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return 0
	}
	return b.epochAt(ctx, h)
}

// treeOf is the tree id of a commit.
func (b *Backend) treeOf(ctx context.Context, commit string) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--verify", "--quiet", commit + "^{tree}"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: read a tree", err)
	}
	return trimmed(out), nil
}

// withFile is tree with path set to blob (blob "" leaves tree as it is),
// built in a private index file so the clone's own index is untouched.
func (b *Backend) withFile(ctx context.Context, tree, path, blob string) (string, error) {
	if blob == "" {
		return tree, nil
	}
	gitDir, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: locate the clone's git dir", err)
	}
	idx := filepath.Join(trimmed(gitDir), "lore-rewrite-index")
	defer func() { _ = os.Remove(idx) }() // a leftover is rebuilt by the next read-tree
	env := []string{"GIT_INDEX_FILE=" + idx}
	if tree == emptyTree {
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"read-tree", "--empty"}, env: env})
	} else {
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"read-tree", tree}, env: env})
	}
	if err == nil {
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"update-index", "--add", "--cacheinfo", "100644," + blob + "," + path}, env: env})
	}
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: build a tree: "+stderrOf(err), err)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"write-tree"}, env: env})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write a tree", err)
	}
	return trimmed(out), nil
}

// blobAt is the blob id of path at commit ("" when absent).
func (b *Backend) blobAt(ctx context.Context, commit, path string) string {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "-q", "--verify", commit + ":" + path}})
	if err != nil {
		return ""
	}
	return trimmed(out)
}

// recoverMerge aborts a merge a crashed run left in the worktree (lore
// owns the worktree, so a MERGE_HEAD can only be its own debris); the
// branch is then where it was before that merge.
func (b *Backend) recoverMerge(ctx context.Context) error {
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "-q", "--verify", "MERGE_HEAD"}}); err != nil {
		return nil // no merge in progress
	}
	return b.abortMerge(ctx)
}

// adoptEpoch handles a remote whose history was compacted since this
// clone last merged it (storepush.Refetch): the branch moves onto the
// remote tip, and local work the remote does not hold is replayed on top
// as one commit (rows both sides changed go through the Merger). The old
// tip is kept in a backup ref. adopted is false when there was nothing to
// adopt; refused rows leave the branch where it was.
func (b *Backend) adoptEpoch(ctx context.Context, remote string) (adopted bool, refused []store.Refusal, err error) {
	h, err := b.head(ctx)
	if err != nil || h == "" || remote == "" {
		return false, nil, err
	}
	if contained, aerr := b.isAncestor(ctx, remote, h); aerr != nil || contained {
		return false, nil, aerr
	}
	re := b.epochAt(ctx, remote)
	if !storepush.Refetch(b.epochAt(ctx, h), re) {
		return false, nil, nil
	}
	base, err := b.replayBase(ctx, remote, h)
	if err != nil {
		return false, nil, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", backupRefNS + "epoch-" + strconv.Itoa(re) + "-" + h[:12], h}}); err != nil {
		return false, nil, errs.Wrap(errs.Internal, "git: keep a backup of the local history", err)
	}
	if base == h {
		// Nothing local beyond what the remote holds: take the remote.
		return true, nil, b.resetTo(ctx, remote)
	}
	return b.replayOnto(ctx, remote, h, base)
}

// replayBase finds the newest local commit whose tree the remote's
// rewritten history holds (a compaction keeps every later tree as it
// was): what this clone had in common with the remote. "" when none is
// found (a clone further behind than the compaction cutoff).
func (b *Backend) replayBase(ctx context.Context, remote, h string) (string, error) {
	rt, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--first-parent", "--format=%T", remote}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: read the remote history", err)
	}
	trees := map[string]bool{}
	for t := range strings.SplitSeq(trimmed(rt), "\n") {
		trees[t] = true
	}
	lt, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--topo-order", "--format=%H %T", h}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: read the local history", err)
	}
	for line := range strings.SplitSeq(trimmed(lt), "\n") {
		c, t, ok := strings.Cut(line, " ")
		if ok && trees[t] {
			return c, nil
		}
	}
	return "", nil
}

// replayOnto merges local work (base..h) into the remote tip without
// common history: three synthetic commits (the base, ours, theirs) give
// git's merge a base, the Merger settles rows both sides changed, and the
// result becomes one commit on remote. The branch moves only at the end.
func (b *Backend) replayOnto(ctx context.Context, remote, h, base string) (bool, []store.Refusal, error) {
	baseTree := emptyTree
	if base != "" {
		t, err := b.treeOf(ctx, base)
		if err != nil {
			return false, nil, err
		}
		baseTree = t
	}
	// The base carries our marker, so the merge takes the remote's (its
	// epoch) without a conflict on a file the Merger does not handle.
	baseTree, err := b.withFile(ctx, baseTree, rowfs.MarkerFile, b.blobAt(ctx, h, rowfs.MarkerFile))
	if err != nil {
		return false, nil, err
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return false, nil, err
	}
	bc, err := b.commitTree(ctx, baseTree, nil, "lore: replay base", env)
	if err != nil {
		return false, nil, err
	}
	ours, err := b.commitTree(ctx, h+"^{tree}", []string{bc}, "lore: replay ours", env)
	if err != nil {
		return false, nil, err
	}
	theirs, err := b.commitTree(ctx, remote+"^{tree}", []string{bc}, "lore: replay theirs", env)
	if err != nil {
		return false, nil, err
	}
	// The worktree moves to ours (same tree as h: the files do not change);
	// a crash from here leaves the branch on ours, whose marker still has
	// the old epoch, so the next sync replays again.
	if err := b.resetTo(ctx, ours); err != nil {
		return false, nil, err
	}
	_, refused, err := b.threeWayResolve(ctx, theirs, env)
	if err != nil || len(refused) > 0 {
		return false, refused, joinKeep(err, b.resetTo(ctx, h))
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"write-tree"}})
	if err != nil {
		return false, nil, joinKeep(errs.Wrap(errs.Internal, "git: write the replayed tree", err), b.abortThenReset(ctx, h))
	}
	tree := trimmed(out)
	rtree, err := b.treeOf(ctx, remote)
	if err != nil {
		return false, nil, joinKeep(err, b.abortThenReset(ctx, h))
	}
	target := remote
	if tree != rtree {
		target, err = b.commitTree(ctx, tree, []string{remote}, b.cfg.withTrailers(replaySubject+strconv.Itoa(b.epochAt(ctx, remote))), env)
		if err != nil {
			return false, nil, joinKeep(err, b.abortThenReset(ctx, h))
		}
	}
	return true, nil, b.resetTo(ctx, target)
}

// abortThenReset ends a merge in progress and puts the branch back on h.
func (b *Backend) abortThenReset(ctx context.Context, h string) error {
	return joinKeep(b.recoverMerge(ctx), b.resetTo(ctx, h))
}

// resetTo moves the branch, index and worktree to commit (lore owns the
// worktree; the sync already required it clean). It also ends a merge in
// progress.
func (b *Backend) resetTo(ctx context.Context, commit string) error {
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"reset", "--hard", "--quiet", commit}}); err != nil {
		return errs.Wrap(errs.Internal, "git: move the store clone", err)
	}
	return nil
}

// joinKeep is both errors, the first one's code leading.
func joinKeep(first, second error) error { return errors.Join(first, second) }

// CompactRequest asks for the store history older than Cutoff to become
// one snapshot commit.
type CompactRequest struct {
	Cutoff time.Time
	// DryRun only plans.
	DryRun bool
	// Squash folds unpushed local commits into one before the compaction
	// pushes them (batch and manual modes).
	Squash bool
}

// CompactResult is what Compact did (or would do).
type CompactResult struct {
	// Nothing: no history older than the cutoff beyond one commit.
	Nothing bool
	// Squashed commits become the snapshot; Kept are rewritten as they were.
	Squashed int
	Kept     int
	// FromEpoch and ToEpoch are the store epoch before and after.
	FromEpoch int
	ToEpoch   int
	// Rows is the row files the snapshot and later history hold at the tip
	// (the same before and after: a compaction keeps every row).
	Rows int
	// Backup is the local ref holding the old tip.
	Backup string
}

// chainCommit is one commit of the first-parent history.
type chainCommit struct {
	id, tree string
	at       time.Time
}

// Compact squashes the history before req.Cutoff into one parentless
// snapshot commit, rewrites the later commits onto it unchanged (trees,
// authors, dates, messages), adds one commit raising the store epoch in
// lore-store.yaml, and force-pushes with a lease on the remote tip it
// read. The branch moves only after the remote took it; the old tip stays
// in a local backup ref. It needs the remote: offline is E_OFFLINE, a push
// that landed in between is E_CONTENDED, and nothing changes then.
func (b *Backend) Compact(ctx context.Context, req CompactRequest) (CompactResult, error) {
	if err := b.usable(ctx); err != nil {
		return CompactResult{}, err
	}
	if b.mode == modeRepo {
		return CompactResult{}, errs.New(errs.Refused, "git: lore never rewrites a code repository's history")
	}
	if !req.DryRun {
		if err := b.compactSync(ctx, req.Squash); err != nil {
			return CompactResult{}, err
		}
	} else {
		// the plan counts what the real run would see: the remote's commits
		// merged into lore's own clone (a fetch, never a push; offline, the
		// plan is the local clone's)
		_, _ = b.syncClone(ctx, SyncOptions{NoPush: true})
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	res, err := b.compactLocked(ctx, req)
	return res, releaseKeep(err, lock)
}

// compactSync brings the clone level with the remote before a compaction:
// everything local pushed, everything remote merged.
func (b *Backend) compactSync(ctx context.Context, squash bool) error {
	rep, err := b.syncClone(ctx, SyncOptions{Squash: squash})
	switch {
	case err != nil:
		return err
	case rep.Offline:
		return errs.New(errs.Offline, "the store is unreachable; compaction rewrites the remote history, so it needs the remote").
			WithHint("run it again when the store is reachable; nothing was changed")
	case len(rep.Refused) > 0:
		return errs.New(errs.Refused, "the store refused this clone's unpushed work ("+rep.Refused[0].Reason+"); compaction waits until everything local is pushed").
			WithHint("`lore sync status` shows the rows and why")
	}
	return nil
}

// compactLocked is Compact under the store lock.
func (b *Backend) compactLocked(ctx context.Context, req CompactRequest) (CompactResult, error) {
	h, err := b.head(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return CompactResult{}, err
	}
	if h == "" || remote == "" {
		return CompactResult{Nothing: true}, nil
	}
	if !req.DryRun && h != remote {
		return CompactResult{}, errs.New(errs.Contended, "a write landed in this clone while the compaction prepared; nothing was changed").
			WithHint("run the compaction again")
	}
	chain, err := b.firstParentChain(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	res := CompactResult{FromEpoch: b.epochAt(ctx, h)}
	res.ToEpoch = res.FromEpoch
	if res.Rows, err = b.countRows(ctx, "", h); err != nil {
		return CompactResult{}, err
	}
	cut := slices.IndexFunc(chain, func(c chainCommit) bool { return c.at.Before(req.Cutoff) })
	if cut < 0 || cut == len(chain)-1 {
		res.Nothing = true // nothing old, or only the root is old
		return res, nil
	}
	res.Squashed, res.Kept = len(chain)-cut, cut
	res.ToEpoch = res.FromEpoch + 1
	if req.DryRun {
		return res, nil
	}
	m, err := b.markerAt(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	tip, err := b.rewrite(ctx, chain, cut, req.Cutoff)
	if err != nil {
		return CompactResult{}, err
	}
	m.Epoch = res.ToEpoch
	tip, err = b.epochCommit(ctx, tip, m)
	if err != nil {
		return CompactResult{}, err
	}
	res.Backup = compactBackupPrefix + strconv.Itoa(res.FromEpoch) + "-" + h[:12]
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", res.Backup, h}}); err != nil {
		return CompactResult{}, errs.Wrap(errs.Internal, "git: keep a backup of the old history", err)
	}
	pruneRefs(ctx, b.git, b.work, compactBackupPrefix, CompactBackupsKept) // bounded (backupprune.go)
	if err := b.forcePush(ctx, tip, remote); err != nil {
		return CompactResult{}, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingRefNS + b.branch, tip}}); err != nil {
		return CompactResult{}, errs.Wrap(errs.Internal, "git: record the pushed tip", err)
	}
	return res, b.resetTo(ctx, tip)
}

// firstParentChain lists the first-parent history of h, newest first.
func (b *Backend) firstParentChain(ctx context.Context, h string) ([]chainCommit, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--first-parent", "--format=%H %T %ct", h}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the store history", err)
	}
	var chain []chainCommit
	for line := range strings.SplitSeq(trimmed(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		sec, perr := strconv.ParseInt(f[2], 10, 64)
		if perr != nil {
			return nil, errs.Wrap(errs.Internal, "git: read a commit date", perr)
		}
		chain = append(chain, chainCommit{id: f[0], tree: f[1], at: time.Unix(sec, 0)})
	}
	return chain, nil
}

// markerAt reads lore-store.yaml at commit; a v0.1.17 marker (no store id)
// is refused, as `lore store visibility` refuses it.
func (b *Backend) markerAt(ctx context.Context, commit string) (rowfs.Marker, error) {
	name := commit + ":" + rowfs.MarkerFile
	blobs, err := b.readBlobs(ctx, []string{name})
	if err != nil {
		return rowfs.Marker{}, err
	}
	raw, ok := blobs[name]
	if !ok {
		return rowfs.Marker{}, errs.Wrap(errs.NotAStore, "git: the store has no "+rowfs.MarkerFile, store.ErrNotAStore)
	}
	m, err := rowfs.ParseMarker(raw)
	if err != nil {
		return rowfs.Marker{}, err
	}
	if m.ID.IsZero() {
		return rowfs.Marker{}, errs.New(errs.OldLore, "the store still has a v0.1.17 "+rowfs.MarkerFile+" (no store id)").
			WithHint("run `lore migrate run` first")
	}
	return m, nil
}

// rewrite makes chain[cut] (and everything before it) one parentless
// snapshot with its tree and date, then rewrites each newer commit onto it
// keeping its tree, author, committer, dates and message.
func (b *Backend) rewrite(ctx context.Context, chain []chainCommit, cut int, cutoff time.Time) (string, error) {
	old := chain[cut]
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	date := "@" + strconv.FormatInt(old.at.Unix(), 10) + " +0000"
	env = append(env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	msg := snapshotSubject + cutoff.UTC().Format(time.DateOnly) + "\n\n" + strconv.Itoa(len(chain)-cut) + " commits squashed; every row is kept"
	prev, err := b.commitTree(ctx, old.tree, nil, b.cfg.withTrailers(msg), env)
	if err != nil {
		return "", err
	}
	for i := cut - 1; i >= 0; i-- {
		if prev, err = b.recommit(ctx, chain[i], prev); err != nil {
			return "", err
		}
	}
	return prev, nil
}

// commitMetaFields is the field count of recommit's log format.
const commitMetaFields = 7

// recommit makes c again on parent with c's own tree, people, dates and
// message.
func (b *Backend) recommit(ctx context.Context, c chainCommit, parent string) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--date=raw", "--format=%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd%x00%B", c.id}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: read a commit", err)
	}
	f := strings.SplitN(string(out), "\x00", commitMetaFields)
	if len(f) != commitMetaFields {
		return "", errs.New(errs.Internal, "git: a commit did not read back")
	}
	env := []string{
		envAuthorName + "=" + f[0], envAuthorEmail + "=" + f[1], "GIT_AUTHOR_DATE=" + f[2],
		envCommitterName + "=" + f[3], envCommitterEmail + "=" + f[4], "GIT_COMMITTER_DATE=" + f[5],
	}
	return b.commitTree(ctx, c.tree, []string{parent}, strings.TrimRight(f[6], "\n"), env)
}

// epochCommit adds one commit on tip whose lore-store.yaml is m.
func (b *Backend) epochCommit(ctx context.Context, tip string, m rowfs.Marker) (string, error) {
	raw, err := m.Encode()
	if err != nil {
		return "", err
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"hash-object", "-w", "--stdin"}, stdin: raw})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the marker", err)
	}
	t, err := b.treeOf(ctx, tip)
	if err != nil {
		return "", err
	}
	tree, err := b.withFile(ctx, t, rowfs.MarkerFile, trimmed(out))
	if err != nil {
		return "", err
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	return b.commitTree(ctx, tree, []string{tip}, b.cfg.withTrailers(compactSubject+strconv.Itoa(m.Epoch)+")"), env)
}

// leaseLost is git's refusal of a --force-with-lease whose expected value
// no longer matches the remote.
const leaseLost = "stale info"

// forcePush replaces the remote branch with tip if it still points at
// expect (--force-with-lease); anything else changes nothing remote.
func (b *Backend) forcePush(ctx context.Context, tip, expect string) error {
	b.pushed()
	dst := headsPrefix + b.branch
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"push", "--porcelain", "--no-verify", "--force-with-lease=" + dst + ":" + expect, "--", RemoteName, tip + ":" + dst}})
	if err != nil {
		if _, ran := exitCode(err); !ran {
			return err
		}
	}
	flag, summary, found := porcelainVerdict(string(out), dst)
	switch {
	case !found:
		return errs.New(errs.Offline, "the store became unreachable before the compacted history was pushed; nothing was changed").
			WithHint("run the compaction again when the store is reachable")
	case flag == flagUpToDate:
		return nil
	case flag == flagForced || flag == flagFastForward:
		b.recordPush(tip, true)
		return nil
	case strings.Contains(summary, leaseLost) || slices.ContainsFunc(lostRaceReasons, func(r string) bool { return strings.Contains(summary, r) }):
		return errs.New(errs.Contended, "another clone pushed while the compaction ran; nothing was changed").
			WithHint("run the compaction again")
	default:
		return errs.New(errs.Refused, "the remote refused the compacted history ("+summary+"); nothing was changed").
			WithHint("a protected branch refuses a rewrite: allow force pushes for lore's account on the store branch, or leave compaction off")
	}
}
