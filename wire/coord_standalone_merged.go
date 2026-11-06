package wire

// coord_standalone_merged.go — F9's lazy done → merged check on cmd/lore
// (LORE_TEAM_SPEC F9, TEAM-017; v0.1.17 lazyMergedCheck and
// storeRows.MergedCheck): the views that show merge state (task list, task
// next, inbox, mission status) look for the merge of every done task
// without merged_at before they list, and record merged_at once for each
// merge found. The checks themselves live in adapters/mergewatch; this file
// supplies the candidates and the write.
//
//	MergedCheck
//	  ├► candidates: done tasks without merged_at (repo rows of this
//	  │  project, a linked store's rows of the group)   ── none: return, no git
//	  ├► openWorld ─► the checkout's repo, its remote's default branch,
//	  │  merge_check.adapter, the repo this checkout is; a linked
//	  │  checkout's store (linkedStoreOpens)  ── cannot open: one warning
//	  ├► mergewatch.Checker.Sweep(here)                  ── writes nothing
//	  └► per merge found: openWorldFor(id) ─► stampMerged (fresh row,
//	     merged_at = the carrier commit's date, updated_at untouched)
//	     ─► mergewatch.Line on out
//
// The check never fails the command (a broken check is a warning line) and
// writes nothing when nothing is found (principle 1).

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/thesatellite-ai/lore/adapters/mergewatch"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Task fields the merged check reads.
const (
	fieldDoneCommit = "done_commit"
	fieldDonePR     = "done_pr"
	fieldTaskID     = "id"
)

// MergedCheck is F9's lazy merged check for this checkout (see the file
// comment). out receives one line per merge found and per failed check
// (stderr in cmd/lore). Silent when there is nothing to check or no
// coordination here (no git repo, no identity).
func (a *App) MergedCheck(ctx context.Context, out io.Writer) {
	h := standaloneCoord{a: a}
	if !a.hasMergeCandidates(ctx) {
		return
	}
	w, err := h.openWorld(ctx)
	if err != nil {
		return // no coordination here: nothing to say
	}
	if !linkedStoreOpens(out, w, func() error { return w.forTask(ctx, "") }) {
		return
	}
	cands := a.mergeCandidates(ctx, w)
	if len(cands) == 0 {
		return
	}
	adapter, _ := mergewatch.AdapterFor(w.mergeCheck, w.repo.Dir())
	ck := mergewatch.Checker{Repo: w.repo, Target: mergewatch.Target(w.remoteName, w.defaultBranch(ctx)), Adapter: adapter}
	found, probs := ck.Sweep(ctx, h.hereRepo(ctx, w), cands)
	for _, p := range probs {
		_, _ = fmt.Fprintln(out, mergewatch.ProblemLine(p)) // the output is gone: nothing left to report to
	}
	for _, f := range found {
		recorded, rerr := h.stampMerged(ctx, f)
		if recorded || rerr != nil {
			_, _ = fmt.Fprintln(out, mergewatch.Line(f, rerr))
		}
	}
}

// mergeUncheckedPrefix starts the one warning line the lazy merged check
// prints when it could not run; scripts and tests match on it (v0.1.17
// coord_store.go, word for word).
const mergeUncheckedPrefix = "lore: warning: merge state not checked: "

// linkedStoreOpens points a linked checkout's world at its store (open)
// and reports whether the check can go on. A checkout with no link has
// nothing to open (true, silent); a linked one whose store cannot be
// opened gets one warning line on out, and the command still succeeds:
// done tasks of the store keep showing "not merged yet" until a later
// command reaches it (later.md 606, v0.1.17 groupMergedCheckIn).
func linkedStoreOpens(out io.Writer, w *coordWorld, open func() error) bool {
	if !w.linked() {
		return true
	}
	if err := open(); err != nil {
		_, _ = fmt.Fprintf(out, "%sopen the linked store: %s; done tasks may show as not merged yet (the next lore command retries)\n",
			mergeUncheckedPrefix, oneLine(err.Error())) // the output is gone: nothing left to report to
		return false
	}
	return true
}

// hasMergeCandidates is the cheap first look: any done task without
// merged_at in any attached store. It keeps the common case (nothing done
// and unmerged) free of the world's git and org resolution. It opens the
// App first: a host hook runs before the operation would open it, and
// Stores is empty until then.
func (a *App) hasMergeCandidates(ctx context.Context) bool {
	if _, err := a.state(ctx); err != nil {
		return false
	}
	docs, err := engineRows{a: a}.List(ctx)
	if err != nil {
		return false
	}
	for _, d := range docs {
		if unmerged(d) {
			return true
		}
	}
	return false
}

