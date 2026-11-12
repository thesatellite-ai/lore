package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// MaxPushAttempts bounds how often Sync pushes after losing a race to
// another writer (fetch, merge, push again; spec 6.1 Sync, commit 52049f3).
const MaxPushAttempts = 3

// mergeSubject is the subject of the commit that merges the remote in.
const mergeSubject = "lore: merge " + RemoteName

// pushVerdict is what one push attempt came to.
type pushVerdict int

const (
	pushApplied  pushVerdict = iota // the remote now holds our commit
	pushLostRace                    // another writer moved the branch first: fetch, merge, retry
	pushOffline                     // no verdict: the remote was not reached
	pushRefused                     // the remote refused for good (hook, protection, permission)
)

// Porcelain flags of `git push --porcelain` (git-push(1) "Output").
const (
	flagFastForward = ' '
	flagForced      = '+'
	flagDeleted     = '-'
	flagNew         = '*'
	flagRejected    = '!'
	flagUpToDate    = '='
)

// porcelainFields is the field count of a ref line:
// "<flag>\t<from>:<to>\t<summary>".
const porcelainFields = 3

// lostRaceReasons are rejection reasons that mean another push moved the
// branch first (saas/pkg/aicoder/gitremote/git.go, including the ref-lock
// wordings of commit 52049f3): the plain "fetch first" / "non-fast-forward"
// and the server-side compare-and-swap losses, which are the same race
// detected one step later. Hook declines and protection never match.
var lostRaceReasons = []string{
	"non-fast-forward", "fetch first",
	"incorrect old value provided", "cannot lock ref", "failed to lock", "reference already exists",
	"atomic transaction failed", "failed to update ref", "unable to update local ref",
}

// syncClone is clone-mode Sync, under the store lock: fetch; merge the
// remote into the local branch (rows both sides changed go through the
// Merger); push; on a lost race fetch and try again, up to MaxPushAttempts.
// Offline at any step is reported, never an error, and loses nothing: the
// local commits stay as the outbox.
//
// o (SyncWith, pushmodes.go): NoPush stops after the merge; Squash folds
// the unpushed commits into one batch commit before each push attempt. A
// remote whose epoch is higher (its history was compacted) is adopted
// before any merge (adoptEpoch).
func (b *Backend) syncClone(ctx context.Context, o SyncOptions) (rep store.SyncReport, err error) {
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return store.SyncReport{}, err
	}
	defer func() { err = releaseKeep(err, lock) }()
	if err = b.recoverMerge(ctx); err != nil {
		return store.SyncReport{}, err
	}
	// Hand edits still in the files (refused by the pick-up, or made while
	// lore ran) are set aside for the merge and put back after it: a sync
	// never clobbers them and never stops on them (brief 2.2).
	putBack, err := b.setAsideHandEdits(ctx)
	if err != nil {
		return store.SyncReport{}, err
	}
	defer func() { err = errors.Join(err, putBack()) }()
	before, err := b.head(ctx)
	if err != nil {
		return store.SyncReport{}, err
	}
	pushedOver, pushed := "", false
	for attempt := 0; ; attempt++ {
		if ferr := b.fetch(ctx, b.work); ferr != nil {
			if !isOffline(ferr) {
				return rep, ferr
			}
			rep.Offline = true
			break
		}
		remote, rerr := b.resolve(ctx, b.work, trackingRefNS+b.branch)
		if rerr != nil {
			return rep, rerr
		}
		if _, refusedEpoch, aerr := b.adoptEpoch(ctx, remote); aerr != nil {
			return rep, aerr
		} else if len(refusedEpoch) > 0 {
			rep.Refused = append(rep.Refused, refusedEpoch...)
			break
		}
		merged, refused, merr := b.mergeRemote(ctx, remote)
		if merr != nil {
			return rep, merr
		}
		rep.Merged = appendNew(rep.Merged, merged)
		if len(refused) > 0 {
			rep.Refused = append(rep.Refused, refused...)
			break
		}
		h, herr := b.head(ctx)
		if herr != nil {
			return rep, herr
		}
		if h == "" || h == remote {
			break // nothing of ours to send
		}
		if o.NoPush {
			break // the local commits wait for a push (batch, manual)
		}
		if o.Squash && remote != "" {
			if h, herr = b.squashOnto(ctx, remote, h); herr != nil {
				return rep, herr
			}
		}
		if b.beforePush != nil {
			b.beforePush(attempt)
		}
		verdict, msg, perr := b.push(ctx, h)
		if perr != nil {
			return rep, perr
		}
		switch verdict {
		case pushApplied:
			pushedOver, pushed = remote, true
		case pushOffline:
			rep.Offline = true
		case pushRefused:
			rep.Refused = append(rep.Refused, b.refusedRows(ctx, remote, h, "the remote refused the push: "+msg)...)
		case pushLostRace:
			if attempt+1 < MaxPushAttempts {
				continue
			}
			return rep, errs.New(errs.Contended, "git: lost the push race "+strconv.Itoa(MaxPushAttempts)+" times; the local commits are kept").
				WithHint("run `lore sync` again")
		}
		break
	}
	b.markHeld(rep.Refused)
	return b.count(ctx, rep, before, pushedOver, pushed)
}

