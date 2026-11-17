package git

// layer.go — branch layers in a store clone (BRANCH_LAYERS_SPEC 6, 7, 10):
// one hidden ref per layer, refs/lore/layer/<store branch>/<folder>/<repo
// key>/<branch key> (logic/branchlayer names it). A layer commit's tree is
// the whole store tree, its first-ever parent the store branch commit the
// layer started from (the layer base), and its message carries the
// layer's facts as trailers. Nothing here touches the clone's worktree or
// checked-out branch: commits are built in a private index and the ref
// moves by compare-and-swap. Landing (layerland.go) is the one step that
// merges into the store branch.
//
//	LayerOpen(key)        local ref, else its fetched copy ─► Layer{Tip, Base, Facts}
//	LayerRows(layer)      git diff base..tip over the folder ─► the rows the layer changed
//	LayerWrite(key, …)    raise _meta.json (first layer of the folder, one
//	                      commit on the store branch), then one commit on the layer
//	LayerList(prefix)     open layers (local and fetched), trashed layers
//	LayerTrash / LayerRestore / LayerPurge
//	LayerPush             sync: push layers and trash moves with a lease

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Tracking namespaces of the layer refs inside a store clone: a fetch
// copies the remote's refs/lore/layer/* and refs/lore/layer-trash/* there.
const (
	LayerTrackingPrefix      = "refs/lore-remote/" + RemoteName + "/layer/"
	LayerTrashTrackingPrefix = "refs/lore-remote/" + RemoteName + "/layer-trash/"
	layerRefspec             = "+" + branchlayer.RefPrefix + "*:" + LayerTrackingPrefix + "*"
	layerTrashRefspec        = "+" + branchlayer.TrashPrefix + "*:" + LayerTrashTrackingPrefix + "*"
	// TrailerStoreBase records the layer base (the store commit the layer
	// started from) on every layer commit: a compacted store branch no
	// longer holds it as an ancestor, the layer's own chain still does.
	TrailerStoreBase = "Lore-Layer-Store-Base"
)

// Layer is one layer as the clone sees it.
type Layer struct {
	Key branchlayer.Key
	// Tip is the layer's newest commit ("" when there is no layer).
	Tip string
	// Local is true when the clone holds the layer ref itself (not only a
	// teammate's fetched copy).
	Local bool
	// Remote is the fetched copy's commit ("" when the remote has none).
	Remote string
	// Base is the store commit the layer started from.
	Base  string
	Facts branchlayer.Facts
}

// Exists reports whether there is a layer.
func (l Layer) Exists() bool { return l.Tip != "" }

// localTrashName is a trash ref's own name, whether ref is it or its
// fetched copy.
func localTrashName(ref string) string {
	if rest, ok := strings.CutPrefix(ref, LayerTrashTrackingPrefix); ok {
		return branchlayer.TrashPrefix + rest
	}
	return ref
}

// trackingOf is the fetched copy of a refs/lore/layer… ref.
func trackingOf(ref string) string {
	if rest, ok := strings.CutPrefix(ref, branchlayer.TrashPrefix); ok {
		return LayerTrashTrackingPrefix + rest
	}
	return LayerTrackingPrefix + strings.TrimPrefix(ref, branchlayer.RefPrefix)
}

// LayerOpen reads the layer of key: the local ref when the clone has one,
// else the fetched copy (a teammate's layer this clone never wrote).
func (b *Backend) LayerOpen(ctx context.Context, key branchlayer.Key) (Layer, error) {
	if err := b.layerUsable(ctx, key); err != nil {
		return Layer{}, err
	}
	l := Layer{Key: key}
	local, err := b.resolve(ctx, b.work, key.Ref())
	if err != nil {
		return Layer{}, err
	}
	if l.Remote, err = b.resolve(ctx, b.work, trackingOf(key.Ref())); err != nil {
		return Layer{}, err
	}
	l.Tip, l.Local = local, local != ""
	if l.Tip == "" {
		l.Tip = l.Remote
	}
	if l.Tip == "" {
		return l, nil
	}
	return b.fillFacts(ctx, l)
}

// fillFacts reads the tip's trailers (facts and base).
func (b *Backend) fillFacts(ctx context.Context, l Layer) (Layer, error) {
	msg, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%B", l.Tip}})
	if err != nil {
		return Layer{}, errs.Wrap(errs.Internal, "git: read the layer commit", err)
	}
	l.Facts, _ = branchlayer.ParseFacts(string(msg))
	l.Base = trailerValue(string(msg), TrailerStoreBase)
	if l.Base == "" {
		return Layer{}, errs.New(errs.Internal, "git: the layer "+l.Key.Ref()+" names no base commit").
			WithHint("the ref was not written by lore; remove it with git update-ref -d " + l.Key.Ref() + " in " + b.work)
	}
	return l, nil
}

// trailerValue is the value of the last "key: value" line of msg.
func trailerValue(msg, key string) string {
	v := ""
	for line := range strings.SplitSeq(msg, "\n") {
		if k, val, ok := strings.Cut(line, ": "); ok && k == key {
			v = strings.TrimSpace(val)
		}
	}
	return v
}

// layerUsable checks the backend is a usable clone and key is complete.
func (b *Backend) layerUsable(ctx context.Context, key branchlayer.Key) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.mode != modeClone {
		return errs.New(errs.Unsupported, "git: branch layers live in a store clone, not in the code repo")
	}
	if err := key.Check(); err != nil {
		return errs.Wrap(errs.InvalidInput, "git: layer key", err)
	}
	return nil
}

