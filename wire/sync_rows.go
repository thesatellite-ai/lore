package wire

// sync_rows.go — the row-level `lore sync` verbs on the core (P3-SYNC-T1,
// P3-SYNC-T2): merge-rows and fix-projects over engine.MergeRows, and the
// `lore sync trash` pointer at `lore trash --local`.
//
//	opssync.Ops(a.syncEnv())
//	   syncRows{base: Options.Sync}
//	     sync.merge_rows / sync.fix_projects / sync.trash.list / sync.trash.restore
//	       ─▶ the base host first (the old binary's lore.db host keeps its own)
//	       ─▶ base answers E_UNSUPPORTED (cmd/lore's live host) ─▶ the core path:
//	   merge-rows   ─▶ App.MergeRows ─▶ engine.MergeRows(w = App): referrers
//	                   re-pointed, the dropped row soft-deleted "merged into <keep>"
//	   fix-projects ─▶ App.FixProjects ─▶ engine.MergeRows per extra project
//	                   (one bat_ batch) ─▶ _meta.json project_id = keep
//	   sync trash   ─▶ stderr pointer (DELETION 3.10, OV31-a) ─▶ the same
//	                   answer as `lore trash --local` / `restore --local <n>`
//	   sync purge   ─▶ stderr pointer (FA-DEL-4) ─▶ the registered
//	                   `lore trash purge --archived-before` operation
//	   sync run     ─▶ the base host's pass, then (opssync.CoreStores hosts)
//	                   every attached store and the queued run time
//	   every other verb ─▶ the base host unchanged
//
// Owned by p3-refrewrite. The hook point in wire.go (`opssync.Ops(a.syncEnv())`)
// is one line in the registry list.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsrun "github.com/thesatellite-ai/lore/ops/run"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// syncTrashPointer is the one stderr line of the old `lore sync trash`
// spelling (DELETION 3.10, CLI_REFERENCE 2.15, OV31-a); %s is the new one.
const syncTrashPointer = "note: lore sync trash is now %s; this spelling goes away in the next release"

// syncEnv is the sync host the registry mounts: Options.Sync with the
// row-level verbs falling back to the core (syncRows). A binary without a
// sync host keeps nil, so every sync verb answers E_UNSUPPORTED (P3-SYNC-1).
func (a *App) syncEnv() opssync.Env {
	if a.opts.Sync == nil {
		return nil
	}
	return syncRows{Env: a.opts.Sync, app: a}
}

// syncRows is Options.Sync whose merge-rows, fix-projects and trash verbs
// run on the core when the host does not implement them (E_UNSUPPORTED).
type syncRows struct {
	opssync.Env
	app *App
}

// fallback reports whether the host's answer hands the verb to the core.
func fallback(err error) bool { return errs.Has(err, errs.Unsupported) }

// SyncMergeRows implements opssync.Env.
func (s syncRows) SyncMergeRows(ctx context.Context, in opssync.MergeRowsIn) (opssync.DoneOut, error) {
	out, err := s.Env.SyncMergeRows(ctx, in)
	if !fallback(err) {
		return out, err
	}
	res, err := s.app.MergeRows(ctx, in.Table, in.Keep, in.Drop)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	done := fmt.Sprintf("merged %s into %s (%d row(s) re-pointed; %s is in the trash, batch %s)",
		res.Drop, res.Keep, len(res.Referrers), res.Drop, res.Batch)
	return opssync.DoneOut{Done: done, Items: mergeItems(res), Lines: []string{"✓ " + done}}, nil
}

// mergeItems is drop, keep, then every re-pointed row id.
func mergeItems(res engine.MergeResult) []string {
	items := []string{res.Drop.String(), res.Keep.String()}
	for _, r := range res.Referrers {
		items = append(items, r.ID.String())
	}
	return items
}

// SyncFixProjects implements opssync.Env.
func (s syncRows) SyncFixProjects(ctx context.Context, in opssync.FixProjectsIn) (opssync.DoneOut, error) {
	out, err := s.Env.SyncFixProjects(ctx, in)
	if !fallback(err) {
		return out, err
	}
	fx, err := s.app.FixProjects(ctx, in.Keep, in.DryRun)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if len(fx.Merged) == 0 {
		const one = "only one project; nothing to fix"
		return opssync.DoneOut{Done: one, Lines: []string{"· " + one}}, nil
	}
	n := 0
	merged := make([]string, 0, len(fx.Merged))
	for _, m := range fx.Merged {
		n += len(m.Referrers)
		merged = append(merged, m.Drop.String())
	}
	done := fmt.Sprintf("kept %s, merged %s (%d row(s) re-pointed)", fx.Keep, strings.Join(merged, ", "), n)
	if in.DryRun {
		done = fmt.Sprintf("%d project row(s): would keep %s and merge %s (%d row(s) to re-point)", len(fx.Merged)+1, fx.Keep, strings.Join(merged, ", "), n)
		return opssync.DoneOut{Done: done, Items: merged, Lines: []string{done}}, nil
	}
	return opssync.DoneOut{Done: done, Items: merged, Lines: []string{"✓ " + done}}, nil
}

// SyncTrashList implements opssync.Env: the old spelling of `lore trash
// --local`, with its pointer line.
func (s syncRows) SyncTrashList(ctx context.Context, in opssync.TrashListIn) (opssync.TrashListOut, error) {
	out, err := s.Env.SyncTrashList(ctx, in)
	if !fallback(err) {
		return out, err
	}
	s.app.notice(fmt.Sprintf(syncTrashPointer, "lore trash --local"))
	copies, err := s.app.LocalTrash(ctx)
	if err != nil {
		return opssync.TrashListOut{}, err
	}
	out = opssync.TrashListOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindTrash, Count: len(copies), Data: []opssync.TrashEntry{}}
	if len(copies) == 0 {
		out.Lines = []string{"· trash is empty"}
	}
	for _, c := range copies {
		out.Data = append(out.Data, opssync.TrashEntry{ID: c.N, Table: c.Table, RowID: c.RowID, Reason: c.Reason, At: c.At})
		out.Lines = append(out.Lines, "#"+strconv.FormatInt(c.N, 10)+" "+c.Table+"/"+c.RowID+"  "+c.At+"  ("+c.Reason+")")
	}
	return out, nil
}

