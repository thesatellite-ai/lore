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
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
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

// PushHoldFunc is asked by a sync right before its push, after the fetch
// and merge brought the remote's state (so a roster change a teammate
// pushed is already in the clone): a hold whose ok is true keeps every
// local commit on this machine when one of them was made after
// PushHold.After, and the sync reports one refusal with the reason
// instead of pushing (F-9: a member removed from the org must not publish
// writes made after the removal with the sync that brings the removal).
// It rides the context (WithPushHold) so every sync path of a clone —
// Sync, SyncWith, a push after a write — is held the same way.
type PushHoldFunc func(ctx context.Context) (PushHold, bool)

// pushHoldKey carries a PushHoldFunc on a context.
type pushHoldKey struct{}

// WithPushHold returns ctx carrying hold for the syncs run under it.
func WithPushHold(ctx context.Context, hold PushHoldFunc) context.Context {
	return context.WithValue(ctx, pushHoldKey{}, hold)
}

// pushHoldOf is the PushHoldFunc ctx carries (nil = none).
func pushHoldOf(ctx context.Context) PushHoldFunc {
	h, _ := ctx.Value(pushHoldKey{}).(PushHoldFunc)
	return h
}

// fetchedCloneKey carries the work tree of a clone this run already fetched.
type fetchedCloneKey struct{}

// WithFetchedClone returns ctx saying the clone at dir (Backend.Dir) was
// fetched earlier in this run, so a sync of it under ctx merges and pushes
// over the tracking ref it has, without a fetch of its own (devco
// L-12-perf: one fetch per remote per `lore sync`). A push that loses the
// race still fetches before it tries again.
func WithFetchedClone(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, fetchedCloneKey{}, dir)
}

// fetchedClone is the clone work tree ctx says was fetched ("" = none).
func fetchedClone(ctx context.Context) string {
	d, _ := ctx.Value(fetchedCloneKey{}).(string)
	return d
}