// LayerRows are the rows of this backend's folder the layer changed since
// its base (added or edited files at the tip; tombstones included).
func (b *Backend) LayerRows(ctx context.Context, l Layer) ([]store.Row, error) {
	if !l.Exists() {
		return nil, nil
	}
	paths, err := b.changedPaths(ctx, l.Base, l.Tip, b.folder.Pathspecs())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(paths))
	rels := make(map[string]string, len(paths))
	for _, p := range paths {
		if rel, ok := b.folder.FileRel(p); ok {
			names = append(names, l.Tip+":"+p)
			rels[l.Tip+":"+p] = rel
		}
	}
	blobs, err := b.readBlobs(ctx, names)
	if err != nil {
		return nil, err
	}
	rows := make([]store.Row, 0, len(blobs))
	for _, n := range names {
		doc, ok := blobs[n]
		if !ok {
			continue
		}
		r, derr := rowfs.DecodeRowFile(rels[n], doc)
		if derr != nil {
			continue // a broken file in a layer is reported by lore doctor, as on the store branch
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// LayerWriteRequest is one write onto a layer.
type LayerWriteRequest struct {
	Key branchlayer.Key
	// Facts are recorded on the commit (Repo, Branch, BaseBranch, Split,
	// CodeHead; Started and By on the first write).
	Facts   branchlayer.Facts
	Changes []store.Change
	// Op names the commit ("task.add").
	Op string
}

// LayerWriteResult says what a layer write did.
type LayerWriteResult struct {
	Layer Layer
	// Created is true when this write started the layer.
	Created bool
	// Declared is the store commit that added branch_layers to the
	// folder's _meta.json ("" when it was declared already).
	Declared string
}

// LayerWrite commits changes onto the layer of req.Key under the store
// lock: the first layer of a folder first declares the format feature
// branch_layers on the store branch (one commit, BRANCH_LAYERS 15); a
// missing layer starts at the store branch tip (or a teammate's fetched
// copy). Expect is checked against the layer's view (the layer's row,
// else the store branch's).
func (b *Backend) LayerWrite(ctx context.Context, req LayerWriteRequest) (LayerWriteResult, error) {
	if err := b.layerUsable(ctx, req.Key); err != nil {
		return LayerWriteResult{}, err
	}
	if b.readOnly {
		return LayerWriteResult{}, store.ErrReadOnly
	}
	if len(req.Changes) == 0 {
		return LayerWriteResult{}, nil
	}
	if err := b.refusePersonGitOp(ctx, b.work); err != nil {
		return LayerWriteResult{}, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return LayerWriteResult{}, err
	}
	res, err := b.layerWriteLocked(ctx, req)
	return res, releaseKeep(err, lock)
}

// layerWriteLocked is LayerWrite under the store lock.
func (b *Backend) layerWriteLocked(ctx context.Context, req LayerWriteRequest) (LayerWriteResult, error) {
	var res LayerWriteResult
	l, err := b.LayerOpen(ctx, req.Key)
	if err != nil {
		return res, err
	}
	if !l.Exists() {
		if res.Declared, err = b.declareLayers(ctx); err != nil {
			return res, err
		}
		h, herr := b.head(ctx)
		if herr != nil {
			return res, herr
		}
		if h == "" {
			return res, errs.New(errs.Refused, "the store "+b.git.remote+" has no commit on "+b.branch+" yet, so a branch layer has nothing to start from").
				WithHint("write once on the default branch (or run lore sync), then write on the branch again")
		}
		l.Base, res.Created = h, true
		req.Facts.Started = b.cfg.now()
		if req.Facts.Parent != "" {
			// stacked: the layer starts from its parent layer's tip, so it
			// holds the parent's notes as they were (BRANCH_LAYERS 12)
			pk := req.Key
			pk.Branch = req.Facts.Parent
			pl, perr := b.LayerOpen(ctx, pk)
			if perr != nil {
				return res, perr
			}
			if pl.Exists() {
				l.Base = pl.Tip
			} else {
				req.Facts.Parent = ""
			}
		}
	} else {
		// keep the facts the layer began with; the write adds its code head
		req.Facts.Parent = l.Facts.Parent
		started, by, split, baseBranch := l.Facts.Started, l.Facts.By, l.Facts.Split, l.Facts.BaseBranch
		if !started.IsZero() {
			req.Facts.Started = started
		}
		if by != "" {
			req.Facts.By = by
		}
		if split != "" {
			req.Facts.Split = split
		}
		if baseBranch != "" {
			req.Facts.BaseBranch = baseBranch
		}
	}
	if err := b.checkLayerExpect(ctx, l, req.Facts.Parent, req.Changes); err != nil {
		return res, err
	}
	puts := make(map[string][]byte, len(req.Changes))
	for _, c := range req.Changes {
		p := b.folder.StorePath(c.Row.Table, c.Row.ID)
		if _, dup := puts[p]; dup {
			return res, errs.Wrap(errs.InvalidInput, "git: layer write", store.ErrBatch)
		}
		puts[p] = c.Row.Doc
	}
	parent := l.Tip
	if parent == "" {
		parent = l.Base
	}
	req.Facts.Folder = req.Key.Folder
	req.Facts.Branch = req.Key.Branch
	msg := layerSubject(req.Op, req.Changes, req.Key) + "\n\n" + req.Facts.Trailers() + TrailerStoreBase + ": " + l.Base + "\n"
	commit, err := b.commitOnto(ctx, parent, puts, nil, msg)
	if err != nil {
		return res, err
	}
	old := ""
	if l.Local {
		old = l.Tip
	}
	if err := b.casRef(ctx, req.Key.Ref(), commit, old); err != nil {
		return res, err
	}
	l.Tip, l.Local, l.Facts = commit, true, req.Facts
	res.Layer = l
	return res, nil
}

// layerSubject is a layer commit's subject: "lore: task.add tsk_… on
// feat/x (app)" for one row, a count for more.
func layerSubject(op string, changes []store.Change, key branchlayer.Key) string {
	if op == "" {
		op = DefaultOp
	}
	what := ""
	if len(changes) == 1 {
		what = changes[0].Row.ID.String()
	} else {
		what = strconv.Itoa(len(changes)) + " rows"
	}
	return "lore: " + op + " " + what + " on " + key.Branch + " (" + key.Folder + ")"
}

// checkLayerExpect is the lost-update guard of a layer write: every
// change's Expect must equal the HLC of the row the layer view holds now
// (the layer's copy, else the store branch's; zero when neither has it).
func (b *Backend) checkLayerExpect(ctx context.Context, l Layer, parent string, changes []store.Change) error {
	layerRows := map[store.Key]store.Row{}
	if parent != "" { // a stacked layer sees its parent's notes under its own
		pk := l.Key
		pk.Branch = parent
		pl, err := b.LayerOpen(ctx, pk)
		if err != nil {
			return err
		}
		rows, err := b.LayerRows(ctx, pl)
		if err != nil {
			return err
		}
		for _, r := range rows {
			layerRows[r.Key()] = r
		}
	}
	if l.Exists() {
		rows, err := b.LayerRows(ctx, l)
		if err != nil {
			return err
		}
		for _, r := range rows {
			layerRows[r.Key()] = r
		}
	}
	for _, c := range changes {
		cur, ok := layerRows[c.Row.Key()]
		if !ok {
			r, err := b.Get(ctx, c.Row.Table, c.Row.ID)
			switch {
			case err == nil:
				cur, ok = r, true
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		var have hlc.HLC
		if ok {
			have = cur.HLC
		}
		if have != c.Expect {
			return errs.Wrap(errs.Stale, "git: "+c.Row.ID.String()+" changed on this branch since it was read", store.ErrStale)
		}
	}
	return nil
}

// declareLayers adds branch_layers to the folder's _meta.json on the store
// branch (one commit) unless it is there; it returns that commit.
func (b *Backend) declareLayers(ctx context.Context) (string, error) {
	d, err := b.folder.Format(ctx)
	if err != nil {
		return "", err
	}
	if slices.Contains(d.Features, formatgate.BranchLayers) {
		return "", nil
	}
	rel, err := b.folder.RaiseFeatures([]string{formatgate.BranchLayers})
	if err != nil || rel == "" {
		return "", err
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"add", "--", rel}}); err != nil {
		return "", errs.Wrap(errs.Internal, "git: stage the folder's format declaration", err)
	}
	// the floor named is the feature's own release, so the commit and the
	// upgrade line a teammate gets never disagree (devco:V-1)
	since := "0.1.18"
	if f, ok := formatgate.Lookup(formatgate.BranchLayers); ok {
		since = f.Since
	}
	msg := "lore: " + b.folder.Name() + " uses branch layers (needs lore " + since + " or later)"
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "-m", b.cfg.withTrailers(msg), "--", rel}, env: env}); err != nil {
		return "", errs.Wrap(errs.Internal, "git: commit the folder's format declaration", err)
	}
	return b.head(ctx)
}

