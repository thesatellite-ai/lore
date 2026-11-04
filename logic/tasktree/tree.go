package tasktree

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// WarnParentCycle is the warning code of a merged tree with a cycle
// (TASK 6.13, 24): shown in list, show and doctor until fixed, never an
// exit status (a write that would make a cycle is E_CYCLE).
const WarnParentCycle = "E_PARENT_CYCLE"

// State is the closed tree view of a task status: open (todo, in_progress,
// blocked), done or cancelled.
type State int

// The states.
const (
	Open State = iota
	Done
	Cancelled
)

// Node is one task as the tree sees it. Parent "" is top level.
type Node struct {
	ID     string
	Parent string
	Rank   string
	State  State
}

// Tree is a built tree. Parent pointers to an id not in the input, or that
// close a cycle at the smallest id, are read as top level.
type Tree struct {
	nodes    map[string]Node
	parent   map[string]string // effective parent ("" = top)
	children map[string][]string
	roots    []string
	cycles   [][]string
}

// Build builds the tree. Children are ordered by rank, ties by id (6.13).
func Build(nodes []Node) *Tree {
	t := &Tree{nodes: map[string]Node{}, parent: map[string]string{}, children: map[string][]string{}}
	for _, n := range nodes {
		t.nodes[n.ID] = n
	}
	for id, n := range t.nodes {
		p := n.Parent
		if _, ok := t.nodes[p]; !ok || p == id {
			p = ""
		}
		t.parent[id] = p
	}
	t.breakCycles()
	for id, p := range t.parent {
		if p == "" {
			t.roots = append(t.roots, id)
		} else {
			t.children[p] = append(t.children[p], id)
		}
	}
	t.sort(t.roots)
	for _, c := range t.children {
		t.sort(c)
	}
	return t
}

// sort orders sibling ids by rank, then id.
func (t *Tree) sort(xs []string) {
	slices.SortFunc(xs, func(a, b string) int {
		return cmp.Or(cmp.Compare(t.nodes[a].Rank, t.nodes[b].Rank), cmp.Compare(a, b))
	})
}

