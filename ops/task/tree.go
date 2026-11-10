package task

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Sub-task tree glue (TASK_SYSTEM_DESIGN 6): load a project's tasks into
// logic/tasktree, read the tree policies, and the shared checks of add,
// edit, split, done, cancel and delete.
//
//	op ──► o.forest(project) ──► tasktree.Build ──► checks / rollup
//	   └─► o.treePolicy(src) ──► tasks.max_depth, tasks.parent_autoclose
//	   └─► o.treeLimits(src) ──► tasks.max_children (E_TREE_WIDTH),
//	                             tasks.auto_start_parent

// Tree fields of a task row.
const (
	fieldParent      = schema.FieldTaskParent
	fieldRank        = schema.FieldTaskRank
	fieldCloseReason = schema.FieldTaskCloseReason
)

// Parent auto-close modes (TSQ2, policy tasks.parent_autoclose).
const (
	autocloseOff     = "off"
	autocloseSuggest = "suggest"
	autocloseOn      = "on"
)

// Defaults used when the Env offers no policy (logic/policy holds the same
// values as the keys' defaults).
const (
	defaultMaxDepth = 4 // TSQ1
	// defaultMaxChildren is tasks.max_children's default (FA-TASK-1).
	defaultMaxChildren = 200
	defaultAutoclose   = autocloseSuggest
	// confirmListed is how many titles a subtree-delete question lists (6.12).
	confirmListed = 10
)

// --children values of done and cancel (6.7).
const (
	childrenDone   = "done"
	childrenCancel = "cancel"
)

// forest is a project's live tasks as a tree, with their rows.
type forest struct {
	tree *tasktree.Tree
	rows map[string]work.Sourced
}

// row returns task id's sourced row.
func (f forest) row(id string) work.Sourced { return f.rows[id] }

// title is task id's title.
func (f forest) title(id string) string { return work.Field(f.rows[id].Row, work.FieldTitle) }

// forest loads every live task of project (all attached stores; "" = every
// project) into a tree. Children are found by query (T1).
func (o ops) forest(ctx context.Context, project string) (forest, error) {
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return forest{}, err
	}
	f := forest{rows: map[string]work.Sourced{}}
	nodes := make([]tasktree.Node, 0, len(all))
	for _, s := range all {
		if s.Row.Deleted || (project != "" && work.Field(s.Row, work.FieldProject) != project) {
			continue
		}
		id := s.Row.ID.String()
		if _, dup := f.rows[id]; dup {
			continue
		}
		f.rows[id] = s
		nodes = append(nodes, nodeOf(s.Row))
	}
	f.tree = tasktree.Build(nodes)
	return f, nil
}

// nodeOf is a task row as a tree node.
func nodeOf(r row.Row) tasktree.Node {
	return tasktree.Node{ID: r.ID.String(), Parent: work.Field(r, fieldParent), Rank: work.Field(r, fieldRank), State: stateOf(r)}
}

// stateOf maps a status to the tree's closed state.
func stateOf(r row.Row) tasktree.State {
	switch work.Field(r, work.FieldStatus) {
	case schema.TaskStatusDone:
		return tasktree.Done
	case schema.TaskStatusCancelled:
		return tasktree.Cancelled
	default:
		return tasktree.Open
	}
}

// treePolicy is the effective tasks.max_depth and tasks.parent_autoclose
// for target (the defaults when the Env has no policy reader).
func (o ops) treePolicy(ctx context.Context, target store.Source) (maxDepth int, autoclose string, err error) {
	maxDepth, autoclose = defaultMaxDepth, defaultAutoclose
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return maxDepth, autoclose, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return 0, "", err
	}
	if n, isSet := set.Limit(policy.KeyTasksMaxDepth); isSet {
		maxDepth = int(n)
	}
	if e, isSet := set.Get(policy.KeyTasksParentAutoclose); isSet && e.Value != nil {
		autoclose = e.Value.String()
	}
	return maxDepth, autoclose, nil
}

// treeLimits is the effective tasks.max_children and
// tasks.auto_start_parent for target (FA-TASK-1, FA-TASK-2; the defaults
// when the Env has no policy reader).
func (o ops) treeLimits(ctx context.Context, target store.Source) (maxChildren int, autoStart bool, err error) {
	maxChildren = defaultMaxChildren
	p, ok := upgrade.As[bulk.Policier](o.env)
	if !ok {
		return maxChildren, false, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return 0, false, err
	}
	if n, isSet := set.Limit(policy.KeyTasksMaxChildren); isSet {
		maxChildren = int(n)
	}
	if e, isSet := set.Get(policy.KeyTasksAutoStartParent); isSet && e.Value != nil {
		autoStart = e.Value.String() == "true"
	}
	return maxChildren, autoStart, nil
}

// checkWidth refuses adding n sub-tasks under parent past the target's
// tasks.max_children (E_TREE_WIDTH, TASK 6.4).
func (o ops) checkWidth(ctx context.Context, f forest, src store.Source, parent string, n int) error {
	maxChildren, _, err := o.treeLimits(ctx, src)
	if err != nil {
		return err
	}
	return f.tree.CheckWidth(parent, n, maxChildren)
}