// commitOnto builds a commit on parent (its tree plus puts, minus dels)
// in a private index, never touching the worktree, and returns it.
func (b *Backend) commitOnto(ctx context.Context, parent string, puts map[string][]byte, dels []string, msg string) (string, error) {
	return b.commitOntoParents(ctx, parent, nil, puts, dels, msg)
}

// commitOntoParents is commitOnto with extra parents after parent (a merge
// commit: a rebased layer keeps its old tip as a parent, so every copy of
// it fast-forwards).
func (b *Backend) commitOntoParents(ctx context.Context, parent string, extra []string, puts map[string][]byte, dels []string, msg string) (string, error) {
	gd := b.gitDirOf(ctx, b.work)
	if gd == "" {
		return "", errs.New(errs.Internal, "git: cannot find the git folder of "+b.work)
	}
	idx, err := os.CreateTemp(gd, "lore-layer-index-*")
	if err != nil {
		return "", errs.FileIO("create a temporary index in", gd, err)
	}
	idxPath := idx.Name()
	_ = idx.Close()
	_ = os.Remove(idxPath) // git read-tree makes it; an empty file is no index
	defer func() { _ = os.Remove(idxPath) }()
	env := []string{"GIT_INDEX_FILE=" + idxPath}
	run := func(stdin []byte, args ...string) (string, error) {
		out, rerr := b.git.run(ctx, call{dir: b.work, args: args, env: env, stdin: stdin})
		return trimmed(out), rerr
	}
	if _, err := run(nil, "read-tree", parent); err != nil {
		return "", errs.Wrap(errs.Internal, "git: read the layer tree", err)
	}
	paths := make([]string, 0, len(puts))
	for p := range puts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var info strings.Builder
	for _, p := range paths {
		oid, err := run(puts[p], "hash-object", "-w", "--stdin")
		if err != nil {
			return "", errs.Wrap(errs.Internal, "git: store a row file", err)
		}
		info.WriteString("100644 " + oid + "\t" + p + "\n")
	}
	for _, p := range dels {
		info.WriteString("0 " + strings.Repeat("0", len(parent)) + "\t" + p + "\n")
	}
	if info.Len() > 0 {
		if _, err := run([]byte(info.String()), "update-index", "--index-info"); err != nil {
			return "", errs.Wrap(errs.Internal, "git: stage the layer's rows", err)
		}
	}
	tree, err := run(nil, "write-tree")
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: write the layer tree", err)
	}
	cenv, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-p", parent}
	for _, p := range extra {
		args = append(args, "-p", p)
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: append(args, "-F", "-"), env: cenv, stdin: []byte(msg)})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: commit onto the layer", err)
	}
	return trimmed(out), nil
}

// casRef moves ref from old ("" = must not exist) to oid.
func (b *Backend) casRef(ctx context.Context, ref, oid, old string) error {
	if old == "" {
		old = zeroOIDOf(oid)
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", ref, oid, old}}); err != nil {
		return errs.Wrap(errs.Contended, "git: "+ref+" moved while lore wrote it", err).WithHint("run the command again")
	}
	return nil
}

