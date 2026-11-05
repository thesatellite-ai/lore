// sync_host.go — this binary's repo-sync host for the core's sync, land and
// explain operations (ops/sync, PASS3 ONE-PATH, P3-SYNC-1).
//
// The commands are registry operations now (CLI, `lore api` and the
// reference come from ops/sync); the work is still v0.1.17's lsync pass,
// git wiring and live session, so this host runs it and returns typed
// outputs: the --json data of v0.1.17 and the stdout lines it printed.
// Progress and warnings keep going to stderr as before.
//
//	ops/sync op ─► wire.Options.Sync = syncHost{} ─► lsync / gitsetup / live
//
// A host method that fails after printing part of its report writes the
// stdout lines first (failWith), as v0.1.17 printed them before its error.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/surface/cli"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// syncHost implements opssync.Env over the v0.1.17 sync code.
type syncHost struct{}

var _ opssync.Env = syncHost{}

// syncTrashOldPath is the old spelling `lore sync trash` (DQ1, OV31-a).
const syncTrashOldPath = "lore sync trash"

// lines splits printed text into lines (no trailing empty line).
func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// failWith prints the report lines a command had produced and returns its
// error (as a core error, so surface/cli prints code, message and hint).
func failWith(ls []string, err error) error {
	for _, l := range ls {
		fmt.Println(l)
	}
	return syncErr(err)
}

// convert copies a v0.1.17 value into its ops/sync twin through JSON: the
// twins have the same keys by construction (ops/sync/types.go).
func convert[From, To opssyncData](from From) (To, error) {
	var to To
	b, err := json.Marshal(from)
	if err != nil {
		return to, errcodes.New(errcodes.Internal, "encode sync report").WithCause(err)
	}
	if err := json.Unmarshal(b, &to); err != nil {
		return to, errcodes.New(errcodes.Internal, "decode sync report").WithCause(err)
	}
	return to, nil
}

// opssyncData is the closed set of values convert moves (old and new).
type opssyncData interface {
	syncReportJSON | opssync.RunData | lsync.Report |
		[]lsync.ConflictRecord | []opssync.Conflict | []lsync.TrashRecord | []opssync.TrashEntry |
		[]lsync.FileError | []opssync.FileError | ciMergeResult | opssync.CIMergeData |
		syncActionResult | opssync.ActionData
}

// openSync opens the Mode A project of the working directory without the
// automatic pass (the operation decides what runs).
func openSync() (*syncTarget, error) { return openSyncTarget(&commonFlags{}) }

// SyncRun is `lore sync`: deliver store writes, run the repo pass, sync the
// 0.1.18 stores, flush the time outbox, then report (CLI_REFERENCE 2.9).
func (syncHost) SyncRun(ctx context.Context, _ opssync.RunIn) (opssync.RunOut, error) {
	out := opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport}
	if err := maybeMaterialize(ctx, &commonFlags{}); err != nil {
		return out, syncErr(materializeError(err))
	}
	// Store writes made offline wait in the bare clones; `lore sync` is the
	// explicit "deliver everything now" (§5.3).
	syncStores(ctx)
	t, err := openSync()
	if err != nil {
		return out, syncErr(err)
	}
	defer t.close()
	rep, err := runCatchUpPass(ctx, t.db, t.root, t.dataDir)
	if err != nil {
		return out, syncErr(errcodes.New(errcodes.Internal, "sync pass").WithCause(err))
	}
	core, coreSyncErr := syncCoreStores(ctx)
	var ls []string
	// TIME 11.9 (FA-LTASK-1): spans queued while a store was unreachable.
	if n := flushTimeOutbox(ctx); n > 0 {
		ls = append(ls, fmt.Sprintf("lore sync: wrote %d queued run time span(s)", n))
	}
	if coreSyncErr == nil {
		rerenderPrivateQuiet(ctx, t.root)
	}
	if out.Data, err = convert[syncReportJSON, opssync.RunData](syncReportJSON{Report: rep, Stores: core.Stores}); err != nil {
		return out, syncErr(err)
	}
	printSyncReport(rep) // stderr, as before
	ls = append(ls, coreSyncReportLines(core.Stores)...)
	for _, h := range scopeRenameHints(ctx, t.root) { // 6.2: a folder renamed in git → lore scope move (never run)
		ls = append(ls, style.Muted("· ")+h)
	}
	if !rep.Changed() && len(rep.Errors) == 0 && len(rep.Conflicts) == 0 && len(core.Stores) == 0 {
		ls = append(ls, style.Muted("· already in sync"))
	}
	out.Lines = ls
	if len(rep.Errors) > 0 {
		return out, failWith(ls, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%d file(s) could not be synced", len(rep.Errors))).
			WithHint("fix the files listed above (see `lore sync status`)"))
	}
	if coreSyncErr != nil {
		return out, failWith(ls, coreSyncErr)
	}
	if refused := storesRefused(core.Stores); refused > 0 {
		// Nothing was lost: the refused rows stay local until resolved
		// (spec 10.4); E_STALE so the exit is non-zero.
		return out, failWith(ls, coreSyncStale(refused))
	}
	return out, nil
}