// SyncTrashRestore implements opssync.Env: the old spelling of `lore
// trash restore --local <n>`, answered by that operation itself.
func (s syncRows) SyncTrashRestore(ctx context.Context, in opssync.TrashRestoreIn) (opssync.DoneOut, error) {
	out, err := s.Env.SyncTrashRestore(ctx, in)
	if !fallback(err) {
		return out, err
	}
	s.app.notice(fmt.Sprintf(syncTrashPointer, "lore trash restore --local --target "+in.ID))
	e, ok := s.app.reg.Lookup(opstrash.OpRestore)
	if !ok {
		return opssync.DoneOut{}, errs.New(errs.Internal, "sync trash restore: "+opstrash.OpRestore+" is not registered")
	}
	body, err := json.Marshal(opstrash.TargetIn{Target: in.ID, Local: true})
	if err != nil {
		return opssync.DoneOut{}, errs.Wrap(errs.Internal, "sync trash restore: encode", err)
	}
	tin, err := e.Decode(op.JSONDecoder{Body: body})
	if err != nil {
		return opssync.DoneOut{}, err
	}
	// The outer run (sync.trash.restore, write access) already passed the
	// Runner's checks; restore --local never writes (it refuses or points at git).
	res, err := e.Invoke(ctx, tin)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	text, _ := res.Text()
	text = strings.TrimSpace(text)
	return opssync.DoneOut{Done: text, Lines: []string{text}}, nil
}

// storeOfflineNote follows the line of a store whose remote stopped
// answering (devco L-10: name the remote and git's reason; L-11: a store
// that sent its commits in this same run is never "local changes kept").
func storeOfflineNote(sent int, url, detail string) string {
	cause := storeOfflineCause(url, detail)
	if sent > 0 {
		return " (sent; then " + cause + "; the next sync fetches again)"
	}
	return " (offline: " + cause + "; local changes kept, sent on the next sync)"
}

// storeOfflineCause is "<url> did not answer: <git's reason>" for a store
// whose fetch or push got no answer (after the one retry of a dropped
// connection), credentials masked.
func storeOfflineCause(url, detail string) string {
	where := "the store's remote"
	if url != "" {
		where = repourl.Mask(url)
	}
	if why := gitfault.Reason(detail); why != "" {
		return where + " did not answer: " + why
	}
	return where + " did not answer"
}

// SyncRun implements opssync.Env: the host's pass, then, for a host that
// leaves them to the core (opssync.CoreStores: cmd/lore's live-only host),
// every attached store is exchanged (spec 6.1, STORE_SERVER_SPEC 5.3:
// `lore sync` delivers the store writes made offline) and the run time
// queued while a store was unreachable is written (TIME 11.9), as the old
// binary's host does. Refused rows end in E_STALE (both sides kept); the
// report lines are printed first on stderr, since a failed run has no
// stdout.
func (s syncRows) SyncRun(ctx context.Context, in opssync.RunIn) (opssync.RunOut, error) {
	out, err := s.syncRun(ctx, in)
	if err == nil {
		// devco R-7 / R-2: what the stores hold but lore did not take (a
		// non-admin's change to lore-org.yaml, files lore does not own) is
		// named with the commit, its author and the step that settles it.
		for _, line := range s.app.storeTrustLines(ctx) {
			out.Lines = append(out.Lines, line)
			out.Data.Warnings = append(out.Data.Warnings, line)
		}
	}
	return out, err
}