// deleteRef removes ref when it points at old ("" = whatever it holds).
func (b *Backend) deleteRef(ctx context.Context, ref, old string) error {
	args := []string{"update-ref", "-d", ref}
	if old != "" {
		args = append(args, old)
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: args}); err != nil {
		return errs.Wrap(errs.Internal, "git: remove "+ref, err)
	}
	return nil
}

// zeroOIDOf is git's all-zero object name of the same length as oid.
func zeroOIDOf(oid string) string { return strings.Repeat("0", len(oid)) }

// LayerEntry is one layer of a listing.
type LayerEntry struct {
	Layer Layer
	// Trashed is the trash stamp of a dropped or landed layer (zero for an
	// open one); Trash is its ref.
	Trashed time.Time
	Trash   string
	// Counts is the rows the layer changed, per table.
	Counts map[row.Table]int
}

// LayerList lists the layers of this backend's folder on its store branch
// (repoKey "" = every repo), open ones first (local and fetched), then the
// trash, each with its row counts.
func (b *Backend) LayerList(ctx context.Context, repoKey string, trash bool) ([]LayerEntry, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	if b.mode != modeClone {
		return nil, nil
	}
	prefix := b.branch + "/" + branchlayer.FolderSeg(b.folder.Name()) + "/"
	if repoKey != "" {
		prefix += repoKey + "/"
	}
	refs, err := b.listRefs(ctx, branchlayer.RefPrefix+prefix, LayerTrackingPrefix+prefix)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []LayerEntry
	for _, ref := range refs {
		k, ok := branchlayer.ParseRef(branchlayer.RefPrefix, ref)
		if !ok {
			k, ok = branchlayer.ParseRef(LayerTrackingPrefix, ref)
		}
		if !ok || seen[k.Ref()] {
			continue
		}
		seen[k.Ref()] = true
		l, err := b.LayerOpen(ctx, k)
		if err != nil {
			return nil, err
		}
		e, err := b.layerEntry(ctx, l)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if !trash {
		return out, nil
	}
	trashRefs, err := b.listRefs(ctx, branchlayer.TrashPrefix+prefix, LayerTrashTrackingPrefix+prefix)
	if err != nil {
		return nil, err
	}
	for _, ref := range trashRefs {
		k, at, ok := branchlayer.ParseTrashRef(branchlayer.TrashPrefix, ref)
		if !ok {
			k, at, ok = branchlayer.ParseTrashRef(LayerTrashTrackingPrefix, ref)
		}
		name := localTrashName(ref)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		tip, err := b.resolve(ctx, b.work, ref)
		if err != nil {
			return nil, err
		}
		l, err := b.fillFacts(ctx, Layer{Key: k, Tip: tip})
		if err != nil {
			return nil, err
		}
		e, err := b.layerEntry(ctx, l)
		if err != nil {
			return nil, err
		}
		e.Trashed, e.Trash = at, name
		out = append(out, e)
	}
	return out, nil
}

// layerEntry counts a layer's rows per table.
func (b *Backend) layerEntry(ctx context.Context, l Layer) (LayerEntry, error) {
	rows, err := b.LayerRows(ctx, l)
	if err != nil {
		return LayerEntry{}, err
	}
	e := LayerEntry{Layer: l, Counts: map[row.Table]int{}}
	for _, r := range rows {
		e.Counts[r.Table]++
	}
	return e, nil
}

// listRefs lists the refs under the given prefixes, sorted.
func (b *Backend) listRefs(ctx context.Context, prefixes ...string) ([]string, error) {
	args := append([]string{"for-each-ref", "--format=%(refname)"}, prefixes...)
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: list the layer refs", err)
	}
	var refs []string
	for r := range strings.SplitSeq(trimmed(out), "\n") {
		if r != "" {
			refs = append(refs, r)
		}
	}
	sort.Strings(refs)
	return refs, nil
}

// LayerTrash moves the layer of key to the trash: an empty commit on its
// tip records the state (and the landing commit), the trash ref points at
// it and the layer ref goes. push also sends both moves to the remote in
// one atomic push (drop and land are explicit acts); a remote that never
// had the layer only gets the trash ref.
func (b *Backend) LayerTrash(ctx context.Context, key branchlayer.Key, state branchlayer.State, landed string, push bool) (string, error) {
	if err := b.layerUsable(ctx, key); err != nil {
		return "", err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return "", err
	}
	ref, err := b.layerTrashLocked(ctx, key, state, landed, push)
	return ref, releaseKeep(err, lock)
}

// layerTrashLocked is LayerTrash under the store lock.
func (b *Backend) layerTrashLocked(ctx context.Context, key branchlayer.Key, state branchlayer.State, landed string, push bool) (string, error) {
	l, err := b.LayerOpen(ctx, key)
	if err != nil {
		return "", err
	}
	if !l.Exists() {
		return "", errs.New(errs.NotFound, "no branch notes for "+key.Branch+" in "+key.Folder).WithHint("lore branch list shows the open ones")
	}
	f := l.Facts
	f.State, f.Landed = state, landed
	msg := "lore: " + string(state) + " the notes of " + key.Branch + " (" + key.Folder + ")\n\n" + f.Trailers() + TrailerStoreBase + ": " + l.Base + "\n"
	commit, err := b.commitOnto(ctx, l.Tip, nil, nil, msg)
	if err != nil {
		return "", err
	}
	// commitOnto made no empty commit when nothing changed: force one
	if commit == l.Tip || commit == "" {
		return "", errs.New(errs.Internal, "git: could not record the trash commit")
	}
	at := b.cfg.now()
	trashRef := ""
	for n := 1; ; n++ {
		cand := key.TrashRefN(at, n)
		have, rerr := b.resolve(ctx, b.work, cand)
		if rerr != nil {
			return "", rerr
		}
		if remote, _ := b.resolve(ctx, b.work, trackingOf(cand)); have == "" && remote == "" {
			trashRef = cand
			break
		}
	}
	if err := b.casRef(ctx, trashRef, commit, ""); err != nil {
		return "", err
	}
	if push {
		if err := b.pushTrashMove(ctx, key.Ref(), l.Remote, trashRef, commit); err != nil {
			_ = b.deleteRef(ctx, trashRef, commit)
			return "", err
		}
	}
	if l.Local {
		if err := b.deleteRef(ctx, key.Ref(), l.Tip); err != nil {
			return "", err
		}
	}
	if l.Remote != "" && push {
		_ = b.deleteRef(ctx, trackingOf(key.Ref()), "")
	}
	return trashRef, nil
}

