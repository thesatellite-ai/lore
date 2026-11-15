package git

// layerland.go — landing a branch layer (BRANCH_LAYERS_SPEC 9) and
// promoting single rows of it, both in the store clone under the store
// lock:
//
//	LayerLand: fetch (store branch + layer refs; offline refuses) ─► the
//	  layer gone and a trash commit says landed? "already landed"
//	  ─► merge the remote store branch in (as lore sync does)
//	  ─► git merge --no-ff <layer tip>: ONE commit on the store branch's
//	     first-parent line whose second parent is the layer tip, so the
//	     roster authority replay (authority.go) sees every row's own
//	     author on the layer's commits (row files -merge: every row both
//	     sides changed goes to the Merger, resolveRow) ─► a conflict aborts,
//	     E_LAND_CONFLICT, nothing committed
//	  ─► ONE commit on the store branch ─► push (a lost race merges the
//	     remote and pushes again) ─► the layer moves to the trash (atomic
//	     push of the trash ref and the layer ref's deletion)
//	LayerPromote: the named rows of the layer onto the store branch now,
//	  then the layer commit that puts those files back to the base

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// LandRequest is one landing.
type LandRequest struct {
	Key branchlayer.Key
	// Subject is the landing commit's first line.
	Subject string
	// Trailers are added to the landing commit (Lore-Land-*).
	Trailers []string
	// DryRun lists what would land and changes nothing.
	DryRun bool
	// Kinds, when set, lands only rows of these tables; the others stay in
	// the layer, which is rebased onto the landing (BRANCH_LAYERS 9).
	Kinds []row.Table
	// Exclude are rows not to land (--review's drops): they go to the trash
	// with the layer, never into the shared notes.
	Exclude []ids.AnyID
	// Take settles a row both sides changed the same text of: true keeps
	// the branch's version, false the store branch's (--take).
	Take map[ids.AnyID]bool
}

// selected reports whether r lands under req.
func (req LandRequest) selected(r store.Row) bool {
	if slices.Contains(req.Exclude, r.ID) {
		return false
	}
	return len(req.Kinds) == 0 || slices.Contains(req.Kinds, r.Table)
}

// LandResult is what a landing did.
type LandResult struct {
	// Commit is the landing commit on the store branch ("" on a dry run).
	Commit string
	// Rows are the layer's rows that landed (or would).
	Rows []store.Row
	// AlreadyLanded is the landing commit a teammate's (or an earlier)
	// landing made; set when the layer was gone and its trash says landed.
	AlreadyLanded string
	// Trash is the trash ref the layer moved to.
	Trash string
	// Pushed is true when the store branch and the trash move reached the
	// remote.
	Pushed bool
	// Merged are rows both sides changed that the Merger settled.
	Merged []ids.AnyID
	// Kept are rows a --kinds landing left in the layer (rebased onto the
	// landing); Dropped the rows --review left out.
	Kept, Dropped []store.Row
	// Rebased are the stacked layers moved onto the landing (their parent
	// landed).
	Rebased []string
}

// Counts are the landed rows per table.
func (r LandResult) Counts() map[row.Table]int {
	m := map[row.Table]int{}
	for _, x := range r.Rows {
		m[x.Table]++
	}
	return m
}

// LayerLand squashes the layer of req.Key into the store branch and moves
// it to the trash (see the file comment). It always pushes: landing is an
// explicit act; offline it refuses before changing anything.
func (b *Backend) LayerLand(ctx context.Context, req LandRequest) (LandResult, error) {
	if err := b.layerUsable(ctx, req.Key); err != nil {
		return LandResult{}, err
	}
	if b.readOnly && !req.DryRun {
		return LandResult{}, store.ErrReadOnly
	}
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return LandResult{}, err
	}
	offline, err := b.LayerFetch(ctx)
	if err != nil {
		return LandResult{}, err
	}
	if offline && !req.DryRun {
		return LandResult{}, errs.New(errs.Offline, "the store's remote "+b.git.remote+" could not be reached; landing needs its newest notes and pushes at once, so nothing changed").
			WithHint("run lore branch land again when online")
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return LandResult{}, err
	}
	res, err := b.layerLandLocked(ctx, req)
	return res, releaseKeep(err, lock)
}