// syncRun is SyncRun before the store trust lines.
func (s syncRows) syncRun(ctx context.Context, in opssync.RunIn) (opssync.RunOut, error) {
	if s.app.setupSyncHere(ctx) {
		return s.app.SyncSetup(ctx) // no code repo here: every store of the setup (syncsetup.go)
	}
	out, err := s.Env.SyncRun(ctx, in)
	if err != nil {
		return out, err
	}
	if c, ok := s.Env.(opssync.CoreStores); !ok || !c.CoreStores() {
		return out, nil
	}
	// G-SYNC-2: a row file a git merge left with conflict markers is a
	// conflict of this pass too (sync status / conflicts list it the same
	// way), so the --json report names it instead of only a stderr line.
	// B2-8: a live row holding a conflict (a strict metadata key two
	// machines set differently, or markers an older lore wrote) is named
	// too, with its cause and next step. D-220: it never fails the sync:
	// the live ref is shared by every clone, so an E_STALE here stopped
	// the sync of people who never touched the row; the row itself does
	// not land until a person settles it.
	stale := 0 // conflicts that are this clone's own to settle (E_STALE)
	if live, lerr := s.hostConflicts(ctx, opssync.ConflictsIn{}); lerr == nil {
		for _, c := range live.Data {
			if c.Resolved {
				continue
			}
			why, next := liveConflictCause(c)
			out.Data.Conflicts = append(out.Data.Conflicts, opssync.ConflictNote{Path: c.Table + "/" + c.RowID, Kept: c.Kept, Why: why})
			out.Lines = append(out.Lines, fmt.Sprintf("conflict #%d live %s/%s: %s (%s)", c.ID, c.Table, c.RowID, why, next))
		}
	}
	if files, ferr := s.app.BranchConflicts(ctx); ferr == nil {
		for _, f := range files {
			out.Data.Conflicts = append(out.Data.Conflicts, opssync.ConflictNote{Path: f.Place, Kept: branchConflictKept, Why: syncMarkedFileWhy})
			out.Lines = append(out.Lines, "conflict "+f.Place+": "+syncMarkedFileWhy)
			stale++
		}
	}
	res, serr := s.app.SyncStores(ctx, "")
	var goneNames, goneNexts []string // H-11 / G-16: stores whose address holds no store now
	goneSaid := map[string]bool{}     // a store's folders share its clone: said once
	cloneSent := storeCloneSent(res.Stores, func(name string) string { return s.app.storeURLOf(ctx, name) })
	for _, r := range res.Stores {
		out.Data.Stores = append(out.Data.Stores, opssync.StoreReport{Store: r.Store, Sent: r.Sent, Received: r.Received,
			SentCommits: r.SentCommits, ReceivedCommits: r.ReceivedCommits, Merged: r.Merged, Refused: r.Refused, Problems: r.Problems, Offline: r.Offline, HeldBy: r.HeldBy, Held: r.Held, Pushed: r.Pushed})
		shown := s.app.shownStoreName(ctx, r.Store) // B-9: the store's own name (storeshown.go)
		pushed := folderPushed(r.Pushed, r.Sent, cloneSent[storeCloneKey(r.Store, s.app.storeURLOf(ctx, r.Store))])
		line := storeSyncLine(shown, storeStatusOf(r.Sent, r.Received, r.Merged, r.Refused, pushed), syncCounts{r.Sent, r.SentCommits, r.Received, r.ReceivedCommits, r.Merged, r.Refused}, pushed)
		if r.Unattached {
			line += syncUnattachedFolder
		}
		if r.Held != "" {
			line = "store " + shown + ": " + r.Held // its push did not run; the refusal is the other folder's line
		}
		var next string
		if r.Offline {
			// set-url and --restore take the store, never one folder of it
			storeName, _, _ := strings.Cut(shown, targetSep)
			u := s.app.storeURLOf(ctx, r.Store)
			head := "store " + shown + ": "
			if cause, n, gone := storeGone(r.OfflineDetail, u, storeName); gone {
				// H-11 / G-16: not offline (it said so with exit 0 forever):
				// the address holds no store now (the FAILED shape of a
				// store line outside a repo, N-15)
				if goneSaid[u] {
					line = head + "FAILED: the same store: see above"
				} else {
					goneSaid[u] = true
					line = head + "FAILED: " + cause
					next = n
					goneNames, goneNexts = append(goneNames, storeName), append(goneNexts, storeName+": "+n)
				}
			} else {
				if r.Sent == 0 && r.Received == 0 && r.Merged == 0 {
					// N-15: the "offline: <cause>" shape of a store line
					// outside a repo, never "up to date" for a store that
					// did not answer
					line = head + opssync.StoreStatusOffline + ": " + storeOfflineCause(u, r.OfflineDetail) + "; local changes kept, sent on the next sync"
				} else {
					line += storeOfflineNote(r.Sent, u, r.OfflineDetail)
				}
				out.Data.Stores[len(out.Data.Stores)-1].URL = repourl.Mask(u)
				out.Data.Stores[len(out.Data.Stores)-1].Error = storeOfflineCause(u, r.OfflineDetail)
			}
		}
		out.Lines = append(out.Lines, line)
		if next != "" {
			out.Lines = append(out.Lines, "  next: "+next)
		}
		for _, p := range r.Problems {
			out.Lines = append(out.Lines, "  "+p)
		}
	}
	// R-5/R-6/R-18: changes no one with authority made, read as their
	// last accepted version, named here (rosterhold.go)
	out.Lines = append(out.Lines, s.app.heldSyncLines(ctx)...)
	out.Lines = append(out.Lines, res.Notes...) // branch layers landed or trashed (BRANCH_LAYERS 9, 10)
	if serr == nil {
		if f := opsrun.FlushOutbox(ctx, s.app); f.Written > 0 || f.Failed != "" {
			if f.Failed != "" {
				s.app.notice("! " + f.Failed)
			}
			if f.Written > 0 {
				out.Lines = append(out.Lines, fmt.Sprintf("lore sync: wrote %d queued run time span(s)", f.Written))
			}
		}
	}
	if serr == nil && len(goneNames) > 0 {
		serr = errs.New(errs.StoreUnreachable, fmt.Sprintf("sync: %d store(s) answer that there is no repository at their address: %s (the rest synced; local changes are kept)", len(goneNames), strings.Join(goneNames, ", "))).
			WithHint(strings.Join(goneNexts, "; "))
	}
	if serr == nil && stale > 0 {
		// the sync ran, but row files of this clone wait on a person: say
		// so in the exit code too, as a refused store row does (E_STALE)
		serr = errs.New(errs.Stale, fmt.Sprintf("sync: %d row(s) hold a conflict to settle", stale)).
			WithHint("both edits are kept: `lore sync conflicts` shows both versions, `lore sync resolve --id <n> --take kept|other` settles one (or edit the row)")
	}
	if serr != nil {
		for _, l := range out.Lines {
			s.app.notice(l)
		}
		return out, serr
	}
	if len(out.Lines) == 0 && len(out.Data.Stores) == 0 {
		out.Lines = []string{syncAlreadyInSync} // v0.1.17: nothing landed, no store, nothing written
		if line := s.app.uncommittedDataLine(ctx); line != "" {
			// E-10: setting up the clone (its repo row, the actor copy,
			// repo_id in _meta.json) wrote files; "already in sync" alone
			// hid them until a git merge refused to overwrite them
			out.Lines = []string{line}
		}
	}
	// devco O-12: a done task whose merge lore can no longer find (its
	// commit squash-merged without the Lore-Task trailer, the branch
	// deleted) is named here, after the push that usually follows such a
	// merge, with the command that settles it.
	if lost, lerr := s.app.DoctorLostMerges(ctx); lerr == nil {
		for _, f := range lost {
			line := "lore: " + f.Message + "; next: " + f.Fix
			out.Lines = append(out.Lines, line)
			out.Data.Warnings = append(out.Data.Warnings, line)
		}
	}
	return out, nil
}

// uncommittedDataLine names the files of the repo's .lore/data that differ
// from the commit (changed or new), with the commit to run; "" when there
// are none, outside a code repo, or when git cannot say.
func (a *App) uncommittedDataLine(ctx context.Context) string {
	paths, ok := a.uncommittedData(ctx)
	if !ok || len(paths) == 0 {
		return ""
	}
	data := rowfs.LoreDir + "/" + rowfs.DataDir
	shown := paths
	more := ""
	if len(shown) > uncommittedShown {
		shown, more = shown[:uncommittedShown], fmt.Sprintf(" and %d more", len(paths)-uncommittedShown)
	}
	return fmt.Sprintf("· in sync; %d file(s) in %s wait for your commit (%s%s): git add %s && git commit", len(paths), data, strings.Join(shown, ", "), more, data)
}

