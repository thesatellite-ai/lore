package git

// layermore.go — the layer moves beyond a plain landing (BRANCH_LAYERS 9,
// 10, 12):
//
//	filteredTip     a copy of the layer tip with some rows put back as the
//	                base has them (land --kinds, --review drops)
//	rebaseLayer     the layer's rows moved onto a new base (a landing): one
//	                merge commit whose parents are the new base and the old
//	                tip, so every copy of the layer fast-forwards to it
//	rebaseChildren  after a layer lands, the layers stacked on it are
//	                rebased onto the landing and lose their parent
//	takeSide        --take: a row both sides changed is settled to one side
//	LayerLandInto   land a stacked layer into its parent layer
//	LayerRevertLanding  restore --revert: a commit that reverts a landing

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// filteredTip is a commit on l's tip that puts the files of rows back as
// l's base has them (removed when the base has none). It is on no ref: a
// landing squashes it in place of the tip.
func (b *Backend) filteredTip(ctx context.Context, l Layer, rows []store.Row) (string, error) {
	puts, dels, err := b.baseVersions(ctx, l.Base, rows)
	if err != nil {
		return "", err
	}
	return b.commitOnto(ctx, l.Tip, puts, dels, "lore: the notes of "+l.Key.Branch+" chosen to land\n")
}

// baseVersions are the files of rows as commit base has them: puts for
// rows it holds, dels for the others.
func (b *Backend) baseVersions(ctx context.Context, base string, rows []store.Row) (map[string][]byte, []string, error) {
	puts := map[string][]byte{}
	var dels []string
	for _, r := range rows {
		at, err := b.LayerRowAt(ctx, base, r.Table, r.ID)
		if err != nil {
			return nil, nil, err
		}
		p := b.folder.StorePath(r.Table, r.ID)
		if at == nil {
			dels = append(dels, p)
			continue
		}
		puts[p] = at.Doc
	}
	return puts, dels, nil
}

// rebaseLayer moves l's rows (keep) onto base: a merge commit of base's
// tree plus the rows, parents base and l's old tip, recording base as the
// layer base and parent as its parent layer ("" = none). The ref moves by
// compare-and-swap and the commit is pushed when the remote has the layer
// (a fast-forward: the old tip is a parent).
func (b *Backend) rebaseLayer(ctx context.Context, l Layer, base string, keep []store.Row, parent string) (string, error) {
	puts := make(map[string][]byte, len(keep))
	for _, r := range keep {
		puts[b.folder.StorePath(r.Table, r.ID)] = r.Doc
	}
	f := l.Facts
	f.Parent = parent
	msg := "lore: the notes of " + l.Key.Branch + " moved onto " + short(base) + " (" + l.Key.Folder + ")\n\n" + f.Trailers() + TrailerStoreBase + ": " + base + "\n"
	commit, err := b.commitOntoParents(ctx, base, []string{l.Tip}, puts, nil, msg)
	if err != nil {
		return "", err
	}
	old := ""
	if l.Local {
		old = l.Tip
	}
	if err := b.casRef(ctx, l.Key.Ref(), commit, old); err != nil {
		return "", err
	}
	if l.Remote != "" {
		if err := b.pushLayerRef(ctx, l.Key.Ref(), commit, l.Remote); err != nil {
			return "", err
		}
	}
	return commit, nil
}

// rebaseChildren rebases every open layer stacked on l (same store
// branch, folder and repo; Lore-Layer-Parent names l's branch) onto the
// landing commit, as layers of the shared notes. It returns their branches.
func (b *Backend) rebaseChildren(ctx context.Context, l Layer, landing string) ([]string, error) {
	prefix := branchlayer.RefPrefix + l.Key.StoreBranch + "/" + branchlayer.FolderSeg(l.Key.Folder) + "/" + l.Key.RepoKey + "/"
	refs, err := b.listRefs(ctx, prefix, trackingOf(prefix))
	if err != nil {
		return nil, err
	}
	var done []string
	for _, ref := range refs {
		k, ok := branchlayer.ParseRef(branchlayer.RefPrefix, ref)
		if !ok {
			k, ok = branchlayer.ParseRef(LayerTrackingPrefix, ref)
		}
		if !ok || k.Branch == l.Key.Branch || slices.Contains(done, k.Branch) {
			continue
		}
		child, err := b.LayerOpen(ctx, k)
		if err != nil {
			return done, err
		}
		if !child.Exists() || child.Facts.Parent != l.Key.Branch {
			continue
		}
		rows, err := b.LayerRows(ctx, child)
		if err != nil {
			return done, err
		}
		if _, err := b.rebaseLayer(ctx, child, landing, rows, ""); err != nil {
			return done, err
		}
		done = append(done, k.Branch)
	}
	return done, nil
}