// layerLandLocked is LayerLand under the store lock.
func (b *Backend) layerLandLocked(ctx context.Context, req LandRequest) (LandResult, error) {
	var res LandResult
	l, err := b.LayerOpen(ctx, req.Key)
	if err != nil {
		return res, err
	}
	if !l.Exists() {
		landed, lerr := b.landedTrash(ctx, req.Key)
		if lerr != nil {
			return res, lerr
		}
		if landed != "" {
			res.AlreadyLanded = landed
			return res, nil
		}
		return res, errs.New(errs.NotFound, "no notes of "+req.Key.Branch+" in "+req.Key.Folder+": nothing to land").
			WithHint("lore branch list shows the open ones")
	}
	all, err := b.LayerRows(ctx, l)
	if err != nil {
		return res, err
	}
	var left []store.Row
	for _, r := range all {
		switch {
		case req.selected(r):
			res.Rows = append(res.Rows, r)
		case slices.Contains(req.Exclude, r.ID):
			res.Dropped = append(res.Dropped, r)
		default:
			left = append(left, r)
		}
	}
	res.Kept = left
	if req.DryRun {
		return res, nil
	}
	if len(res.Rows) == 0 {
		return res, errs.New(errs.NotFound, "none of the notes of "+req.Key.Branch+" are chosen to land").
			WithHint("lore branch show --branch " + req.Key.Branch + " lists them; drop --kinds or keep a row in --review")
	}
	tip := l.Tip
	if len(res.Rows) < len(all) {
		// land a filtered copy: the rows not chosen put back as the base has them
		if tip, err = b.filteredTip(ctx, l, append(append([]store.Row{}, left...), res.Dropped...)); err != nil {
			return res, err
		}
	}
	if err := b.recoverMerge(ctx); err != nil {
		return res, err
	}
	putBack, err := b.setAsideHandEdits(ctx)
	if err != nil {
		return res, err
	}
	commit, merged, err := b.squashLayer(ctx, tip, req)
	err = errors.Join(err, putBack())
	if err != nil {
		return res, err
	}
	res.Commit, res.Merged = commit, merged
	if err := b.pushLanding(ctx); err != nil {
		return res, err
	}
	res.Pushed = true
	if res.Rebased, err = b.rebaseChildren(ctx, l, commit); err != nil {
		return res, errs.Prefix("landed as "+short(commit)+" and pushed, but moving the layers stacked on it failed", err)
	}
	if len(left) > 0 {
		// --kinds: the rest stays open, rebased onto the landing
		if _, err := b.rebaseLayer(ctx, l, commit, left, l.Facts.Parent); err != nil {
			return res, errs.Prefix("landed as "+short(commit)+" and pushed, but keeping the other notes open failed", err)
		}
		return res, nil
	}
	trash, err := b.layerTrashLocked(ctx, req.Key, branchlayer.StateLanded, commit, true)
	if err != nil {
		return res, errs.Prefix("landed as "+short(commit)+" and pushed, but moving the layer to the trash failed", err).
			WithHint("run lore branch land --branch " + req.Key.Branch + " again: it finds the rows landed and only moves the layer")
	}
	res.Trash = trash
	return res, nil
}

// short is a commit's first 7 characters.
func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// LayerLanded is the landing commit the newest trash copy of key records
// ("" when that copy was dropped, not landed): restore brings that copy
// back, so --revert reverts its landing only.
func (b *Backend) LayerLanded(ctx context.Context, key branchlayer.Key) (string, error) {
	if err := b.layerUsable(ctx, key); err != nil {
		return "", err
	}
	refs, err := b.listRefs(ctx, key.TrashPrefixOf()+"@*", trackingOf(key.TrashPrefixOf())+"@*")
	if err != nil || len(refs) == 0 {
		return "", err
	}
	newest, newestAt := "", ""
	for _, ref := range refs {
		_, at, ok := branchlayer.ParseTrashRef(branchlayer.TrashPrefix, ref)
		if !ok {
			_, at, ok = branchlayer.ParseTrashRef(LayerTrashTrackingPrefix, ref)
		}
		stamp := at.Format(branchlayer.StampLayout) + "/" + ref[strings.LastIndex(ref, "@"):]
		if ok && stamp > newestAt {
			newest, newestAt = ref, stamp
		}
	}
	msg, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%B", newest}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: read the trash copy", err)
	}
	if f, ok := branchlayer.ParseFacts(string(msg)); ok && f.State == branchlayer.StateLanded {
		return f.Landed, nil
	}
	return "", nil
}

// landedTrash is the landing commit recorded by the newest trash commit of
// key that says landed ("" when none says so).
func (b *Backend) landedTrash(ctx context.Context, key branchlayer.Key) (string, error) {
	refs, err := b.listRefs(ctx, key.TrashPrefixOf()+"@*", trackingOf(key.TrashPrefixOf())+"@*")
	if err != nil {
		return "", err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(refs)))
	for _, ref := range refs {
		msg, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%B", ref}})
		if err != nil {
			continue
		}
		if f, ok := branchlayer.ParseFacts(string(msg)); ok && f.State == branchlayer.StateLanded {
			return f.Landed, nil
		}
	}
	return "", nil
}

