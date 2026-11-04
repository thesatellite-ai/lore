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
	"errors"
	"fmt"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
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
	if errors.Is(err, errSupersedeRowMissing) || (err == nil && old.projectID != projectID) {
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

// errSupersedeRowMissing is loadSupersedeRow's "no such live row" answer,
// which checkSupersedes turns into E_NOT_FOUND.
var errSupersedeRowMissing = errors.New("supersede: no such row")

// Fields of a row with a replacement chain.
const (
	supersedeFieldProject = "project_id"
	supersedeFieldNext    = "superseded_by_id"
)

// loadSupersedeRow reads the old row of kind through the core (every kind
// with a replacement chain is core-owned, so lore.db is never asked); a
// missing or deleted row is errSupersedeRowMissing. db is kept for the
// callers' shape.
func loadSupersedeRow(ctx context.Context, _ *ent.Client, kind supersedeKind, id string) (supersedeRow, error) {
	t, ok := supersedeTables[kind]
	if !ok {
		return supersedeRow{}, fmt.Errorf("supersede: unknown kind %q", kind)
	}
	if coreApp == nil {
		return supersedeRow{}, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return supersedeRow{}, errSupersedeRowMissing
	}
	_, r, err := coreApp.Get(ctx, engine.Ref{Table: t, ID: aid})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return supersedeRow{}, errSupersedeRowMissing
	}
	if err != nil {
		return supersedeRow{}, coreErr(err)
	}
	out := supersedeRow{}
	if v, ok := r.Fields[supersedeFieldProject]; ok && v != nil {
		out.projectID = v.String()
	}
	if v, ok := r.Fields[supersedeFieldNext]; ok && v != nil && v.Kind() != value.KindNull && v.String() != "" {
		next := v.String()
		out.supersededBy = &next
	}
	return out, nil
}

// supersedeTables maps each kind to its core table: every kind with a
// replacement chain is core-owned, so its successor is written through the
// core (never lore.db, whose changes the lsync import would not export).
var supersedeTables = map[supersedeKind]row.Table{
	supersedeRule:     schema.TableRules,
	supersedeMemory:   schema.TableMemories,
	supersedeDecision: schema.TableDecisions,
	supersedeHotfix:   schema.TableHotfixes,
	supersedePattern:  schema.TablePatterns,
}

// setSupersededBy writes the old row's successor (and a decision's status)
// through the core. db is kept for the callers' shape; it is not written.
func setSupersededBy(ctx context.Context, _ *ent.Client, kind supersedeKind, oldID, newID string) error {
	status := ""
	if kind == supersedeDecision {
		status = schema.DecisionStatusSuperseded
	}
	return setSuccessorCore(kind, func(t row.Table) error { return coreSetSuccessor(ctx, t, oldID, newID, status) })
}

// setSuccessorCore runs write on kind's core table.
func setSuccessorCore(kind supersedeKind, write func(row.Table) error) error {
	t, ok := supersedeTables[kind]
	if !ok {
		return fmt.Errorf("supersede: unknown kind %q", kind)
	}
	return write(t)
}
