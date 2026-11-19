package task

import (
	"context"
	"maps"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// taskTag types a task id the import pins before the create (D-214).
type taskTag struct{}

// Prefix implements ids.Tag.
func (taskTag) Prefix() ids.Prefix { return ids.PrefixTask }

// importedState writes an import's checklist and first status into the
// create's fields (AddIn.status / AddIn.checklist, D-214). The status goes
// through setStatus with the would-be row as the current one, exactly as
// the `task edit --status` the import made after the create did
// (completed_at, close_reason, the caller's time span), so the task reads
// the same; the id is pinned first (engine.WithCreateID), because that
// transition needs the row's id. Outside an import: ctx unchanged.
func (o ops) importedState(ctx context.Context, in AddIn, fields map[string]value.Value) (context.Context, error) {
	if len(in.checklist) > 0 {
		if err := putChecklist(fields, in.checklist); err != nil {
			return ctx, err
		}
	}
	if in.status == "" || in.status == schema.TaskStatusTodo {
		return ctx, nil
	}
	id, err := ids.New[taskTag]()
	if err != nil {
		return ctx, err
	}
	ctx = engine.WithCreateID(ctx, id.Any())
	cur := row.Row{ID: id.Any(), Table: table, Fields: maps.Clone(fields)}
	_, err = o.setStatus(ctx, store.Source(in.To), cur, in.status, false, fields)
	return ctx, err
}

// stagedNote is addNote for a task the same staged batch creates (D-214):
// the task is not readable yet, so its store comes from the add, not from
// a read; the comment row is the one addNote writes.
func (o ops) stagedNote(ctx context.Context, src store.Source, id, body string) error {
	tid, err := taskID(id)
	if err != nil {
		return err
	}
	fields := map[string]value.Value{}
	if fields[schema.FieldEntityTable], err = value.TextOf(string(table)); err != nil {
		return err
	}
	if fields[schema.FieldEntityID], err = value.RefOf(tid); err != nil {
		return err
	}
	if fields["body"], err = value.TextOf(body); err != nil {
		return err
	}
	if a := o.currentActorRef(ctx); a != nil && a.Kind() != value.KindNull {
		fields["created_by_actor_id"] = a
	}
	_, err = o.env.Create(ctx, src, row.Table(schema.TableComments), fields)
	return err
}

// inTree marks the records of an import that are part of a task tree: a
// parent column (a record of the file or a task of the target) or a record
// another one names as its parent. Their adds read the parent back (rank,
// inheritance, width), so they are written one by one, outside the staged
// batch; every other record joins it (D-214).
func inTree(recs []importRecord) []bool {
	tree := make([]bool, len(recs))
	for i, r := range recs {
		if r.parent >= 0 || strings.TrimSpace(r.cols[colParent]) != "" {
			tree[i] = true
		}
		if r.parent >= 0 {
			tree[r.parent] = true
		}
	}
	return tree
}

// keyOfRecord is the (mission, key) a record's --key dedupe looks at
// inside one staged batch ("" without a key): add's own byKey check reads
// the store, where a staged row is not yet.
func keyOfRecord(r importRecord) string {
	k := strings.TrimSpace(r.cols[colKey])
	if k == "" {
		return ""
	}
	return strings.TrimSpace(r.cols[colMission]) + "\x00" + k
}
