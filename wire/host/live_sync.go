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
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
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

// SyncStatus implements opssync.Env.
func (liveSync) SyncStatus(context.Context, opssync.StatusIn) (opssync.StatusOut, error) {
	return opssync.StatusOut{}, unsupported("status")
}

// SyncConflicts implements opssync.Env.
func (liveSync) SyncConflicts(context.Context, opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	return opssync.ConflictsOut{}, unsupported("conflicts")
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
func (liveSync) SyncPeek(context.Context, opssync.PeekIn) (opssync.PeekOut, error) {
	return opssync.PeekOut{}, unsupported("peek")
}

// SyncPromote implements opssync.Env.
func (liveSync) SyncPromote(context.Context, opssync.PromoteIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("promote")
}

// SyncMergeRows implements opssync.Env.
func (liveSync) SyncMergeRows(context.Context, opssync.MergeRowsIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, unsupported("merge-rows")
}

// SyncCIMerge implements opssync.Env.
func (liveSync) SyncCIMerge(context.Context, opssync.CIMergeIn) (opssync.CIMergeOut, error) {
	return opssync.CIMergeOut{}, unsupported("ci-merge")
}

// SyncInstallAction implements opssync.Env.
func (liveSync) SyncInstallAction(context.Context, opssync.InstallActionIn) (opssync.ActionOut, error) {
	return opssync.ActionOut{}, unsupported("install-action")
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
