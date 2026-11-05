package sqlite

import (
	"context"
	"database/sql"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/schema"
)

// The task_tree closure table (TASK_SYSTEM_DESIGN 6, 23 "cache tables"):
//
//	Apply (tasks rows stored) ── syncTaskNodes(source, ids) ─► _task_nodes
//	                              updateTaskTree(tx): new tree vs the closure's
//	                              depth 0/1 rows; only the subtrees of tasks
//	                              whose effective parent changed are rewritten
//	Rebuild(source) / init ───── refreshTaskTree(tx): the whole closure
//	                              (init also backfills _task_nodes from _rows)
//
//	_task_nodes = (source, id, parent, rank) of every live, untrashed task
//	─► logic/tasktree.Build (cycles broken at the smallest id, a missing or
//	   trashed parent reads as top level, 6.13) ─► (ancestor, descendant, depth)
//
// One closure per cache file over every source it holds (a combined cache
// links parents across sources; a parent in another file is top level here,
// the same as a missing one). Self pairs are kept at depth 0, so
// "the subtree of X" is one `WHERE ancestor_id = X`.
//
// DECISION OV41-CLOSURE, P3-PERF-1: a task write rewrites only the closure
// rows of the subtrees whose effective parent changed (a task's closure rows
// depend only on its ancestor chain, so a changed chain always passes a task
// whose effective parent changed: cycles made, broken or re-broken included).
// The tree is read from _task_nodes (three short columns per task), never
// from the task documents, whose json functions cost seconds on a store with
// large task docs (fx-perf: 20 tasks x 5,000 time entries, 1.6 s per task
// add). Depth is capped at 8 (TSQ1). Both tables are derived data: exempt
// from the audit rule like row_meta.

// tblTaskTree is the closure table (its spec name, read as task_tree_all in
// a lens); tblTaskNodes is the internal parent/rank copy it is built from.
const (
	tblTaskTree  = "task_tree"
	tblTaskNodes = "_task_nodes"
)

// taskTreeDDL creates the closure and node tables (idempotent; part of
// internalDDL's set, kept here beside the code that fills them).
var taskTreeDDL = []string{
	`CREATE TABLE IF NOT EXISTS "` + tblTaskTree + `" (ancestor_id TEXT NOT NULL, descendant_id TEXT NOT NULL, depth INTEGER NOT NULL, PRIMARY KEY (ancestor_id, descendant_id))`,
	`CREATE INDEX IF NOT EXISTS "ix_task_tree_desc" ON "` + tblTaskTree + `" (descendant_id)`,
	`CREATE TABLE IF NOT EXISTS "` + tblTaskNodes + `" (source TEXT NOT NULL, id TEXT NOT NULL, parent TEXT NOT NULL, rank TEXT NOT NULL, PRIMARY KEY (source, id))`,
}

// Fixed statements of the closure (schema constants only, no input).
const (
	// taskNodeCols is the node of a `_rows` row: the same projection for the
	// backfill and the per-row sync, so both read a document alike.
	taskNodeCols = `source, id, coalesce(json_extract(doc, '$.` + schema.FieldTaskParent + `'), ''), coalesce(json_extract(doc, '$.` + schema.FieldTaskRank + `'), '')`
	// taskNodeLive is the filter of a tree task: live, valid and untrashed.
	taskNodeLive = `tbl = '` + schema.TableTasks + `' AND deleted = 0 AND json_valid(doc) AND json_type(doc, '$.deleted_at') IS NULL`

	qNodesClear    = `DELETE FROM "` + tblTaskNodes + `"`
	qNodesBackfill = `INSERT INTO "` + tblTaskNodes + `" SELECT ` + taskNodeCols + ` FROM "` + tblRows + `" WHERE ` + taskNodeLive
	qNodeDelete    = `DELETE FROM "` + tblTaskNodes + `" WHERE source = ? AND id = ?`
	qNodeSync      = `INSERT INTO "` + tblTaskNodes + `" SELECT ` + taskNodeCols + ` FROM "` + tblRows + `" WHERE source = ? AND id = ? AND ` + taskNodeLive
	qNodesOfSource = `DELETE FROM "` + tblTaskNodes + `" WHERE source = ?`
	// an id held by two sources counts once: the first by (id, source)
	qTreeTasks = `SELECT id, parent, rank FROM "` + tblTaskNodes + `" ORDER BY id, source`

	qTreeClear     = `DELETE FROM "` + tblTaskTree + `"`
	qTreeInsert    = `INSERT OR IGNORE INTO "` + tblTaskTree + `" (ancestor_id, descendant_id, depth) VALUES (?, ?, ?)`
	qTreeDropDesc  = `DELETE FROM "` + tblTaskTree + `" WHERE descendant_id = ?`
	qTreeParents   = `SELECT descendant_id, ancestor_id, depth FROM "` + tblTaskTree + `" WHERE depth <= 1`
	qTreeNeedsFill = `SELECT (SELECT COUNT(*) FROM "` + tblTaskNodes + `") = 0 AND EXISTS (SELECT 1 FROM "` + tblRows + `" WHERE tbl = '` + schema.TableTasks + `' AND deleted = 0)`
)

