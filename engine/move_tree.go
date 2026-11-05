package engine

import (
	"context"
	"errors"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Task sub-trees on a move (TASK_SYSTEM_DESIGN 6.8, 6.14, B7).
//
//	MoveWith(parent, WithChildren)
//	  ├► taskDescendants: live tasks of every attached place in a
//	  │   tasktree; the parent's subtree not yet in to, parents first
//	  ├► move the parent (as any row)
//	  └► move each descendant: its parent is already live in to, so the
//	     client child-alone check passes; ids and parent_id stay (6.14)
//
//	MoveWith(child) into a client repo, parent not live there
//	  └► E_LEAK_BLOCKED naming the parent (6.14: never a silent strip)

// parentOf is a task row's parent id ("" = top level).
func parentOf(r row.Row) string {
	if v, ok := r.Fields[schema.FieldTaskParent]; ok && v != nil {
		return v.String()
	}
	return ""
}

// refuseOrphanChild refuses moving a sub-task into client repo to when its
// parent is not live there: the client would hold a parent_id it cannot
// see (6.14). The fix is the parent with --with-children, or an explicit
// detach (`lore task edit <child> --no-parent`) first.
func (e *Engine) refuseOrphanChild(ctx context.Context, to store.Source, cur row.Row) error {
	p := parentOf(cur)
	if p == "" {
		return nil
	}
	pid, err := ids.ParseAny(p)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "engine: "+cur.ID.String()+" has a parent_id that is not an id: "+strconv.Quote(p), err)
	}
	_, gone, err := e.getRow(ctx, to, cur.Table, pid)
	switch {
	case err == nil && !gone:
		return nil // the parent already moved there (a --with-children run, or a rerun)
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	return errs.New(errs.LeakBlocked, "engine: "+cur.ID.String()+" is a sub-task of "+p+", which client repo "+string(to)+" cannot see; nothing moved").
		WithHint("lore move " + p + " --to " + string(to) + " --with-children   (or detach first: lore task edit " + cur.ID.String() + " --no-parent)")
}

// WarnMovedFromParent is the line of a sub-task moved without its parent
// (TASK 6.8, OVERNIGHT Qs): it keeps parent_id and gets a note.
const WarnMovedFromParent WarnCode = "W_MOVED_FROM_PARENT"

// fieldCommentBody is the comments table's text field (schema/builtin_link.go
// declares it inline as "body").
const fieldCommentBody = "body"

// noteMovedAlone is TASK 6.8 (OVERNIGHT Qs, owner yes 2026-11-05): a
// sub-task that lands in to while its parent is not live there keeps its
// parent_id and gets one comment row "moved from parent <id>" in to, written
// through the normal create path (scan, audit). A child of a --with-children
// run finds its parent already in to and gets none; a client repo never gets
// here without the parent (refuseOrphanChild, 6.14).
func (e *Engine) noteMovedAlone(ctx context.Context, to store.Source, moved row.Row, w *Written) error {
	p := parentOf(moved)
	if p == "" {
		return nil
	}
	pid, err := ids.ParseAny(p)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "engine: "+moved.ID.String()+" has a parent_id that is not an id: "+strconv.Quote(p), err)
	}
	_, gone, err := e.getRow(ctx, to, schema.TableTasks, pid)
	switch {
	case err == nil && !gone:
		return nil // moved with (or after) its parent
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	body := "moved from parent " + p
	fields := map[string]value.Value{}
	if fields[schema.FieldEntityTable], err = value.TextOf(schema.TableTasks); err != nil {
		return err
	}
	if fields[schema.FieldEntityID], err = value.RefOf(moved.ID); err != nil {
		return err
	}
	if fields[fieldCommentBody], err = value.TextOf(body); err != nil {
		return err
	}
	if _, err = e.Create(ctx, to, schema.TableComments, fields); err != nil {
		return errs.Wrap(errs.CodeOf(err), "engine: "+moved.ID.String()+" moved to "+string(to)+" but its note \""+body+"\" could not be written", err)
	}
	w.Warnings = append(w.Warnings, Warning{Code: WarnMovedFromParent,
		Message: moved.ID.String() + " keeps parent_id " + p + " (its parent did not move); note added: " + body})
	return nil
}

// taskDescendants lists id's sub-tasks to move: the tree is built from
// the live tasks of every attached place (6.13: a child may live in
// another source of the same project; to included, so a child that moved
// earlier still links its own children), and the result is id's
// descendants not already in to, depth first with each parent before its
// children, so moving them in order always finds the parent in to.
func (e *Engine) taskDescendants(ctx context.Context, to store.Source, id ids.AnyID) ([]ids.AnyID, error) {
	seen := map[string]bool{} // Locations already read (one place, several names)
	var nodes []tasktree.Node
	have := map[string]bool{}
	inTo := map[string]bool{}
	// to first, so a row in to and in another place counts as in to.
	for _, name := range append([]store.Source{to}, e.order...) {
		if loc := e.stores[name].Location; loc != "" {
			if seen[loc] {
				continue
			}
			seen[loc] = true
		}
		rows, err := e.List(ctx, name, schema.TableTasks)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			k := r.ID.String()
			if name == to {
				inTo[k] = true
			}
			if have[k] {
				continue
			}
			have[k] = true
			nodes = append(nodes, tasktree.Node{ID: k, Parent: parentOf(r)})
		}
	}
	tree := tasktree.Build(nodes)
	var out []ids.AnyID
	for _, d := range tree.Descendants(id.String()) {
		if inTo[d] {
			continue
		}
		did, err := ids.ParseAny(d)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "engine: task id "+strconv.Quote(d), err)
		}
		out = append(out, did)
	}
	return out, nil
}

// moveChildren moves kids (taskDescendants order) to to after their root
// moved: each lands in Written.Also and its warnings are prefixed with its
// id. Interrupted, the error names how many moved; rerunning the parent's
// move is refused (it left), so the hint moves the rest one by one, which
// the child check allows because each parent is already in to.
func (e *Engine) moveChildren(ctx context.Context, to store.Source, kids []ids.AnyID, opt MoveOptions, w *Written) error {
	child := MoveOptions{DropInternal: opt.DropInternal, WithTime: opt.WithTime}
	for i, k := range kids {
		cw, err := e.MoveWith(ctx, Ref{Table: schema.TableTasks, ID: k}, to, child)
		if err != nil {
			return errs.Wrap(errs.CodeOf(err), "engine: "+strconv.Itoa(i)+" of "+strconv.Itoa(len(kids))+" sub-tasks moved; "+k.String()+" did not", err).
				WithHint("fix the cause, then lore move " + k.String() + " --to " + string(to) + " --with-children (and its later siblings)")
		}
		w.Also = append(w.Also, Changed{Store: to, Row: cw.Row})
		for _, wn := range cw.Warnings {
			wn.Message = k.String() + ": " + wn.Message
			w.Warnings = append(w.Warnings, wn)
		}
	}
	return nil
}