// SyncStatus is `lore sync status`: the lore.db sync state, uncommitted
// files, metadata quarantine and registry caps, and the live rows (side
// effect 13).
func (syncHost) SyncStatus(ctx context.Context, _ opssync.StatusIn) (opssync.StatusOut, error) {
	out := opssync.StatusOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindStatus}
	t, err := openSync()
	if err != nil {
		return out, syncErr(err)
	}
	defer t.close()
	st, err := lsync.ReadStatus(ctx, t.db, t.dataDir)
	if err != nil {
		return out, syncErr(errcodes.New(errcodes.Internal, "read sync status").WithCause(err))
	}
	uncommitted, gitErr := gitsetup.Uncommitted(ctx, t.root, filepath.Join(projresolve.MarkerDir, lsync.DataDirName))
	quar := metaQuarantineLines(ctx) // METADATA 3.13 "counted"
	regs := metaRegistryLines(ctx)   // FA-META-11: registries past the key cap
	fileErrs, err := convert[[]lsync.FileError, []opssync.FileError](st.Errors)
	if err != nil {
		return out, syncErr(err)
	}
	out.Data = opssync.StatusData{Baselined: st.Baselined, DataDirPresent: st.DataDirPresent, TrackedFiles: st.TrackedFiles,
		PendingExports: st.PendingExports, OpenConflicts: st.OpenConflicts, TrashRows: st.TrashRows, Errors: fileErrs,
		Uncommitted: uncommitted, MetaQuarantined: quar, MetaRegistriesCap: regs}
	if gitErr != nil && !errors.Is(gitErr, gitsetup.ErrNotRepo) {
		out.Data.GitError = gitErr.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "baselined:        %v\n", st.Baselined)
	fmt.Fprintf(&b, "data dir present: %v\n", st.DataDirPresent)
	fmt.Fprintf(&b, "tracked files:    %d\n", st.TrackedFiles)
	fmt.Fprintf(&b, "pending exports:  %d\n", st.PendingExports)
	fmt.Fprintf(&b, "open conflicts:   %d\n", st.OpenConflicts)
	fmt.Fprintf(&b, "trash rows:       %d\n", st.TrashRows)
	fmt.Fprintf(&b, "uncommitted:      %d\n", len(uncommitted))
	fmt.Fprintf(&b, "meta quarantined: %d\n", len(quar))
	for _, q := range quar {
		fmt.Fprintln(&b, style.Error("  "+q))
	}
	for _, r := range regs {
		fmt.Fprintln(&b, style.Error("meta registry: "+r))
	}
	for _, e := range st.Errors {
		fmt.Fprintln(&b, style.Error(fmt.Sprintf("  %s %s: %s", e.Kind, e.Path, e.Detail)))
	}
	printLiveStatus(ctx, &b, t.root)
	out.Lines = lines(b.String())
	return out, nil
}

