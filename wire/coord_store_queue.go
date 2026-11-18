package wire

// coord_store_queue.go — task next over an org's store with no code repo
// (devco:D-666). D-635 claimed a store task by id from the home folder,
// but `task next --setup <setup>` from home still refused as a code-repo
// read, though every store task's claim lives on its store's signals ref.
//
//	TaskNext ─► storeQueue: --store <org>[/<folder>] (anywhere), or
//	            --setup <setup> from outside every code repo, or a store
//	            clone with neither
//	   folders of that org's stores (orgFolders / everyOrgFolders)
//	   per store: one world (storeWorldOn), per folder one PeekNext with
//	     env.Folder = the folder, kept to the folder's own tasks
//	   merged ranking (urgency) ─► read lines, or --claim: storeWorldOf
//	     the top task, then task claim's claimOn (D-580 --push rule)
//
// Each claim lands under its task's own folder on its store's signals
// ref, the place a later `task claim|release --id` finds it (D-635): a
// claim under the store root would not match a claim by id. A printed
// next step carries the same --store or --setup, so it works where it is
// printed.

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/queue"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	"github.com/thesatellite-ai/lore/schema"
)

// storeQueueScope is the org store task next ranks outside a code repo:
// its folders, grouped by store, and the flag that names it again in a
// printed next step ("" inside a store clone, where none is needed).
type storeQueueScope struct {
	folders []orgFolder
	flag    string
}

// storeQueueAsked reports whether task next runs as the store task queue
// for input in at dir (devco:D-666): it names --store, or --setup from
// outside every code repo. The repo-only refusal then never applies.
func storeQueueAsked(ctx context.Context, in op.Input, dir string) bool {
	if in.Op() != opscoord.OpTaskNext {
		return false
	}
	if s, ok := in.String("store"); ok && s != "" {
		return true
	}
	return op.SetupOf(ctx) != "" && !inCodeRepo(ctx, dir)
}

// storeQueueRun is storeQueueAsked for the op entry e decodes from d.
func (a *App) storeQueueRun(ctx context.Context, e op.Entry, d op.Decoder) bool {
	if e.Spec().Name != opscoord.OpTaskNext {
		return false
	}
	in, err := e.Decode(d)
	return err == nil && storeQueueAsked(ctx, in, a.opts.RepoDir)
}

// storeHeartbeatRun reports a task heartbeat with no code repo here
// (devco:D-666; with --daemon too, devco:D-671): it renews the store
// claims itself (storeHeartbeat, storeHeartbeatDaemon), so the run opens
// nothing first: the repo open would refuse with E_NOT_A_STORE.
func (a *App) storeHeartbeatRun(ctx context.Context, e op.Entry, _ op.Decoder) bool {
	return e.Spec().Name == opscoord.OpTaskHeartbeat && !inCodeRepo(ctx, a.opts.RepoDir)
}

// storeQueueScopeOf is the scope of task next with in: ok=false hands the
// command to the code repo's queue (a code repo with no --store, or a
// folder with neither flag nor store clone, which openWorld refuses).
func (h standaloneCoord) storeQueueScopeOf(ctx context.Context, in opscoord.NextIn) (storeQueueScope, bool, error) {
	a := h.a
	switch {
	case in.Store != "":
		org, folder, _ := strings.Cut(in.Store, targetSep)
		var keep []orgFolder
		var known []string
		for _, f := range a.everyOrgFolders(ctx).Folders {
			if f.Org != org && f.Label != org {
				if f.Org != "" && !slices.Contains(known, f.Org) {
					known = append(known, f.Org)
				}
				continue
			}
			if folder == "" || f.Folder == folder {
				keep = append(keep, f)
			}
		}
		if len(keep) == 0 {
			return storeQueueScope{}, true, errs.New(errs.NotFound, "--store "+in.Store+" names no folder of an org store on this machine (orgs here: "+strings.Join(known, ", ")+")").
				WithHint("`lore store folder list --store " + cmp.Or(firstOf(known), "<org>") + "` lists an org's folders")
		}
		return storeQueueScope{folders: keep, flag: " --store " + in.Store}, true, nil
	case !inCodeRepo(ctx, a.opts.RepoDir) && op.SetupOf(ctx) != "":
		fs := a.orgFolders(ctx).Folders
		if len(fs) == 0 {
			return storeQueueScope{}, true, errs.New(errs.NotFound, "setup "+op.SetupOf(ctx)+" has no org store folder on this machine to rank tasks in").
				WithHint("`lore store list --setup " + op.SetupOf(ctx) + "` lists the setup's stores")
		}
		return storeQueueScope{folders: oneOrg(fs), flag: " --setup " + op.SetupOf(ctx)}, true, nil
	}
	sc, inClone := storeHere(a.opts.RepoDir)
	if !inClone {
		return storeQueueScope{}, false, nil
	}
	// a clone no org entry owns keeps openWorld's store-clone refusal
	fs := slices.DeleteFunc(oneOrg(a.orgFolders(ctx).Folders), func(f orgFolder) bool { return f.Org == "" })
	if sc.folder != rowfs.FolderRoot {
		var here []orgFolder
		for _, f := range fs {
			if f.Folder == sc.folder && sameDir(f.Dir, sc.top) {
				here = append(here, f)
			}
		}
		fs = here
	}
	if len(fs) == 0 {
		return storeQueueScope{}, false, nil // openWorld's store-clone refusal
	}
	return storeQueueScope{folders: fs}, true, nil
}