// unmerged reports a done task without merged_at.
func unmerged(d coord.Doc) bool {
	return d.Status() == schema.TaskStatusDone && d.Str(fieldMergedAt) == ""
}

// mergeCandidates are the done, unmerged tasks this checkout's view shows
// (coordView.projectDocs: the repo's rows of this project, a linked
// store's rows of the group), each with the mount name of its repo.
func (a *App) mergeCandidates(ctx context.Context, w *coordWorld) []mergewatch.Candidate {
	cv := &coordView{a: a, w: w}
	if p, err := a.Project(ctx); err == nil {
		cv.project = p.String()
	}
	rows := engineRows{a: a}
	var out []mergewatch.Candidate
	for _, d := range cv.projectDocs(ctx, schema.TableTasks) {
		if !unmerged(d) {
			continue
		}
		c := mergewatch.Candidate{TaskID: d.Str(fieldTaskID), DoneCommit: d.Str(fieldDoneCommit), DonePR: d.Int(fieldDonePR)}
		if r := d.Str(vkRepo); r != "" {
			c.Repo = rows.RepoName(r)
		}
		out = append(out, c)
	}
	return out
}

// stampMerged records merged_at = the finding's time on the task, once,
// where the task is coordinated (openWorldFor: the repo's own signals ref
// or the linked store's), through stampMergedRow.
func (h standaloneCoord) stampMerged(ctx context.Context, f mergewatch.Found) (recorded bool, err error) {
	ctx = mergedStampLive(ctx)
	w, err := h.openWorldFor(ctx, f.TaskID)
	if err != nil {
		return false, err
	}
	env := w.env(ctx)
	return stampMergedRow(ctx, env.Rows, env.Signals, f.TaskID, f.Finding.At)
}

// mergedStampLive routes a merged stamp of this repo's task through the
// live ref (v0.1.17 recordMergedLive): the row usually sits in main's data
// already (it landed and left live before the merge), and an edit of a
// branch row would otherwise stay with the code, leaving an uncommitted
// .lore/data change in a checkout that only listed its tasks (trial F5,
// TestMerged_StampTravelsAndCheckoutsStayClean). With no live session
// (LORE_LIVE off, no git remote) the choice routes nothing and the stamp
// is a branch write, as v0.1.17 recordMergedBranch; a store task is
// written by its store and never reads the choice.
func mergedStampLive(ctx context.Context) context.Context {
	return store.WithLiveChoice(ctx, store.LiveChoice{Mode: store.LiveOn, Table: schema.TableTasks})
}

// signalsWriter is the one signals.Store call the stamp makes (a seam for
// the decision tests).
type signalsWriter interface {
	Write(ctx context.Context, decide signals.Decide) (signals.Result, error)
}

// stampMergedRow is the stamp's decision. The row is read and staged inside
// the signals write's decision, so a lost race re-reads the fresh row: a
// teammate who stamped the same merge first leaves nothing to do (recorded
// false, nil error). Only merged_at changes; updated_at stays as it was, so
// two clones that find the same merge stage the same row (v0.1.17
// stampMerged). A failed read, stage or write is returned and recorded is
// false (the caller's line then says it could not be recorded).
func stampMergedRow(ctx context.Context, rows coord.Rows, sig signalsWriter, id string, at time.Time) (recorded bool, err error) {
	_, err = sig.Write(ctx, func(signals.View) (signals.Change, error) {
		recorded = false
		d, ok, gerr := rows.Get(ctx, id)
		if gerr != nil {
			return signals.Change{}, fmt.Errorf("read %s: %w", id, gerr)
		}
		if !ok || !unmerged(d) {
			return signals.Change{}, nil // gone, reopened, or already stamped
		}
		next := d.Clone()
		next.SetTime(fieldMergedAt, at)
		comps, serr := rows.Stage(ctx, coord.RowWrite{ID: id, Doc: next})
		if serr != nil {
			return signals.Change{}, fmt.Errorf("stage %s: %w", id, serr)
		}
		recorded = true
		return signals.Change{Companions: comps}, nil
	})
	if err != nil {
		return false, fmt.Errorf("write %s: %w", id, err)
	}
	return recorded, nil
}