// takeSide settles the conflicted path p by --take when its row is named
// in take: the branch's version (stage 3, the layer) or the store
// branch's (stage 2). taken is false when take does not name the row.
func (b *Backend) takeSide(ctx context.Context, p string, take map[ids.AnyID]bool) (ids.AnyID, bool, error) {
	if len(take) == 0 {
		return ids.AnyID{}, false, nil
	}
	_, rel, ok := rowPlace(p)
	if !ok {
		return ids.AnyID{}, false, nil
	}
	_, id, ok := rowfs.ParseRowPath(rel)
	if !ok {
		return ids.AnyID{}, false, nil
	}
	branch, named := take[id]
	if !named {
		return id, false, nil
	}
	blobs, err := b.readBlobs(ctx, []string{stageBase + p, stageOurs + p, stageTheirs + p})
	if err != nil {
		return id, false, err
	}
	chosen := stageOurs
	if branch {
		chosen = stageTheirs
	}
	doc, found := blobs[chosen+p]
	if !found {
		return id, false, errs.New(errs.Internal, "git: "+p+" has no side to take")
	}
	// devco V-14: --take settles only the clashing fields with the chosen
	// side; a field only the other side changed keeps that change
	if pm, can := b.cfg.merger.(PreferMerger); can && blobs[stageOurs+p] != nil && blobs[stageTheirs+p] != nil {
		if m, merr := pm.MergePrefer(blobs[stageBase+p], blobs[stageOurs+p], blobs[stageTheirs+p], branch); merr == nil {
			doc = m
		}
	}
	if err := b.root.WriteFile(p, doc); err != nil {
		return id, false, err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"add", "--", p}}); err != nil {
		return id, false, errs.Wrap(errs.Internal, "git: stage a settled row", err)
	}
	return id, true, nil
}

// LayerLandInto lands the stacked layer of key into the layer of its
// parent branch into (`lore branch land --into`, BRANCH_LAYERS 12): every
// row the stacked layer changed is merged into the parent layer row by row
// (the stacked layer's base is the parent tip it started from), one
// commit on the parent layer; the stacked layer moves to the trash. push
// sends both.
func (b *Backend) LayerLandInto(ctx context.Context, key branchlayer.Key, into string, push bool) (LandResult, error) {
	if err := b.layerUsable(ctx, key); err != nil {
		return LandResult{}, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return LandResult{}, err
	}
	res, err := b.layerLandIntoLocked(ctx, key, into, push)
	return res, releaseKeep(err, lock)
}

// layerLandIntoLocked is LayerLandInto under the store lock.
func (b *Backend) layerLandIntoLocked(ctx context.Context, key branchlayer.Key, into string, push bool) (LandResult, error) {
	var res LandResult
	child, err := b.LayerOpen(ctx, key)
	if err != nil {
		return res, err
	}
	pk := key
	pk.Branch = into
	parent, err := b.LayerOpen(ctx, pk)
	if err != nil {
		return res, err
	}
	switch {
	case !child.Exists():
		return res, errs.New(errs.NotFound, "no notes of "+key.Branch+" in "+key.Folder).WithHint("lore branch list shows the open ones")
	case !parent.Exists():
		return res, errs.New(errs.NotFound, "no notes of "+into+" in "+key.Folder+" to land into").
			WithHint("land " + key.Branch + " into the shared notes instead: lore branch land --branch " + key.Branch)
	}
	if res.Rows, err = b.LayerRows(ctx, child); err != nil {
		return res, err
	}
	puts := map[string][]byte{}
	var conflicts []string
	for _, r := range res.Rows {
		base, err := b.LayerRowAt(ctx, child.Base, r.Table, r.ID)
		if err != nil {
			return res, err
		}
		ours, err := b.LayerRowAt(ctx, parent.Tip, r.Table, r.ID)
		if err != nil {
			return res, err
		}
		p := b.folder.StorePath(r.Table, r.ID)
		switch {
		case ours == nil || (base != nil && string(ours.Doc) == string(base.Doc)):
			puts[p] = r.Doc // only the stacked branch changed it
		case string(ours.Doc) == string(r.Doc):
		default:
			var baseDoc []byte
			if base != nil {
				baseDoc = base.Doc
			}
			if b.cfg.merger == nil {
				conflicts = append(conflicts, r.ID.String())
				continue
			}
			m, conflict, merr := b.cfg.merger.Merge(baseDoc, ours.Doc, r.Doc)
			if merr != nil {
				return res, errs.Wrap(errs.Internal, "git: merge a row into "+into, merr)
			}
			if conflict {
				conflicts = append(conflicts, r.ID.String())
				continue
			}
			puts[p] = m
			res.Merged = append(res.Merged, r.ID)
		}
	}
	if len(conflicts) > 0 {
		return res, errs.New(errs.LandConflict, key.Branch+" and "+into+" both changed the same text of "+strings.Join(conflicts, ", ")+"; nothing was landed").
			WithHint("edit the row on one branch so both agree, then run lore branch land --branch " + key.Branch + " --into " + into + " again")
	}
	msg := "lore: land " + key.Branch + " into " + into + " (" + key.Folder + ")\n\n" + parent.Facts.Trailers() + TrailerStoreBase + ": " + parent.Base + "\n"
	commit, err := b.commitOnto(ctx, parent.Tip, puts, nil, msg)
	if err != nil {
		return res, err
	}
	old := ""
	if parent.Local {
		old = parent.Tip
	}
	if err := b.casRef(ctx, pk.Ref(), commit, old); err != nil {
		return res, err
	}
	if push {
		if err := b.pushLayerRef(ctx, pk.Ref(), commit, parent.Remote); err != nil {
			return res, err
		}
	}
	res.Commit = commit
	if res.Trash, err = b.layerTrashLocked(ctx, key, branchlayer.StateLanded, commit, push); err != nil {
		return res, errs.Prefix("landed into "+into+" as "+short(commit)+", but moving "+key.Branch+"'s notes to the trash failed", err)
	}
	res.Pushed = push
	return res, nil
}