// fieldClaimEpoch is the task's fencing counter (TEAM §7, internal field).
const fieldClaimEpoch = "claim_epoch"

// fenceClaimed raises claim_epoch on each claimed task of a subtree
// delete (TASK 6.9/6.12, FA-TASK-3), in the caller's batch, so a holder's
// next write with its old epoch is refused by the claim check.
func (o ops) fenceClaimed(ctx context.Context, f forest, claimed []string) error {
	for _, c := range claimed {
		s := f.row(c)
		epoch := int64(0)
		if v, ok := s.Row.Fields[fieldClaimEpoch]; ok && v.Kind() != value.KindNull {
			if n, err := strconv.ParseInt(v.String(), 10, 64); err == nil {
				epoch = n
			}
		}
		patch := map[string]value.Value{fieldClaimEpoch: value.Int(epoch + 1)}
		if _, err := o.hist().Update(ctx, engine.Ref{Store: s.Source, Table: table, ID: s.Row.ID}, patch); err != nil {
			return err
		}
	}
	return nil
}

// activeClaims is the set of tasks among tasks that are actively claimed on
// the host's board (empty without a team board).
func activeClaims(ctx context.Context, tasks []string) (map[string]bool, error) {
	out := map[string]bool{}
	t, ok := work.TeamFrom(ctx)
	if !ok || len(tasks) == 0 {
		return out, nil
	}
	board, err := t.Claims(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range tasks {
		if _, isClaimed, active := board.Claim(id); isClaimed && active {
			out[id] = true
		}
	}
	return out, nil
}

// selfClaimNow reports whether task is actively claimed on the host's board
// (false without a team board).
func selfClaimNow(ctx context.Context, task string) (bool, error) {
	t, ok := work.TeamFrom(ctx)
	if !ok {
		return false, nil
	}
	board, err := t.Claims(ctx)
	if err != nil {
		return false, err
	}
	_, isClaimed, active := board.Claim(task)
	return isClaimed && active, nil
}

// FenceClaimed raises claim_epoch on every actively claimed task among
// rows (FA-TASK-3, FA-TASK-7): the group deletes of other nouns (a
// mission, a tasklist, a plan taking its tasks) call it under their delete
// batch after the confirm and before the tombstones. It returns how many
// were fenced (0 without a team board).
func FenceClaimed(ctx context.Context, env work.Env, rows []work.Sourced) (int, error) {
	t, ok := work.TeamFrom(ctx)
	if !ok || len(rows) == 0 {
		return 0, nil
	}
	board, err := t.Claims(ctx)
	if err != nil {
		return 0, err
	}
	f := forest{rows: map[string]work.Sourced{}}
	var claimed []string
	for _, s := range rows {
		id := s.Row.ID.String()
		if _, isClaimed, active := board.Claim(id); isClaimed && active {
			f.rows[id] = s
			claimed = append(claimed, id)
		}
	}
	return len(claimed), ops{env: env}.fenceClaimed(ctx, f, claimed)
}

// autoStartParent applies tasks.auto_start_parent once child started
// (TASK 6.7, FA-TASK-2): when the policy of the parent's store is on and
// the parent is still todo, the parent becomes in_progress in a second
// write. Like --reopen-parent it opens no time span on the parent (OV7-3:
// the work is on the child). It returns the human line ("" = nothing done).
func (o ops) autoStartParent(ctx context.Context, child row.Row) (string, error) {
	parent := work.Field(child, fieldParent)
	if parent == "" {
		return "", nil
	}
	pid, err := ids.ParseAny(parent)
	if err != nil {
		return "", err
	}
	src, pr, err := work.Get(ctx, o.env, Noun, table, pid)
	// a parent not attached here or in the trash is not started
	if errs.Has(err, errs.NotFound) || errs.Has(err, errs.InTrash) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if work.Field(pr, work.FieldStatus) != schema.TaskStatusTodo {
		return "", nil
	}
	_, on, err := o.treeLimits(ctx, src)
	if err != nil || !on {
		return "", err
	}
	patch := map[string]value.Value{}
	if _, err = o.setStatus(ctx, src, pr, schema.TaskStatusInProgress, true, patch); err != nil {
		return "", err
	}
	if work.Time(pr, fieldStarted).IsZero() {
		patch[fieldStarted] = work.Now(o.env)
	}
	if _, err = o.hist().Update(ctx, engine.Ref{Store: src, Table: table, ID: pr.ID}, patch); err != nil {
		return "", err
	}
	return "  parent " + parent + " " + work.Field(pr, work.FieldTitle) + " started too (tasks.auto_start_parent: on)\n", nil
}

// lastRank is the rank after the last child of parent ("" = top level).
func (f forest) lastRank(parent string) string {
	kids := f.tree.Children(parent)
	if parent == "" {
		kids = f.tree.Roots()
	}
	last := ""
	for _, k := range kids {
		if r := work.Field(f.rows[k].Row, fieldRank); r > last {
			last = r
		}
	}
	return tasktree.After(last)
}

// openList is "id title, …" of ids (for refusals).
func (f forest) openList(xs []string) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, x+" "+f.title(x))
	}
	return strings.Join(parts, ", ")
}

