package host

// live_sync.go — the `lore sync` / `lore land` host of cmd/lore
// (wire.Options.Sync, opssync.Env) for live rows only (P3-MODEB-3):
//
//	sync.run ─▶ (Around: FetchAlways prepare: fetch + integrate)
//	         ─▶ push the waiting live writes ─▶ landing pass (F5)
//	         ─▶ (Around finish: push what landing changed on the live ref)
//	land.run ─▶ the same landing pass
//
// v0.1.17's lore.db catch-up pass and store clone exchange belong to the
// old binary's sync host. Conflicts and resolve work on the live rows'
// conflict markers (G-SYNC-1); merge-rows, fix-projects, the trash and
// purge pointers fall back to the core (wire/sync_rows.go).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/conflictpick"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/wire"
)

// syncPassLive names the pass of a live-only sync in the sync.report data.
const syncPassLive = "live"

// liveSync implements opssync.Env over LiveHook's runs.
type liveSync struct{ h *liveHost }

var (
	_ opssync.Env        = liveSync{}
	_ opssync.CoreStores = liveSync{}
)

// CoreStores implements opssync.CoreStores: this host syncs live rows only,
// so `lore sync` exchanges the attached stores and flushes queued run time
// through the core after it (wire syncRows.SyncRun).
func (liveSync) CoreStores() bool { return true }

// unsupported is the answer of a sync verb this host has not ported.
func unsupported(verb string) error {
	return errs.New(errs.Unsupported, "lore sync "+verb+" is not available in this binary yet")
}

// here is the run's session and folder of the cwd project.
func (l liveSync) here(ctx context.Context) (*liveSession, string, error) {
	root := liveProjectRoot(l.h.cfg.WorkDir)
	if root == "" {
		return nil, "", errs.New(errs.NotProjectRoot, "not inside a lore project")
	}
	r := runFrom(ctx)
	if r == nil || liveDisabled() {
		return nil, "", nil
	}
	top, folder := livePlace(root)
	return r.session(ctx, top), folder, nil
}

// syncLiveWaiting is the sync report line while live writes could not be
// pushed (offline, or the remote refused refs/lore/*): they stay in this
// clone and the next sync sends them, so sync must never say
// "· already in sync" then.
// The %s is why (liveSession.waitWhy): offline, no repository at the
// remote, refused credentials, refused refs/lore/*.
const syncLiveWaiting = "live: %d write(s) not pushed (%s): kept here, sent on the next sync"

// syncWaitWhyDefault is syncLiveWaiting's reason when the push named none.
const syncWaitWhyDefault = "offline, or the remote refused refs/lore/*"

// pushAndLand pushes the waiting live writes, then lands what is due.
// waiting is the number of live writes still not on the remote after the
// push (0 when every write was delivered or there is no remote).
func (l liveSync) pushAndLand(ctx context.Context) (lines []string, waiting int, err error) {
	s, folder, err := l.here(ctx)
	if err != nil || s == nil {
		return nil, 0, err
	}
	if s.blocked != nil {
		// G-SYNC-3: the remote live ref was deleted or rewritten; nothing
		// can be delivered, so sync refuses as every live write does
		// (never "· already in sync"), naming the backup and the restore.
		return nil, 0, rewrittenError(errs.ReadOnly, s.blocked)
	}
	s.syncReport = true // this report's own line says what waits (syncLiveWaiting)
	pushed := s.push(ctx)
	s.syncReport = false
	if !pushed || s.ref.Remote == nil {
		if s.blocked != nil { // the push found it rewritten
			return nil, 0, rewrittenError(errs.ReadOnly, s.blocked)
		}
		if !pushed && s.ref.Remote != nil {
			// The push could not deliver (offline, refused): the writes
			// wait here; say so rather than "already in sync".
			n := s.waiting()
			why := s.waitWhy
			if why == "" {
				why = syncWaitWhyDefault
			}
			return []string{fmt.Sprintf(syncLiveWaiting, n, why)}, n, nil
		}
		return nil, 0, nil
	}
	out, err := s.landPass(ctx, folder, nil, true)
	if err != nil {
		return nil, 0, landErr(err)
	}
	return landLines(out), 0, nil
}