// uncommittedData is the files (slash paths from the repo top) of the
// repo's .lore/data that differ from the commit, changed or new. ok is
// false outside a code repo with a lore folder or when git cannot say.
func (a *App) uncommittedData(ctx context.Context) (paths []string, ok bool) {
	top, err := a.repoTop(ctx)
	if err != nil || !loreFolderAt(top) {
		return nil, false
	}
	data := rowfs.LoreDir + "/" + rowfs.DataDir
	st, err := gitRun{dir: top, args: []string{"-c", "core.quotePath=false", "status", "--porcelain", "--untracked-files=all", "--", data}}.output(ctx)
	if err != nil {
		return nil, false
	}
	for _, l := range strings.Split(string(st), "\n") {
		if len(l) > 3 {
			paths = append(paths, l[3:])
		}
	}
	return paths, true
}

// uncommittedShown is how many uncommitted .lore/data files the sync line
// names before "and N more".
const uncommittedShown = 5

// syncMarkedFileWhy is the why of a marked row file in the sync report.
const syncMarkedFileWhy = "git conflict markers; it is left out of every read until settled (lore sync conflicts, lore sync resolve)"

// syncLiveConflictWhy is the why of a live row with conflict markers in
// the sync report.
const syncLiveConflictWhy = "both sides edited a text field; both kept with conflict markers"

// LiveMetaConflictWhy is the cause of a live row whose strict metadata
// key two machines set differently (METADATA 3.12): both values are kept
// in the entry, the row never lands until a person picks one (D-220).
const LiveMetaConflictWhy = "a strict metadata key was set to different values on two machines; both values are kept in the entry and the row does not land until one is chosen"

// LiveMetaConflictNext is the command that settles a strict metadata key
// conflict on row id: set the key to the value it should hold.
func LiveMetaConflictNext(id, key string) string {
	return "lore meta set --id " + id + " --pair " + key + "=<value>"
}

// liveConflictCause is the cause and next step of one live conflict in
// the sync report: a strict metadata key clash names the key and `lore
// meta set` (D-220); markers in a text field name `lore sync resolve`.
func liveConflictCause(c opssync.Conflict) (why, next string) {
	resolve := fmt.Sprintf("lore sync resolve --id %d --take kept|other", c.ID)
	text := false
	key := ""
	for _, f := range c.Fields {
		if k, ok := strings.CutPrefix(f, "meta."); ok {
			if key == "" {
				key = k
			}
			continue
		}
		text = true
	}
	if key != "" && !text {
		return LiveMetaConflictWhy, LiveMetaConflictNext(c.RowID, key) + ", or " + resolve
	}
	return syncLiveConflictWhy, resolve
}

// syncUnattachedFolder follows the line of a store folder this repo does
// not attach whose rows the clone's sync carried (W-5).
const syncUnattachedFolder = " (a folder of the store this repo does not attach)"

// syncAlreadyInSync is v0.1.17's line for a `lore sync` with nothing to do
// (stdout only; the --json report is unchanged).
const syncAlreadyInSync = "· already in sync"

// coreHost reports whether the sync host leaves the core's own parts
// (stores, row files) to the core (opssync.CoreStores: cmd/lore).
func (s syncRows) coreHost() bool {
	c, ok := s.Env.(opssync.CoreStores)
	return ok && c.CoreStores()
}

// branchConflictKept is the kept field of a marked row file: both sides
// are in the file until one is chosen.
const branchConflictKept = "both"

// SyncConflicts implements opssync.Env: the host's conflicts, then the
// repo's row files a git merge left with conflict markers (G-SYNC-2),
// then the rows of attached stores both sides changed (B2-2,
// sync_store_conflicts.go), numbered in that order.
func (s syncRows) SyncConflicts(ctx context.Context, in opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out, err := s.hostConflicts(ctx, in)
	if err != nil || !s.coreHost() {
		return out, err
	}
	files, ferr := s.app.BranchConflicts(ctx)
	if ferr != nil {
		return out, ferr
	}
	stores, serr := s.app.StoreConflicts(ctx)
	if serr != nil {
		return out, serr
	}
	if len(files) == 0 && len(stores) == 0 {
		s.app.saveConflictList(ctx, out.Data) // a reprinted list replaces the last one, empty too
		return out, nil
	}
	if out.Count == 0 {
		out.Lines = nil // drop "· no conflicts"
	}
	for _, f := range files {
		n := len(out.Data) + 1
		out.Data = append(out.Data, opssync.Conflict{ID: int64(n), Table: f.Table, RowID: f.RowID, Kept: branchConflictKept})
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (%s: git conflict markers; settle with lore sync resolve --id %d --take kept|other, or fix the file)",
			n, f.Table, f.RowID, branchConflictKept, f.Place, n))
	}
	storeConflictNumbered(&out, stores, func(name string) string { return s.app.shownStoreName(ctx, name) })
	out.Count = len(out.Data)
	s.app.saveConflictList(ctx, out.Data)
	return out, nil
}

// SyncResolve implements opssync.Env: a number past the host's conflicts
// names a marked row file, then a store row (SyncConflicts' numbering);
// kept keeps the first side of each block (git's HEAD) or this machine's
// store copy, other the second or the store remote's copy.
func (s syncRows) SyncResolve(ctx context.Context, in opssync.ResolveIn) (opssync.DoneOut, error) {
	if !s.coreHost() {
		return s.Env.SyncResolve(ctx, in)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(in.ID), "#"))
	if err != nil || n < 1 {
		return s.Env.SyncResolve(ctx, in) // the host's own refusal
	}
	shown := n // the number the person typed, from the printed list
	if n, err = s.listedNumber(ctx, n); err != nil {
		return opssync.DoneOut{}, err
	}
	in.ID = strconv.Itoa(n)
	host, err := s.hostConflicts(ctx, opssync.ConflictsIn{})
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if n <= host.Count {
		return s.Env.SyncResolve(ctx, in)
	}
	take := strings.TrimSpace(in.Take)
	if take != resolveKept && take != resolveOther {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "--take must be kept or other").
			WithHint("kept keeps the first side of each clash (this branch, or this machine's copy of a store row), other the second (the branch merged in, or the store's copy)")
	}
	files, err := s.app.BranchConflicts(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	i := n - host.Count - 1
	if i >= len(files) {
		return s.resolveStore(ctx, shown, i-len(files), take)
	}
	f := files[i]
	if err = s.app.ResolveBranchConflict(ctx, f, take == resolveOther); err != nil {
		return opssync.DoneOut{}, err
	}
	done := fmt.Sprintf("resolved #%d %s/%s (took %s); commit %s", shown, f.Table, f.RowID, take, f.Place)
	return opssync.DoneOut{Done: done, Lines: []string{"✓ " + done}}, nil
}

