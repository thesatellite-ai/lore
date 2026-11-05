package sqlite

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/schema"
)

// The task_rollup view of `lore sql` (TASK_SYSTEM_DESIGN 6.6, 23): one row
// per task with the numbers of 6.6, computed at read time (T3: progress is
// never stored):
//
//	lens setup ─► createTaskRollup (after createTimeSpent, before query_only)
//	   tasks_all (grouped by id) + task_tree_all (closure) + task_time_spans
//	   ─► TEMP VIEW task_rollup
//
// Columns: task_id; children_total, children_done (direct children,
// cancelled in neither); leaves_total, leaves_done (subtree leaves: a task
// with no child at all); estimate_own; estimate_subtree, estimate_done (sum
// over the subtree's leaves, never the parent's own, 9.2; NULL without
// sub-tasks); time_subtree (counted seconds of the task and every
// descendant, 6.11; 0 when the lens has no time spans); checklist_total,
// checklist_done (own live items, 7.2).

// viewTaskRollup names the view.
const viewTaskRollup = "task_rollup"

// Task columns the view reads.
const (
	colEstimate  = schema.FieldTaskEstimate
	colChecklist = schema.FieldTaskChecklist
)

// taskRollupSQL is the view (schema constants only, no input). sub(cond)
// is the closure joined to the descendant's row, depth > 0.
const taskRollupSQL = `CREATE TEMP VIEW "` + viewTaskRollup + `" AS
WITH t AS (SELECT id, max("` + colStatus + `") AS status, max("` + colEstimate + `") AS estimate, max("` + colChecklist + `") AS checklist
		FROM "` + schema.TableTasks + lensViewSuffix + `" GROUP BY id),
	tt AS (SELECT DISTINCT ancestor_id, descendant_id, depth FROM "` + tblTaskTree + lensViewSuffix + `"),
	leaf AS (SELECT id FROM t WHERE NOT EXISTS (SELECT 1 FROM tt WHERE tt.ancestor_id = t.id AND tt.depth = 1)),
	sub AS (SELECT tt.ancestor_id AS root, tt.depth AS depth, c.id AS id, c.status AS status, c.estimate AS estimate,
			c.id IN (SELECT id FROM leaf) AS is_leaf
		FROM tt JOIN t c ON c.id = tt.descendant_id WHERE tt.depth > 0),
	secs AS (SELECT task_id, sum(counted_seconds) AS s FROM "` + tblTimeSpans + `" GROUP BY task_id)
SELECT t.id AS task_id,
	(SELECT count(*) FROM sub WHERE sub.root = t.id AND sub.depth = 1 AND sub.status <> '` + schema.TaskStatusCancelled + `') AS children_total,
	(SELECT count(*) FROM sub WHERE sub.root = t.id AND sub.depth = 1 AND sub.status = '` + schema.TaskStatusDone + `') AS children_done,
	(SELECT count(*) FROM sub WHERE sub.root = t.id AND sub.is_leaf AND sub.status <> '` + schema.TaskStatusCancelled + `') AS leaves_total,
	(SELECT count(*) FROM sub WHERE sub.root = t.id AND sub.is_leaf AND sub.status = '` + schema.TaskStatusDone + `') AS leaves_done,
	CAST(t.estimate AS REAL) AS estimate_own,
	(SELECT sum(CAST(sub.estimate AS REAL)) FROM sub WHERE sub.root = t.id AND sub.is_leaf AND sub.status <> '` + schema.TaskStatusCancelled + `') AS estimate_subtree,
	(SELECT sum(CAST(sub.estimate AS REAL)) FROM sub WHERE sub.root = t.id AND sub.is_leaf AND sub.status = '` + schema.TaskStatusDone + `') AS estimate_done,
	coalesce((SELECT sum(secs.s) FROM tt JOIN secs ON secs.task_id = tt.descendant_id WHERE tt.ancestor_id = t.id), 0) AS time_subtree,
	(SELECT count(*) FROM json_each(CASE WHEN json_valid(t.checklist) THEN t.checklist ELSE '[]' END) j
		WHERE coalesce(json_extract(j.value, '$.deleted_at'), '') = '') AS checklist_total,
	(SELECT count(*) FROM json_each(CASE WHEN json_valid(t.checklist) THEN t.checklist ELSE '[]' END) j
		WHERE coalesce(json_extract(j.value, '$.deleted_at'), '') = '' AND json_extract(j.value, '$.done') = 1) AS checklist_done
FROM t`

// createTaskRollup builds task_rollup when the lens has the task columns,
// the closure and the time spans (an older file lacking one gets no view).
func (l *Lens) createTaskRollup(ctx context.Context, tables map[string][]srcTable) error {
	if !hasCols(tables, schema.TableTasks, colStatus, colEstimate, colChecklist) || len(tables[tblTaskTree]) == 0 ||
		!slices.Contains(l.views, viewTimeSpent) {
		return nil
	}
	if _, err := l.conn.ExecContext(ctx, taskRollupSQL); err != nil {
		return dbErr("task_rollup: create view", err)
	}
	l.views = append(l.views, viewTaskRollup)
	return nil
}