// pushTrashMove sends the trash ref and, when the remote holds the layer,
// its deletion in one atomic push with a lease on the layer's remote tip.
func (b *Backend) pushTrashMove(ctx context.Context, layerRef, remoteTip, trashRef, commit string) error {
	args := []string{"push", "--porcelain", "--no-verify", "--atomic"}
	specs := []string{commit + ":" + trashRef}
	if remoteTip != "" {
		args = append(args, "--force-with-lease="+layerRef+":"+remoteTip)
		specs = append(specs, ":"+layerRef)
	}
	args = append(append(args, "--", RemoteName), specs...)
	out, err := b.git.run(ctx, call{dir: b.work, args: args})
	if err == nil {
		b.pushed()
		_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingOf(trashRef), commit}})
		return nil
	}
	if _, ran := exitCode(err); !ran {
		return err
	}
	if _, _, found := porcelainVerdict(string(out), trashRef); !found {
		return errs.Wrap(errs.Offline, "git: push the layer's move to the trash", errors.Join(errOffline, err)).
			WithHint("nothing changed; run it again when the store's remote answers")
	}
	return errs.Wrap(errs.Contended, "git: the remote refused moving the layer to the trash (a teammate wrote to it meanwhile?)", err).
		WithHint("run lore sync, then run the command again")
}

// LayerRestore brings the newest trashed copy of key (or the one stamped
// at, when not zero) back as the open layer; push sends it.
func (b *Backend) LayerRestore(ctx context.Context, key branchlayer.Key, at time.Time, push bool) (Layer, error) {
	if err := b.layerUsable(ctx, key); err != nil {
		return Layer{}, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return Layer{}, err
	}
	l, err := b.layerRestoreLocked(ctx, key, at, push)
	return l, releaseKeep(err, lock)
}

// layerRestoreLocked is LayerRestore under the store lock.
func (b *Backend) layerRestoreLocked(ctx context.Context, key branchlayer.Key, at time.Time, push bool) (Layer, error) {
	cur, err := b.LayerOpen(ctx, key)
	if err != nil {
		return Layer{}, err
	}
	if cur.Exists() {
		return Layer{}, errs.New(errs.NameTaken, key.Branch+" already has open notes in "+key.Folder).
			WithHint("land or drop them first (lore branch land|drop --branch " + key.Branch + ")")
	}
	refs, err := b.listRefs(ctx, key.TrashPrefixOf()+"@*", trackingOf(key.TrashPrefixOf())+"@*")
	if err != nil {
		return Layer{}, err
	}
	pick, pickAt := "", time.Time{}
	for _, ref := range refs {
		_, rat, ok := branchlayer.ParseTrashRef(branchlayer.TrashPrefix, ref)
		if !ok {
			_, rat, ok = branchlayer.ParseTrashRef(LayerTrashTrackingPrefix, ref)
		}
		if !ok || (!at.IsZero() && !rat.Equal(at)) {
			continue
		}
		if pick == "" || rat.After(pickAt) {
			pick, pickAt = ref, rat
		}
	}
	if pick == "" {
		return Layer{}, errs.New(errs.NotFound, "no trashed notes of "+key.Branch+" in "+key.Folder).WithHint("lore branch list --trash shows the trash")
	}
	trashTip, err := b.resolve(ctx, b.work, pick)
	if err != nil {
		return Layer{}, err
	}
	// the open layer continues from the commit before the trash commit
	tip, err := b.resolve(ctx, b.work, trashTip+"^")
	if err != nil || tip == "" {
		return Layer{}, errs.Wrap(errs.Internal, "git: read the trashed layer", err)
	}
	// devco V-8: one commit on top says a person brought the notes back,
	// so lore sync keeps them (their tip time and the mark are new) instead
	// of moving them to the trash again on the next sync
	restored, err := b.fillFacts(ctx, Layer{Key: key, Tip: tip})
	if err != nil {
		return Layer{}, err
	}
	f := restored.Facts
	f.State, f.Landed, f.Restored = "", "", b.cfg.now()
	msg := "lore: restore the notes of " + key.Branch + " (" + key.Folder + ")\n\n" + f.Trailers() + TrailerStoreBase + ": " + restored.Base + "\n"
	if tip, err = b.commitOnto(ctx, tip, nil, nil, msg); err != nil {
		return Layer{}, err
	}
	if err := b.casRef(ctx, key.Ref(), tip, ""); err != nil {
		return Layer{}, err
	}
	if push {
		if err := b.pushLayerRef(ctx, key.Ref(), tip, ""); err != nil {
			_ = b.deleteRef(ctx, key.Ref(), tip)
			return Layer{}, err
		}
	}
	return b.LayerOpen(ctx, key)
}

