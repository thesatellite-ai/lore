package task

// lore task tree check [--fix] (TASK_SYSTEM_DESIGN 6.13, 24): the tree
// check after a merge, as a command. It reports, never exits non-zero for a
// finding (6.13: shown, not an exit status):
//
//	treeCheck ─► o.forest("") (live, untrashed tasks; cycles broken at the
//	          │                smallest id by logic/tasktree)
//	          ├─ parent_cycle   Tree.Cycles            fix: --fix writes the shown result
//	          ├─ orphan         parent_id not live ─► InTrash? "parent in trash" : "parent missing"
//	          ├─ parent_closed  open child under a done/cancelled parent
//	          ├─ over_depth     Depth > tasks.max_depth of the child's store
//	          └─ shared_rank    siblings with one rank (info: ordered by id, none needed)
//	          ─► --fix: only parent_cycle (the one row 6.13 gives a written fix,
//	             "lore doctor --fix tasks writes the shown result"); every other
//	             finding names its command and is left alone.

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// OpTreeCheck is `lore task tree check`.
const OpTreeCheck = "task.tree.check"

// TreeFindingKind is the closed set of tree check findings (6.13 rows).
type TreeFindingKind string

// The findings.
const (
	FindParentCycle  TreeFindingKind = "parent_cycle"
	FindOrphan       TreeFindingKind = "orphan"
	FindParentClosed TreeFindingKind = "parent_closed"
	FindOverDepth    TreeFindingKind = "over_depth"
	FindSharedRank   TreeFindingKind = "shared_rank"
)

