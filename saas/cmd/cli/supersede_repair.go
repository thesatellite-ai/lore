// supersede_repair.go — find and repair --supersedes links written the old
// way (`lore doctor` reports them, `lore doctor --fix` repairs them).
//
// Before the fix in supersede.go, `<kind> add --supersedes <old>` stored the
// link on the NEW row (new.superseded_by_id = old) and left the old row
// untouched, which reads as "the new row was replaced by the old one". The
// current shape keeps the link on the OLD row (old.superseded_by_id = new).
//
// Detection: a row X whose superseded_by_id names a row Y of the same kind
// and project that was created strictly BEFORE X is a reversed link — a row
// cannot be replaced by something older. Equal creation times are left
// alone (the direction cannot be told) and reported.
//
// Repair (idempotent, safe to re-run after a partial failure): for every
// reversed X → Y the old row Y gets superseded_by_id = X (an old decision
// that is still proposed or accepted also moves to `superseded`, as a
// current --supersedes does), and X's link is cleared. All new links are
// written first and the reversed ones cleared after, so an interrupted run
// never loses a link: it leaves both directions, and the next run sees
// Y → X as already correct and only clears X. A target that already has a
// correct, different successor, or that two reversed links point at, is
// skipped and reported: one row has one successor, and picking one would
// silently drop the other.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"dbent/gen/ent"
	entDecision "dbent/gen/ent/decision"
)

// supersedeKinds is every kind with a replacement chain, in report order.
var supersedeKinds = [...]supersedeKind{supersedeRule, supersedeMemory, supersedeDecision, supersedeHotfix, supersedePattern}

// supersedeNode is one row's part in a replacement chain.
type supersedeNode struct {
	id           string
	projectID    string
	supersededBy string // "" when the row has no successor
	createdAt    time.Time
	// decisionStatus is set for decisions only (the status a repair may
	// move to superseded).
	decisionStatus entDecision.Status
}

// supersedeFix is one reversed link to repair: oldID gets superseded_by_id
// = newID and newID's own reversed link is cleared.
type supersedeFix struct {
	kind  supersedeKind
	oldID string
	newID string
}

// supersedeSkip is a reversed (or undecidable) link the repair leaves alone,
// with the reason shown to the user.
type supersedeSkip struct {
	kind   supersedeKind
	id     string
	reason string
}

// supersedePlan is what a repair would do for one kind: the fixes, the
// final superseded_by_id of every row it touches ("" clears), and the
// links it must leave.
type supersedePlan struct {
	fixes []supersedeFix
	set   map[string]string
	skips []supersedeSkip
}

