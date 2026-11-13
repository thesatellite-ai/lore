package wire

// migrate_abort.go — a `lore migrate run` that fails part way leaves the
// data as it found it (E-5).
//
//	run ─► Begin (tree before) ─► steps ─► a step fails
//	    ─► AbortRun: each place back to its tree before
//	         repo .lore/data ─► files restored (created ones removed)
//	         store clone, nothing pushed ─► local ref back to its tip
//	         store clone, already pushed ─► left; the journal stays, so
//	                                         `lore migrate rollback` (or a
//	                                         new run, which resumes) applies
//	    ─► the journal and backup refs go (the earlier run's come back)
//
// Why: one bad row (an orphan entity_tags link) stopped the run after it
// had rewritten hundreds of files, so the repo stayed mixed `_v: 1` /
// `_v: 2` and every doctor named a fix that failed the same way.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

var _ opsmigrate.Aborter = (*App)(nil)

// AbortRun implements opsmigrate.Aborter. A run that converted Mode B is
// left as it is (that conversion has no undo, BUGFIX-07): it says so.
func (a *App) AbortRun(ctx context.Context) ([]string, error) {
	top, path, err := a.journalPath(ctx)
	if err != nil {
		return nil, err
	}
	j, ok, err := readJournal(path)
	if err != nil || !ok || j.Phase != phaseRunning {
		return nil, err
	}
	if j.ModeBPointer != "" {
		return []string{"the run's shared-database conversion stays (it has no undo); run lore migrate run again once the cause is fixed"}, nil
	}
	snaps, err := git.NewSnapshots()
	if err != nil {
		return nil, err
	}
	var lines []string
	keep := false // a place already pushed: the journal must stay for the rollback
	reset := map[string]bool{}
	for _, p := range j.Places {
		if p.Clone {
			head := snaps.Head(ctx, p.Dir)
			if head == p.TipBefore {
				continue
			}
			up := snaps.Upstream(ctx, p.Dir)
			if p.TipBefore == "" || (up != "" && snaps.IsAncestor(ctx, p.Dir, head, up)) {
				keep = true
				lines = append(lines, placeLabel(p)+": the run's store commit already reached the remote; lore migrate rollback undoes it")
				continue
			}
			line, err := a.applyRollback(ctx, snaps, top, rollbackPlan{place: p, localUndo: true}, reset)
			if err != nil {
				return lines, err
			}
			if line != "" {
				lines = append(lines, line)
			}
			continue
		}
		now, err := snaps.Snapshot(ctx, top, p.Dir, p.Data)
		if err != nil {
			return lines, err
		}
		diff, err := snaps.DiffTrees(ctx, top, p.TreeBefore, now)
		if err != nil {
			return lines, err
		}
		if len(diff) == 0 {
			continue
		}
		paths, err := a.restoreFiles(ctx, snaps, top, p, diff, false)
		if err != nil {
			return lines, err
		}
		lines = append(lines, placeLabel(p)+": "+plural(len(paths), "file")+" put back as they were")
	}
	if st, serr := a.state(ctx); serr == nil {
		for _, name := range st.eng.Stores() {
			if _, err := st.eng.Refresh(ctx, name); err != nil {
				return lines, err
			}
		}
	}
	if keep {
		return lines, nil
	}
	return lines, a.dropBackup(ctx, snaps, top, path, j)
}