// resolveStore settles conflict #n, the i-th store conflict, and syncs.
func (s syncRows) resolveStore(ctx context.Context, n, i int, take string) (opssync.DoneOut, error) {
	stores, err := s.app.StoreConflicts(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if i >= len(stores) {
		return opssync.DoneOut{}, errs.New(errs.NotFound, fmt.Sprintf("no open conflict #%d", n)).WithHint("lore sync conflicts lists them")
	}
	c := stores[i]
	if err := s.app.ResolveStoreConflict(ctx, c, take == resolveOther); err != nil {
		return opssync.DoneOut{}, err
	}
	side := "this machine's copy"
	if take == resolveOther {
		side = "the store's copy"
	}
	done := fmt.Sprintf("resolved #%d %s/%s in store %s (took %s: %s) and synced", n, c.Table, c.ID, c.Store, take, side)
	lines := []string{"✓ " + done}
	if left, lerr := s.app.StoreConflicts(ctx); lerr == nil && len(left) > 0 {
		// devco O-6: the other rows still wait; the ids stay the ones the
		// last `lore sync conflicts` printed
		lines = append(lines, fmt.Sprintf(resolveStillOpen, len(left)))
	}
	return opssync.DoneOut{Done: done, Items: []string{c.ID.String()}, Lines: lines}, nil
}

// resolveStillOpen follows a store resolve that left other rows of the
// store open; %d is how many.
const resolveStillOpen = "· %d other conflict(s) still open and held back (the other edits of the store were sent); settle each with lore sync resolve --id <n> --take kept|other, using the ids lore sync conflicts printed"

// listedNumber maps conflict #n of the list `lore sync conflicts` last
// printed here to its number in the list as it stands now: a resolve
// renumbers the rest, and `resolve --id 2` from the printed list used to
// fail E_NOT_FOUND or settle another row (devco O-6). A row of that list
// that is no longer open is refused by name; with no printed list (or n
// past it) n stands.
func (s syncRows) listedNumber(ctx context.Context, n int) (int, error) {
	listed := s.app.conflictList(ctx)
	if n > len(listed) {
		return n, nil
	}
	want := listed[n-1]
	now, err := s.currentConflicts(ctx)
	if err != nil {
		return n, err
	}
	for _, c := range now {
		if c.RowID == want {
			return int(c.ID), nil
		}
	}
	return n, errs.New(errs.NotFound, fmt.Sprintf("conflict #%d (%s) is not open any more: it was settled", n, want)).
		WithHint("lore sync conflicts lists what is still open, with new ids")
}

// currentConflicts is SyncConflicts' list without saving it as printed.
func (s syncRows) currentConflicts(ctx context.Context) ([]opssync.Conflict, error) {
	out, err := s.hostConflicts(ctx, opssync.ConflictsIn{})
	if err != nil {
		return nil, err
	}
	all := slices.Clone(out.Data)
	files, err := s.app.BranchConflicts(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		all = append(all, opssync.Conflict{ID: int64(len(all) + 1), Table: f.Table, RowID: f.RowID})
	}
	stores, err := s.app.StoreConflicts(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range stores {
		all = append(all, opssync.Conflict{ID: int64(len(all) + 1), Table: string(c.Table), RowID: c.ID.String()})
	}
	return all, nil
}

// Explain implements opssync.Env: the host's answer, and for a row the
// repo does not hold, the attached store's copy (and its conflict).
func (s syncRows) Explain(ctx context.Context, in opssync.ExplainIn) (opssync.ExplainOut, error) {
	out, err := s.Env.Explain(ctx, in)
	if err != nil && !errs.Has(err, errs.Unsupported) && !s.inStoreFolder(err) {
		return out, err
	}
	if out.Winner != "" {
		return out, err
	}
	if withStore, ok := s.app.explainStore(ctx, out, strings.TrimSpace(in.ID)); ok {
		withStore.Items = []string{strings.TrimSpace(in.ID)}
		return withStore, nil
	}
	return out, err
}

// hostConflicts is the host's conflict list; a core host with no live
// rows here (not a git repo, LORE_LIVE=0: E_UNSUPPORTED) has none, so the
// marked row files are still listed.
func (s syncRows) hostConflicts(ctx context.Context, in opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out, err := s.Env.SyncConflicts(ctx, in)
	if err != nil && s.coreHost() && (fallback(err) || s.inStoreFolder(err)) {
		return opssync.ConflictsOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindConflicts, Data: []opssync.Conflict{},
			Lines: []string{"· no conflicts"}}, nil
	}
	return out, err
}

// inStoreFolder reports whether err is the host's "not a lore project"
// for a command run inside a store's checkout: no repo, no live rows, but
// the store's own conflicts and refusals are this folder's to show (lore's
// hints send people there, B2-10).
func (s syncRows) inStoreFolder(err error) bool {
	return errs.Has(err, errs.NotProjectRoot) && inStore(s.app.opts.RepoDir)
}

// --take values of sync resolve (G-SYNC-1, G-SYNC-2).
const (
	resolveKept  = "kept"
	resolveOther = "other"
)

// storeRefusalNext is the next step of a push the store's rules refused.
const storeRefusalNext = "the commits wait in the local clone: fix what the store's push rule names (or ask its owner), then run `lore sync`"

// storeConflictNext is the next step of a push held back because both
// sides changed the same rows (E_STALE, E_RESOURCE_CONFLICT): no push rule
// is involved, the person settles the rows.
const storeConflictNext = "the commits wait in the local clone: someone else changed the same row(s); `lore sync conflicts` shows both versions of each, `lore sync resolve --id <n> --take kept|other` settles one (or edit the row here so both agree, then run `lore sync`)"

// storeRefusalNextFor is the next step for a refusal code.
func storeRefusalNextFor(code string) string {
	switch errs.Code(code) {
	case errs.Stale, errs.ResourceConflict:
		return storeConflictNext
	}
	return storeRefusalNext
}

// SyncStatus implements opssync.Env: the host's status, then, for a host
// that leaves the stores to the core (opssync.CoreStores), every store
// whose last push the remote refused (storepush_refused.go) with its rows,
// reason and next step. A local state that cannot be read adds a note line
// instead (status still answers).
func (s syncRows) SyncStatus(ctx context.Context, in opssync.StatusIn) (opssync.StatusOut, error) {
	if s.coreHost() && s.app.setupStatusHere(ctx) {
		return s.app.setupSyncStatus(ctx)
	}
	out, err := s.Env.SyncStatus(ctx, in)
	if err != nil && s.coreHost() && s.inStoreFolder(err) {
		out, err = opssync.StatusOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindStatus}, nil
	}
	if err != nil {
		return out, err
	}
	if !s.coreHost() {
		return out, nil
	}
	// The same source as doctor's [sync] section (the host keeps no
	// lore.db, so these were always false/0 before).
	out.Data.DataDirPresent, out.Data.TrackedFiles = s.app.repoDataFacts(ctx)
	if files, ferr := s.app.BranchConflicts(ctx); ferr == nil { // G-SYNC-2; an unreadable folder is doctor's to report
		out.Data.OpenConflicts += len(files)
		out.Lines = append(out.Lines, fmt.Sprintf("conflicted files: %d", len(files)))
		for _, f := range files {
			out.Lines = append(out.Lines, "  "+f.Place+" (git conflict markers; lore sync conflicts)")
		}
	}
	s.app.syncStoreStates(ctx, &out)
	refused, rerr := s.app.StorePushRefusals(ctx)
	if rerr != nil {
		out.Lines = append(out.Lines, "store pushes:     unknown (local state: "+rerr.Error()+")")
		return out, nil
	}
	own, held := splitHeldRecords(refused)
	out.Lines = append(out.Lines, fmt.Sprintf("refused pushes:   %d", len(own)))
	for _, r := range own {
		rows := "the whole push"
		if len(r.Rows) > 0 {
			rows = strconv.Itoa(len(r.Rows)) + " row(s): " + strings.Join(r.Rows, ", ")
		}
		out.Data.StoreRefusals = append(out.Data.StoreRefusals, opssync.StoreRefusal{Store: r.Store, Code: r.Code, Reason: r.Reason,
			Rows: r.Rows, At: r.At, Next: storeRefusalNextFor(r.Code)})
		// B-9: the store's own name, as sync run's line names it (storeshown.go)
		out.Lines = append(out.Lines, "  store "+s.app.shownStoreName(ctx, r.Store)+": "+r.Code+" at "+r.At+": "+r.Reason,
			"    waiting: "+rows, "    next: "+storeRefusalNextFor(r.Code))
	}
	if len(held) == 0 {
		return out, nil // the v0.1.18 lines, unchanged when nothing is held
	}
	out.Lines = append(out.Lines, fmt.Sprintf("held pushes:      %d", len(held)))
	for _, r := range held {
		on := heldOn(r, own)
		next := heldNext(on)
		out.Data.StoreHeld = append(out.Data.StoreHeld, opssync.StoreHeld{Store: r.Store, HeldBy: opsstores.HeldNames(on), Code: r.Code,
			Rows: r.Rows, At: r.At, Next: next})
		out.Lines = append(out.Lines, "  store "+s.app.shownStoreName(ctx, r.Store)+": "+opsstores.HeldLines(on), "    next: "+next)
	}
	return out, nil
}

