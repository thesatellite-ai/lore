package host

// live_sync.go — the `lore sync` / `lore land` host of cmd/lore
// (wire.Options.Sync, opssync.Env) for live rows only (P3-MODEB-3):
//
//	sync.run ─▶ (Around: FetchAlways prepare: fetch + integrate)
//	         ─▶ push the waiting live writes ─▶ landing pass (F5)
//	         ─▶ (Around finish: push what landing changed on the live ref)
//	land.run ─▶ the same landing pass
//
// v0.1.17's lore.db catch-up pass, store clone exchange, conflicts, trash,
// peek and promote belong to the old binary's sync host; on cmd/lore they
// answer E_UNSUPPORTED until they are ported.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
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
	lines, err := l.pushAndLand(ctx)
	if err != nil {
		return opssync.RunOut{}, err
	}
	return opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport, Data: opssync.RunData{Pass: syncPassLive}, Lines: lines}, nil
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
// cannot land until a person settles it with an edit). This binary keeps
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
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (live, conflict markers in a text field; edit the row to settle it)", len(out.Data), p.Key.Table, p.Key.ID, conflictKeptBoth))
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

// SyncResolve implements opssync.Env.
func (liveSync) SyncResolve(context.Context, opssync.ResolveIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("resolve")
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

// Explain implements opssync.Env.
func (liveSync) Explain(context.Context, opssync.ExplainIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, errs.New(errs.Unsupported, "lore explain is not available in this binary yet")
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