// parentClosed is the E_PARENT_CLOSED refusal (6.7).
func parentClosed(parent, status string) error {
	return errs.New(errs.ParentClosed, "parent "+parent+" is "+status).
		WithHint("pass --reopen-parent to reopen it to in_progress in the same write, or pick an open parent")
}

// reopenPatch is the parent's patch of --reopen-parent.
func (o ops) reopenPatch(ctx context.Context, src store.Source, cur row.Row) (map[string]value.Value, error) {
	p := map[string]value.Value{}
	// D2 through setStatus; reopen opens no span on the parent (OV7-3).
	if _, err := o.setStatus(ctx, src, cur, schema.TaskStatusInProgress, true, p); err != nil {
		return nil, err
	}
	p[fieldCloseReason] = value.Null{}
	p[work.FieldCompleted] = value.Null{}
	return p, nil
}

// closePatch closes a task: done → completed, cancel → cancelled (or the
// given reason, 8.3).
func (o ops) closePatch(ctx context.Context, src store.Source, cur row.Row, status, reason string) (map[string]value.Value, error) {
	var patch map[string]value.Value
	var err error
	if status == schema.TaskStatusDone {
		patch, _, err = o.statusPatch(ctx, src, cur, schema.TaskStatusDone, work.FieldCompleted)
	} else {
		patch = map[string]value.Value{}
		_, err = o.setStatus(ctx, src, cur, status, false, patch)
	}
	if err != nil {
		return nil, err
	}
	if reason == "" {
		reason = schema.TaskCloseCompleted
		if status == schema.TaskStatusCancelled {
			reason = schema.TaskCloseCancelled
		}
	}
	if err = checkReason(reason, schema.TaskCloseReasons()); err != nil {
		return nil, err
	}
	if patch[fieldCloseReason], err = value.TextOf(reason); err != nil {
		return nil, err
	}
	return patch, nil
}

// closeChildren handles the open sub-tasks of a done or cancel (6.7): none
// passes; without --children it is E_OPEN_CHILDREN listing them; with
// --children done|cancel every open descendant is closed under bctx (the
// caller's bat_ batch), after the XQ2 preview. It returns the human lines.
func (o ops) closeChildren(ctx context.Context, f forest, id, verb, children, suggest string) (string, error) {
	open := f.tree.OpenChildren(id)
	if len(open) == 0 {
		return "", nil
	}
	if children == "" {
		return "", errs.New(errs.OpenChildren, "task "+verb+" "+id+": "+strconv.Itoa(len(open))+" open sub-task(s): "+f.openList(open)).
			WithHint("pass --children " + suggest + " to close them in the same write, or close them first")
	}
	status := schema.TaskStatusDone
	switch children {
	case childrenDone:
	case childrenCancel:
		status = schema.TaskStatusCancelled
	default:
		return "", errs.New(errs.InvalidInput, "--children must be done or cancel, not "+work.Quote(children))
	}
	var items []bulk.Item
	var targets []string
	for _, d := range f.tree.Descendants(id) {
		if stateOf(f.rows[d].Row) == tasktree.Open {
			targets = append(targets, d)
			items = append(items, bulk.Item{ID: d, Label: f.title(d)})
		}
	}
	if err := bulk.Preview(ctx, o.env, f.row(id).Source, "task "+verb+" --children "+children, items); err != nil {
		return "", err
	}
	claimed, err := activeClaims(ctx, targets)
	if err != nil {
		return "", err
	}
	// deepest first, so no write ever leaves a closed task over an open one
	slices.Reverse(targets)
	for _, d := range targets {
		s := f.row(d)
		patch, err := o.closePatch(ctx, s.Source, s.Row, status, "")
		if err != nil {
			return "", err
		}
		// TASK 6.9 (BUG-CANCEL-FENCE): a claimed child closed by the cascade
		// gets claim_epoch +1 in the same write, so its holder's next fenced
		// write is refused (E_CANCELLED for a cancel) instead of landing on
		// a task somebody else closed. A done cascade fences the same way:
		// the holder no longer owns the outcome of that task either.
		if claimed[d] {
			patch[fieldClaimEpoch] = value.Int(int64(rowClaimEpoch(s.Row)) + 1)
		}
		if _, err := o.hist().Update(ctx, engine.Ref{Store: s.Source, Table: table, ID: s.Row.ID}, patch); err != nil {
			return "", err
		}
	}
	return "  " + strconv.Itoa(len(targets)) + " sub-task(s) " + map[string]string{childrenDone: "done", childrenCancel: "cancelled"}[children] + "\n", nil
}

// Next is a suggested follow-up command (6.7: --json carries it).
type Next struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Why     string   `json:"why"`
}