// SyncConflicts is `lore sync conflicts [--all]`.
func (syncHost) SyncConflicts(ctx context.Context, in opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out := opssync.ConflictsOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindConflicts}
	t, err := openSync()
	if err != nil {
		return out, syncErr(err)
	}
	defer t.close()
	cs, err := lsync.ListConflicts(ctx, t.db, in.All)
	if err != nil {
		return out, syncErr(errcodes.New(errcodes.Internal, "list conflicts").WithCause(err))
	}
	if out.Data, err = convert[[]lsync.ConflictRecord, []opssync.Conflict](cs); err != nil {
		return out, syncErr(err)
	}
	if out.Data == nil {
		out.Data = []opssync.Conflict{} // `data: []`, never null (R25 #3)
	}
	out.Count = len(cs)
	if len(cs) == 0 {
		out.Lines = []string{style.Muted("· no conflicts")}
		return out, nil
	}
	for _, c := range cs {
		state := "open"
		if c.Resolved {
			state = "resolved"
		}
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s %s at %s", c.ID, c.Table, c.RowID, c.Kept, state, c.At))
		out.Lines = append(out.Lines, lines(indent(c.OtherDoc, "    "))...)
	}
	return out, nil
}

// resolveErr maps the lsync errors of resolve / restore / merge-rows.
func resolveErr(err error, what, notFoundHint string) error {
	switch {
	case errors.Is(err, lsync.ErrCoreOwned):
		// a63c6f8: the message names the way that works for a core row.
		return errcodes.New(errcodes.InvalidInput, err.Error())
	case errors.Is(err, lsync.ErrNotFound):
		e := errcodes.New(errcodes.NotFound, err.Error())
		if notFoundHint != "" {
			e = e.WithHint(notFoundHint)
		}
		return e
	}
	return errcodes.New(errcodes.Internal, what).WithCause(err)
}

// passThen runs the sync pass after a change and returns the done line.
func passThen(ctx context.Context, t *syncTarget, done string, items ...string) (opssync.DoneOut, error) {
	rep, err := runSyncPass(ctx, t.db, t.root, t.dataDir)
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "sync pass").WithCause(err))
	}
	// PASS3-A-4: a row purged by this command is not "kept" in the local trash.
	printSyncReport(withoutPurgedThisRun(rep))
	return opssync.DoneOut{Done: done, Items: items, Lines: []string{style.Success("✓") + " " + done}}, nil
}

// SyncResolve is `lore sync resolve <n> --take kept|other`.
func (syncHost) SyncResolve(ctx context.Context, in opssync.ResolveIn) (opssync.DoneOut, error) {
	id, err := strconv.ParseInt(in.ID, 10, 64)
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "conflict id must be a number").WithCause(err))
	}
	choice := lsync.ResolveTake(in.Take)
	if choice != lsync.TakeKept && choice != lsync.TakeOther {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "--take must be kept or other"))
	}
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	if err := t.catchUp(ctx); err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	if err := lsync.ResolveConflict(ctx, t.db, id, choice); err != nil {
		return opssync.DoneOut{}, syncErr(resolveErr(err, "resolve conflict", "see `lore sync conflicts`"))
	}
	return passThen(ctx, t, fmt.Sprintf("conflict #%d resolved (%s)", id, choice), in.ID)
}

// syncTrashNote prints the one-release note of the old spelling (DQ1,
// OV31-a, CLI_REFERENCE 2.15).
func syncTrashNote(now string) {
	fmt.Fprintf(os.Stderr, "note: %s is now %s; this spelling goes away in the next release\n", syncTrashOldPath, now)
}