// markHeld marks the refusals of rows in another folder of the store than
// this Backend's (store.Refusal.Held): the clone's one merge or push
// carried every folder, so they hold this folder's push too, but each is
// reported by the folder that holds its row. A refusal with no folder
// (the whole push, an epoch) is every folder's own.
func (b *Backend) markHeld(refused []store.Refusal) {
	for i := range refused {
		refused[i].Held = refused[i].Folder != "" && refused[i].Folder != b.folder.Name()
	}
}

// refusedRows is one Refusal per row the refused push carried (row files
// that differ between the remote tip and the local tip, any folder), so a
// caller can say which rows wait; when they cannot be listed (no remote
// tip, git failed, no row file in the range) it is one Refusal with no id.
func (b *Backend) refusedRows(ctx context.Context, remote, tip, reason string) []store.Refusal {
	whole := []store.Refusal{{Code: errs.PolicyDenied, Reason: reason}}
	if remote == "" {
		return whole
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "-z", "--no-renames", remote, tip}})
	if err != nil {
		return whole
	}
	var rows []store.Refusal
	for _, p := range splitZ(out) {
		folder, rel, ok := rowPlace(p)
		if !ok {
			continue
		}
		if _, id, ok := rowfs.ParseRowPath(rel); ok {
			rows = append(rows, store.Refusal{ID: id, Code: errs.PolicyDenied, Reason: reason, Folder: folder})
		}
	}
	if len(rows) == 0 {
		return whole
	}
	return rows
}

// count fills Received (rows changed between the local tip before the sync
// and now) and Sent (rows the pushed commit added over the remote tip).
func (b *Backend) count(ctx context.Context, rep store.SyncReport, before, pushedOver string, pushed bool) (store.SyncReport, error) {
	after, err := b.head(ctx)
	if err != nil {
		return rep, err
	}
	if rep.Received, err = b.countRows(ctx, before, after); err != nil {
		return rep, err
	}
	if pushed {
		if rep.Sent, err = b.countRows(ctx, pushedOver, after); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// countRows counts row files (any folder) that differ between from and to;
// from "" counts every row file at to.
func (b *Backend) countRows(ctx context.Context, from, to string) (int, error) {
	if to == "" || from == to {
		return 0, nil
	}
	var out []byte
	var err error
	if from == "" {
		out, err = b.git.run(ctx, call{dir: b.work, args: []string{"ls-tree", "-r", "-z", "--name-only", to}})
	} else {
		out, err = b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "-z", "--no-renames", from, to}})
	}
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "git: count changed rows", err)
	}
	n := 0
	for _, p := range splitZ(out) {
		if _, ok := rowRel(p); ok {
			n++
		}
	}
	return n, nil
}

// isAncestor reports whether commit a is an ancestor of (or equal to) b.
func (b *Backend) isAncestor(ctx context.Context, a, c string) (bool, error) {
	_, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge-base", "--is-ancestor", a, c}})
	if err == nil {
		return true, nil
	}
	if code, ran := exitCode(err); ran && code == 1 {
		return false, nil
	}
	return false, errs.Wrap(errs.Internal, "git: compare commits", err)
}