// SyncRun implements opssync.Env.
func (l liveSync) SyncRun(ctx context.Context, _ opssync.RunIn) (opssync.RunOut, error) {
	if wire.OldRowsRead(ctx) {
		// D7: nothing is synced into a repo lore 0.1.17 wrote until lore fix
		// (or whose _meta.json cannot be read, until it is fixed)
		line := wire.OldRowsSyncLine
		if wd, werr := os.Getwd(); werr == nil {
			line = wire.OldRowsSyncLineAt(ctx, wd)
		}
		return opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport,
			Data: opssync.RunData{Pass: syncPassLive}, Lines: []string{line}}, nil
	}
	if l.h.cfg.RepoAddresses != nil {
		l.h.cfg.RepoAddresses(ctx) // PROVENANCE_DESIGN 10.3: records and notices only, never fails the sync
	}
	// G-DECIDE-9: fail closed while git still ignores row files after the
	// re-include block (teammates would silently miss those rows).
	if err := l.refuseIgnoredData(ctx); err != nil {
		return opssync.RunOut{}, err
	}
	lines, waiting, err := l.pushAndLand(ctx)
	if err != nil {
		return opssync.RunOut{}, err
	}
	return opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport,
		Data: opssync.RunData{Pass: syncPassLive, LivePending: waiting}, Lines: lines}, nil
}

// refuseIgnoredData is E_SYNC_DATA_IGNORED while git ignores row files of
// the cwd project after the re-include block (LORE_SYNC_GIT=0: checked
// without editing .gitignore).
func (l liveSync) refuseIgnoredData(ctx context.Context) error {
	root := liveProjectRoot(l.h.cfg.WorkDir)
	if root == "" {
		return nil
	}
	still, err := stillIgnoredData(ctx, root, os.Getenv(EnvSyncGit) != envOff)
	if err != nil {
		return err
	}
	if len(still) > 0 {
		return dataIgnoredError(still)
	}
	return nil
}

// Land implements opssync.Env (v0.1.17 `lore land`): the named rows (an
// id, --all, --kind, --mission) land now; --dry-run prints the plan.
func (l liveSync) Land(ctx context.Context, in opssync.LandIn) (opssync.DoneOut, error) {
	if in.ID == "" && !in.All && in.Kind == "" && in.Mission == "" {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "name a row id, or use --all, --kind or --mission")
	}
	if err := landBadID(in.ID); err != nil {
		return opssync.DoneOut{}, err
	}
	s, folder, err := l.here(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if s == nil {
		return opssync.DoneOut{}, errs.New(errs.Unsupported, liveUnsupported).WithHint(liveUnsupportedHint)
	}
	sel := landSelector(in.ID, in.Kind, in.Mission)
	if in.DryRun {
		lines, perr := s.planLines(ctx, folder, sel)
		if perr != nil {
			return opssync.DoneOut{}, errs.Wrap(errs.Internal, "land plan", perr)
		}
		return opssync.DoneOut{Lines: lines}, nil
	}
	if s.blocked != nil {
		return opssync.DoneOut{}, rewrittenError(errs.ReadOnly, s.blocked) // G-SYNC-3
	}
	if !s.push(ctx) {
		if s.blocked != nil {
			return opssync.DoneOut{}, rewrittenError(errs.ReadOnly, s.blocked)
		}
		return opssync.DoneOut{}, errs.New(errs.Offline, "the live ref could not be pushed; nothing was landed").WithHint("run `lore land` again when the remote is reachable")
	}
	out, err := s.landPass(ctx, folder, sel, false)
	if err != nil {
		return opssync.DoneOut{}, landErr(err)
	}
	if len(out.Conflicts) > 0 {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, strings.Join(out.Conflicts, ", ")+" has a conflict (conflict markers in a text field); it cannot land").
			WithHint("settle it with `lore sync conflicts` / `lore sync resolve`, then land again")
	}
	if out.Status == "" {
		if in.ID != "" {
			if err := s.landNamedMissing(ctx, liveProjectRoot(l.h.cfg.WorkDir), folder, in.ID); err != nil {
				return opssync.DoneOut{}, err
			}
		}
		done := "nothing to land: " + out.Branch + " already holds every named row"
		return opssync.DoneOut{Done: done, Lines: []string{done}}, nil
	}
	for _, line := range landLines(out) {
		s.h.line(line) // v0.1.17 printed the landing outcome on stderr
	}
	return opssync.DoneOut{Done: string(out.Status)}, nil
}

