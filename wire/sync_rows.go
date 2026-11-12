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

	"github.com/thesatellite-ai/lore/engine"
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
	s.app.notice(fmt.Sprintf(syncTrashPointer, "lore trash restore --local "+in.ID))
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

// syncStoreOffline follows a store line whose remote could not be reached.
const syncStoreOffline = " (offline: local changes kept, sent on the next sync)"

// SyncRun implements opssync.Env: the host's pass, then, for a host that
// leaves them to the core (opssync.CoreStores: cmd/lore's live-only host),
// every attached store is exchanged (spec 6.1, STORE_SERVER_SPEC 5.3:
// `lore sync` delivers the store writes made offline) and the run time
// queued while a store was unreachable is written (TIME 11.9), as the old
// binary's host does. Refused rows end in E_STALE (both sides kept); the
// report lines are printed first on stderr, since a failed run has no
// stdout.
func (s syncRows) SyncRun(ctx context.Context, in opssync.RunIn) (opssync.RunOut, error) {
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
	if files, ferr := s.app.BranchConflicts(ctx); ferr == nil {
		for _, f := range files {
			out.Data.Conflicts = append(out.Data.Conflicts, opssync.ConflictNote{Path: f.Place, Kept: branchConflictKept, Why: syncMarkedFileWhy})
			out.Lines = append(out.Lines, "conflict "+f.Place+": "+syncMarkedFileWhy)
		}
	}
	res, serr := s.app.SyncStores(ctx, "")
	for _, r := range res.Stores {
		out.Data.Stores = append(out.Data.Stores, opssync.StoreReport{Store: r.Store, Sent: r.Sent, Received: r.Received,
			Merged: r.Merged, Refused: r.Refused, Problems: r.Problems, Offline: r.Offline, HeldBy: r.HeldBy, Held: r.Held})
		line := fmt.Sprintf("store %s: sent %d, received %d, merged %d, refused %d", r.Store, r.Sent, r.Received, r.Merged, r.Refused)
		if r.Held != "" {
			line = "store " + r.Store + ": " + r.Held // its push did not run; the refusal is the other folder's line
		}
		if r.Offline {
			line += syncStoreOffline
		}
		out.Lines = append(out.Lines, line)
		for _, p := range r.Problems {
			out.Lines = append(out.Lines, "  "+p)
		}
	}
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
	if serr != nil {
		for _, l := range out.Lines {
			s.app.notice(l)
		}
		return out, serr
	}
	if len(out.Lines) == 0 && len(out.Data.Stores) == 0 {
		out.Lines = []string{syncAlreadyInSync} // v0.1.17: nothing landed, no store, nothing written
	}
	return out, nil
}

// syncMarkedFileWhy is the why of a marked row file in the sync report.
const syncMarkedFileWhy = "git conflict markers; it is left out of every read until settled (lore sync conflicts, lore sync resolve)"

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
// numbered after them.
func (s syncRows) SyncConflicts(ctx context.Context, in opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out, err := s.hostConflicts(ctx, in)
	if err != nil || !s.coreHost() {
		return out, err
	}
	files, ferr := s.app.BranchConflicts(ctx)
	if ferr != nil {
		return out, ferr
	}
	if len(files) == 0 {
		return out, nil
	}
	if out.Count == 0 {
		out.Lines = nil // drop "· no conflicts"
	}
	for _, f := range files {
		n := len(out.Data) + 1
		out.Data = append(out.Data, opssync.Conflict{ID: int64(n), Table: f.Table, RowID: f.RowID, Kept: branchConflictKept})
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (%s: git conflict markers; settle with lore sync resolve %d --take kept|other, or fix the file)",
			n, f.Table, f.RowID, branchConflictKept, f.Place, n))
	}
	out.Count = len(out.Data)
	return out, nil
}

// SyncResolve implements opssync.Env: a number past the host's conflicts
// names a marked row file (SyncConflicts' numbering); kept keeps the first
// side of each block (git's HEAD), other the second.
func (s syncRows) SyncResolve(ctx context.Context, in opssync.ResolveIn) (opssync.DoneOut, error) {
	if !s.coreHost() {
		return s.Env.SyncResolve(ctx, in)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(in.ID), "#"))
	if err != nil || n < 1 {
		return s.Env.SyncResolve(ctx, in) // the host's own refusal
	}
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
			WithHint("kept keeps the first side of each clash (this branch), other the second (the branch merged in)")
	}
	files, err := s.app.BranchConflicts(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	i := n - host.Count - 1
	if i >= len(files) {
		return opssync.DoneOut{}, errs.New(errs.NotFound, fmt.Sprintf("no open conflict #%d", n)).WithHint("lore sync conflicts lists them")
	}
	f := files[i]
	if err = s.app.ResolveBranchConflict(ctx, f, take == resolveOther); err != nil {
		return opssync.DoneOut{}, err
	}
	done := fmt.Sprintf("resolved #%d %s/%s (took %s); commit %s", n, f.Table, f.RowID, take, f.Place)
	return opssync.DoneOut{Done: done, Lines: []string{"✓ " + done}}, nil
}

// hostConflicts is the host's conflict list; a core host with no live
// rows here (not a git repo, LORE_LIVE=0: E_UNSUPPORTED) has none, so the
// marked row files are still listed.
func (s syncRows) hostConflicts(ctx context.Context, in opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out, err := s.Env.SyncConflicts(ctx, in)
	if err != nil && s.coreHost() && fallback(err) {
		return opssync.ConflictsOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindConflicts, Data: []opssync.Conflict{},
			Lines: []string{"· no conflicts"}}, nil
	}
	return out, err
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
const storeConflictNext = "the commits wait in the local clone: someone else changed the same row(s); compare each row here (lore show <id>) with theirs, edit it here so both agree, then run `lore sync`"

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
	out, err := s.Env.SyncStatus(ctx, in)
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
		out.Lines = append(out.Lines, "  store "+r.Store+": "+r.Code+" at "+r.At+": "+r.Reason,
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
		out.Lines = append(out.Lines, "  store "+r.Store+": "+opsstores.HeldLines(on), "    next: "+next)
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