// ParentNote is the parent part of a done/cancel output.
type ParentNote struct {
	ID           string `json:"id"`
	OpenChildren int    `json:"open_children"`
	Closed       bool   `json:"closed,omitempty"`
}

// afterClose applies tasks.parent_autoclose once id closed (6.7, TSQ2):
// when its parent has no open sub-tasks left, off says nothing, suggest
// prints the close command (Next), on closes the parent in the same batch
// unless an approval gate that is not approved keeps it open.
func (o ops) afterClose(ctx context.Context, f forest, id string) (ParentNote, Next, string, error) {
	parent := f.tree.Parent(id)
	if parent == "" || stateOf(f.row(parent).Row) != tasktree.Open {
		return ParentNote{}, Next{}, "", nil
	}
	var left []string
	for _, c := range f.tree.OpenChildren(parent) {
		if c != id {
			left = append(left, c)
		}
	}
	note := ParentNote{ID: parent, OpenChildren: len(left)}
	if len(left) > 0 {
		return note, Next{}, "", nil
	}
	_, mode, err := o.treePolicy(ctx, f.row(parent).Source)
	if err != nil {
		return ParentNote{}, Next{}, "", err
	}
	ps := f.row(parent)
	gate := work.Field(ps.Row, fieldGate)
	switch {
	case mode == autocloseOff:
		return note, Next{}, "", nil
	case mode == autocloseOn && (gate == "" || gate == schema.TaskGateApproved):
		patch, err := o.closePatch(ctx, ps.Source, ps.Row, schema.TaskStatusDone, "")
		if err != nil {
			return ParentNote{}, Next{}, "", err
		}
		if _, err = o.hist().Update(ctx, engine.Ref{Store: ps.Source, Table: table, ID: ps.Row.ID}, patch); err != nil {
			return ParentNote{}, Next{}, "", err
		}
		note.Closed = true
		return note, Next{}, "  " + parent + " " + f.title(parent) + " closed too: no open sub-tasks (tasks.parent_autoclose: on)\n", nil
	}
	why := "parent has no open sub-tasks (tasks.parent_autoclose: " + mode + ")"
	human := "  " + parent + " " + f.title(parent) + " has no open sub-tasks now; close it: lore task done " + parent + "\n"
	if mode == autocloseOn {
		why = "parent kept open: its approval gate is " + gate
		human = "  " + parent + " " + f.title(parent) + " has no open sub-tasks but its gate is " + gate + "; close it after approval: lore task done " + parent + "\n"
	}
	return note, Next{Command: "lore", Args: []string{Noun, "done", parent}, Why: why}, human, nil
}

// subtreeQuestion is the TSQ3 question of deleting id with children: the
// counts, the first titles and every claimed child with its holder. It
// also returns the claimed children's ids (fenced by fenceClaimed).
func (o ops) subtreeQuestion(ctx context.Context, f forest, id string) (string, []string, error) {
	desc := f.tree.Descendants(id)
	var claimed, holders []string
	if t, ok := work.TeamFrom(ctx); ok {
		board, err := t.Claims(ctx)
		if err != nil {
			return "", nil, err
		}
		for _, d := range desc {
			if holder, isClaimed, active := board.Claim(d); isClaimed && active {
				claimed = append(claimed, d)
				holders = append(holders, d+" ("+holder+")")
			}
		}
	}
	var b strings.Builder
	b.WriteString("deleting " + id + " " + f.title(id) + " also deletes its subtree: " + strconv.Itoa(len(desc)) + " sub-task(s)")
	if len(claimed) > 0 {
		b.WriteString(" (" + strconv.Itoa(len(claimed)) + " claimed)")
	}
	b.WriteString(":")
	for i, d := range desc {
		if i == confirmListed {
			b.WriteString(" and " + strconv.Itoa(len(desc)-confirmListed) + " more")
			break
		}
		b.WriteString(" " + d + " " + f.title(d) + ";")
	}
	if len(claimed) > 0 {
		b.WriteString(" claimed children: " + strings.Join(holders, ", ") + " (their holders' next write fails);")
	}
	b.WriteString(" delete all " + strconv.Itoa(len(desc)+1) + "?")
	return b.String(), claimed, nil
}

// placeRank resolves --before / --after-sibling / --first / --last into a
// rank among the siblings under parent (6.3), "" when none was given.
func placeRank(f forest, parent, self string, in EditIn) (string, error) {
	n := 0
	for _, on := range []bool{in.Before != "", in.AfterSibling != "", in.First, in.Last} {
		if on {
			n++
		}
	}
	if n == 0 {
		return "", nil
	}
	if n > 1 {
		return "", errs.New(errs.InvalidInput, "task edit: give one of --before, --after-sibling, --first, --last")
	}
	sibs := f.tree.Roots()
	if parent != "" {
		sibs = f.tree.Children(parent)
	}
	sibs = slices.DeleteFunc(sibs, func(s string) bool { return s == self })
	rank := func(i int) string {
		if i < 0 || i >= len(sibs) {
			return ""
		}
		return work.Field(f.rows[sibs[i]].Row, fieldRank)
	}
	switch {
	case in.First:
		return tasktree.Between("", rank(0)), nil
	case in.Last:
		return tasktree.After(rank(len(sibs) - 1)), nil
	}
	anchor := in.Before
	if anchor == "" {
		anchor = in.AfterSibling
	}
	i := slices.Index(sibs, anchor)
	if i < 0 {
		return "", errs.New(errs.InvalidInput, anchor+" is not a sibling of "+self).
			WithHint("--before / --after-sibling take a task with the same parent")
	}
	if in.Before != "" {
		return tasktree.Between(rank(i-1), rank(i)), nil
	}
	return tasktree.Between(rank(i), rank(i+1)), nil
}