// planSupersedeRepair finds the reversed links among nodes (all rows of one
// kind) and works out the final link of every row a repair touches. Pure:
// the same nodes always give the same plan, and the plan of an already
// repaired set is empty.
func planSupersedeRepair(kind supersedeKind, nodes []supersedeNode) supersedePlan {
	byID := make(map[string]supersedeNode, len(nodes))
	for _, n := range nodes {
		byID[n.id] = n
	}
	// reversed reports whether n's link points at an older row of the same
	// project (decided by creation time).
	reversed := func(n supersedeNode) (supersedeNode, bool) {
		y, ok := byID[n.supersededBy]
		if n.supersededBy == "" || !ok || y.projectID != n.projectID {
			return supersedeNode{}, false
		}
		return y, y.createdAt.Before(n.createdAt)
	}
	plan := supersedePlan{set: map[string]string{}}
	into := map[string][]string{} // old (target) id → reversed sources
	for _, n := range nodes {
		y, ok := byID[n.supersededBy]
		if n.supersededBy == "" || !ok || y.projectID != n.projectID {
			continue
		}
		if y.createdAt.Equal(n.createdAt) {
			plan.skips = append(plan.skips, supersedeSkip{kind, n.id,
				fmt.Sprintf("links to %s created at the same instant; the direction cannot be told", y.id)})
			continue
		}
		if y.createdAt.Before(n.createdAt) {
			into[y.id] = append(into[y.id], n.id)
		}
	}
	targets := make([]string, 0, len(into))
	for y := range into {
		targets = append(targets, y)
	}
	sort.Strings(targets)
	for _, yID := range targets {
		srcs := into[yID]
		sort.Strings(srcs)
		y := byID[yID]
		if len(srcs) > 1 {
			for _, x := range srcs {
				plan.skips = append(plan.skips, supersedeSkip{kind, x,
					fmt.Sprintf("several rows (%v) claim to replace %s; keep one with `lore %s edit`", srcs, yID, kind)})
			}
			continue
		}
		x := srcs[0]
		// y may already link somewhere: fine when that link is itself
		// reversed (it is cleared below) or already points at x (an
		// interrupted earlier repair); otherwise y has a real successor.
		if y.supersededBy != "" && y.supersededBy != x {
			if _, rev := reversed(y); !rev {
				plan.skips = append(plan.skips, supersedeSkip{kind, x,
					fmt.Sprintf("%s is already replaced by %s", yID, y.supersededBy)})
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

// loadSupersedeNodes reads the chain columns of every row of kind.
func loadSupersedeNodes(ctx context.Context, db *ent.Client, kind supersedeKind) ([]supersedeNode, error) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	var out []supersedeNode
	switch kind {
	case supersedeRule:
		rows, err := db.Rule.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, supersedeNode{id: r.ID, projectID: str(r.ProjectID), supersededBy: str(r.SupersededByID), createdAt: r.CreatedAt})
		}
	case supersedeMemory:
		rows, err := db.Memory.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, supersedeNode{id: r.ID, projectID: r.ProjectID, supersededBy: str(r.SupersededByID), createdAt: r.CreatedAt})
		}
	case supersedeDecision:
		rows, err := db.Decision.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, supersedeNode{id: r.ID, projectID: r.ProjectID, supersededBy: str(r.SupersededByID), createdAt: r.CreatedAt, decisionStatus: r.Status})
		}
	case supersedeHotfix:
		rows, err := db.Hotfix.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, supersedeNode{id: r.ID, projectID: r.ProjectID, supersededBy: str(r.SupersededByID), createdAt: r.CreatedAt})
		}
	case supersedePattern:
		rows, err := db.Pattern.Query().All(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, supersedeNode{id: r.ID, projectID: r.ProjectID, supersededBy: str(r.SupersededByID), createdAt: r.CreatedAt})
		}
	default:
		return nil, fmt.Errorf("supersede: unknown kind %q", kind)
	}
	return out, nil
}

// clearSupersededBy removes a row's successor link.
func clearSupersededBy(ctx context.Context, db *ent.Client, kind supersedeKind, id string) error {
	switch kind {
	case supersedeRule:
		return db.Rule.UpdateOneID(id).ClearSupersededByID().Exec(ctx)
	case supersedeMemory:
		return db.Memory.UpdateOneID(id).ClearSupersededByID().Exec(ctx)
	case supersedeDecision:
		return db.Decision.UpdateOneID(id).ClearSupersededByID().Exec(ctx)
	case supersedeHotfix:
		return db.Hotfix.UpdateOneID(id).ClearSupersededByID().Exec(ctx)
	case supersedePattern:
		return db.Pattern.UpdateOneID(id).ClearSupersededByID().Exec(ctx)
	}
	return fmt.Errorf("supersede: unknown kind %q", kind)
}

// repairSetSupersededBy points an old row at its replacement. Unlike a new
// --supersedes, an old decision's status moves to superseded only while it
// is still proposed or accepted: a status a person set since (deprecated)
// is kept.
func repairSetSupersededBy(ctx context.Context, db *ent.Client, kind supersedeKind, old supersedeNode, newID string) error {
	if kind == supersedeDecision {
		u := db.Decision.UpdateOneID(old.id).SetSupersededByID(newID)
		if old.decisionStatus == entDecision.StatusProposed || old.decisionStatus == entDecision.StatusAccepted {
			u.SetStatus(entDecision.StatusSuperseded)
		}
		return u.Exec(ctx)
	}
	return setSupersededBy(ctx, db, kind, old.id, newID)
}