// LayerRevertLanding adds one commit on the store branch that reverts the
// landing commit landing (restore --revert, BRANCH_LAYERS 9), then pushes
// it. A landing whose rows changed again since is refused with
// E_LAND_CONFLICT and nothing changes.
func (b *Backend) LayerRevertLanding(ctx context.Context, landing, branch string) (string, error) {
	if err := b.usable(ctx); err != nil {
		return "", err
	}
	if b.mode != modeClone {
		return "", errs.New(errs.Unsupported, "git: branch layers live in a store clone")
	}
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return "", err
	}
	if offline, ferr := b.LayerFetch(ctx); ferr != nil {
		return "", ferr
	} else if offline {
		return "", errs.New(errs.Offline, "the store's remote "+b.git.remote+" could not be reached; nothing was reverted").WithHint("run it again when online")
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return "", err
	}
	commit, err := b.revertLocked(ctx, landing, branch)
	return commit, releaseKeep(err, lock)
}

// revertLocked is LayerRevertLanding under the store lock.
func (b *Backend) revertLocked(ctx context.Context, landing, branch string) (string, error) {
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return "", err
	}
	if _, refused, _, merr := b.mergeRemote(ctx, remote); merr != nil {
		return "", merr
	} else if len(refused) > 0 {
		return "", errs.New(errs.Stale, "the store branch cannot take the remote's newest notes first: "+refused[0].Reason).WithHint("run lore sync, then again")
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	putBack, err := b.setAsideHandEdits(ctx)
	if err != nil {
		return "", err
	}
	_, rerr := b.git.run(ctx, call{dir: b.work, args: []string{"revert", "--no-edit", "--no-commit", "-m", "1", landing}, env: env})
	if rerr != nil {
		_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"revert", "--abort"}})
		_ = b.abortSquash(ctx)
		return "", errors.Join(errs.New(errs.LandConflict, "the notes landed by "+short(landing)+" changed again since, so the landing cannot be reverted as a whole; nothing changed").
			WithHint("edit those rows by hand, or restore without --revert"), putBack())
	}
	if _, derr := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--cached", "--quiet"}}); derr == nil {
		_ = b.abortSquash(ctx)
		return "", errors.Join(errs.New(errs.InvalidInput, "the landing "+short(landing)+" of "+branch+" is already reverted; nothing changed").
			WithHint("restore without --revert"), putBack())
	}
	msg := b.cfg.withTrailers(ctx, "lore: revert the landing of "+branch+" ("+short(landing)+")", "Lore-Land-Revert: "+landing)
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", msg}, env: env}); err != nil {
		return "", errors.Join(errs.Wrap(errs.Internal, "git: commit the revert", err), b.abortSquash(ctx), putBack())
	}
	if err := putBack(); err != nil {
		return "", err
	}
	if err := b.pushLanding(ctx); err != nil {
		return "", err
	}
	return b.head(ctx)
}

// LayerRebaseOnto moves the open layer of key onto the store branch's
// current HEAD (its rows kept; no-op when there is no layer): branch adopt
// needs the layer's base to be the commit that trashed the adopted rows.
func (b *Backend) LayerRebaseOnto(ctx context.Context, key branchlayer.Key) error {
	if err := b.layerUsable(ctx, key); err != nil {
		return err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return err
	}
	err = b.rebaseOntoHeadLocked(ctx, key)
	return releaseKeep(err, lock)
}

// rebaseOntoHeadLocked is LayerRebaseOnto under the store lock.
func (b *Backend) rebaseOntoHeadLocked(ctx context.Context, key branchlayer.Key) error {
	l, err := b.LayerOpen(ctx, key)
	if err != nil || !l.Exists() {
		return err
	}
	h, err := b.head(ctx)
	if err != nil {
		return err
	}
	rows, err := b.LayerRows(ctx, l)
	if err != nil {
		return err
	}
	_, err = b.rebaseLayer(ctx, l, h, rows, l.Facts.Parent)
	return err
}