// parentRef is a ref value to task id.
func parentRef(id string) (value.Value, error) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return nil, err
	}
	return value.RefOf(aid)
}

// treeLines renders id's subtree for show --tree, two spaces per level.
func treeLines(f forest, id string) []TreeLine {
	var out []TreeLine
	var walk func(string, int)
	walk = func(at string, depth int) {
		for _, c := range f.tree.Children(at) {
			r := f.rows[c].Row
			out = append(out, TreeLine{ID: c, Title: f.title(c), Status: work.Field(r, work.FieldStatus), Depth: depth, Rank: work.Field(r, fieldRank)})
			walk(c, depth+1)
		}
	}
	walk(id, 1)
	return out
}

// cycleWarnings are the E_PARENT_CYCLE warnings of a merged tree (6.13).
func cycleWarnings(f forest) []Warning {
	var out []Warning
	for _, c := range f.tree.Cycles() {
		out = append(out, Warning{Code: tasktree.WarnParentCycle, Field: fieldParent,
			Message: "parent cycle " + strings.Join(c, " → ") + " → " + c[0] + "; " + c[0] + " is shown at top level; fix: lore task edit " + c[0] + " --parent … or --no-parent"})
	}
	return out
}

// addParent is what task add --parent resolved (6.3, 6.5).
type addParent struct {
	id, title, rank string
	repo            value.Value // the parent's repo_id ref (nil = none)
	inh             Inherited
	reopen          *engine.Ref // --reopen-parent: the parent to reopen
	notes           []string
}

// human is the parent block of task add's human output.
func (p *addParent) human() string {
	out := "  parent: " + p.id + " " + p.title + "\n"
	var parts []string
	for _, kv := range [][2]string{
		{"mission", p.inh.MissionID}, {"tasklist", p.inh.TasklistID}, {"plan", p.inh.PlanID}, {"repo", p.inh.RepoID},
		{"priority", p.inh.Priority}, {"commitment", p.inh.Commitment}, {"team", p.inh.Team},
	} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+" "+kv[1])
		}
	}
	if len(parts) > 0 {
		out += "  inherited: " + strings.Join(parts, ", ") + "\n"
	}
	if p.reopen != nil {
		out += "  reopened parent " + p.id + " (in_progress)\n"
	}
	for _, n := range p.notes {
		out += "  warning: " + n + "\n"
	}
	return out
}

// addParent checks --parent and fills the flags the child inherits (6.5):
// same project (E_PARENT_PROJECT), depth within tasks.max_depth
// (E_TREE_DEPTH), an open parent unless --reopen-parent (E_PARENT_CLOSED),
// the parent's mission (E_PARENT_MISSION). It sets in.To to the parent's
// store when no --to was given.
func (o ops) addParent(ctx context.Context, in *AddIn, project string) (*addParent, error) {
	pid, err := taskID(in.Parent)
	if err != nil {
		return nil, err
	}
	src, pr, err := work.Get(ctx, o.env, Noun, table, pid)
	if err != nil {
		return nil, err
	}
	if got := work.Field(pr, work.FieldProject); got != project {
		return nil, errs.New(errs.ParentProject, "parent "+pid.String()+" is in project "+got+", not "+project).
			WithHint("pick a parent in this project; across projects use --after or a relation")
	}
	f, err := o.forest(ctx, project)
	if err != nil {
		return nil, err
	}
	maxDepth, _, err := o.treePolicy(ctx, src)
	if err != nil {
		return nil, err
	}
	if err = f.tree.CheckMove(newTaskNode, pid.String(), maxDepth); err != nil {
		return nil, err
	}
	// 6.4: one more direct child must fit tasks.max_children (split checks
	// all its pieces before the first add).
	if err = o.checkWidth(ctx, f, src, pid.String(), 1); err != nil {
		return nil, err
	}
	p := &addParent{id: pid.String(), title: work.Field(pr, work.FieldTitle), rank: f.lastRank(pid.String())}
	if stateOf(pr) != tasktree.Open {
		if !in.ReopenParent {
			return nil, parentClosed(p.id, work.Field(pr, work.FieldStatus))
		}
		p.reopen = &engine.Ref{Store: src, Table: table, ID: pid}
	}
	pm := work.Field(pr, work.FieldMission)
	if in.Mission != "" && in.Mission != pm {
		return nil, errs.New(errs.ParentMission, "a sub-task's mission is its parent's: "+p.id+" has mission "+orDefault(pm, "(none)")+", not "+in.Mission).
			WithHint("drop --mission (the sub-task inherits it), or add the task without --parent")
	}
	inherit := func(flag *string, field string, into *string) {
		if *flag == "" {
			if v := work.Field(pr, field); v != "" {
				*flag, *into = v, v
			}
		}
	}
	inherit(&in.Mission, work.FieldMission, &p.inh.MissionID)
	inherit(&in.Tasklist, work.FieldTasklist, &p.inh.TasklistID)
	inherit(&in.Plan, work.FieldPlan, &p.inh.PlanID)
	inherit(&in.Priority, fieldPriority, &p.inh.Priority)
	inherit(&in.Commitment, fieldCommitment, &p.inh.Commitment)
	if err = o.inheritedHere(ctx, pid.String(), p.inh); err != nil {
		return nil, err
	}
	if len(in.Assign) == 0 {
		p.inh.Team = work.Field(pr, fieldTeam)
	}
	if in.Repo == "" {
		if v, ok := pr.Fields[work.FieldRepo]; ok && v.Kind() != value.KindNull {
			p.repo, p.inh.RepoID = v, v.String()
		}
	}
	if in.To == "" {
		in.To = string(src)
	}
	// 6.5 warnings: never refusals.
	// in.Due is a TASK 12 phrase; a bad one is refused later by setDates.
	clk, err := o.clock(ctx, src)
	if err != nil {
		return nil, err
	}
	cd, _ := dayText(clk, "due", in.Due)
	if pd := work.DateText(pr, fieldDue); pd != "" && cd != "" && cd > pd {
		p.notes = append(p.notes, "child due "+cd+" is after parent due "+pd)
	}
	if in.Commitment == schema.TaskCommitmentAccepted && work.Field(pr, fieldCommitment) == schema.TaskCommitmentSomeday {
		p.notes = append(p.notes, "an accepted sub-task under a someday parent")
	}
	return p, nil
}

