package task

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/rowref"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// `task add|edit --ref [--relation]` (v0.1.17 task_refs.go, LORE_TEAM_SPEC
// §1, F15, RES 3.11): the row work is the shared ops/internal/rowref
// helper, also used by decision and incident (RQ7, OQ22).
const (
	// refConfidence is what a --ref / relate pointer records.
	refConfidence = schema.ConfidenceExtracted
	// Field names of knowledge_refs (relate.go reads them too).
	fieldSrcTable    = "src_table"
	fieldSrcID       = "src_id"
	fieldDstTable    = "dst_table"
	fieldDstID       = "dst_id"
	fieldRelation    = "relation"
	fieldConfidence  = "confidence"
	tableKnowledgeRf = row.Table(schema.TableKnowledgeRefs)
)

// RefOut is one pointer of a task in `task show`.
type RefOut = rowref.Out

// taskRef is one parsed --ref.
type taskRef = rowref.Ref

// reservedRelations are the task relation names `task relate` owns
// (TASK 8.1): --relation may not write them, so a duplicates edge always
// comes with its cascade. DECISION OV15B-REL: refuse, with the command.
func reservedRelations() map[string]string {
	m := map[string]string{"blocked_by": "use lore task edit --after / --blocks"}
	for _, r := range Relations() {
		m[r] = "use lore task relate --id <id> --" + strings.ReplaceAll(r, "_", "-") + " <other>"
	}
	return m
}

// relationOf checks a --relation value for a task.
func relationOf(raw string) (string, error) { return rowref.Relation(raw, reservedRelations()) }

// parseRefs reads --ref values (rowref.Parse).
func (o ops) parseRefs(ctx context.Context, raw []string) ([]taskRef, error) {
	return rowref.Parse(ctx, o.env, raw)
}

// checkRefs applies F15 before anything is written (rowref.Check).
func (o ops) checkRefs(ctx context.Context, from store.Source, refs []taskRef) error {
	return rowref.Check(ctx, o.env, from, refs)
}

// saveRefs records refs of task id (project prj) in store src.
func (o ops) saveRefs(ctx context.Context, src store.Source, prj string, id ids.AnyID, rel string, refs []taskRef) error {
	return rowref.Save(ctx, o.env, src, table, prj, id, rel, refs)
}

// refsOf lists the pointers of task id.
func (o ops) refsOf(ctx context.Context, id ids.AnyID) ([]RefOut, error) {
	return rowref.List(ctx, o.env, table, id, nil)
}

// loaded reports whether a ref target is a live row of an attached store.
func (o ops) loaded(ctx context.Context, tbl, rawID string) bool {
	t, err := row.ParseTable(tbl)
	if err != nil {
		return false
	}
	id, err := ids.ParseAny(rawID)
	if err != nil {
		return false
	}
	_, r, err := o.env.Get(ctx, engine.Ref{Table: t, ID: id})
	return err == nil && !r.Deleted
}

// text is a field's string form ("" when unset).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil {
		return v.String()
	}
	return ""
}
