package wire

// hostbase_doctor_supersede.go — reversed --supersedes links in `lore doctor`
// on cmd/lore (port of saas/cmd/cli/supersede_repair.go). lore 0.1.16 and
// older stored `<kind> add --supersedes <old>` on the NEW row
// (new.superseded_by_id = old); since v0.1.17 the link is on the OLD row
// (old.superseded_by_id = new). v0.1.17 wrote the new shape but repaired
// the old links only on an explicit `lore doctor --fix`, so a v0.1.17 repo
// can still hold them (owner report 2026-11-12: 84 such links in adopted
// v0.1.17 data). `lore migrate run` and `lore store adopt` now convert
// them as they bring the rows in (migrateSupersedes, adoptRelink); doctor
// still finds any left.
//
//	lore doctor          ─▶ plan every kind ─▶ one warning listing each fix
//	                        (a dry run: nothing written), notes for the links
//	                        left alone
//	lore doctor --fix    ─▶ not confirmed: E_NEEDS_CONFIRM with the list (the
//	                        terminal asks; --yes answers it); confirmed: one
//	                        batch, new links first, reversed ones cleared after
//
// Detection: a row X whose superseded_by_id names a row Y of the same kind
// and project created strictly BEFORE X is reversed (a row cannot be
// replaced by something older). Equal creation times are left alone and
// noted. A target with a correct, different successor, or two reversed
// links into one row, is skipped and noted: one row has one successor.
// Repair is idempotent: an interrupted run leaves both directions, and the
// next run sees Y → X as correct and only clears X.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// Fields of a replacement chain.
const (
	supersedeFieldProject = "project_id"
	supersedeFieldNext    = "superseded_by_id"
	supersedeFieldStatus  = "status"
)

// supersedeKind is a kind with a replacement chain and its table.
type supersedeKind struct {
	name  string
	table row.Table
}

// supersedeKinds are every kind with a replacement chain, in report order.
var supersedeKinds = []supersedeKind{
	{"rule", schema.TableRules}, {"memory", schema.TableMemories}, {"decision", schema.TableDecisions},
	{"hotfix", schema.TableHotfixes}, {"pattern", schema.TablePatterns},
}

// supersedeNode is one row's part in a replacement chain.
type supersedeNode struct {
	id, projectID, supersededBy string
	createdAt                   time.Time
	decisionStatus              string
}

// supersedeFix is one reversed link: oldID gets superseded_by_id = newID.
type supersedeFix struct {
	kind         string
	oldID, newID string
}

// supersedeSkip is a link the repair leaves alone, and why.
type supersedeSkip struct {
	kind, id, reason string
}

// supersedeConflict is two or more rows that each claim to replace one
// older row: one row has one successor, so it is reported, never fixed.
type supersedeConflict struct {
	kind, target string
	srcs         []string
}

// supersedePlan is one kind's repair: the fixes, the final link of every
// row it touches ("" clears), the links it leaves.
type supersedePlan struct {
	fixes     []supersedeFix
	set       map[string]string
	skips     []supersedeSkip
	conflicts []supersedeConflict
}

// planSupersedeRepair finds the reversed links among nodes (every live row
// of one kind). Pure: the plan of an already repaired set is empty.
func planSupersedeRepair(kind string, nodes []supersedeNode) supersedePlan {
	byID := make(map[string]supersedeNode, len(nodes))
	for _, n := range nodes {
		byID[n.id] = n
	}
	target := func(n supersedeNode) (supersedeNode, bool) {
		y, ok := byID[n.supersededBy]
		return y, n.supersededBy != "" && ok && y.projectID == n.projectID
	}
	plan := supersedePlan{set: map[string]string{}}
	into := map[string][]string{} // old (target) id → reversed sources
	for _, n := range nodes {
		y, ok := target(n)
		switch {
		case !ok:
		case y.createdAt.Equal(n.createdAt):
			plan.skips = append(plan.skips, supersedeSkip{kind, n.id, "links to " + y.id + " created at the same instant; the direction cannot be told"})
		case y.createdAt.Before(n.createdAt):
			into[y.id] = append(into[y.id], n.id)
		}
	}
	targets := make([]string, 0, len(into))
	for y := range into {
		targets = append(targets, y)
	}
	slices.Sort(targets)
	for _, yID := range targets {
		srcs := into[yID]
		slices.Sort(srcs)
		if len(srcs) > 1 {
			plan.conflicts = append(plan.conflicts, supersedeConflict{kind: kind, target: yID, srcs: srcs})
			continue
		}
		x, y := srcs[0], byID[yID]
		// y may already link somewhere: fine when that link is itself
		// reversed (cleared below) or already points at x (an interrupted
		// earlier repair); otherwise y has a real successor.
		if y.supersededBy != "" && y.supersededBy != x {
			if z, ok := target(y); !ok || !z.createdAt.Before(y.createdAt) {
				plan.skips = append(plan.skips, supersedeSkip{kind, x, yID + " is already replaced by " + y.supersededBy})
				continue
			}
		}
		plan.fixes = append(plan.fixes, supersedeFix{kind: kind, oldID: yID, newID: x})
	}
	for _, f := range plan.fixes {
		plan.set[f.oldID] = f.newID
	}
	for _, f := range plan.fixes {
		if _, isTarget := plan.set[f.newID]; !isTarget {
			plan.set[f.newID] = ""
		}
	}
	return plan
}