// LayerPurge deletes trashed layers for good: every trash ref of key, or
// (key zero) every trash ref of this folder stamped before cutoff. push
// deletes them on the remote too. It returns the refs removed.
func (b *Backend) LayerPurge(ctx context.Context, key branchlayer.Key, cutoff time.Time, push bool) ([]string, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return nil, err
	}
	refs, err := b.layerPurgeLocked(ctx, key, cutoff, push)
	return refs, releaseKeep(err, lock)
}

// layerPurgeLocked is LayerPurge under the store lock.
func (b *Backend) layerPurgeLocked(ctx context.Context, key branchlayer.Key, cutoff time.Time, push bool) ([]string, error) {
	prefix := branchlayer.TrashPrefix + b.branch + "/" + branchlayer.FolderSeg(b.folder.Name()) + "/"
	if key.Branch != "" {
		prefix = key.TrashPrefixOf() + "@*"
	}
	refs, err := b.listRefs(ctx, prefix, trackingOf(prefix))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ref := range refs {
		_, at, ok := branchlayer.ParseTrashRef(branchlayer.TrashPrefix, ref)
		if !ok {
			_, at, ok = branchlayer.ParseTrashRef(LayerTrashTrackingPrefix, ref)
		}
		if !ok || (key.Branch == "" && !at.Before(cutoff)) {
			continue
		}
		name := localTrashName(ref)
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if push && len(names) > 0 {
		args := []string{"push", "--porcelain", "--no-verify", "--atomic", "--", RemoteName}
		for _, n := range names {
			if tip, _ := b.resolve(ctx, b.work, trackingOf(n)); tip != "" {
				args = append(args, ":"+n)
			}
		}
		if len(args) > 6 {
			if _, err := b.git.run(ctx, call{dir: b.work, args: args}); err != nil {
				return nil, errs.Wrap(errs.Offline, "git: delete the trashed layers on the remote", errors.Join(errOffline, err)).
					WithHint("nothing was deleted; run it again when the store's remote answers")
			}
			b.pushed()
		}
	}
	for _, n := range names {
		_ = b.deleteRef(ctx, n, "")
		_ = b.deleteRef(ctx, trackingOf(n), "")
	}
	return names, nil
}

// pushLayerRef pushes oid to ref with a lease on the remote's tip ("" =
// the remote must not have it).
func (b *Backend) pushLayerRef(ctx context.Context, ref, oid, remoteTip string) error {
	lease := "--force-with-lease=" + ref + ":" + remoteTip
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"push", "--porcelain", "--no-verify", lease, "--", RemoteName, oid + ":" + ref}})
	if err == nil {
		b.pushed()
		_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingOf(ref), oid}})
		return nil
	}
	if _, ran := exitCode(err); !ran {
		return err
	}
	flag, summary, found := porcelainVerdict(string(out), ref)
	if !found {
		return errs.Wrap(errs.Offline, "git: push "+ref, errors.Join(errOffline, err))
	}
	if flag == flagRejected {
		return errs.New(errs.Contended, "git: the remote's copy of "+ref+" moved ("+summary+")").WithHint("run lore sync to merge it, then again")
	}
	return errs.Wrap(errs.Refused, "git: the remote refused "+ref+": "+summary, err)
}

// LayerPushReport is what LayerPush did.
type LayerPushReport struct {
	Pushed  []string // layer refs sent
	Merged  []string // layer refs merged with a teammate's copy first
	Refused []string // "ref: why"
	// Retired are local layer refs a teammate's landing or drop already
	// holds in the trash: removed here, never pushed back.
	Retired []string
	Offline bool
	// Rows counts the row files the pushed layer refs carried over the
	// remote's copy (a new layer: over the store commit it started from),
	// by data folder: what lore sync reports as sent for the layers
	// (devco:V-13: the whole folder's rows were reported instead).
	Rows map[string]int
	// Commits counts the commits the pushed layer refs carried over the
	// remote's copy (a new layer: over the store commit it started from).
	Commits int
}

// trashedElsewhere reports whether a trash copy of the layer at ref (local
// or fetched) holds local, so the layer was landed or dropped meanwhile.
func (b *Backend) trashedElsewhere(ctx context.Context, ref, local string) (bool, error) {
	k, ok := branchlayer.ParseRef(branchlayer.RefPrefix, ref)
	if !ok {
		return false, nil
	}
	refs, err := b.listRefs(ctx, k.TrashPrefixOf()+"@*", trackingOf(k.TrashPrefixOf())+"@*")
	if err != nil {
		return false, err
	}
	for _, t := range refs {
		tip, rerr := b.resolve(ctx, b.work, t)
		if rerr != nil || tip == "" {
			continue
		}
		if in, aerr := b.isAncestor(ctx, local, tip); aerr == nil && in {
			return true, nil
		}
	}
	return false, nil
}

// LayerPush sends every local layer of this backend's folder that is ahead
// of the remote's copy (lore sync, BRANCH_LAYERS 16): a remote copy the
// local one contains is fast-forwarded with a lease; a diverged pair is
// merged row by row first (mergeLayers), a refused merge is reported and
// nothing of it is pushed. A fetched copy ahead of the local one moves the
// local ref forward.
func (b *Backend) LayerPush(ctx context.Context) (LayerPushReport, error) {
	var rep LayerPushReport
	if err := b.usable(ctx); err != nil || b.mode != modeClone {
		return rep, err
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return rep, err
	}
	rep, err = b.layerPushLocked(ctx, b.branch+"/"+branchlayer.FolderSeg(b.folder.Name())+"/")
	return rep, releaseKeep(err, lock)
}