// mergeRemote brings the remote tip into the local branch: nothing when
// the remote is absent or already contained, a fast-forward when the local
// branch has nothing new, else a three-way merge whose row conflicts go
// through the Merger. Any row that cannot be merged aborts the whole merge
// and is returned as a refusal (fail closed: the local commits stay).
func (b *Backend) mergeRemote(ctx context.Context, remote string) ([]ids.AnyID, []store.Refusal, error) {
	if remote == "" {
		return nil, nil, nil
	}
	h, err := b.head(ctx)
	if err != nil {
		return nil, nil, err
	}
	if h != "" {
		contained, aerr := b.isAncestor(ctx, remote, h)
		if aerr != nil || contained {
			return nil, nil, aerr
		}
	}
	ff := h == ""
	if !ff {
		if ff, err = b.isAncestor(ctx, h, remote); err != nil {
			return nil, nil, err
		}
	}
	// FLAG4: git merge asks for a committer identity before it starts,
	// even for a fast-forward or --no-commit, so every merge gets the
	// commit environment (lore's identity only when git has none).
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return nil, nil, err
	}
	if ff {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge", "--ff-only", "--quiet", remote}, env: env}); err != nil {
			return nil, nil, errs.Wrap(errs.Internal, "git: fast-forward to the remote", err)
		}
		return nil, nil, nil
	}
	return b.threeWay(ctx, remote, env)
}

// threeWay runs the three-way merge of remote. Row files are marked
// -merge (rowAttributes), so every row both sides changed is left
// conflicted with ours in the worktree, and resolved here.
func (b *Backend) threeWay(ctx context.Context, remote string, env []string) ([]ids.AnyID, []store.Refusal, error) {
	merged, refused, err := b.threeWayResolve(ctx, remote, env)
	if err != nil || len(refused) > 0 {
		return nil, refused, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", b.cfg.withTrailers(mergeSubject)}, env: env}); err != nil {
		return nil, nil, errors.Join(errs.Wrap(errs.Internal, "git: commit the merge", err), b.abortMerge(ctx))
	}
	return merged, nil, nil
}

// threeWayResolve is threeWay up to the commit: the merge is resolved in
// the index and worktree (MERGE_HEAD still set); a refusal or an error has
// already aborted it.
func (b *Backend) threeWayResolve(ctx context.Context, remote string, env []string) ([]ids.AnyID, []store.Refusal, error) {
	attrs, err := b.mergeAttributes()
	if err != nil {
		return nil, nil, err
	}
	_, err = b.git.run(ctx, call{dir: b.work, args: []string{"-c", "core.attributesFile=" + attrs, "merge", "--no-ff", "--no-commit", "--no-verify", "--quiet", "-s", "ort", "-X", "no-renames", remote}, env: env})
	if err != nil {
		if code, ran := exitCode(err); !ran || code != 1 {
			return nil, nil, errors.Join(errs.Wrap(errs.Internal, "git: merge the remote", err), b.abortMerge(ctx))
		}
	}
	paths, err := b.unmerged(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, b.abortMerge(ctx))
	}
	var merged []ids.AnyID
	var refused []store.Refusal
	for _, p := range paths {
		if rowfs.IsFolderFile(p) {
			if ferr := b.resolveFolderFile(ctx, p); ferr != nil {
				return nil, nil, errors.Join(ferr, b.abortMerge(ctx))
			}
			continue
		}
		id, refusal, rerr := b.resolveRow(ctx, p)
		if rerr != nil {
			return nil, nil, errors.Join(rerr, b.abortMerge(ctx))
		}
		if refusal != nil {
			refused = append(refused, *refusal)
			continue
		}
		merged = append(merged, id)
	}
	if len(refused) > 0 {
		return nil, refused, b.abortMerge(ctx)
	}
	return merged, nil, nil
}

// abortMerge puts the worktree back to the local tip after a failed merge.
// A merge that failed before it started (no MERGE_HEAD, e.g. git refused
// for want of an identity) has nothing to abort: the abort is skipped so
// the real cause is reported alone (r2-ident).
func (b *Backend) abortMerge(ctx context.Context) error {
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--quiet", "--verify", "MERGE_HEAD"}}); err != nil {
		return nil
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge", "--abort"}}); err != nil {
		return errs.Wrap(errs.Internal, "git: abort the merge", err)
	}
	return nil
}