// splitHeldRecords separates the refusal records of this machine into the
// stores whose own push was refused and the held ones (HeldBy set: another
// folder's refusal stopped their push; owner decision 7).
func splitHeldRecords(all []StorePushRefusal) (own, held []StorePushRefusal) {
	for _, r := range all {
		if r.HeldBy != "" {
			held = append(held, r)
		} else {
			own = append(own, r)
		}
	}
	return own, held
}

// heldOn is every store folder whose refusal holds r, each under its
// attached name (the own refusal record that names one of its rows: the
// folder that holds the row reports it), else the folder itself; a record
// held by one folder (HeldOn empty) names HeldBy with r's rows.
func heldOn(r StorePushRefusal, own []StorePushRefusal) []opsstores.HeldOn {
	folders := r.HeldOn
	if len(folders) == 0 {
		folders = []HeldFolder{{Folder: r.HeldBy, Code: r.Code, Rows: r.Rows}}
	}
	out := make([]opsstores.HeldOn, 0, len(folders))
	for _, f := range folders {
		out = append(out, opsstores.HeldOn{By: heldName(f.Folder, f.Rows, own), Code: f.Code, Rows: f.Rows})
	}
	return out
}

// heldName is the attached name of the store folder that refused rows:
// the own refusal record naming one of them, else HeldFolderName.
func heldName(folder string, rows []string, own []StorePushRefusal) string {
	for _, o := range own {
		for _, id := range rows {
			if slices.Contains(o.Rows, id) {
				return o.Store
			}
		}
	}
	return opsstores.HeldFolderName(folder)
}

// heldNext is the next step of a held push: the holding folders' refusals
// settle it, and this folder's commits go out with the same sync.
func heldNext(on []opsstores.HeldOn) string {
	names := make([]string, 0, len(on))
	for _, h := range on {
		names = append(names, h.By)
	}
	what := "the row in "
	if len(on) > 1 {
		what = "the rows in "
	}
	return "nothing to settle here: settle " + what + opsstores.JoinAnd(names) + ", then run `lore sync`; this folder's commits go with it"
}