// layerPushLocked is LayerPush under the store lock, for every layer ref
// under refs/lore/layer/<prefix> (one folder, or the whole store branch).
func (b *Backend) layerPushLocked(ctx context.Context, prefix string) (LayerPushReport, error) {
	var rep LayerPushReport
	refs, err := b.listRefs(ctx, branchlayer.RefPrefix+prefix)
	if err != nil {
		return rep, err
	}
	for _, ref := range refs {
		local, err := b.resolve(ctx, b.work, ref)
		if err != nil {
			return rep, err
		}
		remote, err := b.resolve(ctx, b.work, trackingOf(ref))
		if err != nil {
			return rep, err
		}
		switch {
		case local == remote:
			continue
		case remote == "":
			// landed or dropped elsewhere (a teammate, the CI step): a
			// trash copy holds this tip, so the local ref retires instead
			// of putting the notes back on the remote
			gone, gerr := b.trashedElsewhere(ctx, ref, local)
			if gerr != nil {
				return rep, gerr
			}
			if gone {
				if err := b.deleteRef(ctx, ref, local); err != nil {
					return rep, err
				}
				rep.Retired = append(rep.Retired, ref)
				continue
			}
		default:
			ahead, aerr := b.isAncestor(ctx, remote, local)
			if aerr != nil {
				return rep, aerr
			}
			if !ahead {
				behind, berr := b.isAncestor(ctx, local, remote)
				if berr != nil {
					return rep, berr
				}
				if behind {
					if err := b.casRef(ctx, ref, remote, local); err != nil {
						return rep, err
					}
					continue
				}
				merged, refusal, merr := b.mergeLayers(ctx, ref, local, remote)
				if merr != nil {
					return rep, merr
				}
				if refusal != "" {
					rep.Refused = append(rep.Refused, ref+": "+refusal)
					continue
				}
				local = merged
				rep.Merged = append(rep.Merged, ref)
			}
		}
		if err := b.pushLayerRef(ctx, ref, local, remote); err != nil {
			if errs.CodeOf(err) == errs.Offline {
				rep.Offline = true
				return rep, nil
			}
			rep.Refused = append(rep.Refused, ref+": "+err.Error())
			continue
		}
		rep.Pushed = append(rep.Pushed, ref)
		from, err := b.layerSentOver(ctx, local, remote)
		if err != nil {
			return rep, err
		}
		n, err := b.layerRowsSent(ctx, local, from)
		if err != nil {
			return rep, err
		}
		c, err := b.countCommits(ctx, from, local) // no shared commit: every commit of the layer is new there
		if err != nil {
			return rep, err
		}
		rep.Commits += c
		for f, c := range n {
			if rep.Rows == nil {
				rep.Rows = map[string]int{}
			}
			rep.Rows[f] += c
		}
	}
	return rep, nil
}

// layerSentOver is the commit a layer push of local is counted over:
// remote (the remote's copy before the push), or for a layer new on the
// remote the store commit it shares with this clone's store branch, never
// every commit or row of the folder (devco:V-13). "" when there is no
// shared commit (best effort: nothing is counted rather than failing).
func (b *Backend) layerSentOver(ctx context.Context, local, remote string) (string, error) {
	if remote != "" {
		return remote, nil
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return "", err
	}
	from, err := b.mergeBase(ctx, local, h)
	if err != nil {
		return "", nil
	}
	return from, nil
}

// layerRowsSent counts the row files a layer push of local carried over
// from (layerSentOver); "" counts nothing.
func (b *Backend) layerRowsSent(ctx context.Context, local, from string) (map[string]int, error) {
	if from == "" {
		return nil, nil // no shared commit: nothing to count against
	}
	return b.countRows(ctx, from, local)
}

// mergeLayers merges a teammate's copy (remote) into the local layer
// (local) row by row, against their merge base, with the clone's Merger:
// the result is a merge commit on ref. A row both changed that the Merger
// cannot merge is a refusal ("" when none) and nothing changes.
func (b *Backend) mergeLayers(ctx context.Context, ref, local, remote string) (string, string, error) {
	base, err := b.mergeBase(ctx, local, remote)
	if err != nil {
		return "", "", err
	}
	ours, err := b.changedFiles(ctx, base, local)
	if err != nil {
		return "", "", err
	}
	theirs, err := b.changedFiles(ctx, base, remote)
	if err != nil {
		return "", "", err
	}
	puts := map[string][]byte{}
	var names []string
	for p := range theirs {
		names = append(names, base+":"+p, local+":"+p, remote+":"+p)
	}
	blobs, err := b.readBlobs(ctx, names)
	if err != nil {
		return "", "", err
	}
	for p := range theirs {
		t := blobs[remote+":"+p]
		if !ours[p] {
			puts[p] = t
			continue
		}
		o := blobs[local+":"+p]
		if string(o) == string(t) {
			continue
		}
		if b.cfg.merger == nil {
			return "", "both copies of the layer changed " + p + " and no merger is configured", nil
		}
		if nm, ok := b.cfg.merger.(NewerTextMerger); ok {
			// devco V-12 (the D-219 rule on a layer): the newer text wins and
			// the merger's report names the one it replaced; never a refusal
			m, merr := nm.MergeNewerText(blobs[base+":"+p], o, t)
			if merr != nil {
				return "", "", errs.Wrap(errs.Internal, "git: merge a layer row", merr)
			}
			puts[p] = m
			continue
		}
		m, conflict, merr := b.cfg.merger.Merge(blobs[base+":"+p], o, t)
		if merr != nil {
			return "", "", errs.Wrap(errs.Internal, "git: merge a layer row", merr)
		}
		if conflict {
			return "", "the same text of " + p + " changed in both copies of the layer", nil
		}
		puts[p] = m
	}
	msg, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%B", local}})
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "git: read the layer commit", err)
	}
	trailers := string(msg)
	if i := strings.Index(trailers, "\n\n"); i >= 0 {
		trailers = trailers[i+2:]
	}
	commit, err := b.commitOnto(ctx, local, puts, nil, "lore: merge a teammate's notes of this branch\n\n"+trailers)
	if err != nil {
		return "", "", err
	}
	// make it a merge commit so the remote copy fast-forwards to it
	tree, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", commit + "^{tree}"}})
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "git: read the merged tree", err)
	}
	cenv, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		return "", "", err
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"commit-tree", trimmed(tree), "-p", local, "-p", remote, "-F", "-"}, env: cenv,
		stdin: []byte("lore: merge a teammate's notes of this branch\n\n" + trailers)})
	if err != nil {
		return "", "", errs.Wrap(errs.Internal, "git: commit the layer merge", err)
	}
	merged := trimmed(out)
	if err := b.casRef(ctx, ref, merged, local); err != nil {
		return "", "", err
	}
	return merged, "", nil
}