// SyncTrashList is the old spelling `lore sync trash` of `lore trash --local`.
func (syncHost) SyncTrashList(ctx context.Context, _ opssync.TrashListIn) (opssync.TrashListOut, error) {
	out := opssync.TrashListOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindTrash}
	syncTrashNote("lore trash --local")
	t, err := openSync()
	if err != nil {
		return out, syncErr(err)
	}
	defer t.close()
	rows, err := lsync.ListTrash(ctx, t.db)
	if err != nil {
		return out, syncErr(errcodes.New(errcodes.Internal, "list trash").WithCause(err))
	}
	if out.Data, err = convert[[]lsync.TrashRecord, []opssync.TrashEntry](rows); err != nil {
		return out, syncErr(err)
	}
	if out.Data == nil {
		out.Data = []opssync.TrashEntry{}
	}
	out.Count = len(rows)
	if len(rows) == 0 {
		out.Lines = []string{style.Muted("· trash is empty")}
	}
	for _, r := range rows {
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s  %s  (%s)", r.ID, r.Table, r.RowID, r.At, r.Reason))
	}
	return out, nil
}

// SyncTrashRestore is the old spelling `lore sync trash restore <n>`.
func (syncHost) SyncTrashRestore(ctx context.Context, in opssync.TrashRestoreIn) (opssync.DoneOut, error) {
	syncTrashNote("lore trash restore --local " + in.ID)
	id, err := strconv.ParseInt(in.ID, 10, 64)
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "trash id must be a number").WithCause(err))
	}
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	if err := t.catchUp(ctx); err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	if err := lsync.RestoreTrash(ctx, t.db, id); err != nil {
		return opssync.DoneOut{}, syncErr(resolveErr(err, "restore from trash", "see `lore trash --local`"))
	}
	return passThen(ctx, t, fmt.Sprintf("restored trash entry #%d", id), in.ID)
}

// SyncPurge is the old spelling of `lore trash purge --archived-before`
// (FA-DEL-4): core-owned rows go through the core's archived purge, the
// rest through lsync.
func (syncHost) SyncPurge(ctx context.Context, in opssync.PurgeIn) (opssync.DoneOut, error) {
	fmt.Fprintln(os.Stderr, "note: lore sync purge is now lore trash purge --archived-before; this spelling goes away in the next release")
	cutoff, err := parseCutoff(in.ArchivedBefore, cliClock().Now())
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "bad --archived-before").WithCause(err))
	}
	if !in.Confirm && !in.DryRun {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "purge is durable; pass --confirm (or --dry-run to preview)"))
	}
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	// The dry run counts what --confirm would purge, so it catches up first.
	if catchErr := t.catchUp(ctx); catchErr != nil {
		return opssync.DoneOut{}, syncErr(catchErr)
	}
	if coreApp == nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted"))
	}
	coreRes, err := coreApp.PurgeArchived(ctx, cutoff, in.DryRun)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if in.DryRun {
		n, countErr := lsync.CountArchivedBefore(ctx, t.db, cutoff)
		if countErr != nil {
			return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "count archived rows").WithCause(countErr))
		}
		done := fmt.Sprintf("would purge %d archived row(s) archived before %s", n+len(coreRes.Rows), cutoff.Format(time.RFC3339))
		return opssync.DoneOut{Done: done, Lines: []string{done}}, nil
	}
	purged, err := lsync.PurgeArchived(ctx, t.db, t.dataDir, cutoff)
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "purge").WithCause(err))
	}
	for _, r := range coreRes.Rows {
		purged = append(purged, string(r.Row.Table)+"/"+r.Row.ID.String())
	}
	return passThen(ctx, t, fmt.Sprintf("purged %d row(s)", len(purged)), purged...)
}