// supersedeRepairResult is the outcome over every kind.
type supersedeRepairResult struct {
	fixes []supersedeFix
	skips []supersedeSkip
}

// repairSupersedes finds reversed --supersedes links in every kind and,
// when apply is true, repairs them on db (a client whose writes sync like
// any other row write). With apply false it only reports. Running it again
// after a successful apply finds nothing.
func repairSupersedes(ctx context.Context, db *ent.Client, apply bool) (supersedeRepairResult, error) {
	var res supersedeRepairResult
	for _, kind := range supersedeKinds {
		nodes, err := loadSupersedeNodes(ctx, db, kind)
		if err != nil {
			return res, fmt.Errorf("read %s rows: %w", kind, err)
		}
		plan := planSupersedeRepair(kind, nodes)
		res.skips = append(res.skips, plan.skips...)
		if !apply {
			res.fixes = append(res.fixes, plan.fixes...)
			continue
		}
		byID := make(map[string]supersedeNode, len(nodes))
		for _, n := range nodes {
			byID[n.id] = n
		}
		ids := make([]string, 0, len(plan.set))
		for id := range plan.set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		// New links first, clears after: an interruption never loses a link.
		for _, id := range ids {
			if next := plan.set[id]; next != "" && byID[id].supersededBy != next {
				if err := repairSetSupersededBy(ctx, db, kind, byID[id], next); err != nil {
					return res, fmt.Errorf("point %s %s at %s: %w", kind, id, next, err)
				}
			}
		}
		for _, id := range ids {
			if plan.set[id] == "" {
				if err := clearSupersededBy(ctx, db, kind, id); err != nil {
					return res, fmt.Errorf("clear the reversed link of %s %s: %w", kind, id, err)
				}
			}
		}
		res.fixes = append(res.fixes, plan.fixes...)
	}
	return res, nil
}

// doctorFlagFix is `lore doctor --fix`.
const doctorFlagFix = "fix"

// doctorSupersedes reports reversed --supersedes links (a warning that
// degrades health, with the --fix hint) and, with --fix, repairs them
// through the normal write client so the change reaches .lore/data like
// any row edit. plain is a hook-free client on the doctor's DB, used only
// to look. A failed repair is an error in the report, never a crash.
func doctorSupersedes(ctx context.Context, f *doctorFlags, plain *ent.Client, r *doctorReport) {
	found, err := repairSupersedes(ctx, plain, false)
	if err != nil {
		r.Errors = append(r.Errors, "supersede links: "+err.Error())
		degrade(r)
		return
	}
	for _, s := range found.skips {
		r.Notes = append(r.Notes, fmt.Sprintf("%s %s: --supersedes link left as is: %s", s.kind, s.id, s.reason))
	}
	if len(found.fixes) == 0 {
		return
	}
	if !f.fix {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d --supersedes link(s) written by an older lore point the wrong way (%s); run `lore doctor --fix`",
			len(found.fixes), describeSupersedeFixes(found.fixes)))
		degrade(r)
		return
	}
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		r.Errors = append(r.Errors, "supersede links: not repaired: "+err.Error())
		degrade(r)
		return
	}
	_, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		r.Errors = append(r.Errors, "supersede links: not repaired: "+err.Error())
		degrade(r)
		return
	}
	defer func() { _ = client.Close() }() // lands the sync session's writes
	done, err := repairSupersedes(ctx, client, true)
	if len(done.fixes) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("repaired %d --supersedes link(s): %s", len(done.fixes), describeSupersedeFixes(done.fixes)))
	}
	if err != nil {
		r.Errors = append(r.Errors, "supersede links: repair stopped: "+err.Error()+"; run `lore doctor --fix` again")
		degrade(r)
	}
}

// describeSupersedeFixes names each repair as "<kind> <old> → <new>".
func describeSupersedeFixes(fixes []supersedeFix) string {
	parts := make([]string, 0, len(fixes))
	for _, f := range fixes {
		parts = append(parts, fmt.Sprintf("%s %s → %s", f.kind, f.oldID, f.newID))
	}
	return strings.Join(parts, ", ")
}
