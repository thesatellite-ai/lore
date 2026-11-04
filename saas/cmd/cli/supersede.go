// supersede.go — `<kind> add --supersedes <old-id>` for the five kinds with a
// replacement chain (rule, memory, decision, hotfix, pattern).
//
// The column is superseded_by_id and it lives on the OLD row: it names the
// row that replaced it (the memory schema's edge says the same: a memory's
// superseded_by edge is its superseded_by_id). So superseding writes the new
// row, then sets the old row's superseded_by_id to the new id, and for a
// decision also moves the old row to status `superseded` (the schema's
// lifecycle: proposed → accepted → superseded / deprecated).
//
// Fail-closed order: checkSupersedes runs BEFORE the new row is saved and
// refuses an unknown, foreign-project or already-replaced old id, so a
// refused --supersedes writes nothing. finishSupersedes runs after the save;
// if marking the old row still fails, it deletes the new row again and
// reports the error, so the command never prints success for half a chain.
// (Not one SQL transaction: the add commands resolve and may create the
// caller's actor row on the plain client while building the new row, and a
// transaction held across that would wait on its own write lock.)
package main

import (
	"context"
	"fmt"

	"dbent/gen/ent"
	entDecision "dbent/gen/ent/decision"
	"saas/pkg/aicoder/errcodes"
)

// supersedeKind names a kind whose rows carry superseded_by_id. A closed set:
// only these five tables have the column.
type supersedeKind string

const (
	supersedeRule     supersedeKind = "rule"
	supersedeMemory   supersedeKind = "memory"
	supersedeDecision supersedeKind = "decision"
	supersedeHotfix   supersedeKind = "hotfix"
	supersedePattern  supersedeKind = "pattern"
)

// checkSupersedes validates --supersedes before anything is written: the
// old row must be a row of kind in projectID with no successor yet (a chain
// has one successor per row; silently re-pointing it would lose the first
// replacement). oldID "" (no flag) passes.
func checkSupersedes(ctx context.Context, db *ent.Client, kind supersedeKind, projectID, oldID string) error {
	if oldID == "" {
		return nil
	}
	old, err := loadSupersedeRow(ctx, db, kind, oldID)
	if ent.IsNotFound(err) || (err == nil && old.projectID != projectID) {
		return errcodes.New(errcodes.NotFound,
			fmt.Sprintf("--supersedes %s: no %s with that id in this project", oldID, kind)).
			WithHint(fmt.Sprintf("run `lore %s list` to see the ids", kind))
	}
	if err != nil {
		return errcodes.New(errcodes.Internal, fmt.Sprintf("read %s %s", kind, oldID)).WithCause(err)
	}
	if old.supersededBy != nil {
		return errcodes.New(errcodes.InvalidInput,
			fmt.Sprintf("--supersedes %s: that %s was already replaced by %s", oldID, kind, *old.supersededBy)).
			WithHint(fmt.Sprintf("supersede %s instead, so the chain stays one line", *old.supersededBy))
	}
	return nil
}

// finishSupersedes points the old row at newID (and moves an old decision
// to `superseded`). On failure it runs undo (delete the new row) so no
// half-written chain is left, and returns the marking error. oldID ""
// (no flag) does nothing.
func finishSupersedes(ctx context.Context, db *ent.Client, kind supersedeKind, oldID, newID string, undo func(context.Context) error) error {
	if oldID == "" {
		return nil
	}
	err := setSupersededBy(ctx, db, kind, oldID, newID)
	if err == nil {
		return nil
	}
	e := errcodes.New(errcodes.Internal, fmt.Sprintf("mark %s %s superseded by %s", kind, oldID, newID)).WithCause(err)
	if uerr := undo(ctx); uerr != nil {
		return e.WithHint(fmt.Sprintf("the new %s %s was written but could not be removed again (%v); delete it with `lore %s delete %s`", kind, newID, uerr, kind, newID))
	}
	return e
}

// supersedeRow is what markSuperseded needs to know about the old row.
type supersedeRow struct {
	projectID    string
	supersededBy *string
}

// loadSupersedeRow reads the old row of kind; a missing row is ent's
// not-found error, which the caller turns into E_NOT_FOUND.
func loadSupersedeRow(ctx context.Context, db *ent.Client, kind supersedeKind, id string) (supersedeRow, error) {
	switch kind {
	case supersedeRule:
		r, err := db.Rule.Get(ctx, id)
		if err != nil {
			return supersedeRow{}, err
		}
		return supersedeRow{projectID: derefStr(r.ProjectID), supersededBy: r.SupersededByID}, nil
	case supersedeMemory:
		m, err := db.Memory.Get(ctx, id)
		if err != nil {
			return supersedeRow{}, err
		}
		return supersedeRow{projectID: m.ProjectID, supersededBy: m.SupersededByID}, nil
	case supersedeDecision:
		d, err := db.Decision.Get(ctx, id)
		if err != nil {
			return supersedeRow{}, err
		}
		return supersedeRow{projectID: d.ProjectID, supersededBy: d.SupersededByID}, nil
	case supersedeHotfix:
		h, err := db.Hotfix.Get(ctx, id)
		if err != nil {
			return supersedeRow{}, err
		}
		return supersedeRow{projectID: h.ProjectID, supersededBy: h.SupersededByID}, nil
	case supersedePattern:
		p, err := db.Pattern.Get(ctx, id)
		if err != nil {
			return supersedeRow{}, err
		}
		return supersedeRow{projectID: p.ProjectID, supersededBy: p.SupersededByID}, nil
	}
	return supersedeRow{}, fmt.Errorf("supersede: unknown kind %q", kind)
}

// setSupersededBy writes the old row's successor (and a decision's status).
func setSupersededBy(ctx context.Context, db *ent.Client, kind supersedeKind, oldID, newID string) error {
	switch kind {
	case supersedeRule:
		return db.Rule.UpdateOneID(oldID).SetSupersededByID(newID).Exec(ctx)
	case supersedeMemory:
		return errMemoryCoreOnly("supersede")
	case supersedeDecision:
		return db.Decision.UpdateOneID(oldID).
			SetSupersededByID(newID).
			SetStatus(entDecision.StatusSuperseded).
			Exec(ctx)
	case supersedeHotfix:
		return db.Hotfix.UpdateOneID(oldID).SetSupersededByID(newID).Exec(ctx)
	case supersedePattern:
		return db.Pattern.UpdateOneID(oldID).SetSupersededByID(newID).Exec(ctx)
	}
	return fmt.Errorf("supersede: unknown kind %q", kind)
}

// errMemoryCoreOnly refuses an old write path on a memory: memories are
// written only by the 0.1.18 core (ops/memory), so lore.db never becomes a
// second source of truth for them. what names the refused action.
func errMemoryCoreOnly(what string) error {
	return errcodes.New(errcodes.Unsupported, what+" is not available for memories in this build: memories are written by the new core").
		WithHint("use `lore memory edit <id>` (or `lore memory archive <id>`) instead")
}