// statusLiveOff is the live line of `lore sync status` where live rows do
// not apply.
const statusLiveOff = "live rows:        off here (LORE_LIVE=0, or not a git repo)"

// SyncStatus implements opssync.Env: the live rows of the cwd project and
// why each waits (v0.1.17 printLiveStatus, side effect 13). This binary
// keeps no lore.db sync state, so the file counters stay zero; open
// conflicts counts live rows with conflict markers.
func (l liveSync) SyncStatus(ctx context.Context, _ opssync.StatusIn) (opssync.StatusOut, error) {
	out := opssync.StatusOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindStatus}
	s, folder, err := l.here(ctx)
	if err != nil {
		return out, err
	}
	if l.h.cfg.MetaStatus != nil { // METADATA 3.13, FA-META-11 (v0.1.17 sync status lines)
		out.Data.MetaQuarantined, out.Data.MetaRegistriesCap = l.h.cfg.MetaStatus(ctx)
		out.Lines = append(out.Lines, fmt.Sprintf("meta quarantined: %d", len(out.Data.MetaQuarantined)))
		for _, q := range out.Data.MetaQuarantined {
			out.Lines = append(out.Lines, "  "+q)
		}
		for _, r := range out.Data.MetaRegistriesCap {
			out.Lines = append(out.Lines, "meta registry: "+r)
		}
	}
	if s == nil {
		// No live rows here (LORE_LIVE=0, or no git repo): status still
		// answers; the core adds the stores, refused pushes and conflicted
		// files (wire syncRows.SyncStatus).
		out.Lines = append(out.Lines, statusLiveOff)
		return out, nil
	}
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		return out, errs.Wrap(errs.Internal, "live status", err)
	}
	// Display only: an unreadable snapshot or pending list shows as none.
	snap, _ := s.ref.Snapshot(ctx)
	pending, _ := s.ref.Pending()
	out.Lines = append(out.Lines, fmt.Sprintf("live rows:        %d (%s)", len(plan), folder))
	if snap.Ahead() {
		out.Lines = append(out.Lines, fmt.Sprintf("live not pushed:  %d write(s) waiting", max(len(pending), 1)))
	}
	protected := s.noteText(liveNoteProtected)
	branch := s.defaultBranch(ctx)
	now := s.clk.Now()
	for _, p := range plan {
		if isConflict(p) {
			out.Data.OpenConflicts++
		}
		why := p.Decision.Reason
		switch {
		case p.Leave:
			why = "on " + branch + "; leaves live on the next sync"
		case p.Decision.Action != live.LandStay && protected != "":
			why = "waiting to land: " + branch + " is protected, rides on your next push"
		case p.Decision.Action != live.LandStay:
			why = "waiting to land (" + why + ")"
		}
		age := now.Sub(p.File.Block.LiveSince).Round(time.Minute)
		out.Lines = append(out.Lines, fmt.Sprintf("  %-10s %-40s live %-8s %s", p.Key.Table, p.Key.ID, age, why))
	}
	return out, nil
}

// SyncConflicts implements opssync.Env: the live rows of the cwd project
// whose text fields hold conflict markers (F4: both edits kept, the row
// cannot land until a person settles it: lore sync resolve or an edit). This binary keeps
// no saved conflict copies, so --all adds nothing.
func (l liveSync) SyncConflicts(ctx context.Context, _ opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	out := opssync.ConflictsOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindConflicts, Data: []opssync.Conflict{}}
	s, folder, err := l.here(ctx)
	if err != nil {
		return out, err
	}
	if s == nil {
		return out, errs.New(errs.Unsupported, liveUnsupported).WithHint(liveUnsupportedHint)
	}
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		return out, errs.Wrap(errs.Internal, "live conflicts", err)
	}
	for _, p := range plan {
		if !isConflict(p) {
			continue
		}
		at := p.File.Block.LiveSince.UTC().Format(time.RFC3339)
		sides := liveConflictSides(p.File.Body)
		names := make([]string, 0, len(sides))
		for _, f := range sides {
			names = append(names, f.name)
		}
		out.Data = append(out.Data, opssync.Conflict{ID: int64(len(out.Data) + 1), Table: p.Key.Table, RowID: p.Key.ID, Kept: conflictKeptBoth, At: at, Fields: names})
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (live, conflict markers in a text field; settle with lore sync resolve --id %d --take kept|other, or edit the row)", len(out.Data), p.Key.Table, p.Key.ID, conflictKeptBoth, len(out.Data)))
		// B2-23: both versions of each clashing field, as for a store row;
		// kept is the first section (the edit of the machine that merged),
		// other the second
		for _, f := range sides {
			out.Lines = append(out.Lines, "    "+f.name+" kept (first):   "+strconv.Quote(f.kept), "    "+f.name+" other (second): "+strconv.Quote(f.other))
		}
	}
	out.Count = len(out.Data)
	if out.Count == 0 {
		out.Lines = []string{"· no conflicts"}
	}
	return out, nil
}

