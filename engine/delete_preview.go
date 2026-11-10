package engine

// A delete planned without writing (r3-projdel, DELETION_DESIGN 3.4): the
// same checks and the same group a soft delete would move, so a command
// can name the counts in its question, or refuse before it writes the
// first of several deletes.
//
//	PreviewDelete(refs…) ─► per ref: locate → actor refusal → writable
//	                        → E_IN_TRASH for a trashed row
//	                     ─► planTrashMany (cascade deps; restrict refuses
//	                        with E_HAS_DEPENDENTS unless the pointing row
//	                        is one of refs)
//	                     ─► DeletePreview (roots first, nothing written)

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// DeletePreview is the group a delete of some rows would move to the
// trash: the roots first (in the order asked), then every cascade
// dependent. Nothing is written to make it.
type DeletePreview struct {
	Rows []Changed
}

// Dependents is the number of rows beyond the roots (n roots asked).
func (p DeletePreview) Dependents(roots int) int {
	return max(len(p.Rows)-roots, 0)
}

// Count is the number of rows of table in the group, skipping the first
// skip rows (the roots, when only dependents count).
func (p DeletePreview) Count(table row.Table, skip int) int {
	n := 0
	for i, c := range p.Rows {
		if i >= skip && c.Row.Table == table {
			n++
		}
	}
	return n
}

// Tasks is the number of tasks among the dependents (3.4: a group with a
// task besides its roots always asks).
func (p DeletePreview) Tasks(roots int) int {
	return p.Count(schema.TableTasks, roots)
}

// PreviewDelete plans a delete of refs as one batch and returns the group
// it would move, with the refusals the delete itself would give (an actor
// row E_REFUSED, a read-only store, a trashed row E_IN_TRASH, a restrict
// relation E_HAS_DEPENDENTS). A row of refs never blocks another one, so
// a command that deletes several rows in one batch can check them all
// before its first write.
func (e *Engine) PreviewDelete(ctx context.Context, refs ...Ref) (DeletePreview, error) {
	roots := make([]trashItem, 0, len(refs))
	for _, ref := range refs {
		src, cur, err := e.locate(ctx, ref)
		if err != nil {
			return DeletePreview{}, err
		}
		if err = refuseActorDelete(cur); err != nil {
			return DeletePreview{}, err
		}
		st, err := e.writable(src)
		if err != nil {
			return DeletePreview{}, err
		}
		if cur.Deleted {
			return DeletePreview{}, deletedErr(ctx, st, cur, "is already deleted")
		}
		roots = append(roots, trashItem{src, cur})
	}
	plan, err := e.planTrashMany(ctx, roots)
	if err != nil {
		return DeletePreview{}, err
	}
	out := DeletePreview{Rows: make([]Changed, 0, len(plan))}
	for _, it := range plan {
		if _, werr := e.writable(it.src); werr != nil {
			return DeletePreview{}, werr // the delete checks every store before writing any
		}
		out.Rows = append(out.Rows, Changed{Store: it.src, Row: it.row})
	}
	return out, nil
}
