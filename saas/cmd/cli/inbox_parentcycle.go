package main

// inbox_parentcycle.go — the TASK 20 parent-cycle inbox item (6.13):
//
//	docs ─► tasktree.Build (parent_id, status) ─► Cycles()
//	     ─► a cycle with an open task of mine ─► one "parent" item per cycle
//
// Two clones can each move a task under the other (each write valid, the
// merged tree a cycle). Every machine already reads the smallest id of the
// cycle as top level (tasktree.breakCycles), and `lore task tree check`
// reports and fixes it; this item tells the people whose open work sits in
// the cycle, computed on read like the other FB-TASK-3 items (nothing
// stored, so every clone shows the same item). P3-TASK-CYCLE-1.

import (
	"fmt"
	"strings"

	"dbent/schema"
	"saas/pkg/aicoder/signals/coord"

	"github.com/thesatellite-ai/lore/logic/tasktree"
)

// cycleArrow joins the ids of a parent cycle in the item text.
const cycleArrow = " → "

// parentCycleItems returns one inboxParent item per parent cycle among
// docs that holds an open task mine reports true for. The item names the
// cycle from the task every machine reads as top level, and the repair.
func parentCycleItems(docs []coord.Doc, mine func(coord.Doc) bool) []inboxItem {
	byID := make(map[string]coord.Doc, len(docs))
	nodes := make([]tasktree.Node, 0, len(docs))
	for _, d := range docs {
		id := d.Str(docKeyID)
		byID[id] = d
		nodes = append(nodes, tasktree.Node{ID: id, Parent: d.Str(docKeyParent), State: treeState(d)})
	}
	var out []inboxItem
	for _, cyc := range tasktree.Build(nodes).Cycles() {
		mission, hit := "", false
		for _, id := range cyc {
			if d := byID[id]; docOpen(d) && mine(d) {
				mission, hit = d.Str(fieldMissionID), true
				break
			}
		}
		if !hit {
			continue
		}
		top := cyc[0]
		out = append(out, inboxItem{Mission: mission, Kind: inboxParent,
			Text: fmt.Sprintf("parent cycle %s%s%s: %s reads as top level; lore task tree check --fix (or lore task edit %s --parent <id> | --no-parent)",
				strings.Join(cyc, cycleArrow), cycleArrow, top, top, top)})
	}
	return out
}

// treeState is the tree's view of a task document's status.
func treeState(d coord.Doc) tasktree.State {
	switch d.Status() {
	case string(schema.TaskStatusDone):
		return tasktree.Done
	case string(schema.TaskStatusCancelled):
		return tasktree.Cancelled
	}
	return tasktree.Open
}