// breakCycles finds every cycle of parent pointers and makes the byte-wise
// smallest id of each a top-level task (6.13: deterministic on every
// machine), recording the cycle for the E_PARENT_CYCLE warning.
func (t *Tree) breakCycles() {
	ids := make([]string, 0, len(t.parent))
	for id := range t.parent {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	done := map[string]bool{}
	for _, start := range ids {
		if done[start] {
			continue
		}
		pos := map[string]int{}
		var path []string
		at := start
		for at != "" && !done[at] {
			if i, seen := pos[at]; seen {
				cyc := slices.Clone(path[i:])
				least := slices.Min(cyc)
				t.parent[least] = ""
				// the cycle printed from its new top down to the closer
				k := slices.Index(cyc, least)
				rot := append(slices.Clone(cyc[k:]), cyc[:k]...)
				slices.Reverse(rot[1:])
				t.cycles = append(t.cycles, rot)
				break
			}
			pos[at] = len(path)
			path = append(path, at)
			at = t.parent[at]
		}
		for _, p := range path {
			done[p] = true
		}
	}
	slices.SortFunc(t.cycles, func(a, b []string) int { return cmp.Compare(a[0], b[0]) })
}

// Has reports whether id is in the tree.
func (t *Tree) Has(id string) bool { _, ok := t.nodes[id]; return ok }

// Parent is id's effective parent ("" = top level).
func (t *Tree) Parent(id string) string { return t.parent[id] }

// Roots are the top-level ids in order.
func (t *Tree) Roots() []string { return slices.Clone(t.roots) }

// Children are id's direct children in order.
func (t *Tree) Children(id string) []string { return slices.Clone(t.children[id]) }

// Cycles are the merged cycles found, each starting at the id now read as
// top level.
func (t *Tree) Cycles() [][]string { return t.cycles }

// Descendants are every task under id, depth first in sibling order.
func (t *Tree) Descendants(id string) []string {
	var out []string
	var walk func(string)
	walk = func(at string) {
		for _, c := range t.children[at] {
			out = append(out, c)
			walk(c)
		}
	}
	walk(id)
	return out
}

// Ancestors are id's parent, grandparent, … up to the top.
func (t *Tree) Ancestors(id string) []string {
	var out []string
	for p := t.parent[id]; p != ""; p = t.parent[p] {
		out = append(out, p)
	}
	return out
}

// Depth is id's level: 1 = top level (6.4).
func (t *Tree) Depth(id string) int { return len(t.Ancestors(id)) + 1 }

// Height is the number of levels of id's subtree, id included (1 = no
// children).
func (t *Tree) Height(id string) int {
	h := 0
	for _, c := range t.children[id] {
		h = max(h, t.Height(c))
	}
	return h + 1
}

// OpenChildren are id's direct children that are neither done nor
// cancelled.
func (t *Tree) OpenChildren(id string) []string {
	var out []string
	for _, c := range t.children[id] {
		if t.nodes[c].State == Open {
			out = append(out, c)
		}
	}
	return out
}

// IsLeaf reports a task with no open children (6.1): what can be worked on.
func (t *Tree) IsLeaf(id string) bool { return len(t.OpenChildren(id)) == 0 }

// Rollup is the derived progress of a subtree (6.6). Cancelled tasks count
// in neither total.
type Rollup struct {
	ChildrenTotal int `json:"children_total"`
	ChildrenDone  int `json:"children_done"`
	LeavesTotal   int `json:"leaves_total"`
	LeavesDone    int `json:"leaves_done"`
}

// Rollup computes id's progress on read (T3: never stored).
func (t *Tree) Rollup(id string) Rollup {
	var r Rollup
	for _, c := range t.children[id] {
		switch t.nodes[c].State {
		case Done:
			r.ChildrenTotal++
			r.ChildrenDone++
		case Open:
			r.ChildrenTotal++
		case Cancelled:
		}
	}
	for _, d := range t.Descendants(id) {
		if len(t.children[d]) > 0 {
			continue
		}
		switch t.nodes[d].State {
		case Done:
			r.LeavesTotal++
			r.LeavesDone++
		case Open:
			r.LeavesTotal++
		case Cancelled:
		}
	}
	return r
}

// CheckMove checks putting id (with its subtree) under parent: not under
// itself or a descendant (E_CYCLE, path shown) and the deepest moved task
// within maxDepth (E_TREE_DEPTH, TSQ1). id may be new (not in the tree):
// then it is a single task. parent "" always passes.
func (t *Tree) CheckMove(id, parent string, maxDepth int) error {
	if parent == "" {
		return nil
	}
	if parent == id {
		return errs.New(errs.Cycle, "task "+id+" cannot be its own parent").WithHint("pick another parent, or --no-parent")
	}
	if slices.Contains(t.Ancestors(parent), id) {
		path := append([]string{id}, reversed(t.pathDown(id, parent))...)
		return errs.New(errs.Cycle, "parent cycle: "+strings.Join(path, " → ")+" → "+id).
			WithHint("a task cannot go under its own sub-task; move the sub-task out first (--no-parent)")
	}
	height := 1
	if t.Has(id) {
		height = t.Height(id)
	}
	if depth := t.Depth(parent) + height; depth > maxDepth {
		chain := append(reversed(t.Ancestors(parent)), parent, id)
		return errs.New(errs.TreeDepth, "sub-task tree would be "+strconv.Itoa(depth)+" levels deep, over tasks.max_depth "+
			strconv.Itoa(maxDepth)+": "+strings.Join(chain, " → ")).
			WithHint("flatten the tree, or raise the policy: lore policy set tasks.max_depth <n> (at most 8)")
	}
	return nil
}

// pathDown is the ids from below id down to target (target included),
// target being a descendant of id, listed bottom up.
func (t *Tree) pathDown(id, target string) []string {
	var out []string
	for at := target; at != id && at != ""; at = t.parent[at] {
		out = append(out, at)
	}
	return out
}

func reversed(xs []string) []string {
	out := slices.Clone(xs)
	slices.Reverse(out)
	return out
}
