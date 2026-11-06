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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/wire"
)

// syncPassLive names the pass of a live-only sync in the sync.report data.
const syncPassLive = "live"

// liveSync implements opssync.Env over LiveHook's runs.
type liveSync struct{ h *liveHost }

var _ opssync.Env = liveSync{}

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

// pushAndLand pushes the waiting live writes, then lands what is due.
func (l liveSync) pushAndLand(ctx context.Context) ([]string, error) {
	s, folder, err := l.here(ctx)
	if err != nil || s == nil {
		return nil, err
	}
	if !s.push(ctx) || s.ref.Remote == nil {
		return nil, nil
	}
	out, err := s.landPass(ctx, folder, nil, true)
	if err != nil {
		return nil, landErr(err)
	}
	return landLines(out), nil
}

// SyncRun implements opssync.Env.
func (l liveSync) SyncRun(ctx context.Context, _ opssync.RunIn) (opssync.RunOut, error) {
	if l.h.cfg.RepoAddresses != nil {
		l.h.cfg.RepoAddresses(ctx) // PROVENANCE_DESIGN 10.3: records and notices only, never fails the sync
	}
	// G-DECIDE-9: fail closed while git still ignores row files after the
	// re-include block (teammates would silently miss those rows).
	if err := l.refuseIgnoredData(ctx); err != nil {
		return opssync.RunOut{}, err
	}
	lines, err := l.pushAndLand(ctx)
	if err != nil {
		return opssync.RunOut{}, err
	}
	return opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport, Data: opssync.RunData{Pass: syncPassLive}, Lines: lines}, nil
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
	s, folder, err := l.here(ctx)
	if err != nil {
		return opssync.DoneOut{}, err
	}
	if s == nil {
		return opssync.DoneOut{}, errs.New(errs.Unsupported, liveUnsupported)
	}
	sel := landSelector(in.ID, in.Kind, in.Mission)
	if in.DryRun {
		lines, perr := s.planLines(ctx, folder, sel)
		if perr != nil {
			return opssync.DoneOut{}, errs.Wrap(errs.Internal, "land plan", perr)
		}
		return opssync.DoneOut{Lines: lines}, nil
	}
	if !s.push(ctx) {
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
		done := "nothing to land: " + out.Branch + " already holds every named row"
		return opssync.DoneOut{Done: done, Lines: []string{done}}, nil
	}
	for _, line := range landLines(out) {
		s.h.line(line) // v0.1.17 printed the landing outcome on stderr
	}
	return opssync.DoneOut{Done: string(out.Status)}, nil
}

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
	if s == nil {
		return out, errs.New(errs.Unsupported, liveUnsupported)
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
		return out, errs.New(errs.Unsupported, liveUnsupported)
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
		out.Data = append(out.Data, opssync.Conflict{ID: int64(len(out.Data) + 1), Table: p.Key.Table, RowID: p.Key.ID, Kept: conflictKeptBoth, At: at})
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (live, conflict markers in a text field; settle with lore sync resolve %d --take kept|other, or edit the row)", len(out.Data), p.Key.Table, p.Key.ID, conflictKeptBoth, len(out.Data)))
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

// Sides `lore sync resolve --take` names (G-SYNC-1): kept is the first
// section of each conflict block (this machine's edit, merge3 "ours"),
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
			WithHint("kept keeps this machine's edit of each clash, other the edit that met it")
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
		return opssync.DoneOut{}, errs.New(errs.Unsupported, liveUnsupported)
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
	body := settleObject(hit.File.Body, take == takeOther)
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
	s.markFolder(folder)
	done := fmt.Sprintf("resolved #%d %s/%s (took %s)", n, hit.Key.Table, hit.Key.ID, take)
	return opssync.DoneOut{Done: done, Lines: []string{"✓ " + done}}, nil
}

// settleObject is body with every conflict settled to one side (other:
// the second section, else the first): marker blocks in any string at any
// depth, and structured meta conflict entries (METADATA 3.12).
func settleObject(body canonjson.Object, other bool) canonjson.Object {
	out := make(canonjson.Object, len(body))
	for k, v := range body {
		out[k] = settleValue(v, other)
	}
	if meta, ok := out[row.KeyMeta].AsObject(); ok {
		out[row.KeyMeta] = canonjson.ObjectValue(settleMetaEntries(meta, other))
	}
	return out
}

// settleValue settles the marker blocks of every string inside v.
func settleValue(v canonjson.Value, other bool) canonjson.Value {
	if str, ok := v.AsString(); ok {
		return canonjson.String(settleText(str, other))
	}
	if arr, ok := v.AsArray(); ok {
		out := make([]canonjson.Value, len(arr))
		for i, it := range arr {
			out[i] = settleValue(it, other)
		}
		return canonjson.Array(out...)
	}
	if obj, ok := v.AsObject(); ok {
		return canonjson.ObjectValue(settleObject(obj, other))
	}
	return v
}

// Structured meta conflict members (logic/merge entries.go, METADATA 3.12).
const (
	metaConflictKey = "conflict"
	metaOursKey     = "ours"
	metaTheirsKey   = "theirs"
	metaValueKey    = "v"
)

// settleMetaEntries replaces each entry's {"conflict": {base, ours,
// theirs}} with the chosen side's value; a side that removed the value
// (null) removes the entry.
func settleMetaEntries(meta canonjson.Object, other bool) canonjson.Object {
	side := metaOursKey
	if other {
		side = metaTheirsKey
	}
	out := make(canonjson.Object, len(meta))
	for id, e := range meta {
		entry, ok := e.AsObject()
		if !ok {
			out[id] = e
			continue
		}
		c, has := entry[metaConflictKey]
		if !has {
			out[id] = e
			continue
		}
		sides, _ := c.AsObject()
		chosen, present := sides[side]
		if !present || chosen.Kind() == canonjson.KindNull {
			continue
		}
		next := make(canonjson.Object, len(entry))
		for k, v := range entry {
			if k != metaConflictKey {
				next[k] = v
			}
		}
		next[metaValueKey] = chosen
		out[id] = canonjson.ObjectValue(next)
	}
	return out
}

// settleText keeps one section of every complete marker block of s
// (open "<<<<<<< ", split "=======", close ">>>>>>> ", git's shape);
// an unfinished block is left as written.
func settleText(s string, other bool) string {
	if !merge3.ContainsConflictMarkers(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	const (
		outside = iota
		first
		second
	)
	state, start := outside, 0
	var firstLines, secondLines []string
	for i, line := range lines {
		switch {
		case state == outside && strings.HasPrefix(line, merge3.MarkerOurs[:8]):
			state, start, firstLines, secondLines = first, i, nil, nil
		case state == first && line == merge3.MarkerSplit:
			state = second
		case state == second && strings.HasPrefix(line, merge3.MarkerTheirs[:8]):
			if other {
				out = append(out, secondLines...)
			} else {
				out = append(out, firstLines...)
			}
			state = outside
		case state == first:
			firstLines = append(firstLines, line)
		case state == second:
			secondLines = append(secondLines, line)
		default:
			out = append(out, line)
		}
	}
	if state != outside {
		out = append(out, lines[start:]...) // an unfinished block stays as written
	}
	return strings.Join(out, "\n")
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
	return l.repo().SyncCIMerge(ctx, in)
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