// inheritedRef is one container a sub-task takes from its parent: the
// tasks field, the table that holds its rows and the noun the error names.
type inheritedRef struct {
	field, noun string
	table       row.Table
}

// inheritedRefs are the parent's containers a sub-task inherits (the
// inherit calls of addParent), in the order the refusal checks them.
var inheritedRefs = []inheritedRef{
	{work.FieldTasklist, "task list", schema.TableTasklists},
	{work.FieldPlan, "plan", schema.TablePlans},
	{work.FieldMission, "mission", schema.TableMissions},
}

// inheritedHere refuses, before any write, a sub-task whose inherited
// container no attached store holds: the parent arrived (live, or a
// pulled commit) while the row it names did not, the case of a teammate's
// task list committed on a branch that has not reached the default branch
// (F-ADDADD-2) or a live write not fetched yet. Without it the engine's
// write-time check answered "create the target first, or attach the
// store", the wrong next step for a row a teammate already made. A row in
// the trash, or one only a hidden store holds, is left to the engine's
// own check (E_IN_TRASH / E_LEAK_BLOCKED keep their codes).
func (o ops) inheritedHere(ctx context.Context, parent string, inh Inherited) error {
	got := map[string]string{work.FieldTasklist: inh.TasklistID, work.FieldPlan: inh.PlanID, work.FieldMission: inh.MissionID}
	for _, r := range inheritedRefs {
		v := got[r.field]
		if v == "" {
			continue
		}
		id, err := ids.ParseAny(v)
		if err != nil {
			return err
		}
		_, _, err = o.env.Get(ctx, engine.Ref{Table: r.table, ID: id})
		if !errs.Has(err, errs.NotFound) {
			continue // found, or an error the write reports with its own code
		}
		return errs.New(errs.NotFound, "parent "+parent+" is in "+r.noun+" "+v+", which is not in this clone yet").
			WithHint("the teammate who made it has not pushed it to the default branch, or its live write was not fetched: run `git pull` and `lore sync`, or wait for their push, then run this again")
	}
	return nil
}

// newTaskNode stands for a task not written yet in CheckMove (no row has
// this id: it is not a valid tsk_ id).
const newTaskNode = "(new)"

// treeEdit is what task edit's tree flags add to the write: fields of the
// task itself, and updates of other tasks (its subtree's mission, a
// reopened parent) written in the same bat_ batch.
type treeEdit struct {
	patch  map[string]value.Value
	others []otherUpdate
}

// otherUpdate is one update of another task in the edit's batch.
type otherUpdate struct {
	ref   engine.Ref
	patch map[string]value.Value
}