// syncPurgePointer is the one stderr line of the old `lore sync purge`
// spelling (FA-DEL-4, CLI_REFERENCE F71).
const syncPurgePointer = "note: lore sync purge is now lore trash purge --archived-before; this spelling goes away in the next release"

// SyncPurge implements opssync.Env: the old spelling of `lore trash purge
// --archived-before <cutoff>` (FA-DEL-4), answered by that operation
// itself, so its --confirm / --dry-run rules and refusals are the one path.
func (s syncRows) SyncPurge(ctx context.Context, in opssync.PurgeIn) (opssync.DoneOut, error) {
	out, err := s.Env.SyncPurge(ctx, in)
	if !fallback(err) {
		return out, err
	}
	s.app.notice(syncPurgePointer)
	if strings.TrimSpace(in.ArchivedBefore) == "" {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "sync purge: --archived-before is required").
			WithHint("lore trash purge --archived-before <date|age> --confirm")
	}
	e, ok := s.app.reg.Lookup(opstrash.OpPurge)
	if !ok {
		return opssync.DoneOut{}, errs.New(errs.Internal, "sync purge: "+opstrash.OpPurge+" is not registered")
	}
	body, err := json.Marshal(opstrash.PurgeIn{ArchivedBefore: in.ArchivedBefore, Confirm: in.Confirm, DryRun: in.DryRun})
	if err != nil {
		return opssync.DoneOut{}, errs.Wrap(errs.Internal, "sync purge: encode", err)
	}
	pin, err := e.Decode(op.JSONDecoder{Body: body})
	if err != nil {
		return opssync.DoneOut{}, err
	}
	// The outer run (sync.purge, write access) already passed the Runner's
	// checks; the purge op keeps its own confirm rule (no --confirm and no
	// --dry-run is E_NEEDS_CONFIRM, nothing purged).
	res, err := e.Invoke(ctx, pin)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	text, _ := res.Text()
	text = strings.TrimSpace(text)
	return opssync.DoneOut{Done: text, Lines: []string{text}}, nil
}

// notice hands one line to Options.Notice (stderr on the CLI).
func (a *App) notice(msg string) {
	if a.opts.Notice != nil {
		a.opts.Notice(msg)
	}
}

// MergeRows folds row drop into row keep of table (`lore sync merge-rows
// <table> --keep <id> --drop <id>`): engine.MergeRows with the App as the
// write path, so every edit and the delete pass the App's guards.
func (a *App) MergeRows(ctx context.Context, table, keep, drop string) (engine.MergeResult, error) {
	if keep == "" || drop == "" {
		return engine.MergeResult{}, errs.New(errs.InvalidInput, "merge-rows: --keep and --drop are required")
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return engine.MergeResult{}, err
	}
	t := row.Table(table)
	def, ok := eng.Registry().Table(t)
	if !ok {
		return engine.MergeResult{}, errs.New(errs.InvalidInput, "merge-rows: "+strconv.Quote(table)+" is not a lore table").WithHint("name the table of both rows (memories, rules, tags, …)")
	}
	keepID, err := parseRowID(def, keep)
	if err != nil {
		return engine.MergeResult{}, err
	}
	dropID, err := parseRowID(def, drop)
	if err != nil {
		return engine.MergeResult{}, err
	}
	return eng.MergeRows(ctx, a, t, keepID, dropID, false)
}

// parseRowID parses id and checks that it is an id of def's table.
func parseRowID(def schema.TableDef, id string) (ids.AnyID, error) {
	v, err := ids.ParseAny(id)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.InvalidID, "merge-rows: "+strconv.Quote(id), err)
	}
	if v.Prefix() != def.Prefix {
		return ids.AnyID{}, errs.New(errs.InvalidInput, "merge-rows: "+id+" is not an id of "+string(def.Name)).
			WithHint("name the table both ids belong to (" + string(def.Prefix) + "_… ids are " + string(def.Name) + ")")
	}
	return v, nil
}

// FixResult is what FixProjects did (or would do on a dry run).
type FixResult struct {
	Keep   ids.AnyID
	Merged []engine.MergeResult
}

// FixProjects folds every extra live project row of the repo store into
// one (`lore sync fix-projects [--keep <id>] [--dry-run]`; E1, PROV
// FA-AGENT-10: one project per repo data folder). keep defaults to the
// project _meta.json names, else the smallest id. All folds share one bat_
// batch, so `lore trash restore <bat_…>` brings every folded project back;
// _meta.json's project_id is set to keep afterwards (a checkout without
// the file is left as it is).
func (a *App) FixProjects(ctx context.Context, keep string, dryRun bool) (FixResult, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return FixResult{}, err
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return FixResult{}, err
	}
	all := make([]string, 0, len(rows))
	for _, r := range rows {
		all = append(all, r.ID.String())
	}
	slices.Sort(all)
	if len(all) < 2 {
		return FixResult{}, nil
	}
	if keep == "" {
		if top, terr := a.knownRepoTop(ctx); terr == nil {
			if pid, ok := repoMetaProjectID(top); ok && slices.Contains(all, pid) {
				keep = pid
			}
		}
		if keep == "" {
			keep = all[0]
		}
	}
	if !slices.Contains(all, keep) {
		return FixResult{}, errs.New(errs.NotFound, "fix-projects: "+keep+" is not a live project of this repository").WithHint("see `lore project list`")
	}
	keepID, err := ids.ParseAny(keep)
	if err != nil {
		return FixResult{}, errs.Wrap(errs.InvalidID, "fix-projects: "+strconv.Quote(keep), err)
	}
	fx := FixResult{Keep: keepID}
	for _, s := range all {
		if s == keep {
			continue
		}
		drop, perr := ids.ParseAny(s)
		if perr != nil {
			return fx, errs.Wrap(errs.InvalidID, "fix-projects: "+strconv.Quote(s), perr)
		}
		res, merr := eng.MergeRows(ctx, a, schema.TableProjects, keepID, drop, dryRun)
		if merr != nil {
			return fx, merr
		}
		if !res.Batch.IsZero() {
			ctx = engine.WithBatch(ctx, engine.Batch{ID: res.Batch, Kind: engine.BatchKindMerge})
		}
		fx.Merged = append(fx.Merged, res)
	}
	if dryRun {
		return fx, nil
	}
	pid, err := ids.As[ids.Project](keepID)
	if err != nil {
		return fx, err
	}
	if werr := a.WriteRepoProjectID(ctx, pid); werr != nil && !errs.Has(werr, errs.NotFound) {
		return fx, werr
	}
	return fx, nil
}