// supersedeNodes reads the chain fields of every live row of k in every
// attached store.
func (a *App) supersedeNodes(ctx context.Context, k supersedeKind) ([]supersedeNode, error) {
	text := func(r row.Row, f string) string {
		v, ok := r.Fields[f]
		if !ok || v == nil || v.Kind() == value.KindNull {
			return ""
		}
		return v.String()
	}
	var out []supersedeNode
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, k.table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			n := supersedeNode{id: r.ID.String(), projectID: text(r, supersedeFieldProject), supersededBy: text(r, supersedeFieldNext), createdAt: r.CreatedAt}
			if k.table == schema.TableDecisions {
				n.decisionStatus = text(r, supersedeFieldStatus)
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// setSuccessor writes id's superseded_by_id (next "" clears it) and, when
// status is set, a decision's status.
func (a *App) setSuccessor(ctx context.Context, t row.Table, id, next, status string) error {
	ref, patch, err := successorEdit(t, id, next, status)
	if err != nil {
		return err
	}
	_, err = a.Update(ctx, ref, patch)
	return err
}

// successorEdit is setSuccessor's edit: the row and its patch.
func successorEdit(t row.Table, id, next, status string) (engine.Ref, map[string]value.Value, error) {
	rid, err := ids.ParseAny(id)
	if err != nil {
		return engine.Ref{}, nil, errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	var v value.Value = value.Null{}
	if next != "" {
		nid, perr := ids.ParseAny(next)
		if perr != nil {
			return engine.Ref{}, nil, errs.Wrap(errs.InvalidID, next+" is not an id", perr)
		}
		if v, err = value.RefOf(nid); err != nil {
			return engine.Ref{}, nil, err
		}
	}
	patch := map[string]value.Value{supersedeFieldNext: v}
	if status != "" {
		s, serr := value.TextOf(status)
		if serr != nil {
			return engine.Ref{}, nil, serr
		}
		patch[supersedeFieldStatus] = s
	}
	return engine.Ref{Table: t, ID: rid}, patch, nil
}

// describeSupersedeFixes names each repair as "<kind> <old> → <new>".
func describeSupersedeFixes(fixes []supersedeFix) string {
	parts := make([]string, 0, len(fixes))
	for _, f := range fixes {
		parts = append(parts, f.kind+" "+f.oldID+" → "+f.newID)
	}
	return strings.Join(parts, ", ")
}

// supersedeQuestion is the confirmation `doctor --fix` asks before writing.
const supersedeQuestion = "Repair %d --supersedes link(s) written by an older lore (%s)?"

// conflictText is the finding of one supersedeConflict: both ids, why
// lore does not choose, and the next step.
func conflictText(c supersedeConflict) string {
	return fmt.Sprintf("%s rows %s each claim to replace %s (--supersedes links written by lore 0.1.16 or older); one row has one successor, so lore does not choose: "+
		"if one is a duplicate, move it to the trash (`lore %s delete <id>`), then `lore doctor --fix` links the other", c.kind, strings.Join(c.srcs, " and "), c.target, c.kind)
}

// supersedeWarning is doctor's finding for the reversed links: each one
// by its rows, and the fix command.
func supersedeWarning(fixes []supersedeFix) string {
	return fmt.Sprintf("%d --supersedes link(s) written by lore 0.1.16 or older point from the new row to the old one (%s; each read as old → new, the row that replaces it); "+
		"run `lore doctor --fix` to move each link onto the old row (lore migrate run and lore store adopt convert them too)", len(fixes), describeSupersedeFixes(fixes))
}

// supersedeKindPlan is one kind's plan with its rows by id.
type supersedeKindPlan struct {
	k     supersedeKind
	nodes map[string]supersedeNode
	plan  supersedePlan
}

// planSupersedes plans every kind over the attached stores.
func (a *App) planSupersedes(ctx context.Context) ([]supersedeKindPlan, []supersedeFix, error) {
	var plans []supersedeKindPlan
	var fixes []supersedeFix
	for _, k := range supersedeKinds {
		nodes, err := a.supersedeNodes(ctx, k)
		if err != nil {
			return nil, nil, errs.Prefix("supersede links: read "+k.name+" rows", err)
		}
		p := planSupersedeRepair(k.name, nodes)
		byID := make(map[string]supersedeNode, len(nodes))
		for _, n := range nodes {
			byID[n.id] = n
		}
		plans = append(plans, supersedeKindPlan{k, byID, p})
		fixes = append(fixes, p.fixes...)
	}
	return plans, fixes, nil
}

// doctorSupersedes reports reversed --supersedes links and, with fix and
// the caller's confirmation, repairs them in one batch. Without the
// confirmation it answers E_NEEDS_CONFIRM naming every fix (the terminal
// asks; --yes answers). A read failure is an error line, never a crash.
func (a *App) doctorSupersedes(ctx context.Context, fix bool, warn, note, fail func(string)) error {
	plans, fixes, err := a.planSupersedes(ctx)
	if err != nil {
		fail(err.Error())
		return nil
	}
	for _, kp := range plans {
		for _, s := range kp.plan.skips {
			note(s.kind + " " + s.id + ": --supersedes link left as is: " + s.reason)
		}
		for _, c := range kp.plan.conflicts {
			warn(conflictText(c))
		}
	}
	if len(fixes) == 0 {
		return nil
	}
	list := describeSupersedeFixes(fixes)
	if !fix {
		warn(supersedeWarning(fixes))
		return nil
	}
	if !op.Confirmed(ctx) {
		q := fmt.Sprintf(supersedeQuestion, len(fixes), list)
		return errs.New(errs.NeedsConfirm, q).WithQuestion(q).WithHint("run `lore doctor` to list them; `lore doctor --fix --yes` repairs them")
	}
	done, err := a.applySupersedes(ctx, plans)
	if err != nil {
		fail(err.Error() + "; run `lore doctor --fix` again")
		return nil
	}
	note(fmt.Sprintf("repaired %d --supersedes link(s): %s", done, list))
	return nil
}

// applySupersedes writes every plan: new links first, clears after, all
// of a store's rows in ONE store commit (owner report 2026-11-12: one
// commit and push per link took 24 minutes for 85 links). A row that
// cannot be written is listed with its reason; the others are written.
// It returns the links repaired.
func (a *App) applySupersedes(ctx context.Context, plans []supersedeKindPlan) (int, error) {
	b := a.bulk(ctx, "doctor --fix: --supersedes links onto the rows they replace", nil)
	stage := func(t row.Table, id, next, status string) {
		ref, patch, err := successorEdit(t, id, next, status)
		if err != nil {
			b.fail(bulkFailure{Table: string(t), ID: id}, err, ref)
			return
		}
		b.update(ref, patch)
	}
	fixes := 0
	for _, kp := range plans {
		rowIDs := make([]string, 0, len(kp.plan.set))
		for id := range kp.plan.set {
			rowIDs = append(rowIDs, id)
		}
		slices.Sort(rowIDs)
		for _, id := range rowIDs {
			next := kp.plan.set[id]
			if next == "" || kp.nodes[id].supersededBy == next {
				continue
			}
			status := ""
			if s := kp.nodes[id].decisionStatus; kp.k.table == schema.TableDecisions && (s == schema.DecisionStatusProposed || s == schema.DecisionStatusAccepted) {
				status = schema.DecisionStatusSuperseded // a status a person set since (deprecated) is kept
			}
			stage(kp.k.table, id, next, status)
		}
		for _, id := range rowIDs {
			if kp.plan.set[id] == "" {
				stage(kp.k.table, id, "", "")
			}
		}
		fixes += len(kp.plan.fixes)
	}
	cerr := b.commit()
	if ferr := b.failedErr("supersede links", "lore doctor --fix"); ferr != nil {
		return 0, ferr
	}
	if cerr != nil {
		return 0, cerr
	}
	return fixes, nil
}

// StepSupersedes is the migrate step that converts the old links.
const StepSupersedes = "supersedes"

// migrateSupersedes is `lore migrate run`'s conversion of the --supersedes
// links lore 0.1.16 and older wrote on the new row (the doctor repair,
// without the question: the run is the person's yes to converting the
// repo). Rows two links claim are left for doctor to name.
func (a *App) migrateSupersedes(ctx context.Context, dryRun bool) ([]string, int, error) {
	plans, fixes, err := a.planSupersedes(ctx)
	if err != nil || len(fixes) == 0 {
		return nil, 0, err
	}
	verb := "would move"
	if !dryRun {
		if _, err = a.applySupersedes(ctx, plans); err != nil {
			return nil, 0, err
		}
		verb = "moved"
	}
	return []string{StepSupersedes + ": " + verb + " " + plural(len(fixes), "--supersedes link") + " written by lore 0.1.16 or older onto the row each replaces (" + describeSupersedeFixes(fixes) + ")"}, len(fixes), nil
}
