package sqlite

import (
	"context"
	"database/sql"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/schema"
)

// The task_tree closure table (TASK_SYSTEM_DESIGN 6, 23 "cache tables"):
//
//	Apply (a tasks row stored) ─┐
//	Rebuild(source) ────────────┼─► refreshTaskTree(tx)
//	init (table empty, tasks) ──┘     _rows tasks, live, not in the trash
//	                                  ─► logic/tasktree.Build (cycles broken at
//	                                     the smallest id, a missing or trashed
//	                                     parent reads as top level, 6.13)
//	                                  ─► DELETE + INSERT (ancestor, descendant, depth)
//
// One closure per cache file over every source it holds (a combined cache
// links parents across sources; a parent in another file is top level here,
// the same as a missing one). Self pairs are kept at depth 0, so
// "the subtree of X" is one `WHERE ancestor_id = X`.
//
// DECISION OV41-CLOSURE: the whole file's closure is rebuilt when any task
// row lands (the design says "changed ids only"); depth is capped at 8
// (TSQ1), so a rebuild is O(tasks × 8) and needs no change tracking. The
// table is derived data: exempt from the audit rule like row_meta.

// tblTaskTree is the closure table (its spec name, read as task_tree_all in
// a lens).
const tblTaskTree = "task_tree"

// taskTreeDDL creates the closure table (idempotent; part of internalDDL's
// set, kept here beside the code that fills it).
var taskTreeDDL = []string{
	`CREATE TABLE IF NOT EXISTS "` + tblTaskTree + `" (ancestor_id TEXT NOT NULL, descendant_id TEXT NOT NULL, depth INTEGER NOT NULL, PRIMARY KEY (ancestor_id, descendant_id))`,
	`CREATE INDEX IF NOT EXISTS "ix_task_tree_desc" ON "` + tblTaskTree + `" (descendant_id)`,
}

// Fixed statements of the closure (schema constants only, no input).
const (
	qTreeTasks = `SELECT id, coalesce(json_extract(doc, '$.` + schema.FieldTaskParent + `'), ''), coalesce(json_extract(doc, '$.` + schema.FieldTaskRank + `'), '')
		FROM "` + tblRows + `" WHERE tbl = '` + schema.TableTasks + `' AND deleted = 0 AND json_valid(doc)
		AND json_type(doc, '$.deleted_at') IS NULL ORDER BY id`
	qTreeClear  = `DELETE FROM "` + tblTaskTree + `"`
	qTreeInsert = `INSERT OR IGNORE INTO "` + tblTaskTree + `" (ancestor_id, descendant_id, depth) VALUES (?, ?, ?)`
	qTreeEmpty  = `SELECT (SELECT COUNT(*) FROM "` + tblTaskTree + `") = 0 AND EXISTS (SELECT 1 FROM "` + tblRows + `" WHERE tbl = '` + schema.TableTasks + `' AND deleted = 0)`
)

// refreshTaskTree rebuilds the closure from `_rows`.
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
		if _, err := tx.ExecContext(ctx, qTreeInsert, n.ID, n.ID, 0); err != nil {
			return dbErr("task_tree: fill", err)
		}
		for d, a := range t.Ancestors(n.ID) {
			if _, err := tx.ExecContext(ctx, qTreeInsert, a, n.ID, d+1); err != nil {
				return dbErr("task_tree: fill", err)
			}
		}
	}
	return nil
}

// treeNodes reads every live, untrashed task of the file (an id held by two
// sources counts once, the first by source order of the scan).
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

// backfillTaskTree fills the closure of a file written before the table
// existed (empty table, live tasks held).
func backfillTaskTree(ctx context.Context, tx *sql.Tx) error {
	var need bool
	if err := tx.QueryRowContext(ctx, qTreeEmpty).Scan(&need); err != nil {
		return dbErr("task_tree: check", err)
	}
	if !need {
		return nil
	}
	return refreshTaskTree(ctx, tx)
}