// SyncFixProjects is `lore sync fix-projects [--keep] [--dry-run]`.
func (syncHost) SyncFixProjects(ctx context.Context, in opssync.FixProjectsIn) (opssync.DoneOut, error) {
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	if in.DryRun {
		var n int
		if qErr := t.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n); qErr != nil {
			return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "count projects").WithCause(qErr))
		}
		done := fmt.Sprintf("%d project row(s); %d would be merged", n, max(n-1, 0))
		return opssync.DoneOut{Done: done, Lines: []string{done}}, nil
	}
	if catchErr := t.catchUp(ctx); catchErr != nil {
		return opssync.DoneOut{}, syncErr(catchErr)
	}
	kept, merged, err := lsync.FixProjects(ctx, t.db, t.dataDir, in.Keep)
	if err != nil {
		if errors.Is(err, lsync.ErrNotFound) {
			return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.NotFound, err.Error()).WithHint("see `lore project list`"))
		}
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "fix projects").WithCause(err))
	}
	if len(merged) == 0 {
		return opssync.DoneOut{Done: "only one project; nothing to fix", Lines: []string{style.Muted("· only one project; nothing to fix")}}, nil
	}
	return passThen(ctx, t, fmt.Sprintf("kept %s, merged %s", kept, strings.Join(merged, ", ")), merged...)
}

// SyncPeek is `lore sync peek <ref> [table]`: rows of another git ref,
// nothing imported.
func (syncHost) SyncPeek(ctx context.Context, in opssync.PeekIn) (opssync.PeekOut, error) {
	out := opssync.PeekOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindPeek, Data: []opssync.PeekRow{}}
	t, err := openSync()
	if err != nil {
		return out, syncErr(err)
	}
	defer t.close()
	base := filepath.ToSlash(filepath.Join(projresolve.MarkerDir, lsync.DataDirName))
	prefix := base
	if in.Table != "" {
		prefix += "/" + in.Table
	}
	files, err := gitsetup.ReadTree(ctx, t.root, in.Ref, prefix)
	if err != nil {
		return out, syncErr(errcodes.New(errcodes.InvalidInput, "read "+in.Ref).WithCause(err).
			WithHint("is it a valid ref? try `git fetch` first for remote branches"))
	}
	for p, b := range files {
		table, file := filepath.Split(strings.TrimPrefix(p, base+"/"))
		if table == "" {
			continue // _meta.json / _purged.json
		}
		table = strings.TrimSuffix(table, "/")
		doc, derr := lsync.DecodeRowFile(b)
		if derr != nil {
			out.Data = append(out.Data, opssync.PeekRow{Table: table, ID: strings.TrimSuffix(file, ".json"), Summary: "(unreadable: " + derr.Error() + ")"})
			continue
		}
		id, _ := doc["id"].(string)
		out.Data = append(out.Data, opssync.PeekRow{Table: table, ID: id, Summary: rowSummary(doc)})
	}
	sort.Slice(out.Data, func(i, j int) bool {
		if out.Data[i].Table != out.Data[j].Table {
			return out.Data[i].Table < out.Data[j].Table
		}
		return out.Data[i].ID < out.Data[j].ID
	})
	out.Count = len(out.Data)
	if len(out.Data) == 0 {
		out.Lines = []string{style.Muted("· no lore rows at " + in.Ref)}
	}
	for _, r := range out.Data {
		out.Lines = append(out.Lines, fmt.Sprintf("%-18s %s  %s", r.Table, r.ID, r.Summary))
	}
	return out, nil
}

// promoteSHALen is how much of the promote commit id is printed.
const promoteSHALen = 12