// editTree applies the tree rules of task edit (6.3, 6.5, 6.7, 6.8), in
// order: one parent flag; same project; no cycle; depth; the subtree takes
// the parent's mission (E_PARENT_MISSION for a child's own other mission);
// a reopened child under a closed parent needs --reopen-parent; a close
// with open children is E_OPEN_CHILDREN; the rank of --before/…/--last.
func (o ops) editTree(ctx context.Context, in EditIn, src store.Source, cur row.Row) (treeEdit, error) {
	te := treeEdit{patch: map[string]value.Value{}}
	if in.Parent != "" && in.NoParent {
		return te, errs.New(errs.InvalidInput, "task edit: --parent and --no-parent together")
	}
	id := cur.ID.String()
	project := work.Field(cur, work.FieldProject)
	f, err := o.forest(ctx, project)
	if err != nil {
		return te, err
	}
	oldParent := f.tree.Parent(id)
	parent := oldParent
	switch {
	case in.NoParent:
		parent = ""
	case in.Parent != "":
		pid, perr := taskID(in.Parent)
		if perr != nil {
			return te, perr
		}
		_, pr, gerr := work.Get(ctx, o.env, Noun, table, pid)
		if gerr != nil {
			return te, gerr
		}
		if got := work.Field(pr, work.FieldProject); got != project {
			return te, errs.New(errs.ParentProject, "parent "+pid.String()+" is in project "+got+", not "+project).
				WithHint("pick a parent in this project; across projects use --after or a relation")
		}
		parent = pid.String()
	}
	moved := parent != oldParent
	if moved && parent != "" {
		maxDepth, _, perr := o.treePolicy(ctx, src)
		if perr != nil {
			return te, perr
		}
		if err = f.tree.CheckMove(id, parent, maxDepth); err != nil {
			return te, err
		}
		// 6.8: the width of the new parent (FA-TASK-1).
		if err = o.checkWidth(ctx, f, src, parent, 1); err != nil {
			return te, err
		}
	}
	if moved {
		if te.patch[fieldParent], err = refOrNull(parent); err != nil {
			return te, err
		}
	}
	// Mission: under a parent the subtree always has the parent's (T2).
	mission, missionSet := work.Field(cur, work.FieldMission), false
	switch {
	case in.ClearMission:
		mission, missionSet = "", true
	case in.Mission != "":
		mission, missionSet = in.Mission, true
	}
	if parent != "" {
		pm := work.Field(f.row(parent).Row, work.FieldMission)
		if missionSet && mission != pm {
			return te, errs.New(errs.ParentMission, "a sub-task's mission is its parent's: "+parent+" has mission "+orDefault(pm, "(none)")).
				WithHint("detach it first: --no-parent, or change the parent's mission")
		}
		if moved && pm != work.Field(cur, work.FieldMission) {
			mission, missionSet = pm, true
			if te.patch[work.FieldMission], err = refOrNull(pm); err != nil {
				return te, err
			}
		}
	}
	if missionSet {
		if err = o.subtreeMission(ctx, f, id, mission, &te); err != nil {
			return te, err
		}
	}
	// Status rules (6.7).
	if in.Status != "" {
		closing := in.Status == schema.TaskStatusDone || in.Status == schema.TaskStatusCancelled
		if closing && len(f.tree.OpenChildren(id)) > 0 {
			return te, errs.New(errs.OpenChildren, "task "+id+" has open sub-tasks: "+f.openList(f.tree.OpenChildren(id))).
				WithHint("use lore task done|cancel " + id + " --children done|cancel")
		}
		if !closing && stateOf(cur) != tasktree.Open && parent != "" && stateOf(f.row(parent).Row) != tasktree.Open {
			if !in.ReopenParent {
				return te, parentClosed(parent, work.Field(f.row(parent).Row, work.FieldStatus))
			}
			ps := f.row(parent)
			patch, perr := o.reopenPatch(ctx, ps.Source, ps.Row)
			if perr != nil {
				return te, perr
			}
			te.others = append(te.others, otherUpdate{ref: engine.Ref{Store: ps.Source, Table: table, ID: ps.Row.ID}, patch: patch})
		}
		if !closing {
			te.patch[fieldCloseReason] = value.Null{}
		}
	}
	// Rank: placement flags, else the end of the new siblings.
	rank, err := placeRank(f, parent, id, in)
	if err != nil {
		return te, err
	}
	if rank == "" && moved {
		rank = f.lastRank(parent)
	}
	if rank != "" {
		if te.patch[fieldRank], err = value.TextOf(rank); err != nil {
			return te, err
		}
	}
	return te, nil
}

// subtreeMission gives every descendant of id the mission (6.5: moving a
// parent to another mission moves the whole subtree in the same write),
// after the XQ2 preview.
func (o ops) subtreeMission(ctx context.Context, f forest, id, mission string, te *treeEdit) error {
	var items []bulk.Item
	var refs []engine.Ref
	for _, d := range f.tree.Descendants(id) {
		s := f.row(d)
		if work.Field(s.Row, work.FieldMission) == mission {
			continue
		}
		items = append(items, bulk.Item{ID: d, Label: f.title(d)})
		refs = append(refs, engine.Ref{Store: s.Source, Table: table, ID: s.Row.ID})
	}
	if len(refs) == 0 {
		return nil
	}
	if err := bulk.Preview(ctx, o.env, f.row(id).Source, "task edit (sub-tasks follow the mission)", items); err != nil {
		return err
	}
	v, err := refOrNull(mission)
	if err != nil {
		return err
	}
	for _, r := range refs {
		te.others = append(te.others, otherUpdate{ref: r, patch: map[string]value.Value{work.FieldMission: v}})
	}
	return nil
}