// squashLayer merges the remote store branch in, then merges the layer
// tip into the worktree and commits once: a merge commit, so the store
// branch's first-parent history holds one landing commit while the
// layer's own commits (each by the person who wrote it) stay reachable
// for the authority replay (devco R-5, C35: a squash credited every row to
// whoever landed it, and a CI runner off the roster had every landed row
// held back).
func (b *Backend) squashLayer(ctx context.Context, tip string, req LandRequest) (string, []ids.AnyID, error) {
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return "", nil, err
	}
	if _, refused, _, merr := b.mergeRemote(ctx, remote); merr != nil {
		return "", nil, merr
	} else if len(refused) > 0 {
		return "", nil, errs.New(errs.Stale, "the store branch "+b.branch+" cannot take the remote's newest notes first: "+refused[0].Reason).
			WithHint("run lore sync and settle what it names, then land again")
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", nil, err
	}
	attrs, err := b.mergeAttributes()
	if err != nil {
		return "", nil, err
	}
	if err := b.markOwnMerge(ctx); err != nil {
		return "", nil, err
	}
	defer b.clearOwnMerge(ctx)
	_, err = b.git.run(ctx, call{dir: b.work, args: []string{"-c", "core.attributesFile=" + attrs, "merge", "--no-ff", "--no-commit", "--no-verify", "--quiet", "-s", "ort", "-X", "no-renames", tip}, env: env})
	if err != nil {
		if code, ran := exitCode(err); !ran || code != 1 {
			return "", nil, errors.Join(errs.Wrap(errs.Internal, "git: merge the layer", err), b.abortLanding(ctx))
		}
	}
	paths, err := b.unmerged(ctx)
	if err != nil {
		return "", nil, errors.Join(err, b.abortLanding(ctx))
	}
	var merged []ids.AnyID
	var conflicts []string
	for _, p := range paths {
		if id, taken, terr := b.takeSide(ctx, p, req.Take); terr != nil {
			return "", nil, errors.Join(terr, b.abortLanding(ctx))
		} else if taken {
			merged = append(merged, id)
			continue
		}
		id, refusal, rerr := b.resolveRow(ctx, p)
		if rerr != nil {
			return "", nil, errors.Join(rerr, b.abortLanding(ctx))
		}
		if refusal != nil {
			conflicts = append(conflicts, refusal.ID.String())
			continue
		}
		merged = append(merged, id)
	}
	if len(conflicts) > 0 {
		return "", nil, errors.Join(errs.New(errs.LandConflict, "the branch and "+b.branch+" both changed the same text of "+strings.Join(conflicts, ", ")+"; nothing was landed").
			WithHint("edit the row on one side so both agree (lore show --id <id> on the branch and on "+b.branch+"), then run lore branch land again"), b.abortLanding(ctx))
	}
	msg := b.cfg.withTrailers(req.Subject, req.Trailers...)
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "--allow-empty", "-m", msg}, env: env}); err != nil {
		return "", nil, errors.Join(errs.Wrap(errs.Internal, "git: commit the landing", err), b.abortLanding(ctx))
	}
	b.removeSquashMsg(ctx)
	h, err := b.head(ctx)
	return h, merged, err
}

// abortLanding undoes a landing merge that will not be committed: git
// merge --abort when the merge started (MERGE_HEAD), else the worktree and
// index back to HEAD.
func (b *Backend) abortLanding(ctx context.Context) error {
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--quiet", "--verify", "MERGE_HEAD"}}); err == nil {
		defer b.removeSquashMsg(ctx)
		if _, aerr := b.git.run(ctx, call{dir: b.work, args: []string{"merge", "--abort"}}); aerr != nil {
			return errs.Wrap(errs.Internal, "git: abort the landing merge", aerr)
		}
		return nil
	}
	return b.abortSquash(ctx)
}

// abortSquash puts the worktree and index back to HEAD after a squash
// that will not be committed (a squash sets no MERGE_HEAD, so git merge
// --abort cannot).
func (b *Backend) abortSquash(ctx context.Context) error {
	defer b.removeSquashMsg(ctx)
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"read-tree", "--reset", "-u", "HEAD"}}); err != nil {
		return errs.Wrap(errs.Internal, "git: undo the layer's squash", err)
	}
	return nil
}

// removeSquashMsg removes the SQUASH_MSG a squash merge leaves.
func (b *Backend) removeSquashMsg(ctx context.Context) {
	if gd := b.gitDirOf(ctx, b.work); gd != "" {
		_ = os.Remove(filepath.Join(gd, "SQUASH_MSG")) // absent is fine
	}
}