// storeCloneKey names the clone a store report's folder lives in: the
// folders of one store (agency/client, agency/org) share its url and so
// one clone and one push; a store with no url known is its own clone.
func storeCloneKey(name, url string) string {
	if url == "" {
		return "name:" + name
	}
	return "url:" + url
}

// storeCloneSent is the rows each clone's push sent, summed over the
// folders that share it (storeCloneKey): one push carries every folder's
// rows, so a folder that sent none of them did not push "commits with no
// note rows" when a sibling folder's rows rode the same push (after-land
// 29e8d071: agency/client said so beside agency/org's "sent 2").
func storeCloneSent(reps []opsstores.Report, urlOf func(string) string) map[string]int {
	sent := map[string]int{}
	for _, r := range reps {
		sent[storeCloneKey(r.Store, urlOf(r.Store))] += r.Sent
	}
	return sent
}

// folderPushed says whether a store folder's line reports a push: the
// folders of one store share one clone and one push, so a folder that sent
// no rows while a sibling's rows rode that push is "up to date", not
// "pushed (sent 0, …)" (devco N-15: agency/client said "pushed" with every
// count 0 beside agency/org's "sent 2"). A push that carried no row of any
// folder is still a push (store links, org settings: devco J-4).
func folderPushed(clonePushed bool, sent, cloneSent int) bool {
	return clonePushed && (sent > 0 || cloneSent == 0)
}

// storeStatusOf is the opssync.StoreStatus* word of a store's sync from
// its counts: what moved, refused when the store only refused rows (they
// stay here), or up to date.
func storeStatusOf(sent, received, merged, refused int, pushed bool) string {
	out := sent > 0 || pushed
	in := received > 0 || merged > 0
	switch {
	case refused > 0 && !out && !in:
		return storeStatusRefused
	case out && in:
		return opssync.StoreStatusBoth
	case out:
		return opssync.StoreStatusPushed
	case in:
		return opssync.StoreStatusPulled
	}
	return opssync.StoreStatusUpToDate
}

// storeStatusRefused is a store line's status when the store refused this
// clone's rows and nothing else moved.
const storeStatusRefused = "refused"

// storeCountsLine is the one shape of a store's sync line, in a code repo,
// a client repo and outside any repo alike (devco N-15: the two paths
// printed "sent 1, received 0, merged 0, refused 0" and "pushed (sent 3,
// received 0, merged 0)"): "store <name>: up to date", or the status and
// every count.
func storeCountsLine(name, status string, c syncCounts) string {
	if status == opssync.StoreStatusUpToDate {
		return "store " + name + ": " + status
	}
	sent, received := orRows(c.sentCommits, c.sent), orRows(c.receivedCommits, c.received)
	return fmt.Sprintf("store %s: %s (sent %d, received %d, merged %d, refused %d)", name, status, sent, received, c.merged, c.refused) + rowsNote(c)
}

// syncCounts are a store line's numbers: rows sent, received, merged and
// refused, and the commits git counted going out and coming in.
type syncCounts struct {
	sent, sentCommits, received, receivedCommits, merged, refused int
}

// orRows is the commits git counted, or the rows when it counted none (a
// backend without commits): a store line's "sent" and "received" are
// commits, counted with rev-list over the tips before and after, so they
// agree with git and with the push line (devco D-648: "sent 5" after 3
// commits went out, "received 4" when 8 commits arrived).
func orRows(commits, rows int) int {
	if commits > 0 {
		return commits
	}
	return rows
}

// rowsNote names the rows behind the commit counts when they differ
// ("; rows: 5 sent, 4 received"); nothing when they agree.
func rowsNote(c syncCounts) string {
	var parts []string
	if c.sentCommits > 0 && c.sent != c.sentCommits {
		parts = append(parts, strconv.Itoa(c.sent)+" sent")
	}
	if c.receivedCommits > 0 && c.received != c.receivedCommits {
		parts = append(parts, strconv.Itoa(c.received)+" received")
	}
	if len(parts) == 0 {
		return ""
	}
	return "; rows: " + strings.Join(parts, ", ")
}

// syncNoNoteRows is the tail of a store line whose push carried no note
// row: the commits that went out are store links or org settings.
const syncNoNoteRows = " with no note rows (store links or org settings)"

// syncPushedNoRows is that note when git gave no commit count (a backend
// without commits).
const syncPushedNoRows = "; pushed local commit(s)" + syncNoNoteRows

// storeSyncLine is a store's sync line once the sync knows whether its
// push ran. One rule for every push (devco D-698, owner use pass 5 weak
// 4): "sent" is the commits that went out, as the stderr push line
// "lore: pushed store <s>: commit … (2 commits)" and --json sent_commits
// count them, and the note rows are named beside it. A push whose commits
// carry no note row (a store link, the org file, a set-url move) says so
// with the count: "pushed (sent 2, …); pushed 2 local commits with no note
// rows (…)", never "sent 0" under a push line of 2 commits, and never the
// bare "rows: 0 sent" without the reason (devco D-527). Every path that
// prints a store line (code repo, setup or home folder, waiting org store
// push) goes through here.
func storeSyncLine(name, status string, c syncCounts, pushed bool) string {
	if !pushed || c.sent != 0 || status == opssync.StoreStatusUpToDate {
		return storeCountsLine(name, status, c)
	}
	if c.sentCommits <= 0 {
		return storeCountsLine(name, status, c) + syncPushedNoRows
	}
	n := c.sentCommits
	c.sent = n // the note below names the rows: no "rows: 0 sent" beside it
	return storeCountsLine(name, status, c) + "; pushed " + plural(n, "local commit") + syncNoNoteRows
}