// SyncPromote is `lore sync promote <id>... --to <branch> [--dry-run]`.
func (syncHost) SyncPromote(ctx context.Context, in opssync.PromoteIn) (opssync.DoneOut, error) {
	if in.Branch == "" {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "--to <branch> is required"))
	}
	if len(in.IDs) == 0 {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "name at least one row id"))
	}
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	// Export pending changes first so the files are current.
	if _, passErr := runSyncPass(ctx, t.db, t.root, t.dataDir); passErr != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "sync pass").WithCause(passErr))
	}
	files := map[string][]byte{}
	var paths []string
	for _, id := range in.IDs {
		rel, b, findErr := findRowFile(t.dataDir, id)
		if findErr != nil {
			return opssync.DoneOut{}, syncErr(findErr)
		}
		p := filepath.ToSlash(filepath.Join(projresolve.MarkerDir, lsync.DataDirName, rel))
		files[p] = b
		paths = append(paths, p)
	}
	if in.DryRun {
		out := opssync.DoneOut{Items: paths}
		for _, p := range paths {
			out.Lines = append(out.Lines, fmt.Sprintf("would commit %s onto %s", p, in.Branch))
		}
		return out, nil
	}
	sha, err := gitsetup.CommitFiles(ctx, t.root, in.Branch, "lore: promote "+strings.Join(in.IDs, ", "), files)
	if err != nil {
		if errors.Is(err, gitsetup.ErrCurrentBranch) {
			return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, err.Error()))
		}
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "promote").WithCause(err))
	}
	done := fmt.Sprintf("committed %d file(s) onto %s (%s) — not pushed", len(files), in.Branch, sha[:min(promoteSHALen, len(sha))])
	return opssync.DoneOut{Done: done, Items: paths, Lines: []string{style.Success("✓") + " " + done}}, nil
}

// SyncMergeRows is `lore sync merge-rows <table> --keep <id> --drop <id>`.
func (syncHost) SyncMergeRows(ctx context.Context, in opssync.MergeRowsIn) (opssync.DoneOut, error) {
	if in.Keep == "" || in.Drop == "" {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "--keep and --drop are required"))
	}
	t, err := openSync()
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	defer t.close()
	if err := t.catchUp(ctx); err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	if err := lsync.MergeRows(ctx, t.db, in.Table, in.Keep, in.Drop); err != nil {
		if errors.Is(err, lsync.ErrCoreOwned) || errors.Is(err, lsync.ErrNotFound) {
			return opssync.DoneOut{}, syncErr(resolveErr(err, "merge rows", ""))
		}
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "merge rows").WithCause(err))
	}
	return passThen(ctx, t, fmt.Sprintf("merged %s into %s", in.Drop, in.Keep), in.Drop, in.Keep)
}

// SyncCIMerge is `lore sync ci-merge --base <ref> [--dry-run]`; every
// outcome exits 0 (LORE_SYNC 9.1).
func (syncHost) SyncCIMerge(ctx context.Context, in opssync.CIMergeIn) (opssync.CIMergeOut, error) {
	out := opssync.CIMergeOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindCIMerge}
	if strings.TrimSpace(in.Base) == "" {
		return out, syncErr(errcodes.New(errcodes.InvalidInput, "--base is required").WithHint("e.g. --base origin/main"))
	}
	res, err := runCIMerge(ctx, ".", in.Base, in.DryRun)
	if err != nil {
		return out, syncErr(err)
	}
	if out.Data, err = convert[ciMergeResult, opssync.CIMergeData](res); err != nil {
		return out, syncErr(err)
	}
	var b strings.Builder
	printCIMerge(&b, res, in.DryRun)
	out.Lines = lines(b.String())
	return out, nil
}

// SyncInstallAction is `lore sync install-action`.
func (syncHost) SyncInstallAction(ctx context.Context, in opssync.InstallActionIn) (opssync.ActionOut, error) {
	out := opssync.ActionOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindAction}
	o := syncActionOptions{branches: in.Branches, auto: in.Auto, manualSet: in.Manual, loreVersion: in.LoreVersion, dryRun: in.DryRun, force: in.Force}
	if in.LoreVersion != "" {
		o.pin = syncActionPinFixed // --lore-version pins a version lore never changes
	}
	res, err := installSyncAction(ctx, ".", o)
	if err != nil {
		return out, syncErr(err)
	}
	if out.Data, err = convert[syncActionResult, opssync.ActionData](res); err != nil {
		return out, syncErr(err)
	}
	var b strings.Builder
	printSyncAction(&b, res)
	out.Lines = lines(b.String())
	return out, nil
}