// unmerged lists the conflicted paths of the index, sorted.
func (b *Backend) unmerged(ctx context.Context) ([]string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-files", "-u", "-z"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: list conflicted files", err)
	}
	var paths []string
	for _, e := range splitZ(out) {
		_, p, ok := strings.Cut(e, "\t") // "<mode> <oid> <stage>\t<path>"
		if ok && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

// Index stages of a conflicted path.
const (
	stageBase   = ":1:"
	stageOurs   = ":2:"
	stageTheirs = ":3:"
)

// resolveRow resolves one conflicted path. A non-row file is an error (lore
// does not merge files it does not own). A row missing on one side takes
// the other (a removed file is not a delete). A row on both sides goes to
// the Merger; no Merger, or a merge it calls a conflict, is a refusal.
func (b *Backend) resolveRow(ctx context.Context, path string) (ids.AnyID, *store.Refusal, error) {
	folder, rel, ok := rowPlace(path)
	if !ok {
		return ids.AnyID{}, nil, errs.New(errs.Stale, "git: the store file "+quotePath(path)+" changed on both sides and is not a row")
	}
	table, id, _ := rowfs.ParseRowPath(rel)
	stages, err := b.readBlobs(ctx, []string{stageBase + path, stageOurs + path, stageTheirs + path})
	if err != nil {
		return ids.AnyID{}, nil, err
	}
	base, ours, theirs := stages[stageBase+path], stages[stageOurs+path], stages[stageTheirs+path]
	var result []byte
	switch {
	case ours == nil:
		result = theirs
	case theirs == nil:
		result = ours
	case b.cfg.merger == nil:
		return id, &store.Refusal{ID: id, Code: errs.Stale, Reason: "both sides changed the row and no merger is configured", Folder: folder}, nil
	default:
		m, conflict, merr := b.cfg.merger.Merge(base, ours, theirs)
		if merr != nil {
			return ids.AnyID{}, nil, errs.Wrap(errs.Internal, "git: merge a row", merr)
		}
		if conflict {
			return id, &store.Refusal{ID: id, Code: conflictCode(table), Reason: "both sides changed the row and it cannot be merged automatically", Folder: folder}, nil
		}
		result = m
	}
	if err := checkMerged(rel, result); err != nil {
		return ids.AnyID{}, nil, err
	}
	if err := b.root.WriteFile(path, result); err != nil {
		return ids.AnyID{}, nil, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"add", "--", path}}); err != nil {
		return ids.AnyID{}, nil, errs.Wrap(errs.Internal, "git: stage a merged row", err)
	}
	return id, nil, nil
}

// conflictCode is the refusal code of a row the Merger could not merge:
// a resource, kind or kind field row is E_RESOURCE_CONFLICT (RES 3.13,
// 3.19: the local commit stays unpushed until a person settles it), any
// other row E_STALE.
func conflictCode(t row.Table) errs.Code {
	switch t {
	case schema.TableResources, schema.TableKinds, schema.TableKindFields:
		return errs.ResourceConflict
	}
	return errs.Stale
}

// checkMerged refuses a merge result that is not a canonical document of
// the row at rel: a Merger bug must never reach the store.
func checkMerged(rel string, doc []byte) error {
	if len(doc) == 0 || len(doc) > rowfs.MaxFileSize {
		return errs.New(errs.Internal, "git: the merged row is empty or too large")
	}
	c, err := canon.Document(doc)
	if err != nil || !bytes.Equal(c, doc) {
		return errs.New(errs.Internal, "git: the merged row is not canonical JSON")
	}
	if _, err := rowfs.DecodeRowFile(rel, doc); err != nil {
		return errs.Wrap(errs.Internal, "git: the merged row does not decode", err)
	}
	return nil
}

// push sends the local tip to the store branch as a fast-forward and reads
// git's verdict from --porcelain. msg is git's summary for a refusal.
func (b *Backend) push(ctx context.Context, tip string) (pushVerdict, string, error) {
	b.pushed() // the open's fetch no longer stands for the remote (OpenFetchReuse)
	dst := headsPrefix + b.branch
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"push", "--porcelain", "--no-verify", "--", RemoteName, tip + ":" + dst}})
	if err != nil {
		if _, ran := exitCode(err); !ran {
			return 0, "", err
		}
	}
	flag, summary, found := porcelainVerdict(string(out), dst)
	if !found {
		return pushOffline, "", nil
	}
	switch flag {
	case flagUpToDate:
		return pushApplied, "", nil
	case flagFastForward, flagForced, flagDeleted, flagNew:
		b.recordPush(tip, flag == flagForced)
		return pushApplied, "", nil
	case flagRejected:
		for _, r := range lostRaceReasons {
			if strings.Contains(summary, r) {
				return pushLostRace, summary, nil
			}
		}
		return pushRefused, summary, nil
	default:
		return pushRefused, summary, nil
	}
}