// firstOf is the first of names, "" for none.
func firstOf(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// sameDir reports whether two folder paths name the same folder.
func sameDir(x, y string) bool {
	ix, ex := os.Stat(x)
	iy, ey := os.Stat(y)
	return ex == nil && ey == nil && os.SameFile(ix, iy)
}

// oneOrg keeps the folders of the first folder's org: one queue ranks one
// org's tasks (a setup names one org).
func oneOrg(fs []orgFolder) []orgFolder {
	if len(fs) == 0 {
		return nil
	}
	var out []orgFolder
	for _, f := range fs {
		if f.Org == fs[0].Org {
			out = append(out, f)
		}
	}
	return out
}

// storeQueue is task next as the store task queue (see the file comment);
// ok=false: not this queue's (a code repo's own task next runs).
func (h standaloneCoord) storeQueue(ctx context.Context, in opscoord.NextIn) (opscoord.NextOut, bool, error) {
	sc, ok, err := h.storeQueueScopeOf(ctx, in)
	if !ok || err != nil {
		return opscoord.NextOut{}, ok, err
	}
	a := h.a
	cfg, _, cerr := orgcfg.LoadConfig(a.opts.Home)
	if cerr != nil {
		return opscoord.NextOut{}, true, errs.Wrap(errs.Internal, "read config.yaml", cerr)
	}
	org := cfg.FindOrg(sc.folders[0].Org)
	if org == nil && sc.flag == "" {
		return opscoord.NextOut{}, false, nil // a store clone no org entry owns: openWorld's refusal
	}
	if org == nil {
		return opscoord.NextOut{}, true, errs.New(errs.NotFound, "no org entry of config.yaml owns the store of "+sc.folders[0].Label).
			WithHint("`lore org list` lists the org entries")
	}
	// one world per store (its signals ref), one peek per folder on it
	byStore := map[string][]orgFolder{}
	var order []string
	for _, f := range sc.folders {
		k := placeKey(f.URL, f.Branch)
		if _, seen := byStore[k]; !seen {
			order = append(order, k)
		}
		byStore[k] = append(byStore[k], f)
	}
	merged := queue.Result{Scores: map[string]urgency.Score{}}
	holding, push := "", ""
	for _, k := range order {
		fs := byStore[k]
		stores := make([]attach.Store, 0, len(fs))
		for _, f := range fs {
			stores = append(stores, a.orgFolderStore(ctx, f))
		}
		also := append(slices.Clone(stores[1:]), a.companyFolderOf(ctx, stores[0])...)
		w, werr := h.storeWorldOn(ctx, cfg, org, stores[0], also...)
		if werr != nil {
			return opscoord.NextOut{}, true, werr
		}
		push = w.claimPushFlag()
		for _, f := range fs {
			pk, perr := h.peekFolder(ctx, w, f)
			if perr != nil {
				return opscoord.NextOut{}, true, perr
			}
			if holding == "" {
				holding = pk.Holding
			}
			merged.Ready = append(merged.Ready, pk.Queue.Ready...)
			merged.Waiting = append(merged.Waiting, pk.Queue.Waiting...)
			for id, s := range pk.Queue.Scores {
				merged.Scores[id] = s
			}
		}
	}
	// claim order across folders: urgency, then each folder's own order
	slices.SortStableFunc(merged.Ready, func(x, y queue.Task) int {
		return cmp.Compare(merged.Scores[y.ID].Total, merged.Scores[x.ID].Total)
	})
	if !in.Claim || in.DryRun {
		out := nextRanking(merged)
		if in.DryRun {
			out.Lines = dryRunLines(merged, in.Why)
			return out, true, nil
		}
		out.Holding = holding
		out.Lines = readLinesAt(coord.Peek{Holding: holding, Queue: merged}, in.Why, push, sc.flag, !inCodeRepo(ctx, a.opts.RepoDir))
		return out, true, nil
	}
	if holding != "" {
		return opscoord.NextOut{Claimed: holding, Lines: []string{fmt.Sprintf("you hold %s; resume, or `lore task release --id %s`", holding, holding)}}, true, nil
	}
	if len(merged.Ready) == 0 {
		return opscoord.NextOut{Lines: []string{"nothing ready"}}, true, nil
	}
	top := merged.Ready[0].ID
	w, found, werr := h.storeWorldOf(ctx, top)
	if werr != nil {
		return opscoord.NextOut{}, true, werr
	}
	if !found {
		return opscoord.NextOut{}, true, errs.New(errs.NotFound, "the store folder holding "+top+" is gone from this machine").
			WithHint("`lore sync" + sc.flag + "` refreshes the store clones")
	}
	rep, err := h.claimOn(ctx, w, opscoord.ClaimIn{ID: top, Owns: in.Owns, Push: in.Push})
	if err != nil {
		return opscoord.NextOut{Lines: rep.Lines}, true, err
	}
	lines := rep.Lines
	if s, ok := merged.Scores[top]; ok && in.Why {
		lines = append(lines, "  urgency "+urgency.Format(s.Total))
		for _, l := range s.Lines() {
			lines = append(lines, "    "+l)
		}
	}
	lines = append(lines, "  read its notes: lore task show --id "+top)
	return opscoord.NextOut{Claimed: top, Lines: lines}, true, nil
}

// peekFolder is PeekNext on world w with env.Folder the folder f, kept
// to f's own tasks (the world's rows also hold its other folders' and the
// company folder's, which the ranking reads for dependencies).
func (h standaloneCoord) peekFolder(ctx context.Context, w *coordWorld, f orgFolder) (coord.Peek, error) {
	folder := f.Folder
	if folder == "" || folder == rowfs.FolderRoot {
		folder = signalsRootFolder
	}
	w.folder, w.spot.folder = folder, folder
	env := w.env(ctx)
	rows := engineRows{a: h.a}
	if er, ok := env.Rows.(engineRows); ok {
		rows = er
	}
	req := coord.NextRequest{
		Filter: queue.Filter{},
		Opts:   coord.ClaimOptions{WorkBranch: w.workBranch(ctx), InRepo: w.taskInRepo()},
		List:   rows.List, RepoName: rows.RepoName, Pick: cryptoPick, Urgency: h.urgencyContext(ctx),
	}
	pk, err := coord.PeekNext(ctx, env, req)
	if err != nil {
		return coord.Peek{}, w.coordErr(err)
	}
	own := folderTaskIDs(f)
	keep := func(id string) bool { return own[id] }
	pk.Queue.Ready = slices.DeleteFunc(pk.Queue.Ready, func(t queue.Task) bool { return !keep(t.ID) })
	pk.Queue.Waiting = slices.DeleteFunc(pk.Queue.Waiting, func(wt queue.Waiting) bool { return !keep(wt.Task.ID) })
	if pk.Holding != "" && !keep(pk.Holding) {
		pk.Holding = ""
	}
	return pk, nil
}

// folderTaskIDs is the set of task ids whose row file is in folder f of
// its store clone (<dir>/<folder>/tasks/<id>.json).
func folderTaskIDs(f orgFolder) map[string]bool {
	out := map[string]bool{}
	dir := f.Dir
	if f.Folder != "" && f.Folder != rowfs.FolderRoot {
		dir = filepath.Join(dir, filepath.FromSlash(f.Folder))
	}
	hits, _ := filepath.Glob(filepath.Join(dir, string(schema.TableTasks), "*.json"))
	for _, p := range hits {
		out[strings.TrimSuffix(filepath.Base(p), ".json")] = true
	}
	return out
}

// storeHeartbeat renews the store task claims this caller holds, with no
// code repo (devco:D-666): id alone, or with id "" every claim held on the
// org stores of this machine. Their held records live in each store
// clone's state dir (the claim was taken there or from home), so a
// heartbeat with no --id from home, in the store clone or in a code repo
// (skip: the ids the code repo renewed itself) finds them.
func (h standaloneCoord) storeHeartbeat(ctx context.Context, id string, skip []string) (opscoord.Report, error) {
	if id != "" {
		w, found, err := h.storeWorldOf(ctx, id)
		if err != nil {
			return opscoord.Report{}, err
		}
		if !found {
			return opscoord.Report{}, errs.New(errs.NotFound, "no org store on this machine holds "+id+", and here is no code repo whose claims it could be").
				WithHint("`lore task heartbeat` renews every claim you hold; `lore task show --id " + id + "` shows where the task lives")
		}
		return h.renewHeld(ctx, w, []string{id}, false)
	}
	var lines []string
	err := h.eachHeldStore(ctx, func(w *coordWorld, recs []coord.HeldRecord) error {
		for _, rec := range recs {
			if slices.Contains(skip, rec.Task) {
				continue
			}
			if w.renewedRecently(rec.Task) {
				lines = append(lines, fmt.Sprintf("  %s held until %s (renewed less than %s ago)", rec.Task, rec.Expires.UTC().Format(time.RFC3339), shortDuration(w.lease.RenewEvery)))
				continue
			}
			s, rerr := w.renewStoreHeld(ctx, rec)
			if rerr != nil {
				return rerr
			}
			lines = append(lines, fmt.Sprintf("%s %s renewed until %s (seq %d)", markOK, rec.Task, s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq))
		}
		return nil
	})
	if err != nil {
		return opscoord.Report{Lines: lines}, err
	}
	if len(lines) == 0 && skip == nil {
		lines = []string{"no claims held by this session"}
	}
	return opscoord.Report{Lines: lines}, nil
}

// eachHeldStore calls fn once per org store of this machine whose store
// clone keeps a held record of this session, with the world on that store
// and the records (devco:D-666, D-671). Each world reopens the App
// store-only on its store, so fn must finish with w before the next call.
// No org config: no store, no call.
func (h standaloneCoord) eachHeldStore(ctx context.Context, fn func(w *coordWorld, recs []coord.HeldRecord) error) error {
	a := h.a
	cfg, _, cerr := orgcfg.LoadConfig(a.opts.Home)
	if cerr != nil || cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, f := range a.everyOrgFolders(ctx).Folders {
		k := placeKey(f.URL, f.Branch)
		if seen[k] || f.Dir == "" {
			continue
		}
		seen[k] = true
		if !(coord.HeldStore{Path: filepath.Join(gitbackend.StateDirOf(f.Dir), signalsStateDir, heldFile)}).Any() {
			continue // no claim was ever held on this store here
		}
		org := cfg.FindOrg(f.Org)
		if org == nil {
			continue
		}
		w, err := h.storeWorldOn(ctx, cfg, org, a.orgFolderStore(ctx, f))
		if err != nil {
			return err
		}
		if recs := w.held().List(w.session()); len(recs) > 0 {
			if err = fn(w, recs); err != nil {
				return err
			}
		}
	}
	return nil
}

// renewStoreHeld renews the store claim rec on world w, under the folder
// the claim was taken in.
func (w *coordWorld) renewStoreHeld(ctx context.Context, rec coord.HeldRecord) (signals.Signal, error) {
	w.folder, w.spot.folder = rec.Folder, rec.Folder
	s, err := coord.Renew(ctx, w.env(ctx), rec.Task)
	if err != nil {
		return s, w.coordErr(err)
	}
	return s, nil
}