// Land is `lore land [id] [--all --kind --mission --dry-run]` (LORE_TEAM F5).
func (syncHost) Land(ctx context.Context, in opssync.LandIn) (opssync.DoneOut, error) {
	if in.ID == "" && !in.All && in.Kind == "" && in.Mission == "" {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, "name a row id, or use --all, --kind or --mission"))
	}
	s, folder, err := liveHere(liveCobraCtx(ctx))
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	var args []string
	if in.ID != "" {
		args = []string{in.ID}
	}
	sel := landSelector(args, in.Kind, in.Mission)
	if in.DryRun {
		var b strings.Builder
		if planErr := s.printLandPlan(ctx, &b, folder, sel); planErr != nil {
			return opssync.DoneOut{}, syncErr(planErr)
		}
		return opssync.DoneOut{Lines: lines(b.String())}, nil
	}
	out, err := s.landPass(ctx, folder, sel, false)
	if err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "land").WithCause(err))
	}
	if len(out.Conflicts) > 0 {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.InvalidInput, strings.Join(out.Conflicts, ", ")+" has a conflict (conflict markers in a text field); it cannot land").
			WithHint("settle it with `lore sync conflicts` / `lore sync resolve`, then land again"))
	}
	if out.Status == "" {
		done := fmt.Sprintf("nothing to land: %s already holds every named row", out.Branch)
		return opssync.DoneOut{Done: done, Lines: []string{done}}, nil
	}
	// v0.1.17 printed the landing outcome on stderr (like autoLand).
	if err := printLandOutcome(os.Stderr, out); err != nil {
		return opssync.DoneOut{}, syncErr(errcodes.New(errcodes.Internal, "print land outcome").WithCause(err))
	}
	return opssync.DoneOut{Done: string(out.Status), Items: out.Conflicts}, nil
}

// Explain is `lore explain <id>`: every copy of a row and which one wins
// (LORE_TEAM_SPEC 9, F1 "Debug").
func (syncHost) Explain(ctx context.Context, in opssync.ExplainIn) (opssync.DoneOut, error) {
	s, folder, err := liveHere(liveCobraCtx(ctx))
	if err != nil {
		return opssync.DoneOut{}, syncErr(err)
	}
	var b strings.Builder
	if err := s.explain(ctx, &b, folder, in.ID); err != nil {
		return opssync.DoneOut{}, failWith(lines(b.String()), err)
	}
	return opssync.DoneOut{Items: []string{in.ID}, Lines: lines(b.String())}, nil
}

// liveCobraCtx is a command carrying ctx, for liveHere (it reads only the
// context of the command).
func liveCobraCtx(ctx context.Context) *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(ctx)
	return c
}

// coreSyncReportLines is printCoreSyncReport's lines: one per 0.1.18 store
// and one indented line per problem.
func coreSyncReportLines(stores []opsstores.Report) []string {
	var ls []string
	for _, r := range stores {
		line := "store " + r.Store + ": sent " + strconv.Itoa(r.Sent) +
			", received " + strconv.Itoa(r.Received) +
			", merged " + strconv.Itoa(r.Merged) +
			", refused " + strconv.Itoa(r.Refused)
		if r.Offline {
			line += style.Muted(" (offline: local changes kept, sent on the next sync)")
		}
		ls = append(ls, line)
		for _, p := range r.Problems {
			ls = append(ls, "  "+p)
		}
	}
	return ls
}

// storesRefused counts the rows the 0.1.18 stores refused.
func storesRefused(stores []opsstores.Report) int {
	n := 0
	for _, r := range stores {
		n += r.Refused
	}
	return n
}