// TreeCheckIn is the input of task.tree.check.
type TreeCheckIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"check one project's tasks (prj_… id; default every project)"`
	Fix     bool   `json:"fix,omitempty" flag:"fix" desc:"write the shown result for parent cycles (the smallest id becomes top level); other findings are reported only"`
}

// TreeFinding is one finding.
type TreeFinding struct {
	Kind TreeFindingKind `json:"kind"`
	// Code is the warning code (E_PARENT_CYCLE for a cycle, "" otherwise).
	Code    string `json:"code,omitempty"`
	ID      string `json:"id"`
	Parent  string `json:"parent,omitempty"`
	Message string `json:"message"`
	// Fix is the command that repairs it ("" = none needed).
	Fix   string `json:"fix,omitempty"`
	Fixed bool   `json:"fixed,omitempty"`
}

// TreeCheckOut is the output of task.tree.check.
type TreeCheckOut struct {
	Tasks    int           `json:"tasks"`
	Findings []TreeFinding `json:"findings"`
	Fixed    int           `json:"fixed"`
	Human    string        `json:"-"`
}

// Text implements op.Texter.
func (o TreeCheckOut) Text() string { return o.Human }

func treeCheckOps(o ops) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpTreeCheck, Summary: "Check the sub-task tree: parent cycles, orphans, open children of closed parents, depth, shared ranks (--fix repairs cycles)",
			// --fix writes, so the op is a write; without --fix it writes nothing.
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: work.WriteErrors,
			Examples: []op.Example{{Title: "check as JSON", CLI: "lore task tree check --json", Want: `"findings"`}},
		}, o.treeCheck),
	}
}

func (o ops) treeCheck(ctx context.Context, in TreeCheckIn) (TreeCheckOut, error) {
	if in.Project != "" {
		if _, err := work.ParseID("project", ids.PrefixProject, in.Project); err != nil {
			return TreeCheckOut{}, err
		}
	}
	f, err := o.forest(ctx, in.Project)
	if err != nil {
		return TreeCheckOut{}, err
	}
	out := TreeCheckOut{Tasks: len(f.rows), Findings: []TreeFinding{}}
	for _, c := range f.tree.Cycles() {
		out.Findings = append(out.Findings, TreeFinding{Kind: FindParentCycle, Code: tasktree.WarnParentCycle, ID: c[0],
			Parent:  work.Field(f.row(c[0]).Row, fieldParent),
			Message: "parent cycle " + strings.Join(c, " → ") + " → " + c[0] + "; " + c[0] + " is shown at top level",
			Fix:     "lore task tree check --fix (or lore task edit --id " + c[0] + " --parent …)"})
	}
	order := make([]string, 0, len(f.rows))
	for id := range f.rows {
		order = append(order, id)
	}
	slices.Sort(order)
	depthCap := map[string]int{}
	for _, id := range order {
		found, err := o.nodeFindings(ctx, f, id, depthCap)
		if err != nil {
			return TreeCheckOut{}, err
		}
		out.Findings = append(out.Findings, found...)
	}
	out.Findings = append(out.Findings, sharedRanks(f)...)
	if in.Fix {
		if err := o.fixCycles(ctx, f, &out); err != nil {
			return out, err
		}
	}
	out.Human = treeCheckHuman(out)
	return out, nil
}

// nodeFindings are the per-task findings of id: orphan, parent_closed,
// over_depth (depthCap caches tasks.max_depth per store).
func (o ops) nodeFindings(ctx context.Context, f forest, id string, depthCap map[string]int) ([]TreeFinding, error) {
	var out []TreeFinding
	s := f.row(id)
	stored := work.Field(s.Row, fieldParent)
	if stored != "" && !f.tree.Has(stored) {
		fd := TreeFinding{Kind: FindOrphan, ID: id, Parent: stored,
			Message: "parent " + stored + " is missing; " + id + " is shown at top level",
			Fix:     "lore task edit --id " + id + " --no-parent"}
		if pid, perr := ids.ParseAny(stored); perr == nil {
			if lookup.InTrash(ctx, o.env, engine.Ref{Store: s.Source, Table: table, ID: pid}) != nil {
				fd.Message = "parent " + stored + " is in the trash; " + id + " is shown at top level"
				fd.Fix = "lore trash restore --target " + stored + " (or lore task edit --id " + id + " --no-parent)"
			}
		}
		out = append(out, fd)
	}
	if p := f.tree.Parent(id); p != "" && stateOf(s.Row) == tasktree.Open && stateOf(f.row(p).Row) != tasktree.Open {
		out = append(out, TreeFinding{Kind: FindParentClosed, ID: id, Parent: p,
			Message: "parent " + p + " is " + work.Field(f.row(p).Row, work.FieldStatus) + " but " + id + " is open",
			Fix:     "lore task edit --id " + p + " --status in_progress (reopen the parent), or close " + id})
	}
	src := string(s.Source)
	maxDepth, ok := depthCap[src]
	if !ok {
		var err error
		if maxDepth, _, err = o.treePolicy(ctx, s.Source); err != nil {
			return nil, err
		}
		depthCap[src] = maxDepth
	}
	if d := f.tree.Depth(id); d > maxDepth {
		out = append(out, TreeFinding{Kind: FindOverDepth, ID: id, Parent: f.tree.Parent(id),
			Message: id + " is at depth " + strconv.Itoa(d) + ", over tasks.max_depth " + strconv.Itoa(maxDepth),
			Fix:     "lore task edit --id " + id + " --parent … (a shallower parent)"})
	}
	return out, nil
}

// sharedRanks lists siblings holding one rank (6.13: kept, ordered by id;
// none needed).
func sharedRanks(f forest) []TreeFinding {
	var out []TreeFinding
	groups := [][]string{f.tree.Roots()}
	for id := range f.rows {
		groups = append(groups, f.tree.Children(id))
	}
	for _, g := range groups {
		byRank := map[string][]string{}
		for _, c := range g {
			if r := work.Field(f.row(c).Row, fieldRank); r != "" {
				byRank[r] = append(byRank[r], c)
			}
		}
		for r, same := range byRank {
			if len(same) < 2 {
				continue
			}
			out = append(out, TreeFinding{Kind: FindSharedRank, ID: same[0], Parent: f.tree.Parent(same[0]),
				Message: strconv.Itoa(len(same)) + " siblings share rank " + work.Quote(r) + " (" + strings.Join(same, ", ") + "); ordered by id"})
		}
	}
	slices.SortFunc(out, func(a, b TreeFinding) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// fixCycles writes parent_id empty on each cycle's top task (the shown
// result, 6.13), through the history-recording updater.
func (o ops) fixCycles(ctx context.Context, f forest, out *TreeCheckOut) error {
	for i, fd := range out.Findings {
		if fd.Kind != FindParentCycle {
			continue
		}
		s := f.row(fd.ID)
		if _, err := o.hist().Update(ctx, engine.Ref{Store: s.Source, Table: table, ID: s.Row.ID},
			map[string]value.Value{fieldParent: value.Null{}}); err != nil {
			return errs.Prefix("task tree check --fix: "+fd.ID, err)
		}
		out.Findings[i].Fixed = true
		out.Fixed++
	}
	return nil
}

// treeCheckHuman is the human report.
func treeCheckHuman(out TreeCheckOut) string {
	var b strings.Builder
	if len(out.Findings) == 0 {
		b.WriteString(work.Success + " task tree ok (" + strconv.Itoa(out.Tasks) + " tasks)\n")
		return b.String()
	}
	b.WriteString(strconv.Itoa(len(out.Findings)) + " finding(s) in " + strconv.Itoa(out.Tasks) + " tasks\n")
	for _, fd := range out.Findings {
		mark := "  " + string(fd.Kind) + "  "
		if fd.Fixed {
			mark = "  " + string(fd.Kind) + " (fixed)  "
		}
		b.WriteString(mark + fd.Message + "\n")
		if fd.Fix != "" && !fd.Fixed {
			b.WriteString("      fix: " + fd.Fix + "\n")
		}
	}
	return b.String()
}

// CheckTree is the read-only tree check for `lore doctor` (6.13 lists
// doctor among the places a cycle is shown): every finding, nothing
// written.
func CheckTree(ctx context.Context, env work.Env) ([]TreeFinding, error) {
	out, err := ops{env: env}.treeCheck(ctx, TreeCheckIn{})
	return out.Findings, err
}
