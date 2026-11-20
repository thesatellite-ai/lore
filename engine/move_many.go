package engine

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Bulk move (devco D-712): `lore move --from <store>/<folder> --to …`
// moved a folder of 296 rows with one `lore move --id` each, two commits
// per row (~600 commits). MoveMany runs the one-row move (MoveWith: every
// refusal, the secret scan, the client-repo projection, the role guard)
// for each row, but holds its writes:
//
//	ctx stage (wire withActorRows) ── each row's copy at to (+ notes)
//	tombstone stage (moveTombs)   ── each row's tombstone at its source
//	any refusal ─► both stages dropped, nothing written
//	all planned ─► role guard of every store's batch ─► ONE write at to
//	           ─► ONE write per source store (Lore-Moved-To trailer)
//
// Interrupted between the two writes, every row is in both stores; the
// same bulk move run again finds the copies in to and writes only the
// tombstones (MoveWith's interrupted-move rule), as for one row.

// moveTombsKey is the context key of a bulk move's tombstone stage.
type moveTombsKey struct{}

// tombStageOf returns the tombstone stage a bulk move put on ctx (nil
// outside MoveMany): MoveWith stages its source tombstone there instead
// of writing it, and leaves its copy in ctx's own stage.
func tombStageOf(ctx context.Context) *stage {
	s, ok := ctx.Value(moveTombsKey{}).(*stage)
	if !ok {
		return nil
	}
	return s
}

// MovedMany is what MoveMany wrote: each row as MoveWith returns it (in
// the order moved) and the source stores that got a tombstone batch.
type MovedMany struct {
	Rows    []Written
	Sources []store.Source
}

// MoveMany moves every row of refs to the attached store to in one write
// at to and one write per source store (see the file comment). refs must
// be live rows of other stores; a task's parent is moved before its
// sub-tasks when both are in refs (ordered here), so a client repo's
// "parent first" check and the moved-alone note see the parent.
// opt.WithChildren is refused: every row to move is named in refs.
func (e *Engine) MoveMany(ctx context.Context, refs []Ref, to store.Source, opt MoveOptions) (MovedMany, error) {
	if opt.WithChildren {
		return MovedMany{}, errs.New(errs.InvalidInput, "a bulk move names every row it moves; --with-children is for one task")
	}
	if len(refs) == 0 {
		return MovedMany{}, nil
	}
	dst, err := e.writable(ctx, to)
	if err != nil {
		return MovedMany{}, err
	}
	own := !InStage(ctx)
	if own { // a caller without wire's stage (engine tests): MoveMany opens one
		ctx = Staging(ctx)
		defer Discard(ctx)
	}
	tombs := &stage{bySrc: map[store.Source]*stagedStore{}, written: map[store.Key]bool{}, created: map[store.Key]bool{}}
	tctx := context.WithValue(ctx, moveTombsKey{}, tombs)
	closeTombs := func() { Discard(context.WithValue(ctx, stageKey{}, tombs)) }
	defer closeTombs()
	ordered := e.parentsFirst(ctx, refs)
	// D-717: every row is screened before the first is staged, so the
	// refusal names every row holding a secret (not only the first), and
	// several --secret-ref values are counted against the whole batch.
	plan, err := e.PlanMoveSecrets(ctx, ordered, to, opt.SecretRerun)
	if err != nil {
		return MovedMany{}, err
	}
	var out MovedMany
	for i, ref := range ordered {
		w, merr := e.MoveWith(e.ForRow(tctx, plan, ref.ID), ref, to, opt)
		if merr != nil {
			return MovedMany{}, errs.Prefix("bulk move: "+ref.ID.String()+" ("+strconv.Itoa(i+1)+" of "+strconv.Itoa(len(refs))+") refused; nothing moved", merr)
		}
		out.Rows = append(out.Rows, w)
	}
	copies := absorbStaged(ctx, to)
	tombs.mu.Lock()
	order := slices.Clone(tombs.order)
	tombs.mu.Unlock()
	// The role guard (D-694) judges every batch before the first lands:
	// a tombstone a member may not write never follows copies already in to.
	if err = e.guardWrites(ctx, dst, copies.changes); err != nil {
		return MovedMany{}, err
	}
	for _, name := range order {
		p := tombs.bySrc[name]
		if err = e.guardWrites(ctx, p.st, p.changes); err != nil {
			return MovedMany{}, err
		}
	}
	if opt.Plan { // D-761: the dry run stops here, every check passed
		return out, nil
	}
	srcNames := make([]string, len(order))
	for i, n := range order {
		srcNames[i] = string(n)
	}
	// one subject for both commits: "lore: move 296 row(s) from notes/old to notes/new"
	direct := store.WithCommitTitle(unstaged(ctx), "move "+strconv.Itoa(len(out.Rows))+" row(s) from "+strings.Join(srcNames, ", ")+" to "+string(to))
	if len(copies.changes) > 0 {
		if err = e.writeChanges(direct, dst, copies.changes, copies.journal); err != nil {
			return MovedMany{}, errs.Prefix("bulk move: nothing moved (writing "+string(to)+")", err)
		}
		aw, ferr := e.finishW(direct, to, copies.docs, copies.events...)
		out.addWarnings(aw)
		if ferr != nil {
			return out, ferr
		}
	}
	tctxTrail := store.WithCommitTrailers(direct, MovedToTrailer+": "+e.placeOf(to))
	for _, name := range order {
		p := tombs.bySrc[name]
		if err = e.writeChanges(tctxTrail, p.st, p.changes, p.journal); err != nil {
			return out, errs.Prefix("bulk move: the rows are now in "+string(to)+" but their old copies in "+string(name)+" could not be removed", err).
				WithHint("run the same move again: it only removes the old copies")
		}
		aw, ferr := e.finishW(direct, name, p.docs, p.events...)
		out.addWarnings(aw)
		if ferr != nil {
			return out, ferr
		}
		out.Sources = append(out.Sources, name)
	}
	if own {
		aw, cerr := Commit(ctx)
		out.addWarnings(aw)
		if cerr != nil {
			return out, cerr
		}
	}
	return out, nil
}