// mergeBase is the merge base of a and c.
func (b *Backend) mergeBase(ctx context.Context, a, c string) (string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge-base", a, c}})
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: find the layers' merge base", err)
	}
	return trimmed(out), nil
}

// changedFiles is the set of files added or modified from a to c.
func (b *Backend) changedFiles(ctx context.Context, a, c string) (map[string]bool, error) {
	paths, err := b.changedPaths(ctx, a, c, nil)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set, nil
}

// LayerFetch fetches the remote's layer and trash refs into their tracking
// namespaces (and the store branch). Offline is reported, never an error.
func (b *Backend) LayerFetch(ctx context.Context) (offline bool, err error) {
	if err := b.usable(ctx); err != nil || b.mode != modeClone {
		return false, err
	}
	ferr := b.runFetch(ctx, b.work, "fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", "--", RemoteName, headsRefspec, layerRefspec, layerTrashRefspec)
	if ferr != nil {
		if isOffline(ferr) {
			return true, nil
		}
		return false, ferr
	}
	return false, nil
}

// LayerRowAt reads one row of the folder from commit (nil when absent).
func (b *Backend) LayerRowAt(ctx context.Context, commit string, table row.Table, id ids.AnyID) (*store.Row, error) {
	p := b.folder.StorePath(table, id)
	blobs, err := b.readBlobs(ctx, []string{commit + ":" + p})
	if err != nil {
		return nil, err
	}
	doc, ok := blobs[commit+":"+p]
	if !ok {
		return nil, nil
	}
	r, err := rowfs.DecodeRowFile(rowfs.RowPath(table, id), doc)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// LayerRename moves the layer of from to to (a renamed code branch,
// BRANCH_LAYERS 7): the same commits under the new name, the old ref
// removed; push sends both moves in one atomic push. to must have no
// layer yet.
func (b *Backend) LayerRename(ctx context.Context, from, to branchlayer.Key, push bool) (Layer, error) {
	if err := b.layerUsable(ctx, from); err != nil {
		return Layer{}, err
	}
	if err := to.Check(); err != nil {
		return Layer{}, errs.Wrap(errs.InvalidInput, "git: layer key", err)
	}
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return Layer{}, err
	}
	l, err := b.layerRenameLocked(ctx, from, to, push)
	return l, releaseKeep(err, lock)
}

// layerRenameLocked is LayerRename under the store lock.
func (b *Backend) layerRenameLocked(ctx context.Context, from, to branchlayer.Key, push bool) (Layer, error) {
	src, err := b.LayerOpen(ctx, from)
	if err != nil {
		return Layer{}, err
	}
	if !src.Exists() {
		return Layer{}, errs.New(errs.NotFound, "no notes of "+from.Branch+" in "+from.Folder).WithHint("lore branch list shows the branches that have some")
	}
	dst, err := b.LayerOpen(ctx, to)
	if err != nil {
		return Layer{}, err
	}
	if dst.Exists() {
		return Layer{}, errs.New(errs.NameTaken, to.Branch+" already has notes in "+to.Folder).
			WithHint("land or drop them first (lore branch land|drop --branch " + to.Branch + ")")
	}
	f := src.Facts
	f.Branch = to.Branch
	msg := "lore: rename the notes of " + from.Branch + " to " + to.Branch + " (" + to.Folder + ")\n\n" + f.Trailers() + TrailerStoreBase + ": " + src.Base + "\n"
	commit, err := b.commitOnto(ctx, src.Tip, nil, nil, msg)
	if err != nil {
		return Layer{}, err
	}
	if err := b.casRef(ctx, to.Ref(), commit, ""); err != nil {
		return Layer{}, err
	}
	if push {
		args := []string{"push", "--porcelain", "--no-verify", "--atomic"}
		specs := []string{commit + ":" + to.Ref()}
		if src.Remote != "" {
			args = append(args, "--force-with-lease="+from.Ref()+":"+src.Remote)
			specs = append(specs, ":"+from.Ref())
		}
		args = append(append(args, "--", RemoteName), specs...)
		if _, err := b.git.run(ctx, call{dir: b.work, args: args}); err != nil {
			_ = b.deleteRef(ctx, to.Ref(), commit)
			return Layer{}, errs.Wrap(errs.Offline, "git: push the renamed notes", errors.Join(errOffline, err)).
				WithHint("nothing changed; run it again when the store's remote answers")
		}
		b.pushed()
		_, _ = b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", trackingOf(to.Ref()), commit}})
		_ = b.deleteRef(ctx, trackingOf(from.Ref()), "")
	}
	if src.Local {
		if err := b.deleteRef(ctx, from.Ref(), src.Tip); err != nil {
			return Layer{}, err
		}
	}
	return b.LayerOpen(ctx, to)
}

// LayerTipTime is the commit time of a layer's tip (its last write).
func (b *Backend) LayerTipTime(ctx context.Context, l Layer) (time.Time, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"log", "-1", "--format=%ct", l.Tip}})
	if err != nil {
		return time.Time{}, errs.Wrap(errs.Internal, "git: read the layer's last write time", err)
	}
	sec, err := strconv.ParseInt(trimmed(out), 10, 64)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.Internal, "git: read the layer's last write time", err)
	}
	return time.Unix(sec, 0).UTC(), nil
}