// conflictKeptBoth is the kept side of a live text clash: both edits stay
// in the field between markers.
const conflictKeptBoth = "both"

// isConflict: the plan keeps the row live because a text field holds
// conflict markers (live.ShouldLand's "conflict…" reason).
func isConflict(p landPlanRow) bool {
	return p.Decision.Action == live.LandStay && strings.HasPrefix(p.Decision.Reason, "conflict")
}

// liveConflictField is one field of a live row holding conflict markers:
// the text --take kept would leave and the text --take other would.
type liveConflictField struct {
	name, kept, other string
}

// maxLiveConflictValue bounds a side repeated in a conflicts line.
const maxLiveConflictValue = 200

// liveConflictSides is every top-level string field of body holding a
// marker block, by name, with both sides (conflictpick, the same choice
// resolve makes).
func liveConflictSides(body canonjson.Object) []liveConflictField {
	var out []liveConflictField
	for _, k := range slices.Sorted(maps.Keys(body)) {
		v, ok := body[k].AsString()
		if !ok || !merge3.ContainsConflictMarkers(v) {
			continue
		}
		out = append(out, liveConflictField{name: k, kept: cutLive(conflictpick.Text(v, false)), other: cutLive(conflictpick.Text(v, true))})
	}
	return out
}

// cutLive bounds one side for display.
func cutLive(s string) string {
	if len(s) > maxLiveConflictValue {
		return s[:maxLiveConflictValue] + "…"
	}
	return s
}

// Sides `lore sync resolve --take` names (G-SYNC-1): kept is the first
// section of each conflict block (merge3 "ours": the edit of the machine
// that merged the two, which is this machine only if it did the merge; the
// markers record no owner, so lists and hints say first/second, W-5),
// other the second (the edit that met it, "theirs").
const (
	takeKept  = "kept"
	takeOther = "other"
)

// SyncResolve implements opssync.Env (G-SYNC-1): conflict #n of `lore sync
// conflicts` (the same numbering: the folder's conflicted live rows in
// table/id order) is settled by keeping one side of every conflict block
// in the row, and of every structured meta conflict entry; the settled row
// is a new live version (pushed by the run's finish, then it may land). A
// row still holding markers afterwards (nested or hand-made blocks) is
// refused: edit it by hand.
func (l liveSync) SyncResolve(ctx context.Context, in opssync.ResolveIn) (opssync.DoneOut, error) {
	take := strings.TrimSpace(in.Take)
	if take != takeKept && take != takeOther {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "--take must be kept or other").
			WithHint("kept keeps the first section of each clash (the edit of the machine that merged the two, not always this one: `lore sync conflicts` shows both), other the second")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(in.ID), "#"))
	if err != nil || n < 1 {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, "name a conflict number (see lore sync conflicts)")
	}
	s, folder, err := l.here(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if s == nil {
		return opssync.DoneOut{}, errs.New(errs.Unsupported, liveUnsupported).WithHint(liveUnsupportedHint)
	}
	if s.blocked != nil {
		return opssync.DoneOut{}, errLiveBlocked()
	}
	plan, err := s.plan(ctx, folder, nil)
	if err != nil {
		return opssync.DoneOut{}, errs.Wrap(errs.Internal, "live conflicts", err)
	}
	var hit *landPlanRow
	count := 0
	for i := range plan {
		if !isConflict(plan[i]) {
			continue
		}
		if count++; count == n {
			hit = &plan[i]
			break
		}
	}
	if hit == nil {
		return opssync.DoneOut{}, errs.New(errs.NotFound, fmt.Sprintf("no open conflict #%d", n)).WithHint("lore sync conflicts lists them")
	}
	body := conflictpick.Object(hit.File.Body, take == takeOther)
	prev := hit.File
	next := live.NewVersion(&prev, body, s.clk.Now(), liveAnonymousWriter)
	if next.HasConflictMarkers() {
		return opssync.DoneOut{}, errs.New(errs.InvalidInput, hit.Key.Table+"/"+hit.Key.ID+" still holds conflict markers after keeping one side (nested or hand-made blocks)").
			WithHint("edit the row by hand to settle it")
	}
	rows := map[live.RowKey]live.File{hit.Key: next}
	if err = s.h.leakCheck(ctx, leakWhereLive, folder, rows); err != nil {
		return opssync.DoneOut{}, err
	}
	res, err := s.ref.Write(ctx, live.WriteRequest{Folder: folder, Rows: rows, BinaryVersion: s.h.cfg.Version})
	if err != nil {
		return opssync.DoneOut{}, errs.Wrap(errs.Internal, "live: write the local live ref", err)
	}
	s.wrote += len(res.Written)
	done := fmt.Sprintf("resolved #%d %s/%s (took %s)", n, hit.Key.Table, hit.Key.ID, take)
	return opssync.DoneOut{Done: done, Lines: []string{"✓ " + done}}, nil
}