// addWarnings files the batch's own warnings (audit deferred, …) under
// its first row, so a caller reading Rows sees every warning once.
func (m *MovedMany) addWarnings(ws []Warning) {
	if len(ws) == 0 || len(m.Rows) == 0 {
		return
	}
	m.Rows[0].Warnings = append(m.Rows[0].Warnings, ws...)
}

// parentsFirst orders refs so a row comes after every row of refs it
// points at: a task after its parent, and (devco D-761) any row after the
// container it belongs to (its task list, mission, plan, …). A pointer to
// a row not in refs puts no constraint; every other row keeps its place.
// Pointers are read from the rows as they are now. Without the container
// rule a folder's tasks (table order: task_lists after tasks) moved before
// their list, leaveContainers found the list only in the source and
// cleared every task's tasklist_id although the list moved in the same
// batch.
func (e *Engine) parentsFirst(ctx context.Context, refs []Ref) []Ref {
	in := map[ids.AnyID]bool{}
	for _, r := range refs {
		in[r.ID] = true
	}
	deps := map[ids.AnyID][]ids.AnyID{}
	for _, r := range refs {
		def, err := e.tableDef(r.Table)
		if err != nil {
			continue // MoveWith reports it in order
		}
		fields := pointerFields(def)
		if len(fields) == 0 {
			continue
		}
		_, cur, err := e.locate(ctx, r)
		if err != nil {
			continue // MoveWith reports it in order
		}
		for _, f := range fields {
			v, ok := cur.Fields[f]
			if !ok || v == nil || v.String() == "" {
				continue
			}
			if pid, perr := ids.ParseAny(v.String()); perr == nil && in[pid] && pid != r.ID {
				deps[r.ID] = append(deps[r.ID], pid)
			}
		}
	}
	if len(deps) == 0 {
		return refs
	}
	out := make([]Ref, 0, len(refs))
	done := map[ids.AnyID]bool{}
	byID := map[ids.AnyID]Ref{}
	for _, r := range refs {
		byID[r.ID] = r
	}
	var visit func(r Ref)
	visit = func(r Ref) {
		if done[r.ID] {
			return
		}
		done[r.ID] = true // marked first: a pointer cycle ends here
		for _, d := range deps[r.ID] {
			visit(byID[d])
		}
		out = append(out, r)
	}
	for _, r := range refs {
		visit(r)
	}
	return out
}

// pointerFields are the fields of def whose target a bulk move must move
// first: the self pointer (a task's parent_id) and every belongs-to
// container leaveContainers would clear (not the project, kinds and
// metadata keys, which have their own move rule, nor roster pointers).
func pointerFields(def schema.TableDef) []string {
	var out []string
	for _, rel := range def.Relations {
		switch {
		case rel.Kind == schema.Self:
			out = append(out, rel.Field)
		case rel.Kind == schema.BelongsTo && !ownMoveRule[rel.Target] && !rosterTables[rel.Target]:
			out = append(out, rel.Field)
		}
	}
	return out
}

// PlanMoveSecrets is a bulk move's secret pre-scan (devco D-717): every
// row of refs that would be copied into to, scanned as its write would
// scan it, before anything is staged. It refuses the batch naming every
// row that holds a finding no flag answers, with rerun (the command to
// repeat, "" for the flags alone) in each printed answer; with an answer
// each row's part is in the plan (ForRow). A row MoveWith would refuse
// or only tombstone (deleted, already in to) is skipped here: MoveWith
// reports it in order. The dry run of `lore move --from` calls it too,
// so a preview never promises a move the real run refuses.
func (e *Engine) PlanMoveSecrets(ctx context.Context, refs []Ref, to store.Source, rerun string) (SecretPlan, error) {
	rows := make([]row.Row, 0, len(refs))
	for _, ref := range refs {
		_, cur, err := e.locateExcept(ctx, ref, to)
		if err != nil || cur.Deleted {
			continue // MoveWith reports it in order
		}
		if there, _, gerr := e.getRow(ctx, to, cur.Table, cur.ID); gerr == nil && !there.ID.IsZero() {
			continue // a rerun only tombstones: no copy, nothing to scan
		}
		def, err := e.tableDef(cur.Table)
		if err != nil {
			return SecretPlan{}, err
		}
		cur.Fields = retype(def, cur.Fields) // as MoveWith scans it
		rows = append(rows, cur)
	}
	return e.PlanSecrets(ctx, "bulk move", rerun, to, rows)
}
