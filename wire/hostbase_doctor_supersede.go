package wire

// hostbase_doctor_supersede.go — reversed --supersedes links in `lore doctor`
// on cmd/lore (port of saas/cmd/cli/supersede_repair.go). An older lore
// stored `<kind> add --supersedes <old>` on the NEW row (new.superseded_by_id
// = old); the shape is the link on the OLD row (old.superseded_by_id = new).
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

// supersedePlan is one kind's repair: the fixes, the final link of every
// row it touches ("" clears), the links it leaves.
type supersedePlan struct {
	fixes []supersedeFix
	set   map[string]string
	skips []supersedeSkip
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
			for _, x := range srcs {
				plan.skips = append(plan.skips, supersedeSkip{kind, x,
					fmt.Sprintf("several rows (%s) claim to replace %s; keep one with `lore %s edit`", strings.Join(srcs, ", "), yID, kind)})
			}
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
	rid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	var v value.Value = value.Null{}
	if next != "" {
		nid, perr := ids.ParseAny(next)
		if perr != nil {
			return errs.Wrap(errs.InvalidID, next+" is not an id", perr)
		}
		if v, err = value.RefOf(nid); err != nil {
			return err
		}
	}
	patch := map[string]value.Value{supersedeFieldNext: v}
	if status != "" {
		s, serr := value.TextOf(status)
		if serr != nil {
			return serr
		}
		patch[supersedeFieldStatus] = s
	}
	_, err = a.Update(ctx, engine.Ref{Table: t, ID: rid}, patch)
	return err
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

// doctorSupersedes reports reversed --supersedes links and, with fix and
// the caller's confirmation, repairs them in one batch. Without the
// confirmation it answers E_NEEDS_CONFIRM naming every fix (the terminal
// asks; --yes answers). A read failure is an error line, never a crash.
func (a *App) doctorSupersedes(ctx context.Context, fix bool, warn, note, fail func(string)) error {
	type kindPlan struct {
		k     supersedeKind
		nodes map[string]supersedeNode
		plan  supersedePlan
	}
	var plans []kindPlan
	var fixes []supersedeFix
	for _, k := range supersedeKinds {
		nodes, err := a.supersedeNodes(ctx, k)
		if err != nil {
			fail("supersede links: read " + k.name + " rows: " + err.Error())
			return nil
		}
		p := planSupersedeRepair(k.name, nodes)
		for _, s := range p.skips {
			note(s.kind + " " + s.id + ": --supersedes link left as is: " + s.reason)
		}
		byID := make(map[string]supersedeNode, len(nodes))
		for _, n := range nodes {
			byID[n.id] = n
		}
		plans = append(plans, kindPlan{k, byID, p})
		fixes = append(fixes, p.fixes...)
	}
	if len(fixes) == 0 {
		return nil
	}
	list := describeSupersedeFixes(fixes)
	if !fix {
		warn(fmt.Sprintf("%d --supersedes link(s) written by an older lore point the wrong way (%s); run `lore doctor --fix`", len(fixes), list))
		return nil
	}
	if !op.Confirmed(ctx) {
		q := fmt.Sprintf(supersedeQuestion, len(fixes), list)
		return errs.New(errs.NeedsConfirm, q).WithQuestion(q).WithHint("run `lore doctor` to list them; `lore doctor --fix --yes` repairs them")
	}
	done := 0
	for _, kp := range plans {
		rowIDs := make([]string, 0, len(kp.plan.set))
		for id := range kp.plan.set {
			rowIDs = append(rowIDs, id)
		}
		slices.Sort(rowIDs)
		// New links first, clears after: an interruption never loses a link.
		for _, id := range rowIDs {
			next := kp.plan.set[id]
			if next == "" || kp.nodes[id].supersededBy == next {
				continue
			}
			status := ""
			if s := kp.nodes[id].decisionStatus; kp.k.table == schema.TableDecisions && (s == schema.DecisionStatusProposed || s == schema.DecisionStatusAccepted) {
				status = schema.DecisionStatusSuperseded // a status a person set since (deprecated) is kept
			}
			if err := a.setSuccessor(ctx, kp.k.table, id, next, status); err != nil {
				fail(fmt.Sprintf("supersede links: repair stopped at %s %s → %s: %v; run `lore doctor --fix` again", kp.k.name, id, next, err))
				return nil
			}
		}
		for _, id := range rowIDs {
			if kp.plan.set[id] != "" {
				continue
			}
			if err := a.setSuccessor(ctx, kp.k.table, id, "", ""); err != nil {
				fail(fmt.Sprintf("supersede links: repair stopped clearing %s %s: %v; run `lore doctor --fix` again", kp.k.name, id, err))
				return nil
			}
		}
		done += len(kp.plan.fixes)
	}
	note(fmt.Sprintf("repaired %d --supersedes link(s): %s", done, list))
	return nil
}