// pushLanding pushes the store branch; a lost race fetches, merges the
// remote and pushes again (MaxPushAttempts), as lore sync does.
func (b *Backend) pushLanding(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		h, err := b.head(ctx)
		if err != nil {
			return err
		}
		verdict, msg, err := b.push(ctx, h)
		if err != nil {
			return err
		}
		switch verdict {
		case pushApplied:
			return nil
		case pushOffline:
			return errs.New(errs.Offline, "the landing commit "+short(h)+" is made but the store's remote could not be reached ("+msg+"); the layer stays open").
				WithHint("run lore sync to push it, then lore branch land again to move the layer to the trash")
		case pushRefused:
			return errs.New(errs.Refused, "the store's remote refused the landing commit: "+msg).
				WithHint("the commit stays local; lore sync status shows it")
		case pushLostRace:
			if attempt+1 >= MaxPushAttempts {
				return errs.New(errs.Contended, "lost the push race "+strconv.Itoa(MaxPushAttempts)+" times; the landing commit is kept").WithHint("run lore branch land again")
			}
			if ferr := b.fetch(ctx, b.work); ferr != nil {
				return ferr
			}
			remote, rerr := b.resolve(ctx, b.work, trackingRefNS+b.branch)
			if rerr != nil {
				return rerr
			}
			if _, refused, _, merr := b.mergeRemote(ctx, remote); merr != nil {
				return merr
			} else if len(refused) > 0 {
				return errs.New(errs.Stale, "a teammate's newer notes conflict with the landing: "+refused[0].Reason).WithHint("run lore sync, settle it, then push")
			}
		}
	}
}

// PromoteResult is what LayerPromote did.
type PromoteResult struct {
	Rows   []store.Row
	Commit string // store branch commit
}

// LayerPromote writes the named rows of the layer onto the store branch now
// (one commit, through the folder's normal write, so the store branch's
// own rules apply) and puts those files back to the layer base on the
// layer, so they no longer differ there. Nothing is pushed: lore sync does.
func (b *Backend) LayerPromote(ctx context.Context, key branchlayer.Key, want []ids.AnyID) (PromoteResult, error) {
	var res PromoteResult
	l, err := b.LayerOpen(ctx, key)
	if err != nil {
		return res, err
	}
	if !l.Exists() {
		return res, errs.New(errs.NotFound, "no notes of "+key.Branch+" in "+key.Folder).WithHint("lore branch list shows the open ones")
	}
	rows, err := b.LayerRows(ctx, l)
	if err != nil {
		return res, err
	}
	var changes []store.Change
	for _, id := range want {
		i := slices.IndexFunc(rows, func(r store.Row) bool { return r.ID == id })
		if i < 0 {
			return res, errs.New(errs.NotFound, id.String()+" is not one of the notes of "+key.Branch).WithHint("lore branch show --branch " + key.Branch + " lists them")
		}
		r := rows[i]
		c := store.Change{Row: r}
		cur, gerr := b.Get(ctx, r.Table, r.ID)
		switch {
		case gerr == nil:
			c.Expect = cur.HLC
		case !errors.Is(gerr, store.ErrNotFound):
			return res, gerr
		}
		changes = append(changes, c)
		res.Rows = append(res.Rows, r)
	}
	if err := b.WriteBatch(ctx, Batch{Op: "branch.promote"}, changes); err != nil {
		return res, err
	}
	if res.Commit, err = b.head(ctx); err != nil {
		return res, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return res, err
	}
	err = b.resetLayerRows(ctx, l, res.Rows)
	return res, releaseKeep(err, lock)
}

// resetLayerRows makes one layer commit that puts the files of rows back
// as the layer base has them (removed when the base has none).
func (b *Backend) resetLayerRows(ctx context.Context, l Layer, rows []store.Row) error {
	puts := map[string][]byte{}
	var dels []string
	for _, r := range rows {
		at, err := b.LayerRowAt(ctx, l.Base, r.Table, r.ID)
		if err != nil {
			return err
		}
		p := b.folder.StorePath(r.Table, r.ID)
		if at == nil {
			dels = append(dels, p)
			continue
		}
		puts[p] = at.Doc
	}
	msg := "lore: branch.promote " + strconv.Itoa(len(rows)) + " rows to " + b.branch + " (" + l.Key.Folder + ")\n\n" + l.Facts.Trailers() + TrailerStoreBase + ": " + l.Base + "\n"
	commit, err := b.commitOnto(ctx, l.Tip, puts, dels, msg)
	if err != nil {
		return err
	}
	old := ""
	if l.Local {
		old = l.Tip
	}
	return b.casRef(ctx, l.Key.Ref(), commit, old)
}
