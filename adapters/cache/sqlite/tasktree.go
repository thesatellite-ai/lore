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
//	                              updateTaskTree(tx, ids): the nodes around ids
//	                              vs the closure's depth 0/1 rows; only the
//	                              subtrees of tasks whose effective parent
//	                              changed are rewritten (treeview.go)
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
	// a write's update looks nodes up by id and by parent (devco:D-1019)
	`CREATE INDEX IF NOT EXISTS "ix_task_nodes_id" ON "` + tblTaskNodes + `" (id, source)`,
	`CREATE INDEX IF NOT EXISTS "ix_task_nodes_parent" ON "` + tblTaskNodes + `" (parent)`,
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

	qTreeClear    = `DELETE FROM "` + tblTaskTree + `"`
	qTreeInsert   = `INSERT OR IGNORE INTO "` + tblTaskTree + `" (ancestor_id, descendant_id, depth) VALUES (?, ?, ?)`
	qTreeDropDesc = `DELETE FROM "` + tblTaskTree + `" WHERE descendant_id = ?`
	// one node by id: the copy of the first source, as qTreeTasks counts it
	qNodeOne = `SELECT parent FROM "` + tblTaskNodes + `" WHERE id = ? ORDER BY source LIMIT 1`
	// the ids that name a task as their parent (any source; the caller
	// checks each one's counted copy)
	qNodeKids = `SELECT DISTINCT id FROM "` + tblTaskNodes + `" WHERE parent = ?`
	// the closure rows above one task: depth 0 = present, 1 = its parent
	qTreeAbove     = `SELECT ancestor_id, depth FROM "` + tblTaskTree + `" WHERE descendant_id = ?`
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
// write that synced the nodes of changed, touching only the closure rows of
// the tasks whose presence or effective parent changed and of their new
// subtrees. The result equals refreshTaskTree's
// (TestTaskTreeIncrementalMatchesFull).
//
// Why it reads only around changed (devco:D-1019): reading every node and
// the whole closure on each write made the first build of a cache, one
// Apply per folder, O(folders x tasks): 22-28 min on a 1.26M-row store.
// A task's effective parent depends only on its own node, on whether its
// parent has a node, and on whether it is the smallest id of a cycle of
// parent links (tasktree.Build), so a write can change it only for:
//   - a changed task itself;
//   - a task naming a changed task as parent (that parent came or went);
//   - the members of a cycle through a changed task (made or re-broken);
//   - the smallest id of a cycle a changed task left or broke: in the old
//     tree it is the top of that task's chain, one of its old ancestors.
func updateTaskTree(ctx context.Context, tx *sql.Tx, changed []string) error {
	v := newTreeView(ctx, tx)
	cand := map[string]bool{}
	done := map[string]bool{} // a changed id already looked around (cand may hold it as another's neighbour)
	for _, id := range changed {
		if done[id] {
			continue
		}
		done[id] = true
		cand[id] = true
		kids, err := v.linkedKids(id)
		if err != nil {
			return err
		}
		for _, k := range kids {
			cand[k] = true
		}
		old, err := v.oldAbove(id)
		if err != nil {
			return err
		}
		for _, a := range old.ancestors {
			cand[a] = true
		}
		cyc, err := v.cycleThrough(id)
		if err != nil {
			return err
		}
		for _, m := range cyc {
			cand[m] = true
		}
	}
	var moved []string
	for _, id := range slices.Sorted(maps.Keys(cand)) {
		old, err := v.oldAbove(id)
		if err != nil {
			return err
		}
		has, err := v.has(id)
		if err != nil {
			return err
		}
		p := ""
		if has {
			if p, err = v.parent(id); err != nil {
				return err
			}
		}
		if has != old.present || p != old.parent {
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
		if err := v.subtree(id, affected); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(affected)) {
		if _, err := txExec(ctx, tx, qTreeDropDesc, id); err != nil {
			return dbErr("task_tree: update", err)
		}
		has, err := v.has(id)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		up, err := v.ancestors(id)
		if err != nil {
			return err
		}
		if err := insertChain(ctx, tx, id, up); err != nil {
			return err
		}
	}
	return nil
}

// insertClosure writes id's closure rows: itself at depth 0 and each
// ancestor at its distance.
func insertClosure(ctx context.Context, tx *sql.Tx, t *tasktree.Tree, id string) error {
	return insertChain(ctx, tx, id, t.Ancestors(id))
}

// insertChain writes id's closure rows given its ancestors, nearest first.
func insertChain(ctx context.Context, tx *sql.Tx, id string, ancestors []string) error {
	if _, err := txExec(ctx, tx, qTreeInsert, id, id, 0); err != nil {
		return dbErr("task_tree: fill", err)
	}
	for d, a := range ancestors {
		if _, err := txExec(ctx, tx, qTreeInsert, a, id, d+1); err != nil {
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
	treeNodeReads.Add(int64(len(out)))
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