// syncTaskNodes re-reads the nodes of the given task ids of source from
// `_rows` (one document each): a tombstoned, trashed or invalid row leaves
// no node.
func syncTaskNodes(ctx context.Context, tx *sql.Tx, source string, taskIDs []string) error {
	for _, id := range taskIDs {
		if _, err := tx.ExecContext(ctx, qNodeDelete, source, id); err != nil {
			return dbErr("task_tree: sync node", err)
		}
		if _, err := tx.ExecContext(ctx, qNodeSync, source, id); err != nil {
			return dbErr("task_tree: sync node", err)
		}
	}
	return nil
}

// dropTaskNodes forgets every node of source (Rebuild).
func dropTaskNodes(ctx context.Context, tx *sql.Tx, source string) error {
	if _, err := tx.ExecContext(ctx, qNodesOfSource, source); err != nil {
		return dbErr("task_tree: drop nodes", err)
	}
	return nil
}

// refreshTaskTree rebuilds the whole closure from `_task_nodes`.
func refreshTaskTree(ctx context.Context, tx *sql.Tx) error {
	nodes, err := treeNodes(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, qTreeClear); err != nil {
		return dbErr("task_tree: clear", err)
	}
	t := tasktree.Build(nodes)
	for _, n := range nodes {
		if err := insertClosure(ctx, tx, t, n.ID); err != nil {
			return err
		}
	}
	return nil
}

// updateTaskTree brings the closure in line with `_task_nodes` after a
// write, touching only the closure rows of the tasks whose presence or
// effective parent changed and of their new subtrees. The result equals refreshTaskTree's
// (TestTaskTreeIncrementalMatchesFull).
func updateTaskTree(ctx context.Context, tx *sql.Tx) error {
	nodes, err := treeNodes(ctx, tx)
	if err != nil {
		return err
	}
	t := tasktree.Build(nodes)
	// the closure's own view of the old tree: depth 0 = present, depth 1 =
	// effective parent
	oldParent := map[string]string{}
	rs, err := tx.QueryContext(ctx, qTreeParents)
	if err != nil {
		return dbErr("task_tree: read closure", err)
	}
	for rs.Next() {
		var d, a string
		var depth int
		if err := rs.Scan(&d, &a, &depth); err != nil {
			_ = rs.Close()
			return dbErr("task_tree: read closure", err)
		}
		if depth == 1 {
			oldParent[d] = a
		} else if _, ok := oldParent[d]; !ok {
			oldParent[d] = ""
		}
	}
	if err := rs.Close(); err != nil {
		return dbErr("task_tree: read closure", err)
	}
	if err := rs.Err(); err != nil {
		return dbErr("task_tree: read closure", err)
	}
	var moved []string
	for _, n := range nodes {
		if p, ok := oldParent[n.ID]; !ok || p != t.Parent(n.ID) {
			moved = append(moved, n.ID)
		}
	}
	for id := range oldParent {
		if !t.Has(id) {
			moved = append(moved, id)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	// a task whose chain changed has, on its new chain (itself upward), a
	// first task whose effective parent or presence changed: it is in that
	// task's new subtree, or is that task
	affected := map[string]bool{}
	for _, id := range moved {
		affected[id] = true
		for _, d := range t.Descendants(id) {
			affected[d] = true
		}
	}
	ids := slices.Sorted(maps.Keys(affected))
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, qTreeDropDesc, id); err != nil {
			return dbErr("task_tree: update", err)
		}
		if t.Has(id) {
			if err := insertClosure(ctx, tx, t, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertClosure writes id's closure rows: itself at depth 0 and each
// ancestor at its distance.
func insertClosure(ctx context.Context, tx *sql.Tx, t *tasktree.Tree, id string) error {
	if _, err := tx.ExecContext(ctx, qTreeInsert, id, id, 0); err != nil {
		return dbErr("task_tree: fill", err)
	}
	for d, a := range t.Ancestors(id) {
		if _, err := tx.ExecContext(ctx, qTreeInsert, a, id, d+1); err != nil {
			return dbErr("task_tree: fill", err)
		}
	}
	return nil
}

// treeNodes reads every node of the file (an id held by two sources counts
// once, the first by (id, source)).
func treeNodes(ctx context.Context, q *sql.Tx) ([]tasktree.Node, error) {
	rs, err := q.QueryContext(ctx, qTreeTasks)
	if err != nil {
		return nil, dbErr("task_tree: read tasks", err)
	}
	defer func() { _ = rs.Close() }()
	var out []tasktree.Node
	seen := map[string]bool{}
	for rs.Next() {
		var n tasktree.Node
		if err := rs.Scan(&n.ID, &n.Parent, &n.Rank); err != nil {
			return nil, dbErr("task_tree: read tasks", err)
		}
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, n)
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("task_tree: read tasks", err)
	}
	return out, nil
}

// backfillTaskTree fills the nodes and the closure of a file written before
// `_task_nodes` existed (no nodes, live task rows held).
func backfillTaskTree(ctx context.Context, tx *sql.Tx) error {
	var need bool
	if err := tx.QueryRowContext(ctx, qTreeNeedsFill).Scan(&need); err != nil {
		return dbErr("task_tree: check", err)
	}
	if !need {
		return nil
	}
	if _, err := tx.ExecContext(ctx, qNodesClear); err != nil {
		return dbErr("task_tree: backfill nodes", err)
	}
	if _, err := tx.ExecContext(ctx, qNodesBackfill); err != nil {
		return dbErr("task_tree: backfill nodes", err)
	}
	return refreshTaskTree(ctx, tx)
}