// SyncTrashList implements opssync.Env.
func (liveSync) SyncTrashList(context.Context, opssync.TrashListIn) (opssync.TrashListOut, error) {
	return opssync.TrashListOut{}, unsupported("trash")
}

// SyncTrashRestore implements opssync.Env.
func (liveSync) SyncTrashRestore(context.Context, opssync.TrashRestoreIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("trash restore")
}

// SyncPurge implements opssync.Env.
func (liveSync) SyncPurge(context.Context, opssync.PurgeIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("purge")
}

// SyncFixProjects implements opssync.Env.
func (liveSync) SyncFixProjects(context.Context, opssync.FixProjectsIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("fix-projects")
}

// SyncPeek implements opssync.Env.
func (l liveSync) SyncPeek(ctx context.Context, in opssync.PeekIn) (opssync.PeekOut, error) {
	return l.repo().SyncPeek(ctx, in)
}

// SyncPromote implements opssync.Env.
func (l liveSync) SyncPromote(ctx context.Context, in opssync.PromoteIn) (opssync.DoneOut, error) {
	defer dropCachedReads(ctx) // reposync moves a branch ref with its own git
	return l.repo().SyncPromote(ctx, in)
}

// SyncMergeRows implements opssync.Env.
func (liveSync) SyncMergeRows(context.Context, opssync.MergeRowsIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("merge-rows")
}

// repo is the repo-level sync host (install-action, ci-merge, peek,
// promote; wire/syncrepo_standalone.go, b11-sync).
func (l liveSync) repo() wire.RepoSync {
	return wire.RepoSync{WorkDir: l.h.cfg.WorkDir, Version: l.h.cfg.Version, Clock: l.h.clock()}
}

// SyncCIMerge implements opssync.Env.
func (l liveSync) SyncCIMerge(ctx context.Context, in opssync.CIMergeIn) (opssync.CIMergeOut, error) {
	defer dropCachedReads(ctx) // reposync merges and commits HEAD with its own git
	return l.repo().SyncCIMerge(ctx, in)
}

// dropCachedReads forgets the cached git answers of every live session of
// the run on ctx. A delegate that writes refs with its own git (reposync's
// promote and ci-merge do not run through the session's runner) calls it
// when it returns, success or error, so the next read of HEAD or a branch
// in the same operation sees the new commit (gitref.ReadCache invariant).
// Delegates that only read git, or only write work-tree files (peek,
// install-action), need not.
func dropCachedReads(ctx context.Context) {
	if r := runFrom(ctx); r != nil {
		r.dropCaches()
	}
}

// SyncInstallAction implements opssync.Env.
func (l liveSync) SyncInstallAction(ctx context.Context, in opssync.InstallActionIn) (opssync.ActionOut, error) {
	return l.repo().SyncInstallAction(ctx, in)
}

// landErr keeps a coded refusal of the landing pass (E_LEAK_BLOCKED, a
// rewritten remote) as it is, and wraps any other failure as E_INTERNAL.
func landErr(err error) error {
	var e *errs.Error
	if errors.As(err, &e) {
		return err
	}
	return errs.Wrap(errs.Internal, "land", err)
}
