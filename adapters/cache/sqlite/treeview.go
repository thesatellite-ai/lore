package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync/atomic"
)

// treeView answers updateTaskTree's questions about the tree after a write
// by reading single nodes and closure rows by index, never the whole
// `_task_nodes` or `task_tree` (devco:D-1019). Its answers equal
// tasktree.Build's over every node: a node's link is its parent when that
// parent has a node and is not itself, and the smallest id of each cycle of
// links is top level instead. Reads are kept for the one update: the nodes
// are final before it starts and the closure is read before it is written.
type treeView struct {
	ctx context.Context
	tx  *sql.Tx

	nodes   map[string]viewNode   // id → its counted node
	old     map[string]oldClosure // id → the closure rows above it, before the write
	settled map[string]bool       // ids whose cycle membership is known
	cycleOf map[string][]string   // id → the members of the cycle it is on
	broken  map[string]bool       // the smallest id of each cycle: top level
}

// viewNode is one id's counted node: ok is false when no source holds a
// live task with that id.
type viewNode struct {
	parent string
	ok     bool
}

// oldClosure is what the closure held for one task before the write.
type oldClosure struct {
	present   bool
	parent    string   // depth 1, "" at top level
	ancestors []string // depth >= 1, any order
}

// treeNodeReads counts the node rows the closure update read (treeNodes
// and treeView.node), so a test can tell that a write reads around the
// tasks it changed, not every task of the file.
var treeNodeReads atomic.Int64

func newTreeView(ctx context.Context, tx *sql.Tx) *treeView {
	return &treeView{
		ctx: ctx, tx: tx,
		nodes:   map[string]viewNode{},
		old:     map[string]oldClosure{},
		settled: map[string]bool{},
		cycleOf: map[string][]string{},
		broken:  map[string]bool{},
	}
}

// node reads id's counted node: the copy of the first source, as
// treeNodes counts an id two sources hold.
func (v *treeView) node(id string) (viewNode, error) {
	if n, ok := v.nodes[id]; ok {
		return n, nil
	}
	var n viewNode
	err := txQueryRow(v.ctx, v.tx, qNodeOne, id).Scan(&n.parent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return viewNode{}, dbErr("task_tree: read node", err)
	default:
		n.ok = true
		treeNodeReads.Add(1)
	}
	v.nodes[id] = n
	return n, nil
}

// has reports whether id is a task of the tree.
func (v *treeView) has(id string) (bool, error) {
	n, err := v.node(id)
	return n.ok, err
}

// link is id's parent before cycles are broken: "" when it names no task
// of the tree or itself.
func (v *treeView) link(id string) (string, error) {
	n, err := v.node(id)
	if err != nil || !n.ok || n.parent == "" || n.parent == id {
		return "", err
	}
	ok, err := v.has(n.parent)
	if err != nil || !ok {
		return "", err
	}
	return n.parent, nil
}

// settle follows the links up from id until a top-level task, a task
// already settled or a repeat; a repeat closes a cycle, whose smallest id
// is top level (tasktree.Build's breakCycles). Every task passed is then
// settled: one walk per task per update.
func (v *treeView) settle(id string) error {
	var path []string
	pos := map[string]int{}
	for at := id; at != "" && !v.settled[at]; {
		if i, seen := pos[at]; seen {
			cyc := slices.Clone(path[i:])
			v.broken[slices.Min(cyc)] = true
			for _, m := range cyc {
				v.cycleOf[m] = cyc
			}
			break
		}
		pos[at] = len(path)
		path = append(path, at)
		next, err := v.link(at)
		if err != nil {
			return err
		}
		at = next
	}
	for _, p := range path {
		v.settled[p] = true
	}
	return nil
}

// parent is id's effective parent in the new tree ("" = top level).
func (v *treeView) parent(id string) (string, error) {
	if err := v.settle(id); err != nil {
		return "", err
	}
	if v.broken[id] {
		return "", nil
	}
	return v.link(id)
}

// cycleThrough returns the members of the cycle of links id is on (nil
// when it is on none).
func (v *treeView) cycleThrough(id string) ([]string, error) {
	if err := v.settle(id); err != nil {
		return nil, err
	}
	return v.cycleOf[id], nil
}

// linkedKids returns the tasks whose counted node names id as parent
// (cycle members included: the caller decides their effective parent).
func (v *treeView) linkedKids(id string) ([]string, error) {
	rs, err := txQuery(v.ctx, v.tx, qNodeKids, id)
	if err != nil {
		return nil, dbErr("task_tree: read children", err)
	}
	var named []string
	for rs.Next() {
		var k string
		if err := rs.Scan(&k); err != nil {
			_ = rs.Close()
			return nil, dbErr("task_tree: read children", err)
		}
		named = append(named, k)
	}
	if err := rs.Close(); err != nil {
		return nil, dbErr("task_tree: read children", err)
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("task_tree: read children", err)
	}
	var out []string
	for _, k := range named {
		n, err := v.node(k) // another source's copy may name another parent
		if err != nil {
			return nil, err
		}
		if n.ok && n.parent == id && k != id {
			out = append(out, k)
		}
	}
	return out, nil
}

// children returns id's children in the new tree.
func (v *treeView) children(id string) ([]string, error) {
	kids, err := v.linkedKids(id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range kids {
		p, err := v.parent(k)
		if err != nil {
			return nil, err
		}
		if p == id {
			out = append(out, k)
		}
	}
	return out, nil
}

// subtree adds id and its descendants in the new tree to into.
func (v *treeView) subtree(id string, into map[string]bool) error {
	stack := []string{id}
	for len(stack) > 0 {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if into[at] {
			continue
		}
		into[at] = true
		kids, err := v.children(at)
		if err != nil {
			return err
		}
		stack = append(stack, kids...)
	}
	return nil
}

// ancestors returns id's ancestors in the new tree, nearest first.
func (v *treeView) ancestors(id string) ([]string, error) {
	var out []string
	seen := map[string]bool{id: true}
	for {
		p, err := v.parent(id)
		if err != nil {
			return nil, err
		}
		if p == "" || seen[p] { // a broken cycle has no repeat; the guard keeps a bug finite
			return out, nil
		}
		seen[p] = true
		out = append(out, p)
		id = p
	}
}

// oldAbove reads what the closure held above id before the write.
func (v *treeView) oldAbove(id string) (oldClosure, error) {
	if o, ok := v.old[id]; ok {
		return o, nil
	}
	rs, err := txQuery(v.ctx, v.tx, qTreeAbove, id)
	if err != nil {
		return oldClosure{}, dbErr("task_tree: read closure", err)
	}
	var o oldClosure
	for rs.Next() {
		var a string
		var depth int
		if err := rs.Scan(&a, &depth); err != nil {
			_ = rs.Close()
			return oldClosure{}, dbErr("task_tree: read closure", err)
		}
		switch {
		case depth == 0:
			o.present = true
		case depth == 1:
			o.parent = a
			o.ancestors = append(o.ancestors, a)
		default:
			o.ancestors = append(o.ancestors, a)
		}
	}
	if err := rs.Close(); err != nil {
		return oldClosure{}, dbErr("task_tree: read closure", err)
	}
	if err := rs.Err(); err != nil {
		return oldClosure{}, dbErr("task_tree: read closure", err)
	}
	v.old[id] = o
	return o, nil
}