// PushHold is why and since when a clone's local commits must not be
// pushed (PushHoldFunc).
type PushHold struct {
	// After: commits made later than this (committer time, the app clock
	// lore commits with) are held.
	After time.Time
	// Reason is the refusal's text, naming who, where and since when.
	Reason string
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
	// %at too: a batch commit a push squashed but could not send keeps its
	// first write's author date (squashOnto)
	args := []string{"log", "--format=%at %ct", h}
	if remote != "" {
		args = append(args, "^"+remote)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return time.Time{}, errs.Wrap(errs.Internal, "git: read the waiting commits", err)
	}
	var oldest time.Time
	for field := range strings.FieldsSeq(trimmed(out)) {
		sec, perr := strconv.ParseInt(field, 10, 64)
		if perr != nil {
			continue
		}
		if t := time.Unix(sec, 0); oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	return oldest, nil
}

// commitsAfter counts the commits reachable from to and not from from
// ("" = every commit of to) whose committer time is later than after
// (PushHoldFunc).
func (b *Backend) commitsAfter(ctx context.Context, from, to string, after time.Time) (int, error) {
	args := []string{"log", "--format=%ct", to}
	if from != "" {
		args = append(args, "^"+from)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "git: read the commits waiting to be pushed", err)
	}
	n := 0
	for line := range strings.SplitSeq(trimmed(out), "\n") {
		sec, perr := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if perr == nil && time.Unix(sec, 0).After(after) {
			n++
		}
	}
	return n, nil
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

// What a push squashes (store growth item 4, owner 2026-11-25: one commit
// per write made a busy store ~600 commits a day). The commits of
// remote..h become one batch commit per run of commits by the same
// author, in order; the commits the authority replay must judge one by
// one keep a commit of their own with their author, committer, dates and
// message (recommit):
//
//	remote ─ w1 ─ w2 ─ H ─ w3 ─ R ─ w4 ─ x5 ─ x6
//	remote ─ batch(w1,w2) ─ H' ─ w3' ─ R' ─ w4' ─ batch(x5,x6)
//
//	H  a hand edit (brief 2.1: committed as that person; handEditTrailer
//	   from clone.go commitBatch)
//	R  a roster change or a file that is not a row (squashSolo): the
//	   replay judges each later commit against the roster as the earlier
//	   ones left it, so a run never spans one
//	x  another author than w (an agent session authored as its person,
//	   a second identity on the clone): the replay judges each commit by
//	   its author, so authors are never mixed
//
// Each batch keeps its run's author and committer (never the identity of
// the process that pushes), the first commit's author date and the last
// one's committer date: the push hold (commitsAfter, committer time) sees
// no write as older than it was. Each new commit takes its original's
// tree, so the result's tree is h's. A local history with a merge in it
// is squashed only when it is one run with no solo commit (a tree of a
// commit before the merge would undo the merge); else it is pushed as it
// is.

// handEditKey is the trailer key that marks a person's own change.
const handEditKey = "Lore-Hand-Edit"

// handEditTrailer is the trailer line of a hand-edit commit.
const handEditTrailer = handEditKey + ": true"

// localCommit is one commit of remote..h for the squash.
type localCommit struct {
	id string
	// parent is the first parent (a merge's other parents are not kept)
	parent string
	// merge: the commit has more than one parent
	merge bool
	// solo: a commit the squash keeps as a commit of its own (a hand edit,
	// a roster change, a file that is not a row; squashSolo)
	solo bool
	// who is the author and committer (name and e-mail each): a run of
	// commits with the same who becomes one batch
	who string
	// authorDate and committerDate are git's raw dates ("<unix> <tz>")
	authorDate, committerDate string
	// author and committer are the people of the commit
	author, committer Signature
}

// squashOnto makes the commits of remote..h one commit per author run on
// remote with h's tree and a batch message, and moves the branch there
// (compare-and-swap on h). One commit or none is left as it is; so is a
// run of one commit sitting where it was. See above for what stays.
func (b *Backend) squashOnto(ctx context.Context, remote, h string) (string, error) {
	n, err := b.countCommits(ctx, remote, h)
	if err != nil || n <= 1 {
		return h, err
	}
	local, linear, err := b.localCommits(ctx, remote, h)
	if err != nil {
		return h, err
	}
	// A branch layer names the store commit it was made on (its
	// Lore-Layer-Store-Base trailer) and lands against it: a local commit a
	// layer names, and every one before it, stays as it is; only the
	// commits after the newest of them are folded.
	bases, err := b.layerBases(ctx)
	if err != nil {
		return h, err
	}
	for i := len(local) - 1; i >= 0; i-- {
		if bases[local[i].id] {
			remote, local, linear = local[i].id, local[i+1:], linearFrom(local[i].id, local[i+1:])
			break
		}
	}
	if len(local) <= 1 {
		return h, nil
	}
	if !linear && !squashableMerge(local) {
		return h, nil // nothing lore may fold without losing a commit the replay judges alone
	}
	next := remote
	var run []localCommit // the current run of one author's commits
	flush := func() error {
		if len(run) == 0 {
			return nil
		}
		defer func() { run = nil }()
		last := run[len(run)-1]
		if len(run) == 1 {
			var err error
			next, err = b.keepCommit(ctx, last, next)
			return err
		}
		msg, err := b.batchMessage(ctx, run[0].id+"^", last.id)
		if err != nil {
			return err
		}
		env := slices.Concat(authorEnv(run[0].author), committerEnv(last.committer),
			[]string{"GIT_AUTHOR_DATE=" + run[0].authorDate, "GIT_COMMITTER_DATE=" + last.committerDate})
		next, err = b.commitTree(ctx, last.id+"^{tree}", []string{next}, b.cfg.withTrailers(ctx, msg), env)
		return err
	}
	for _, c := range local {
		if !c.solo && (len(run) == 0 || run[0].who == c.who) {
			run = append(run, c)
			continue
		}
		if err := flush(); err != nil {
			return h, err
		}
		if c.solo {
			if next, err = b.keepCommit(ctx, c, next); err != nil {
				return h, err
			}
			continue
		}
		run = []localCommit{c}
	}
	if err := flush(); err != nil {
		return h, err
	}
	if next == h {
		return h, nil
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-m", "lore: batch", "HEAD", next, h}}); err != nil {
		return h, errs.Wrap(errs.Internal, "git: move the branch to the batch commit", err)
	}
	return next, nil
}

// layerBases is the set of store commits the clone's own branch layers
// (live and trashed) name as their base (TrailerStoreBase).
func (b *Backend) layerBases(ctx context.Context) (map[string]bool, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"for-each-ref",
		"--format=%(trailers:key=" + TrailerStoreBase + ",valueonly,separator=%x2C)", branchlayer.RefPrefix, branchlayer.TrashPrefix}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the branch layers' bases", err)
	}
	bases := map[string]bool{}
	for _, f := range strings.FieldsFunc(string(out), func(r rune) bool { return r == '\n' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			bases[f] = true
		}
	}
	return bases, nil
}

// linearFrom reports whether local is a line of single-parent commits on
// base.
func linearFrom(base string, local []localCommit) bool {
	prev := base
	for _, c := range local {
		if c.merge || c.parent != prev {
			return false
		}
		prev = c.id
	}
	return true
}