// attachSyncHost adjusts the generated sync, land and explain commands
// (mountCore calls it after cli.Mount): bare `lore sync`, `lore land`,
// `lore explain <id>` and `lore sync trash` run their default verb, as
// v0.1.17 spelled them, and the hidden `lore sync hook` entry point of the
// installed git hook blocks hangs under the generated sync group (git
// plumbing, not an operation). A missing group (core not mounted) adds
// nothing.
func attachSyncHost(root *cobra.Command) {
	for _, noun := range []string{opssync.Noun, opssync.NounLand, opssync.NounExplain} {
		if g, _, err := root.Find([]string{noun}); err == nil && g.Name() == noun {
			indentGenerated(g)
		}
	}
	if g := defaultVerb(root, opssync.Noun, opssync.VerbRun); g != nil {
		g.AddCommand(newSyncHookCommand())
		defaultVerb(g, opssync.NounTrash, opssync.VerbList)
		groupHelp(g, syncGroupLong)
	}
	defaultVerb(root, opssync.NounLand, opssync.VerbRun)
	defaultVerb(root, opssync.NounExplain, opssync.VerbRun)
}

// syncGroupLong is the help of the `lore sync` group (v0.1.17's text, the
// dropped verbs taken out: p3/dropped.md).
const syncGroupLong = `sync reconciles .lore/lore.db with .lore/data/ — one JSON file per shared row,
committed to git, so lore knowledge follows branches and reaches main when a
PR merges.

A pass runs automatically before and after every lore command; run it by
hand after editing files yourself, or to see what it does:

  lore sync                 run a pass now and print what changed
  lore sync status          what is pending, conflicted, or broken
  lore sync conflicts       versions kept aside when both sides edited a row
  lore sync trash           rows removed because their file disappeared

Environment: LORE_SYNC=0 disables sync; LORE_SYNC_GIT=0 leaves git config,
.gitattributes and hooks alone; LORE_SYNC_QUIET=1 silences progress lines.`

// groupHelp makes `--help`/`-h` on a defaultVerb group print the group's
// own help (long text plus its subcommands), as v0.1.17's `lore sync
// --help` did, instead of the default verb's: defaultVerb turns flag
// parsing off, so the help flag arrives as an argument. An unknown verb
// answers "unknown command" as v0.1.17's cobra group did.
func groupHelp(g *cobra.Command, long string) {
	g.Long = long
	run := g.RunE
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if slices.ContainsFunc(args, func(a string) bool { return a == "-h" || a == "--help" }) {
			return cmd.Help()
		}
		// The default verb takes no arguments, so a word here is a verb
		// this group does not have (a dropped one: p3/dropped.md).
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			return errcodes.New(errcodes.InvalidInput, `unknown command "`+args[0]+`" for "`+cmd.CommandPath()+`"`).
				WithHint("see `" + cmd.CommandPath() + " --help`")
		}
		return run(cmd, args)
	}
}

// indentGenerated gives every generated command under g v0.1.17's indented
// --json output (writeIndented), as addCompatFlags does for the core
// nouns: v0.1.17 scripts and tests read `"kind": "sync.status"`. The rest
// of addCompatFlags (--db, --read-only, the core sync before the command)
// does not apply: the sync host runs its own pass.
func indentGenerated(g *cobra.Command) {
	for _, c := range g.Commands() {
		indentGenerated(c)
		if !cli.Generated(c) || c.RunE == nil {
			continue
		}
		inner := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON); !asJSON {
				return inner(cmd, args)
			}
			var buf bytes.Buffer
			out := cmd.OutOrStdout()
			cmd.SetOut(&buf)
			err := inner(cmd, args)
			cmd.SetOut(out)
			return errors.Join(err, hosthooks.WriteIndented(out, buf.Bytes()))
		}
	}
}

// syncErr converts an lsync/live error into the core's typed error like
// coreErr, but keeps the wrapped cause in the message ("read main: git
// ls-tree …: Not a valid object name"), as the v0.1.17 sync commands
// printed it: the cause is what tells the user which ref or file failed.
func syncErr(err error) error {
	var old *errcodes.CLIError
	if errors.As(err, &old) && old.Wrapped != nil {
		flat := errcodes.New(old.Code, old.Message+": "+old.Wrapped.Error())
		if old.Hint != "" {
			flat = flat.WithHint(old.Hint)
		}
		return coreErr(flat)
	}
	return coreErr(err)
}