// refOrNull is a ref to id, or Null for "" (cleared).
func refOrNull(id string) (value.Value, error) {
	if id == "" {
		return value.Null{}, nil
	}
	return parentRef(id)
}

// closeTree is the sub-task part of done and cancel: it loads the tree,
// and when the task has a parent or children starts one bat_ batch (so
// the children's closes, the task and an auto-closed parent are one
// write) and applies closeChildren. It returns the context to write with.
func (o ops) closeTree(ctx context.Context, id ids.AnyID, verb, children, suggest string) (context.Context, forest, string, error) {
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return ctx, forest{}, "", err
	}
	f, err := o.forest(ctx, work.Field(cur, work.FieldProject))
	if err != nil {
		return ctx, forest{}, "", err
	}
	sid := id.String()
	if f.tree.Parent(sid) == "" && len(f.tree.Children(sid)) == 0 {
		return ctx, f, "", nil
	}
	if len(f.tree.OpenChildren(sid)) > 0 && children == "" {
		_, err = o.closeChildren(ctx, f, sid, verb, "", suggest)
		return ctx, f, "", err
	}
	bctx, _, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return ctx, forest{}, "", err
	}
	lines, err := o.closeChildren(bctx, f, sid, verb, children, suggest)
	return bctx, f, lines, err
}

// treeFilter is list's --parent / --under / --top / --leaves (6.3): a
// predicate over task ids (every id passes with no tree flag).
func treeFilter(f forest, in ListIn) (func(string) bool, error) {
	keep := map[string]bool{}
	only := false
	pick := func(flag, raw string, of func(string) []string) error {
		if raw == "" {
			return nil
		}
		id, err := taskID(raw)
		if err != nil {
			return err
		}
		if !f.tree.Has(id.String()) {
			return errs.New(errs.NotFound, "--"+flag+" "+work.Quote(raw)+": no such task")
		}
		next := map[string]bool{}
		for _, x := range of(id.String()) {
			if !only || keep[x] {
				next[x] = true
			}
		}
		keep, only = next, true
		return nil
	}
	if err := pick("parent", in.Parent, f.tree.Children); err != nil {
		return nil, err
	}
	if err := pick("under", in.Under, f.tree.Descendants); err != nil {
		return nil, err
	}
	return func(id string) bool {
		switch {
		case only && !keep[id],
			in.Top && f.tree.Parent(id) != "",
			in.Leaves && !f.tree.IsLeaf(id):
			return false
		}
		return true
	}, nil
}

// decorate adds the parent id and, for an umbrella, the rollup and its
// "done/total" mark to a list brief (6.6: list shows 2/5).
func decorate(f forest, b *Brief, r row.Row) {
	id := r.ID.String()
	b.ParentID = f.tree.Parent(id)
	if len(f.tree.Children(id)) == 0 {
		return
	}
	ru := f.tree.Rollup(id)
	b.Rollup = ru
	mark := " " + strconv.Itoa(ru.ChildrenDone) + "/" + strconv.Itoa(ru.ChildrenTotal)
	b.base += mark
	b.Line = b.base + storeLabel(b.Label)
}

// showTree fills show's tree fields: parent, rank, close reason, the
// rollup of an umbrella, the cycle warnings of the project's merged tree
// that involve the task, and with --tree the subtree.
func (o ops) showTree(ctx context.Context, full *Full, r row.Row, withTree bool) error {
	full.Rank, full.CloseReason = work.Field(r, fieldRank), work.Field(r, fieldCloseReason)
	f, err := o.forest(ctx, work.Field(r, work.FieldProject))
	if err != nil {
		return err
	}
	id := r.ID.String()
	if !f.tree.Has(id) {
		full.ParentID = work.Field(r, fieldParent)
		return nil
	}
	full.ParentID = f.tree.Parent(id)
	if len(f.tree.Children(id)) > 0 {
		ru := f.tree.Rollup(id)
		full.Rollup = ru
	}
	for _, w := range cycleWarnings(f) {
		if strings.Contains(w.Message, id) {
			full.TreeWarnings = append(full.TreeWarnings, w)
		}
	}
	if withTree {
		full.Tree = treeLines(f, id)
	}
	return nil
}

// checkReason refuses a --reason outside set. --reason on task cancel,
// decline and close is the close reason (a closed set); the free-text
// reason a --not-a-secret answer needs has its own flag there, which the
// hint names (P2-CTX-3 lets an op's --reason double as it elsewhere).
func checkReason(v string, set []string) error {
	if slices.Contains(set, v) {
		return nil
	}
	return errs.New(errs.InvalidInput, Noun+": bad --reason "+v+" (want: "+strings.Join(set, " | ")+")").
		WithHint("use one of: " + strings.Join(set, ", ") + "; the reason for --not-a-secret goes in --not-a-secret-why")
}