// squashableMerge reports whether a local history with a merge in it may
// be squashed at all: one author's commits and no solo commit, so the one
// batch takes h's tree and drops no commit the replay must judge alone.
func squashableMerge(local []localCommit) bool {
	for _, c := range local {
		if c.solo || c.who != local[0].who {
			return false
		}
	}
	return true
}

// keepCommit puts c on next unchanged: c itself when it already sits there
// (its original parent was not rewritten), else c made again on next with
// its own tree, people, dates and message.
func (b *Backend) keepCommit(ctx context.Context, c localCommit, next string) (string, error) {
	if c.parent == next {
		return c.id, nil
	}
	m, err := b.metaOf(ctx, c.id)
	if err != nil {
		return "", err
	}
	return b.commitTree(ctx, c.id+"^{tree}", []string{next}, m.msg, m.env)
}

// squashSolo reports whether a commit with these changes keeps a commit
// of its own in a squash: a row of a table the authority replay keeps
// (the roster, project and team membership, team contacts), the org file
// (each change is judged against the admins at its parent) or the store
// marker (settings, epoch). A starter file or the signpost a first write
// adds is not. A new actor row is roster too: a refused roster change
// holds back every row of its commit, so folded with the author's other
// writes it would hold those back as well (TestSquashKeepsTheHeldSet).
func squashSolo(paths []string) bool {
	for _, p := range paths {
		if p == rowfs.MarkerFile || rowfs.IsOrgFileName(path.Base(p)) {
			return true
		}
		if t := tableOfPath(p); isRowFile(p) && (kept(t) || teamInfoTables[t]) {
			return true
		}
	}
	return false
}

// localCommits lists remote..h oldest first, each with its people, dates
// and whether it stays a commit of its own (squashSolo, hand edits);
// linear is false when one of them is a merge or the first one does not
// sit on remote. A merge lists no paths (git log shows none for it).
func (b *Backend) localCommits(ctx context.Context, remote, h string) ([]localCommit, bool, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "--reverse", "--topo-order", "--no-renames", "--name-only", "-z", "--date=raw",
		"--format=%x1e%H%x00%P%x00%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd%x00%(trailers:key=" + handEditKey + ",valueonly,separator=%x2C)%x1f", h, "^" + remote}})
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
		head, files, _ := strings.Cut(rec, "\x1f")
		f := strings.Split(head, "\x00")
		if len(f) != localCommitFields || f[0] == "" {
			continue
		}
		parents := strings.Fields(f[1])
		if len(parents) != 1 || parents[0] != prev {
			linear = false
		}
		prev = f[0]
		var paths []string
		for p := range strings.SplitSeq(files, "\x00") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		author, committer := Signature{Name: f[2], Email: f[3]}, Signature{Name: f[5], Email: f[6]}
		list = append(list, localCommit{
			id: f[0], parent: first(parents), merge: len(parents) > 1, solo: strings.TrimSpace(f[8]) != "" || squashSolo(paths),
			who:    strings.Join([]string{f[2], f[3], f[5], f[6]}, "\x00"),
			author: author, committer: committer, authorDate: f[4], committerDate: f[7],
		})
	}
	return list, linear, nil
}

// localCommitFields is the field count of localCommits' log format.
const localCommitFields = 9

// first is the first of list ("" when empty).
func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
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

// recoverMerge aborts a merge a crashed run of lore left in the worktree
// (its ownMergeFile mark is there); the branch is then where it was
// before that merge. A merge, rebase or other git operation the person
// has in progress there (the clone is set up for plain git) is refused,
// never aborted (B2-9, gitop.go).
func (b *Backend) recoverMerge(ctx context.Context) error {
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "-q", "--verify", "MERGE_HEAD"}}); err != nil {
		b.clearOwnMerge(ctx)
		return nil // no merge in progress
	}
	return b.abortMerge(ctx)
}