// porcelainVerdict finds the ref line for dst in `git push --porcelain`
// output; found is false when git printed none (it never reached the
// remote).
func porcelainVerdict(out, dst string) (byte, string, bool) {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, "\t", porcelainFields)
		if len(fields) != porcelainFields || len(fields[0]) != 1 {
			continue
		}
		if _, to, ok := strings.Cut(fields[1], ":"); ok && to == dst {
			return fields[0][0], fields[2], true
		}
	}
	return 0, "", false
}

// rowRel returns a store path's row path relative to its folder's data dir
// ("memories/mem_….json") when the path is a row file of any folder.
func rowRel(p string) (string, bool) {
	_, rel, ok := rowPlace(p)
	return rel, ok
}

// rowPlace is rowRel plus the folder that holds the row (rowfs.FolderRoot
// for the store root's table folders). Only store clones sync, so the
// path is read in the store layout (<folder>/<table>/<id>.json), or in the
// old layout of a folder not moved yet (rowfs Folder.OldLayout).
func rowPlace(p string) (folder, rel string, ok bool) {
	if folder, rel, ok = rowfs.LayoutStore.SplitRowPath(p); ok {
		return folder, rel, true
	}
	return rowfs.LayoutRepo.SplitRowPath(p)
}

// appendNew appends the ids of add not already in list.
func appendNew(list, add []ids.AnyID) []ids.AnyID {
	for _, id := range add {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}

// maxQuotedPath bounds a path repeated in a message.
const maxQuotedPath = 96

// quotePath renders a store path for a message, truncated.
func quotePath(p string) string {
	if len(p) > maxQuotedPath {
		p = p[:maxQuotedPath] + "…"
	}
	return `"` + p + `"`
}

// resolveFolderFile merges a folder's _meta.json or _purged.json both
// sides changed (rowfs.MergeFolderFile: links and purged ids are unions,
// the first project_id wins), writes and stages the result. These files
// are never refused: a store must always be able to sync.
func (b *Backend) resolveFolderFile(ctx context.Context, path string) error {
	stages, err := b.readBlobs(ctx, []string{stageBase + path, stageOurs + path, stageTheirs + path})
	if err != nil {
		return err
	}
	merged, err := rowfs.MergeFolderFile(path, stages[stageBase+path], stages[stageOurs+path], stages[stageTheirs+path])
	if err != nil {
		return err
	}
	if err := b.root.WriteFile(path, merged); err != nil {
		return err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"add", "--", path}}); err != nil {
		return errs.Wrap(errs.Internal, "git: stage a merged "+path, err)
	}
	return nil
}

// mergeAttributesName is the attributes file of lore's own three-way
// merge, in the clone's lore state folder (.git/lore, beside its lock).
const mergeAttributesName = "merge-attributes"

// mergeAttributes writes (when missing or different) the attributes file
// lore's own merge passes as core.attributesFile: `*.json -merge`, so git
// never text-merges a row file and leaves every row both sides changed to
// the Merger, whether or not the clone's .git/info/attributes was set up
// for plain git (owner bug 6: a read no longer writes that file, and a
// read's refresh may merge).
func (b *Backend) mergeAttributes() (string, error) {
	dir := filepath.Dir(b.git.noHooks)
	p := filepath.Join(dir, mergeAttributesName)
	want := rowAttributesNoDriver + "\n"
	if cur, err := os.ReadFile(filepath.Clean(p)); err == nil && string(cur) == want {
		return filepath.ToSlash(p), nil
	}
	if err := os.MkdirAll(dir, cloneDirMode); err != nil {
		return "", errs.FileIO("create", dir, err)
	}
	if err := os.WriteFile(p, []byte(want), 0o600); err != nil {
		return "", errs.FileIO("write", p, err)
	}
	return filepath.ToSlash(p), nil
}
