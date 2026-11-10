// Package tasktree is the pure sub-task tree (TASK_SYSTEM_DESIGN 6, CODE
// ORG L1): it builds the tree from parent pointers, finds leaves, depth and
// height, breaks merged cycles deterministically, computes the derived
// progress rollup and mints sibling ranks. No git, no DB, no engine: the
// task operations (ops/task) feed it rows and act on its answers.
//
//	ops/task (add/edit/split/list/show/done/delete)
//	   │ []Node{id, parent, rank, state}
//	   ▼
//	Build ──► Tree ── Children / Descendants / Ancestors / Depth / Height
//	   │             ├─ IsLeaf (no open children, 6.1)  ├─ Rollup (6.6, T3)
//	   │             └─ Cycles (6.13: smallest id is the top, E_PARENT_CYCLE)
//	   ▼
//	CheckMove (E_CYCLE, E_TREE_DEPTH: 6.4, 6.8)    rank: First/After/Between
//
// Invariant: nothing is stored here; children are never a field of the
// parent (T1), progress is derived on read (T3).
package tasktree