// adoptEpoch handles a remote whose history was compacted (or a
// compaction undone) since this clone last merged it (storepush.Refetch):
// the clone first re-does that rewrite from its own history
// (verifyEpoch: the rewriter an admin, the snapshot the trusted view, every
// kept commit unchanged); a rewrite it cannot reproduce is refused and the
// clone stays on its own history. Verified, the branch moves onto the
// remote tip, the clone's layers are re-made on the new ids, and local
// work the remote does not hold is replayed on top as one commit (rows
// both sides changed go through the Merger). The old tip is kept in a
// backup ref. adopted is false when there was nothing to adopt.
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
	proof, why, err := b.verifyEpoch(ctx, remote, h, 0)
	if err != nil {
		return false, nil, err
	}
	if why != "" {
		return false, refusalOf(adoptRefusal(b.RemoteURL(), why)), nil
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", backupRefNS + "epoch-" + strconv.Itoa(re) + "-" + h[:12], h}}); err != nil {
		return false, nil, errs.Wrap(errs.Internal, "git: keep a backup of the local history", err)
	}
	layers, err := b.openLayerRefs(ctx, true)
	if err != nil {
		return false, nil, err
	}
	if _, err := b.rebaseLayers(ctx, layers, proof.idMap, proof.oldTip); err != nil {
		return false, nil, err
	}
	// this clone's own signals and live refs move the same way (their
	// fetched copies come from the remote, which the compactor moved)
	if err := b.moveOwnLoreRefs(ctx, remote, proof); err != nil {
		return false, nil, err
	}
	if anc, aerr := b.isAncestor(ctx, h, proof.oldTip); aerr != nil {
		return false, nil, aerr
	} else if anc {
		// Nothing local beyond what the remote holds: take the remote.
		return true, nil, b.resetTo(ctx, remote)
	}
	base, err := b.lines(ctx, "find the replay base", "merge-base", h, proof.oldTip)
	if err != nil || len(base) != 1 {
		return false, nil, errors.Join(errs.New(errs.Internal, "git: no common commit with the compacted history"), err)
	}
	return b.replayOnto(ctx, remote, h, base[0])
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
	_, refused, _, err := b.threeWayResolve(ctx, theirs, env)
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
		target, err = b.commitTree(ctx, tree, []string{remote}, b.cfg.withTrailers(ctx, replaySubject+strconv.Itoa(b.epochAt(ctx, remote))), env)
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
	// Archive is the ref (in the store's remote, or archive_url) holding
	// the old tip for every clone.
	Archive string
	// Held is how many held changes the snapshot carries as their last
	// accepted version.
	Held int
	// Layers is how many layers (open and trashed) were re-made on the
	// new history; LayerNote says why pushing them failed, or how many
	// still reach the old history ("" = all moved and pushed, or none).
	Layers    int
	LayerNote string
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
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return CompactResult{}, err // B2-9
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
	all, err := b.countRows(ctx, "", h)
	if err != nil {
		return CompactResult{}, err
	}
	for _, n := range all {
		res.Rows += n // every folder's rows: the compaction rewrites the whole clone
	}
	cut := slices.IndexFunc(chain, func(c chainCommit) bool { return c.at.Before(req.Cutoff) })
	if cut >= 0 && cut < len(chain)-1 {
		if cut, err = b.closedCut(ctx, chain, cut, h); err != nil {
			return CompactResult{}, err
		}
	}
	if cut < 0 || cut == len(chain)-1 {
		res.Nothing = true // nothing old, or only the root is old
		return res, nil
	}
	res.Squashed, res.Kept = len(chain)-cut, cut
	res.ToEpoch = res.FromEpoch + 1
	res.Archive = archiveRef(res.FromEpoch)
	if req.DryRun {
		return res, nil
	}
	env, err := b.compactorCheck(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	m, err := b.markerAt(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	c := chain[cut]
	at, err := b.replayAt(ctx, c.id)
	if err != nil {
		return CompactResult{}, err
	}
	held, err := b.heldAt(ctx, at, c.id)
	if err != nil {
		return CompactResult{}, err
	}
	newTip, idMap, err := b.rewriteFrom(ctx, c.id, h, held, b.snapshotMeta(ctx, env, c, res.Squashed, req.Cutoff, res.ToEpoch))
	if err != nil {
		return CompactResult{}, err
	}
	cp := Checkpoint{V: checkpointVersion, Kind: CheckpointCompact, FromEpoch: res.FromEpoch, ToEpoch: res.ToEpoch, OldTip: h,
		Archive: res.Archive, ArchiveURL: m.ArchiveURL, Cut: c.id, CutTree: c.tree, Cutoff: req.Cutoff.UTC().Format(time.DateOnly)}
	for _, p := range slices.Sorted(maps.Keys(held)) {
		cp.Held = append(cp.Held, CheckpointHeld{Path: p, Raw: held[p].raw, Trusted: held[p].trusted})
	}
	res.Held = len(held)
	m.Epoch = res.ToEpoch
	tip, err := b.epochCommitV2(ctx, newTip, m, cp)
	if err != nil {
		return CompactResult{}, err
	}
	return b.publishRewrite(ctx, res, h, remote, tip, idMap[c.id], idMap)
}

// publishRewrite archives the old tip h, force-pushes tip over remote,
// re-makes the layers on the new ids and moves the clone there: the end of
// a compaction and of an undo. Nothing moves when the archive or the
// force-push is refused.
func (b *Backend) publishRewrite(ctx context.Context, res CompactResult, h, remote, tip, snap string, idMap map[string]string) (CompactResult, error) {
	m, err := b.markerAt(ctx, h)
	if err != nil {
		return CompactResult{}, err
	}
	res.Backup = compactBackupPrefix + strconv.Itoa(res.FromEpoch) + "-" + h[:12]
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", res.Backup, h}}); err != nil {
		return CompactResult{}, errs.Wrap(errs.Internal, "git: keep a backup of the old history", err)
	}
	pruneRefs(ctx, b.git, b.work, compactBackupPrefix, CompactBackupsKept) // bounded (backupprune.go)
	if err := b.pushArchive(ctx, m.ArchiveURL, res.Archive, h); err != nil {
		return CompactResult{}, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", res.Archive, h}}); err != nil {
		return CompactResult{}, errs.Wrap(errs.Internal, "git: record the archive", err)
	}
	// every lore ref the remote holds, so none that reaches the old
	// history is missed (the org link code fetches them all anyway)
	_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", RemoteName, "+refs/lore/*:" + loreTrackingNS + "*"}})
	layers, err := b.openLayerRefs(ctx, true)
	if err != nil {
		return CompactResult{}, err
	}
	others, err := b.otherLoreRefs(ctx)
	if err != nil {
		return CompactResult{}, err
	}
	if err := b.forcePush(ctx, tip, remote); err != nil {
		return CompactResult{}, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingRefNS + b.branch, tip}}); err != nil {
		return CompactResult{}, errs.Wrap(errs.Internal, "git: record the pushed tip", err)
	}
	moved, err := b.rebaseLayers(ctx, layers, idMap, h)
	stuck := map[string]string{}
	if err == nil {
		names := map[string]bool{}
		for ref := range moved {
			names[layerName(ref)] = true
		}
		res.Layers = len(names)
		for ref, t := range layers {
			if _, ok := moved[ref]; ok || names[layerName(ref)] {
				continue
			}
			if mb, _ := b.lines(ctx, "compare a layer", "merge-base", t, h); len(mb) > 0 {
				stuck[ref] = t // a trashed layer older than the cut: into the archive
			}
		}
		for ref, t := range others {
			n, ok, merr := b.moveOffHistory(ctx, t, h, snap, idMap)
			if merr != nil {
				err = merr
				break
			}
			if ok {
				if _, uerr := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", ref, n, t}}); uerr != nil {
					err = errs.Wrap(errs.Internal, "git: move "+ref+" onto the compacted history", uerr)
					break
				}
				moved[ref] = n
			}
		}
	}
	if err == nil {
		was := maps.Clone(layers)
		maps.Copy(was, others)
		err = b.pushMovedRefs(ctx, moved, stuck, was, res.FromEpoch)
		if err == nil && len(stuck) > 0 {
			stuckNames := map[string]bool{}
			for ref := range stuck {
				stuckNames[layerName(ref)] = true
			}
			res.LayerNote = strconv.Itoa(len(stuckNames)) + " trashed layer(s) older than the cut moved to " + archivedRefPrefix + strconv.Itoa(res.FromEpoch) + "/"
		}
	}
	if err != nil {
		res.LayerNote = "not pushed (" + err.Error() + "): each one's next lore sync pushes it"
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

// layerName is a layer or trash ref without its namespace: one name for a
// layer's own ref and its fetched copy.
func layerName(ref string) string {
	for _, p := range []string{LayerTrackingPrefix, branchlayer.RefPrefix} {
		if rest, ok := strings.CutPrefix(ref, p); ok {
			return "layer/" + rest
		}
	}
	for _, p := range []string{LayerTrashTrackingPrefix, branchlayer.TrashPrefix} {
		if rest, ok := strings.CutPrefix(ref, p); ok {
			return "trash/" + rest
		}
	}
	return ref
}

// commitMetaFields is the field count of metaOf's log format.
const commitMetaFields = 7

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
		b.recordPush(tip, true, pushStats{}) // a compaction rewrites every folder: named by the store
		return nil
	case strings.Contains(summary, leaseLost) || slices.ContainsFunc(lostRaceReasons, func(r string) bool { return strings.Contains(summary, r) }):
		return errs.New(errs.Contended, "another clone pushed while the compaction ran; nothing was changed").
			WithHint("run the compaction again")
	default:
		return errs.New(errs.Refused, "the remote refused the compacted history ("+summary+"); nothing was changed").
			WithHint("a protected branch refuses a rewrite: allow force pushes for lore's account on the store branch, or leave compaction off")
	}
}
